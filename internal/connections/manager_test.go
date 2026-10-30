package connections

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// 本文件的每次凭据操作只触及内存，不读取、解锁或修改系统钥匙串。
type memoryStore struct {
	mu                  sync.Mutex
	values              map[string][]byte
	sets, gets, deletes int
	setHook             func(string) error
	getHook             func(context.Context, string) error
	deleteErr           error
}

func newMemoryStore() *memoryStore { return &memoryStore{values: map[string][]byte{}} }

func (store *memoryStore) Set(ctx context.Context, handle string, value []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	store.mu.Lock()
	store.sets++
	store.values[handle] = append([]byte(nil), value...)
	hook := store.setHook
	store.mu.Unlock()
	if hook != nil {
		return hook(handle)
	}
	return nil
}

func (store *memoryStore) Get(ctx context.Context, handle string) ([]byte, error) {
	store.mu.Lock()
	store.gets++
	hook := store.getHook
	store.mu.Unlock()
	if hook != nil {
		if err := hook(ctx, handle); err != nil {
			return nil, err
		}
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	value, exists := store.values[handle]
	if !exists {
		return nil, ErrSecretNotFound
	}
	return append([]byte(nil), value...), nil
}

func (store *memoryStore) Delete(ctx context.Context, handle string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	store.deletes++
	if store.deleteErr != nil {
		return store.deleteErr
	}
	value, exists := store.values[handle]
	if !exists {
		return ErrSecretNotFound
	}
	clear(value)
	delete(store.values, handle)
	return nil
}

func (store *memoryStore) counts() (int, int, int, int) {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.sets, store.gets, store.deletes, len(store.values)
}

func newFixture(t *testing.T) (*Manager, *memoryStore, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "connections.json")
	store := newMemoryStore()
	manager, err := New(path, store)
	if err != nil {
		t.Fatal(err)
	}
	return manager, store, path
}

func saveFixture(t *testing.T, manager *Manager, id, key string) Profile {
	t.Helper()
	profile, err := manager.Save(context.Background(), SaveInput{ID: id, ProviderID: "openai", Name: "图片连接", ModelID: "gpt-image", APIKey: key})
	if err != nil {
		t.Fatal(err)
	}
	return profile
}

func requireCode(t *testing.T, err error, code string) {
	t.Helper()
	var typed *Error
	if !errors.As(err, &typed) || typed.Code != code {
		t.Fatalf("错误代码不符合预期：%v，需要 %s", err, code)
	}
}

func readMetadata(t *testing.T, path string) metadataFile {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var stored metadataFile
	if err := json.Unmarshal(data, &stored); err != nil {
		t.Fatal(err)
	}
	return stored
}

