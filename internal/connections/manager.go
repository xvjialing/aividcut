package connections

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

const MaxAPIKeyBytes = 2048
const metadataLimit = 1 << 20
const profileLimit = 256
const garbageLimit = 4096

var profilePattern = regexp.MustCompile(`^conn-[a-f0-9]{32}$`)
var handlePattern = regexp.MustCompile(`^credential-[a-f0-9]{32}$`)
var providerPattern = regexp.MustCompile(`^[a-z][a-z0-9._-]{0,63}$`)

type storedProfile struct {
	Profile
	SecretHandle string `json:"secretHandle,omitempty"`
}
type metadataFile struct {
	Version  int             `json:"version"`
	Profiles []storedProfile `json:"profiles"`
	Garbage  []string        `json:"garbage"`
}

// Manager 每个资料路径应只有一个写入实例。元数据与清理意图不含明文凭据。
type Manager struct {
	mu        sync.Mutex
	cleanupMu sync.Mutex
	path      string
	store     SecretStore
	entries   map[string]storedProfile
	garbage   map[string]bool
	uses      map[string]int
	pins      map[string]int
	busy      map[string]string
	reserved  map[string]bool
	now       func() time.Time
	writeFile func(string, []byte) error
}

func New(path string, store SecretStore) (*Manager, error) {
	if strings.TrimSpace(path) == "" || nilStore(store) {
		return nil, fail("configuration", "连接资料路径和安全凭据存储不能为空")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, fail("metadata", "连接资料路径无效")
	}
	manager := &Manager{path: absolute, store: store, entries: map[string]storedProfile{}, garbage: map[string]bool{}, uses: map[string]int{}, pins: map[string]int{}, busy: map[string]string{}, reserved: map[string]bool{}, now: time.Now, writeFile: atomicMetadataWrite}
	if err := manager.load(); err != nil {
		return nil, err
	}
	// 不在构造、载入或 List 中校验/清理系统凭据，避免启动时解锁或提示。
	return manager, nil
}

func nilStore(store SecretStore) bool {
	if store == nil {
		return true
	}
	value := reflect.ValueOf(store)
	switch value.Kind() {
	case reflect.Ptr, reflect.Map, reflect.Func, reflect.Interface, reflect.Slice, reflect.Chan:
		return value.IsNil()
	}
	return false
}

func validText(value string, max int, required bool) bool {
	if !utf8.ValidString(value) || utf8.RuneCountInString(value) > max || (required && strings.TrimSpace(value) == "") {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func (manager *Manager) load() error {
	info, err := os.Lstat(manager.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Size() > metadataLimit {
		return fail("metadata", "连接资料不是可读取的普通文件或超过限制")
	}
	file, err := os.Open(manager.path)
	if err != nil {
		return fail("metadata", "无法读取连接资料")
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, metadataLimit+1))
	if err != nil || len(data) > metadataLimit {
		return fail("metadata", "连接资料读取失败或超过限制")
	}
	var stored metadataFile
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&stored); err != nil {
		return fail("metadata", "连接资料格式无效")
	}
	var trailing any
	if decoder.Decode(&trailing) != io.EOF {
		return fail("metadata", "连接资料包含额外内容")
	}
	if stored.Version != 1 || len(stored.Profiles) > profileLimit || len(stored.Garbage) > garbageLimit {
		return fail("metadata", "连接资料版本或数量不受支持")
	}
	handles := map[string]bool{}
	for _, entry := range stored.Profiles {
		if !profilePattern.MatchString(entry.ID) || !providerPattern.MatchString(entry.ProviderID) || !validText(entry.Name, 120, true) || !validText(entry.ModelID, 256, false) || entry.CreatedAt.IsZero() || entry.UpdatedAt.Before(entry.CreatedAt) || entry.Configured != (entry.SecretHandle != "") {
			return fail("metadata", "连接资料字段无效")
		}
		if _, exists := manager.entries[entry.ID]; exists {
			return fail("metadata", "连接标识重复")
		}
		if entry.SecretHandle != "" {
			if !handlePattern.MatchString(entry.SecretHandle) || handles[entry.SecretHandle] {
				return fail("metadata", "系统凭据引用无效或重复")
			}
			handles[entry.SecretHandle] = true
		}
		manager.entries[entry.ID] = entry
	}
	for _, handle := range stored.Garbage {
		if !handlePattern.MatchString(handle) || manager.garbage[handle] || handles[handle] {
			return fail("metadata", "凭据清理意图无效或引用了活动连接")
		}
		manager.garbage[handle] = true
	}
	return nil
}

func (manager *Manager) List() []Profile {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	values := make([]Profile, 0, len(manager.entries))
	for _, entry := range manager.entries {
		values = append(values, entry.Profile)
	}
	sort.Slice(values, func(i, j int) bool {
		if values[i].CreatedAt.Equal(values[j].CreatedAt) {
			return values[i].ID < values[j].ID
		}
		return values[i].CreatedAt.Before(values[j].CreatedAt)
	})
	return values
}

func randomID(prefix string) (string, error) {
	var data [16]byte
	if _, err := rand.Read(data[:]); err != nil {
		return "", fail("random", "系统随机数不可用，未创建凭据引用")
	}
	return prefix + hex.EncodeToString(data[:]), nil
}
func copyEntries(values map[string]storedProfile) map[string]storedProfile {
	copy := make(map[string]storedProfile, len(values))
	for id, value := range values {
		copy[id] = value
	}
	return copy
}
func copyGarbage(values map[string]bool) map[string]bool {
	copy := make(map[string]bool, len(values))
	for id, value := range values {
		copy[id] = value
	}
	return copy
}

func (manager *Manager) publishLocked(entries map[string]storedProfile, garbage map[string]bool) error {
	if len(entries) > profileLimit || len(garbage) > garbageLimit {
		return fail("metadata_limit", "连接或待清理凭据过多，请先完成清理")
	}
	stored := metadataFile{Version: 1, Profiles: make([]storedProfile, 0, len(entries)), Garbage: make([]string, 0, len(garbage))}
	for _, entry := range entries {
		stored.Profiles = append(stored.Profiles, entry)
	}
	sort.Slice(stored.Profiles, func(i, j int) bool { return stored.Profiles[i].ID < stored.Profiles[j].ID })
	for handle := range garbage {
		stored.Garbage = append(stored.Garbage, handle)
	}
	sort.Strings(stored.Garbage)
	data, err := json.MarshalIndent(stored, "", "  ")
	if err != nil || len(data) > metadataLimit {
		return fail("metadata", "连接资料无法编码或超过限制")
	}
	if err := manager.writeFile(manager.path, append(data, '\n')); err != nil {
		return fail("metadata_write", "连接资料保存失败，原有连接保持不变")
	}
	manager.entries, manager.garbage = entries, garbage
	return nil
}

func atomicMetadataWrite(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	if info, err := os.Lstat(path); err == nil && !info.Mode().IsRegular() {
		return errors.New("metadata path is not regular")
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".connections-*")
	if err != nil {
		return err
	}
	name := file.Name()
	defer os.Remove(name)
	if _, err := file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

func (manager *Manager) Save(ctx context.Context, input SaveInput) (Profile, error) {
	if err := ctx.Err(); err != nil {
		return Profile{}, err
	}
	input.Name, input.ModelID = strings.TrimSpace(input.Name), strings.TrimSpace(input.ModelID)
	if !validText(input.Name, 120, true) || !validText(input.ModelID, 256, false) {
		return Profile{}, fail("validation", "连接名称或模型标识无效")
	}
	key := input.APIKey
	if key != "" {
		if len(key) > MaxAPIKeyBytes || !validText(key, MaxAPIKeyBytes, true) || strings.TrimSpace(key) == "" {
			return Profile{}, fail("validation", "API Key 为空、含控制字符或超过 2048 字节限制")
		}
		key = strings.TrimSpace(key)
	}
	manager.mu.Lock()
	if err := ctx.Err(); err != nil {
		manager.mu.Unlock()
		return Profile{}, err
	}
	var previous storedProfile
	id := input.ID
	if id == "" {
		if len(manager.entries) >= profileLimit {
			manager.mu.Unlock()
			return Profile{}, fail("metadata_limit", "连接数量已达限制")
		}
		var err error
		id, err = randomID("conn-")
		if err != nil {
			manager.mu.Unlock()
			return Profile{}, err
		}
	} else {
		var exists bool
		previous, exists = manager.entries[id]
		if !exists {
			manager.mu.Unlock()
			return Profile{}, fail("not_found", "需要更新的连接不存在")
		}
		if input.ProviderID == "" {
			input.ProviderID = previous.ProviderID
		}
		if input.ProviderID != previous.ProviderID {
			manager.mu.Unlock()
			return Profile{}, fail("provider_change", "现有连接不能改变服务归属，请新建连接以避免误用原凭据")
		}
	}
	if !providerPattern.MatchString(input.ProviderID) {
		manager.mu.Unlock()
		return Profile{}, fail("validation", "连接服务标识无效")
	}
	if manager.busy[id] != "" {
		manager.mu.Unlock()
		return Profile{}, fail("connection_busy", "连接正在保存凭据，请稍后重试")
	}
	now := manager.now().UTC()
	if now.Before(previous.UpdatedAt) {
		now = previous.UpdatedAt
	}
	profile := Profile{ID: id, ProviderID: input.ProviderID, Name: input.Name, ModelID: input.ModelID, Configured: previous.SecretHandle != "", CreatedAt: previous.CreatedAt, UpdatedAt: now}
	if profile.CreatedAt.IsZero() {
		profile.CreatedAt = now
	}
	if key == "" {
		next := copyEntries(manager.entries)
		next[id] = storedProfile{Profile: profile, SecretHandle: previous.SecretHandle}
		err := manager.publishLocked(next, copyGarbage(manager.garbage))
		manager.mu.Unlock()
		if err != nil {
			return Profile{}, err
		}
		return profile, nil
	}
	if manager.uses[id] > 0 || manager.pins[id] > 0 {
		manager.mu.Unlock()
		return Profile{}, fail("connection_in_use", "连接正被任务使用或等待恢复，不能替换凭据")
	}
	handle, err := randomID("credential-")
	if err != nil {
		manager.mu.Unlock()
		return Profile{}, err
	}
	manager.busy[id] = handle
	manager.reserved[handle] = true
	// 先记录非秘密回收意图，Profile仍指向旧凭据；进程中断也不会遗失新句柄。
	intent := copyGarbage(manager.garbage)
	intent[handle] = true
	if err := manager.publishLocked(copyEntries(manager.entries), intent); err != nil {
		delete(manager.busy, id)
		delete(manager.reserved, handle)
		manager.mu.Unlock()
		return Profile{}, err
	}
	manager.mu.Unlock()
	secret := []byte(key)
	storeErr := manager.store.Set(ctx, handle, secret)
	clear(secret)
	if storeErr != nil {
		manager.rollbackNewSecret(id, handle)
		if err := ctx.Err(); err != nil {
			return Profile{}, err
		}
		return Profile{}, fail("credentials_write", "系统凭据未能保存，原连接保持不变；如有清理待办请重试清理")
	}
	if err := ctx.Err(); err != nil {
		manager.rollbackNewSecret(id, handle)
		return Profile{}, err
	}
	manager.mu.Lock()
	if err := ctx.Err(); err != nil {
		manager.mu.Unlock()
		manager.rollbackNewSecret(id, handle)
		return Profile{}, err
	}
	now = manager.now().UTC()
	if now.Before(profile.UpdatedAt) {
		now = profile.UpdatedAt
	}
	profile.UpdatedAt = now
	profile.Configured = true
	next := copyEntries(manager.entries)
	next[id] = storedProfile{Profile: profile, SecretHandle: handle}
	garbage := copyGarbage(manager.garbage)
	delete(garbage, handle)
	if previous.SecretHandle != "" {
		garbage[previous.SecretHandle] = true
	}
	if err := manager.publishLocked(next, garbage); err != nil {
		manager.mu.Unlock()
		manager.rollbackNewSecret(id, handle)
		return Profile{}, err
	}
	delete(manager.busy, id)
	delete(manager.reserved, handle)
	manager.mu.Unlock()
	if previous.SecretHandle != "" {
		if err := manager.cleanupHandles(ctx, []string{previous.SecretHandle}); err != nil {
			return profile, &Error{Code: "cleanup_pending", Committed: true, Message: "连接已保存，旧凭据清理待处理；请勿重复提交新密钥，可稍后重试清理"}
		}
	}
	return profile, nil
}

func (manager *Manager) rollbackNewSecret(id, handle string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := manager.store.Delete(ctx, handle)
	manager.mu.Lock()
	defer manager.mu.Unlock()
	delete(manager.busy, id)
	delete(manager.reserved, handle)
	if err == nil || errors.Is(err, ErrSecretNotFound) {
		garbage := copyGarbage(manager.garbage)
		delete(garbage, handle)
		_ = manager.publishLocked(copyEntries(manager.entries), garbage)
	}
}

func (manager *Manager) Delete(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	manager.mu.Lock()
	if err := ctx.Err(); err != nil {
		manager.mu.Unlock()
		return err
	}
	entry, exists := manager.entries[id]
	if !exists {
		manager.mu.Unlock()
		return fail("not_found", "需要删除的连接不存在")
	}
	if manager.busy[id] != "" {
		manager.mu.Unlock()
		return fail("connection_busy", "连接正在保存凭据，不能删除")
	}
	if manager.uses[id] > 0 || manager.pins[id] > 0 {
		manager.mu.Unlock()
		return fail("connection_in_use", "连接正被任务使用或等待恢复，不能删除")
	}
	next := copyEntries(manager.entries)
	delete(next, id)
	garbage := copyGarbage(manager.garbage)
	if entry.SecretHandle != "" {
		garbage[entry.SecretHandle] = true
	}
	if err := manager.publishLocked(next, garbage); err != nil {
		manager.mu.Unlock()
		return err
	}
	manager.mu.Unlock()
	if entry.SecretHandle != "" {
		if err := manager.cleanupHandles(ctx, []string{entry.SecretHandle}); err != nil {
			return &Error{Code: "cleanup_pending", Committed: true, Message: "连接已删除，旧凭据清理待处理；可稍后重试清理"}
		}
	}
	return nil
}

func (manager *Manager) Acquire(ctx context.Context, id string) (*Lease, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	manager.mu.Lock()
	if err := ctx.Err(); err != nil {
		manager.mu.Unlock()
		return nil, err
	}
	entry, exists := manager.entries[id]
	if !exists {
		manager.mu.Unlock()
		return nil, fail("not_found", "连接不存在")
	}
	if manager.busy[id] != "" {
		manager.mu.Unlock()
		return nil, fail("connection_busy", "连接正在保存凭据，请稍后重试")
	}
	if entry.SecretHandle == "" {
		manager.mu.Unlock()
		return nil, fail("not_configured", "此连接尚未保存凭据")
	}
	manager.uses[id]++
	manager.mu.Unlock()
	secret, err := manager.store.Get(ctx, entry.SecretHandle)
	if ctx.Err() != nil {
		clear(secret)
		manager.release(id, false)
		return nil, ctx.Err()
	}
	if err != nil || len(secret) == 0 || len(secret) > MaxAPIKeyBytes || !utf8.Valid(secret) || len(bytes.TrimSpace(secret)) == 0 || bytes.IndexFunc(secret, unicode.IsControl) >= 0 {
		clear(secret)
		manager.release(id, false)
		return nil, fail("credentials_read", "系统凭据不可用，请解锁系统凭据库或重新配置连接")
	}
	if err := ctx.Err(); err != nil {
		clear(secret)
		manager.release(id, false)
		return nil, err
	}
	copy := append([]byte(nil), secret...)
	clear(secret)
	return &Lease{Profile: entry.Profile, manager: manager, id: id, provider: entry.ProviderID, secret: copy}, nil
}

func (manager *Manager) Pin(id string) (*Pin, error) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	entry, exists := manager.entries[id]
	if !exists {
		return nil, fail("not_found", "连接不存在")
	}
	if manager.busy[id] != "" {
		return nil, fail("connection_busy", "连接正在保存凭据，请稍后重试")
	}
	if entry.SecretHandle == "" {
		return nil, fail("not_configured", "不能固定尚未配置凭据的连接")
	}
	manager.pins[id]++
	return &Pin{Profile: entry.Profile, manager: manager, id: id}, nil
}

func (manager *Manager) release(id string, pin bool) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	counter := manager.uses
	if pin {
		counter = manager.pins
	}
	if counter[id] > 1 {
		counter[id]--
	} else {
		delete(counter, id)
	}
}
func (manager *Manager) activeHandleLocked(handle string) bool {
	if manager.reserved[handle] {
		return true
	}
	for _, entry := range manager.entries {
		if entry.SecretHandle == handle {
			return true
		}
	}
	return false
}