func TestMetadataRestartAndPublicRedaction(t *testing.T) {
	manager, store, path := newFixture(t)
	if len(manager.List()) != 0 {
		t.Fatal("新资料应为空")
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("构造函数不应写入元数据")
	}
	const secret = "fixture-only-sensitive-value"
	input := SaveInput{ProviderID: "openai", Name: "图片连接", ModelID: "gpt-image", APIKey: secret}
	profile, err := manager.Save(context.Background(), input)
	if err != nil || !profile.Configured || !profilePattern.MatchString(profile.ID) {
		t.Fatalf("保存连接失败：%v", err)
	}
	if profile.CreatedAt.IsZero() || profile.UpdatedAt.Before(profile.CreatedAt) {
		t.Fatal("连接时间无效")
	}
	metadata := readMetadata(t, path)
	if len(metadata.Profiles) != 1 || !handlePattern.MatchString(metadata.Profiles[0].SecretHandle) || len(metadata.Garbage) != 0 {
		t.Fatal("私有元数据未关联有效随机凭据引用")
	}
	restarted, err := New(path, store)
	if err != nil || len(restarted.List()) != 1 || restarted.List()[0] != profile {
		t.Fatalf("重开资料未保留连接：%v", err)
	}
	if sets, gets, deletes, count := store.counts(); sets != 1 || gets != 0 || deletes != 0 || count != 1 {
		t.Fatal("构造或列表访问了安全凭据库")
	}
	lease, err := restarted.Acquire(context.Background(), profile.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	for _, value := range []any{input, profile, restarted.List(), lease, manager} {
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		formatted := fmt.Sprintf("%v %+v %#v", value, value, value)
		if strings.Contains(string(encoded), secret) || strings.Contains(formatted, secret) || strings.Contains(string(encoded), "secretHandle") {
			t.Fatal("公开返回或格式化包含秘密内容/私有句柄")
		}
	}
	data, err := os.ReadFile(path)
	if err != nil || strings.Contains(string(data), secret) || strings.Contains(string(data), "apiKey") {
		t.Fatal("API Key 不得写入元数据")
	}
	if err := filepath.WalkDir(filepath.Dir(path), func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		if err == nil && strings.Contains(string(data), secret) {
			t.Error("凭据出现在资料目录文件中")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	var decoded SaveInput
	if err := json.Unmarshal([]byte(`{"providerId":"google","name":"测试","apiKey":"decode-only-value"}`), &decoded); err != nil || decoded.APIKey != "decode-only-value" {
		t.Fatal("JSON 请求必须仍能解码凭据字段")
	}
}

func TestLeaseAndPinProtectCredentialsButAllowMetadata(t *testing.T) {
	manager, store, _ := newFixture(t)
	profile := saveFixture(t, manager, "", "first-secret")
	lease, err := manager.Acquire(context.Background(), profile.ID)
	if err != nil {
		t.Fatal(err)
	}
	pin, err := manager.Pin(profile.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, gets, _, _ := store.counts(); gets != 1 {
		t.Fatal("Pin 不应读取密钥")
	}
	for _, operation := range []func() error{
		func() error {
			_, err := manager.Save(context.Background(), SaveInput{ID: profile.ID, Name: "替换", APIKey: "second-secret"})
			return err
		},
		func() error { return manager.Delete(context.Background(), profile.ID) },
	} {
		requireCode(t, operation(), "connection_in_use")
	}
	updated, err := manager.Save(context.Background(), SaveInput{ID: profile.ID, Name: "新名称", ModelID: "new-model"})
	if err != nil || updated.Name != "新名称" || !updated.Configured || updated.CreatedAt != profile.CreatedAt {
		t.Fatalf("在用连接的非秘密资料更新失败：%v", err)
	}
	if lease.Profile != profile || pin.Profile != profile {
		t.Fatal("在途请求的资料快照被后续编辑改变")
	}
	lease.Profile.ProviderID = "google"
	if _, err := lease.Secret(context.Background(), "google"); err == nil {
		t.Fatal("展示快照不能改变密钥服务归属")
	}
	if got, err := lease.Secret(context.Background(), "openai"); err != nil || got != "first-secret" {
		t.Fatal("租约未保持原始凭据")
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	if len(lease.secret) != 0 {
		t.Fatal("关闭租约应清零并释放私有凭据")
	}
	requireCode(t, manager.Delete(context.Background(), profile.ID), "connection_in_use")
	if _, err := lease.Secret(context.Background(), "openai"); err == nil {
		t.Fatal("关闭后的租约仍可读密钥")
	}
	_ = lease.Close()
	_ = pin.Close()
	_ = pin.Close()
	saveFixture(t, manager, profile.ID, "second-secret")
	if _, _, _, count := store.counts(); count != 1 {
		t.Fatal("替换后旧凭据未清理")
	}
	if err := manager.Delete(context.Background(), profile.ID); err != nil || len(manager.List()) != 0 {
		t.Fatalf("释放使用者后删除失败：%v", err)
	}
}

func TestAcquireReservesBeforeBlockingStoreReadAndReleasesOnFailure(t *testing.T) {
	manager, store, _ := newFixture(t)
	profile := saveFixture(t, manager, "", "first-secret")
	started := make(chan struct{})
	store.getHook = func(ctx context.Context, _ string) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { _, err := manager.Acquire(ctx, profile.ID); result <- err }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("未开始读取内存凭据")
	}
	requireCode(t, manager.Delete(context.Background(), profile.ID), "connection_in_use")
	_, err := manager.Save(context.Background(), SaveInput{ID: profile.ID, Name: "替换", APIKey: "next-secret"})
	requireCode(t, err, "connection_in_use")
	if _, err := manager.Save(context.Background(), SaveInput{ID: profile.ID, Name: "读取中改名"}); err != nil {
		t.Fatal(err)
	}
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("取消读取未返回取消错误：%v", err)
	}
	store.getHook = nil
	if err := manager.Delete(context.Background(), profile.ID); err != nil {
		t.Fatalf("失败读取遗留使用计数：%v", err)
	}
}

func TestRotationWritesCredentialBeforePublishingNewProfile(t *testing.T) {
	manager, store, path := newFixture(t)
	profile := saveFixture(t, manager, "", "original-secret")
	oldHandle := readMetadata(t, path).Profiles[0].SecretHandle
	store.setHook = func(handle string) error {
		stored := readMetadata(t, path)
		if stored.Profiles[0].SecretHandle != oldHandle || len(stored.Garbage) != 1 || stored.Garbage[0] != handle {
			t.Error("写入凭据前应保留原连接，并已落盘候选凭据清理意图")
		}
		if manager.PendingCleanupCount() != 0 {
			t.Error("仍在写入的候选凭据不能被 Cleanup 删除")
		}
		if err := manager.Cleanup(context.Background()); err != nil {
			t.Error(err)
		}
		return nil
	}
	updated := saveFixture(t, manager, profile.ID, "replacement-secret")
	if !updated.Configured || readMetadata(t, path).Profiles[0].SecretHandle == oldHandle || manager.PendingCleanupCount() != 0 {
		t.Fatal("轮换未原子发布或清理完成")
	}
	lease, err := manager.Acquire(context.Background(), profile.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	if secret, err := lease.Secret(context.Background(), "openai"); err != nil || secret != "replacement-secret" {
		t.Fatal("轮换后未使用新凭据")
	}
}

func TestPublishAndStoreFailuresKeepOriginalConnection(t *testing.T) {
	for _, stage := range []string{"intent", "credential", "publish"} {
		t.Run(stage, func(t *testing.T) {
			manager, store, path := newFixture(t)
			profile := saveFixture(t, manager, "", "original-secret")
			originalWriter := manager.writeFile
			setsBefore, _, _, _ := store.counts()
			switch stage {
			case "intent":
				manager.writeFile = func(string, []byte) error { return errors.New("fixture write failed") }
			case "credential":
				// 模拟后端已部分写入但随后报错，必须尝试删除候选句柄。
				store.setHook = func(string) error { return errors.New("backend leaked replacement-secret") }
			case "publish":
				store.setHook = func(string) error {
					manager.writeFile = func(string, []byte) error { return errors.New("fixture publish failed") }
					return nil
				}
			}
			_, err := manager.Save(context.Background(), SaveInput{ID: profile.ID, Name: "不应保留", APIKey: "replacement-secret"})
			if err == nil || IsCommitted(err) || strings.Contains(err.Error(), "replacement-secret") || manager.List()[0] != profile {
				t.Fatalf("失败保存更改了原连接或泄露秘密：%v", err)
			}
			if sets, _, _, count := store.counts(); count != 1 || (stage == "intent" && sets != setsBefore) {
				t.Fatal("失败保存未回滚候选凭据")
			}
			manager.writeFile = originalWriter
			store.setHook = nil
			restarted, err := New(path, store)
			if err != nil || restarted.List()[0] != profile {
				t.Fatalf("重开失败保存后的元数据丢失原连接：%v", err)
			}
			if err := restarted.Cleanup(context.Background()); err != nil || restarted.PendingCleanupCount() != 0 {
				t.Fatalf("遗留意图无法安全重试：%v", err)
			}
			lease, err := restarted.Acquire(context.Background(), profile.ID)
			if err != nil {
				t.Fatal(err)
			}
			defer lease.Close()
			if secret, err := lease.Secret(context.Background(), "openai"); err != nil || secret != "original-secret" {
				t.Fatal("失败轮换破坏了原凭据")
			}
		})
	}
}

func TestCommittedCleanupFailureSurvivesRestart(t *testing.T) {
	manager, store, path := newFixture(t)
	profile := saveFixture(t, manager, "", "original-secret")
	store.deleteErr = errors.New("fixture private original-secret error")
	updated, err := manager.Save(context.Background(), SaveInput{ID: profile.ID, Name: "已更新", APIKey: "replacement-secret"})
	if !IsCommitted(err) || updated.ID != profile.ID || manager.List()[0] != updated || !strings.Contains(err.Error(), "连接已保存，旧凭据清理待处理") || strings.Contains(err.Error(), "original-secret") {
		t.Fatalf("保存提交状态或安全错误消息不正确：%v", err)
	}
	if manager.PendingCleanupCount() != 1 {
		t.Fatal("未记录旧凭据回收待办")
	}
	_, getsBefore, deletesBefore, _ := store.counts()
	restarted, err := New(path, store)
	if err != nil || restarted.List()[0] != updated || restarted.PendingCleanupCount() != 1 {
		t.Fatalf("清理待办未跨重启保留：%v", err)
	}
	if _, gets, deletes, _ := store.counts(); gets != getsBefore || deletes != deletesBefore {
		t.Fatal("重启和列表不应自动读写钥匙串")
	}
	lease, err := restarted.Acquire(context.Background(), profile.ID)
	if err != nil {
		t.Fatal(err)
	}
	if secret, err := lease.Secret(context.Background(), "openai"); err != nil || secret != "replacement-secret" {
		t.Fatal("清理警告不应影响新连接使用")
	}
	_ = lease.Close()
	store.deleteErr = nil
	if err := restarted.Cleanup(context.Background()); err != nil || restarted.PendingCleanupCount() != 0 {
		t.Fatalf("显式清理失败：%v", err)
	}
	if _, _, _, count := store.counts(); count != 1 {
		t.Fatal("显式清理删除了活动凭据或未删除旧凭据")
	}
}

func TestDeleteCommitAndFailureSemantics(t *testing.T) {
	manager, store, path := newFixture(t)
	profile := saveFixture(t, manager, "", "delete-secret")
	writer := manager.writeFile
	manager.writeFile = func(string, []byte) error { return errors.New("fixture cannot persist") }
	requireCode(t, manager.Delete(context.Background(), profile.ID), "metadata_write")
	if manager.List()[0] != profile {
		t.Fatal("资料保存失败时不应删除连接")
	}
	if _, _, deletes, _ := store.counts(); deletes != 0 {
		t.Fatal("未发布删除前不应删除凭据")
	}
	manager.writeFile = writer
	store.deleteErr = errors.New("fixture delete failure")
	err := manager.Delete(context.Background(), profile.ID)
	if !IsCommitted(err) || len(manager.List()) != 0 || manager.PendingCleanupCount() != 1 {
		t.Fatalf("删除提交与清理待办不正确：%v", err)
	}
	restarted, err := New(path, store)
	if err != nil || len(restarted.List()) != 0 {
		t.Fatalf("已删除连接重启后重新出现：%v", err)
	}
	store.deleteErr = nil
	if err := restarted.Cleanup(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, _, _, count := store.counts(); count != 0 {
		t.Fatal("删除清理未回收旧凭据")
	}
}

func TestValidationDraftAndSanitizedReadFailure(t *testing.T) {
	manager, store, _ := newFixture(t)
	profile := saveFixture(t, manager, "", "")
	if profile.Configured {
		t.Fatal("没有保存密钥的连接应为草稿")
	}
	_, err := manager.Acquire(context.Background(), profile.ID)
	requireCode(t, err, "not_configured")
	_, err = manager.Pin(profile.ID)
	requireCode(t, err, "not_configured")
	for _, input := range []SaveInput{
		{ProviderID: "openai", Name: ""},
		{ProviderID: "../openai", Name: "测试"},
		{ProviderID: "openai", Name: "测试", APIKey: "   "},
		{ProviderID: "openai", Name: "测试", APIKey: "private\nvalue"},
		{ProviderID: "openai", Name: "测试", APIKey: strings.Repeat("a", MaxAPIKeyBytes+1)},
		{ProviderID: "openai", Name: "测试", ModelID: "bad\x00model"},
	} {
		if _, err := manager.Save(context.Background(), input); err == nil {
			t.Fatal("无效输入被接受")
		}
	}
	_, err = manager.Save(context.Background(), SaveInput{ID: profile.ID, ProviderID: "google", Name: "变更服务"})
	requireCode(t, err, "provider_change")
	if sets, gets, deletes, count := store.counts(); sets != 0 || gets != 0 || deletes != 0 || count != 0 {
		t.Fatal("草稿和校验不应操作安全凭据库")
	}
	profile = saveFixture(t, manager, profile.ID, "private-test-secret")
	store.getHook = func(context.Context, string) error { return errors.New("backend private-test-secret") }
	_, err = manager.Acquire(context.Background(), profile.ID)
	if err == nil || strings.Contains(err.Error(), "private-test-secret") {
		t.Fatal("底层凭据错误直接泄露到公开错误")
	}
	store.getHook = nil
	if err := manager.Delete(context.Background(), profile.ID); err != nil {
		t.Fatal("读取失败遗留了锁定连接")
	}
}

func TestInvalidMetadataNeverTouchesStore(t *testing.T) {
	manager, store, path := newFixture(t)
	saveFixture(t, manager, "", "metadata-secret")
	valid, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{
		"unknown":    []byte(`{"version":1,"profiles":[],"garbage":[],"apiKey":"invalid"}`),
		"version":    []byte(`{"version":2,"profiles":[],"garbage":[]}`),
		"trailing":   append(append([]byte(nil), valid...), []byte(`{}`)...),
		"handle":     []byte(strings.ReplaceAll(string(valid), "credential-", "unsafe-")),
		"configured": []byte(strings.ReplaceAll(string(valid), `"configured": true`, `"configured": false`)),
	} {
		t.Run(name, func(t *testing.T) {
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := New(path, store); err == nil {
				t.Fatal("损坏资料未被拒绝")
			}
		})
	}
	if _, gets, deletes, _ := store.counts(); gets != 0 || deletes != 0 {
		t.Fatal("载入损坏资料时触及了安全存储")
	}
	var nilPointer *memoryStore
	if _, err := New(path, nilPointer); err == nil {
		t.Fatal("未拒绝 typed nil 凭据存储")
	}
}

func TestIndependentConcurrentSavesPreserveBothProfiles(t *testing.T) {
	manager, store, path := newFixture(t)
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	store.setHook = func(string) error { started <- struct{}{}; <-release; return nil }
	results := make(chan error, 2)
	for _, provider := range []string{"openai", "google"} {
		go func(provider string) {
			_, err := manager.Save(context.Background(), SaveInput{ProviderID: provider, Name: provider, APIKey: provider + "-fixture"})
			results <- err
		}(provider)
	}
	for range 2 {
		select {
		case <-started:
		case <-time.After(3 * time.Second):
			t.Fatal("独立连接保存没有并行进入安全存储")
		}
	}
	if len(manager.List()) != 0 || manager.PendingCleanupCount() != 0 {
		t.Fatal("凭据尚未保存完成时不应暴露候选连接或回收其句柄")
	}
	close(release)
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	if len(manager.List()) != 2 || len(readMetadata(t, path).Profiles) != 2 {
		t.Fatal("并发保存丢失了其他连接")
	}
}

func TestCanceledOperationsHaveNoCredentialSideEffects(t *testing.T) {
	manager, store, _ := newFixture(t)
	profile := saveFixture(t, manager, "", "cancel-fixture-secret")
	lease, err := manager.Acquire(context.Background(), profile.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	setsBefore, getsBefore, deletesBefore, _ := store.counts()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, operation := range []func() error{
		func() error {
			_, err := manager.Save(ctx, SaveInput{ProviderID: "openai", Name: "取消", APIKey: "new-secret"})
			return err
		},
		func() error { _, err := manager.Save(ctx, SaveInput{ID: profile.ID, Name: "取消改名"}); return err },
		func() error { return manager.Delete(ctx, profile.ID) },
		func() error { _, err := manager.Acquire(ctx, profile.ID); return err },
		func() error { _, err := lease.Secret(ctx, "openai"); return err },
		func() error { return manager.Cleanup(ctx) },
	} {
		if err := operation(); !errors.Is(err, context.Canceled) {
			t.Fatalf("已取消操作未返回取消：%v", err)
		}
	}
	if manager.List()[0] != profile {
		t.Fatal("取消操作更改了连接")
	}
	if sets, gets, deletes, _ := store.counts(); sets != setsBefore || gets != getsBefore || deletes != deletesBefore {
		t.Fatal("提前取消仍触发了凭据操作")
	}
}

func TestCancelDuringCredentialWriteLeavesRecoverableCleanupIntent(t *testing.T) {
	for _, backendAlsoFails := range []bool{false, true} {
		t.Run(fmt.Sprint(backendAlsoFails), func(t *testing.T) {
			manager, store, path := newFixture(t)
			profile := saveFixture(t, manager, "", "original-fixture-secret")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			store.setHook = func(string) error {
				cancel()
				if backendAlsoFails {
					return errors.New("fixture backend canceled")
				}
				return nil
			}
			// 即使回滚删除也失败，必须持久化清理意图，保留原资料及凭据。
			store.deleteErr = errors.New("fixture key store locked")
			_, err := manager.Save(ctx, SaveInput{ID: profile.ID, Name: "不应保留", APIKey: "canceled-fixture-secret"})
			if !errors.Is(err, context.Canceled) || IsCommitted(err) || manager.List()[0] != profile || manager.PendingCleanupCount() != 1 {
				t.Fatalf("提交前取消的状态不正确：%v", err)
			}
			_, getsBefore, deletesBefore, _ := store.counts()
			restarted, err := New(path, store)
			if err != nil || restarted.List()[0] != profile || restarted.PendingCleanupCount() != 1 {
				t.Fatalf("失败回滚的清理意图未保留：%v", err)
			}
			if _, gets, deletes, _ := store.counts(); gets != getsBefore || deletes != deletesBefore {
				t.Fatal("构造不应自动处理清理意图")
			}
			store.deleteErr = nil
			store.setHook = nil
			if err := restarted.Cleanup(context.Background()); err != nil || restarted.PendingCleanupCount() != 0 {
				t.Fatalf("取消后的显式回收失败：%v", err)
			}
			if _, _, _, count := store.counts(); count != 1 {
				t.Fatal("取消后的清理未准确保留原凭据")
			}
		})
	}
}

func TestCancelAfterMetadataCommitReturnsCommittedProfile(t *testing.T) {
	manager, store, path := newFixture(t)
	profile := saveFixture(t, manager, "", "original-fixture-secret")
	oldHandle := readMetadata(t, path).Profiles[0].SecretHandle
	writer := manager.writeFile
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager.writeFile = func(path string, data []byte) error {
		if err := writer(path, data); err != nil {
			return err
		}
		var metadata metadataFile
		if err := json.Unmarshal(data, &metadata); err != nil {
			return err
		}
		if metadata.Profiles[0].SecretHandle != oldHandle {
			cancel()
		}
		return nil
	}
	updated, err := manager.Save(ctx, SaveInput{ID: profile.ID, Name: "已提交", APIKey: "replacement-fixture-secret"})
	if !IsCommitted(err) || updated.Name != "已提交" || manager.List()[0] != updated || manager.PendingCleanupCount() != 1 {
		t.Fatalf("提交后的取消不得伪报整个保存失败：%v", err)
	}
	manager.writeFile = writer
	if err := manager.Cleanup(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, _, _, count := store.counts(); count != 1 {
		t.Fatal("提交后取消的旧凭据未可恢复清理")
	}
}

func TestCleanupCanRetryAfterCredentialRemovedButMetadataWriteFailed(t *testing.T) {
	manager, store, path := newFixture(t)
	profile := saveFixture(t, manager, "", "original-fixture-secret")
	store.deleteErr = errors.New("fixture locked")
	_, err := manager.Save(context.Background(), SaveInput{ID: profile.ID, Name: "已保存", APIKey: "replacement-fixture-secret"})
	if !IsCommitted(err) {
		t.Fatal("应产生可清理的已提交连接")
	}
	store.deleteErr = nil
	writer := manager.writeFile
	manager.writeFile = func(string, []byte) error { return errors.New("fixture disk full") }
	requireCode(t, manager.Cleanup(context.Background()), "cleanup_pending")
	if manager.PendingCleanupCount() != 1 {
		t.Fatal("清理记录未保存时应保留待办")
	}
	if _, _, _, count := store.counts(); count != 1 {
		t.Fatal("清理未删除旧凭据")
	}
	manager.writeFile = writer
	restarted, err := New(path, store)
	if err != nil {
		t.Fatal(err)
	}
	if err := restarted.Cleanup(context.Background()); err != nil || restarted.PendingCleanupCount() != 0 {
		t.Fatalf("对已不存在的旧凭据应能幂等完成清理：%v", err)
	}
	if _, _, _, count := store.counts(); count != 1 {
		t.Fatal("重试清理误删了活动凭据")
	}
}

func TestSavingConnectionRejectsCompetingMutation(t *testing.T) {
	manager, store, _ := newFixture(t)
	profile := saveFixture(t, manager, "", "original-fixture-secret")
	started, release := make(chan struct{}), make(chan struct{})
	store.setHook = func(string) error { close(started); <-release; return nil }
	result := make(chan error, 1)
	go func() {
		_, err := manager.Save(context.Background(), SaveInput{ID: profile.ID, Name: "新连接", APIKey: "next-fixture-secret"})
		result <- err
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("保存未进入内存安全存储")
	}
	for _, operation := range []func() error{
		func() error {
			_, err := manager.Save(context.Background(), SaveInput{ID: profile.ID, Name: "竞争改名"})
			return err
		},
		func() error { return manager.Delete(context.Background(), profile.ID) },
		func() error { _, err := manager.Acquire(context.Background(), profile.ID); return err },
		func() error { _, err := manager.Pin(profile.ID); return err },
	} {
		requireCode(t, operation(), "connection_busy")
	}
	close(release)
	if err := <-result; err != nil {
		t.Fatal(err)
	}
}