func (manager *Manager) PendingCleanupCount() int {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	count := 0
	for handle := range manager.garbage {
		if !manager.activeHandleLocked(handle) {
			count++
		}
	}
	return count
}

func (manager *Manager) Cleanup(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	manager.mu.Lock()
	handles := make([]string, 0, len(manager.garbage))
	for handle := range manager.garbage {
		if !manager.activeHandleLocked(handle) {
			handles = append(handles, handle)
		}
	}
	manager.mu.Unlock()
	sort.Strings(handles)
	return manager.cleanupHandles(ctx, handles)
}

func (manager *Manager) cleanupHandles(ctx context.Context, handles []string) error {
	manager.cleanupMu.Lock()
	defer manager.cleanupMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	for _, handle := range handles {
		if err := ctx.Err(); err != nil {
			return err
		}
		manager.mu.Lock()
		eligible := manager.garbage[handle] && !manager.activeHandleLocked(handle)
		manager.mu.Unlock()
		if !eligible {
			continue
		}
		err := manager.store.Delete(ctx, handle)
		if err != nil && !errors.Is(err, ErrSecretNotFound) {
			return fail("cleanup_pending", "系统凭据清理未完成，请解锁后重试清理")
		}
		manager.mu.Lock()
		garbage := copyGarbage(manager.garbage)
		delete(garbage, handle)
		err = manager.publishLocked(copyEntries(manager.entries), garbage)
		manager.mu.Unlock()
		if err != nil {
			return fail("cleanup_pending", "系统凭据已移除，但清理记录保存失败，可安全重试清理")
		}
	}
	return nil
}

func (manager *Manager) String() string {
	return "connections.Manager{profiles:" + integerString(len(manager.List())) + ",pendingCleanup:" + integerString(manager.PendingCleanupCount()) + "}"
}
func (manager *Manager) GoString() string { return manager.String() }
func integerString(value int) string      { data, _ := json.Marshal(value); return string(data) }
