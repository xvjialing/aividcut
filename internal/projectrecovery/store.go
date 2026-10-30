// Package projectrecovery 保存未保存工程的滚动恢复点，不改写用户工程文件。
package projectrecovery

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"aividcut/internal/editor"
	"aividcut/internal/projectstore"
	"aividcut/internal/snapshotstorage"
	"aividcut/internal/statefilelock"
)

const MaxRecords = 50
const MaxTotalBytes int64 = 512 << 20
const MaxHeaderBytes int64 = 16 << 10
const MaxRecordBytes = projectstore.MaxProjectFileBytes + MaxHeaderBytes

var ErrRetired = errors.New("恢复点已退役，不重放旧写入")

type Summary struct {
	Sequences     int       `json:"sequences,omitempty"`
	SequenceName  string    `json:"sequenceName,omitempty"`
	FormatVersion int       `json:"formatVersion"`
	ID            string    `json:"id"`
	ProjectID     string    `json:"projectId"`
	Name          string    `json:"name"`
	ProjectPath   string    `json:"projectPath"`
	Revision      int64     `json:"revision"`
	UpdatedAt     time.Time `json:"updatedAt"`
	Assets        int       `json:"assets"`
	Clips         int       `json:"clips"`
	SHA256        string    `json:"sha256"`
	BeforeRestore bool      `json:"beforeRestore"`
}
type Page struct {
	Records    []Summary `json:"records"`
	Total      int       `json:"total"`
	Unreadable int       `json:"unreadable"`
	Offset     int       `json:"offset"`
	Limit      int       `json:"limit"`
}
type Store struct {
	mu        sync.Mutex
	directory string
	lock      *statefilelock.Lock
	retired   map[string]bool
	closed    bool
}

func Open(directory string) (*Store, error) {
	if strings.TrimSpace(directory) == "" {
		return nil, errors.New("请指定自动恢复目录")
	}
	absolute, err := filepath.Abs(directory)
	if err != nil {
		return nil, err
	}
	lock, err := statefilelock.Acquire(filepath.Join(absolute, ".lock"))
	if err != nil {
		return nil, err
	}
	return &Store{directory: absolute, lock: lock, retired: map[string]bool{}}, nil
}
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	return s.lock.Close()
}
func validID(id string) bool {
	if !strings.HasPrefix(id, "recovery-") || len(id) > 96 || len(id) <= 9 {
		return false
	}
	for _, c := range strings.TrimPrefix(id, "recovery-") {
		if c != '-' && (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
func (s *Store) path(id string) string { return filepath.Join(s.directory, id+".json") }
func validSummary(v Summary) bool {
	digest, err := hex.DecodeString(v.SHA256)
	return v.FormatVersion == 1 && validID(v.ID) && v.ProjectID != "" && strings.TrimSpace(v.Name) != "" && v.Revision >= 0 && !v.UpdatedAt.IsZero() && v.Assets >= 0 && v.Clips >= 0 && err == nil && len(digest) == 32 && (v.ProjectPath == "" || filepath.IsAbs(v.ProjectPath))
}
func (s *Store) entries() ([]os.DirEntry, int64, error) {
	f, err := os.Open(s.directory)
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()
	values := []os.DirEntry{}
	var total int64
	scanned := 0
	for {
		rows, err := f.ReadDir(64)
		if err != nil && err != io.EOF {
			return nil, 0, err
		}
		for _, row := range rows {
			scanned++
			if scanned > 4096 {
				return nil, 0, errors.New("自动恢复目录内容超出当前支持范围")
			}
			if !strings.HasSuffix(row.Name(), ".json") || !validID(strings.TrimSuffix(row.Name(), ".json")) {
				continue
			}
			info, err := row.Info()
			if err != nil {
				return nil, 0, err
			}
			if !info.Mode().IsRegular() {
				return nil, 0, errors.New("自动恢复目录包含非普通记录文件")
			}
			if info.Size() < 0 || info.Size() > math.MaxInt64-total {
				return nil, 0, errors.New("恢复资料总大小超出支持范围")
			}
			total += info.Size()
			values = append(values, row)
			if len(values) > MaxRecords {
				return nil, 0, errors.New("自动恢复记录超出当前支持数量")
			}
		}
		if err == io.EOF {
			break
		}
	}
	return values, total, nil
}
func openRegular(path string) (*os.File, error) {
	before, err := os.Lstat(path)
	if err != nil || !before.Mode().IsRegular() || before.Size() > MaxRecordBytes {
		return nil, errors.New("恢复副本缺失、类型无效或超过大小限制")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	opened, err := f.Stat()
	if err != nil || !os.SameFile(before, opened) {
		f.Close()
		return nil, errors.New("恢复文件读取期间已变化")
	}
	return f, nil
}
func (s *Store) summary(id string) (Summary, error) {
	return summaryAt(s.directory, id)
}
func summaryAt(directory, id string) (Summary, error) {
	f, err := openRegular(filepath.Join(directory, id+".json"))
	if err != nil {
		return Summary{}, err
	}
	defer f.Close()
	d := json.NewDecoder(io.LimitReader(f, MaxHeaderBytes))
	if token, err := d.Token(); err != nil || token != json.Delim('{') {
		return Summary{}, errors.New("恢复摘要格式无效")
	}
	if token, err := d.Token(); err != nil || token != "metadata" {
		return Summary{}, errors.New("恢复摘要顺序无效")
	}
	var v Summary
	if err := d.Decode(&v); err != nil || !validSummary(v) || v.ID != id {
		return Summary{}, errors.New("恢复摘要损坏")
	}
	return v, nil
}

func (s *Store) StorageItems(view string) ([]snapshotstorage.Item, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, errors.New("自动恢复存储已关闭")
	}
	return s.storageItems(view)
}
func (s *Store) storageItems(view string) ([]snapshotstorage.Item, error) {
	return snapshotstorage.Catalog(s.directory, view, false, validID, func(base, id string) (snapshotstorage.Label, error) {
		v, err := summaryAt(base, id)
		return snapshotstorage.Label{Name: v.Name, ProjectID: v.ProjectID, Revision: v.Revision, Time: v.UpdatedAt, Readable: err == nil}, err
	}, MaxRecords)
}
func (s *Store) ChangeStorage(p snapshotstorage.Proof, action string, protected []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("自动恢复存储已关闭")
	}
	id := strings.TrimSuffix(p.Filename, ".json")
	if p.Root != s.directory || p.Directory || !validID(id) || p.Filename != id+".json" || !snapshotstorage.ValidView(p.View) {
		return errors.New("恢复资料身份无效")
	}
	if action == "restore" {
		entries, total, err := s.entries()
		if err != nil {
			return err
		}
		if len(entries) >= MaxRecords || p.Info.Size() > MaxTotalBytes-total {
			return errors.New("活动恢复资料容量不足，请先整理其它记录")
		}
	}
	if action == "trash" {
		rows, err := s.storageItems("trash")
		if err != nil {
			return err
		}
		var total int64
		for _, row := range rows {
			total += row.Bytes
		}
		if len(rows) >= MaxRecords || p.Info.Size() > MaxTotalBytes-total {
			return errors.New("恢复回收站已到50条或512MiB上限，请先永久清理确认不再需要的资料")
		}
	}
	wasRetired := s.retired[id]
	if action == "trash" {
		s.retired[id] = true
	}
	err := snapshotstorage.Apply(p, action, protected)
	if err != nil && !wasRetired {
		delete(s.retired, id)
	}
	if err == nil && (action == "restore" || action == "purge") {
		delete(s.retired, id)
	}
	return err
}
func (s *Store) Write(id string, p editor.Project, originalPath string, beforeRestore bool) (Summary, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return Summary{}, errors.New("自动恢复存储已关闭")
	}
	if !validID(id) {
		return Summary{}, errors.New("恢复标识无效")
	}
	if s.retired[id] {
		return Summary{}, ErrRetired
	}
	body, err := projectstore.Encode(p)
	if err != nil {
		return Summary{}, err
	}
	body = bytes.TrimSpace(body)
	sum := sha256.Sum256(body)
	v := Summary{Sequences: len(p.OtherSequences) + 1, SequenceName: editor.CurrentSequenceName(p), FormatVersion: 1, ID: id, ProjectID: p.ID, Name: p.Name, ProjectPath: originalPath, Revision: p.Revision, UpdatedAt: time.Now().UTC(), Assets: len(p.Assets), Clips: len(p.Clips), SHA256: hex.EncodeToString(sum[:]), BeforeRestore: beforeRestore}
	if !validSummary(v) {
		return Summary{}, errors.New("恢复摘要参数无效")
	}
	meta, err := json.Marshal(v)
	if err != nil || int64(len(meta)) > MaxHeaderBytes-64 {
		return Summary{}, errors.New("恢复摘要超出大小限制")
	}
	entries, total, err := s.entries()
	if err != nil {
		return Summary{}, err
	}
	var previous int64
	exists := false
	for _, entry := range entries {
		if entry.Name() == id+".json" {
			info, err := entry.Info()
			if err != nil {
				return Summary{}, err
			}
			previous = info.Size()
			exists = true
		}
	}
	size := int64(len(body) + len(meta) + len(`{"metadata":,"project":}`) + 1)
	if !exists && len(entries) >= MaxRecords {
		return Summary{}, errors.New("恢复副本已达50份，请手动保存并处理恢复中心的旧副本")
	}
	if total-previous+size > MaxTotalBytes {
		return Summary{}, errors.New("自动恢复资料已达512MiB预算，请手动保存并处理旧副本")
	}
	f, err := os.CreateTemp(s.directory, ".recovery-pending-*")
	if err != nil {
		return Summary{}, err
	}
	temp := f.Name()
	defer os.Remove(temp)
	var writeErr error
	for _, part := range [][]byte{[]byte(`{"metadata":`), meta, []byte(`,"project":`), body, []byte("}\n")} {
		if writeErr == nil {
			_, writeErr = f.Write(part)
		}
	}
	if writeErr == nil {
		writeErr = f.Sync()
	}
	closeErr := f.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		return Summary{}, err
	}
	if err := os.Rename(temp, s.path(id)); err != nil {
		return Summary{}, err
	}
	return v, nil
}
func (s *Store) Retire(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("自动恢复存储已关闭")
	}
	if id == "" {
		return nil
	}
	if !validID(id) {
		return errors.New("恢复标识无效")
	}
	s.retired[id] = true
	err := os.Remove(s.path(id))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// 写入者已结束后才清除退役标记，避免长会话累计无需保留的内存。
func (s *Store) ForgetRetired(id string) { s.mu.Lock(); delete(s.retired, id); s.mu.Unlock() }
func (s *Store) List(exclude string, offset, limit int) (Page, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return Page{}, errors.New("自动恢复存储已关闭")
	}
	if offset < 0 || limit < 1 || limit > 50 {
		return Page{}, errors.New("恢复列表分页范围无效")
	}
	entries, _, err := s.entries()
	if err != nil {
		return Page{}, err
	}
	values := []Summary{}
	unreadable := 0
	for _, entry := range entries {
		id := strings.TrimSuffix(entry.Name(), ".json")
		if id == exclude {
			continue
		}
		v, err := s.summary(id)
		if err != nil {
			unreadable++
			continue
		}
		values = append(values, v)
	}
	sort.Slice(values, func(i, j int) bool {
		if values[i].UpdatedAt.Equal(values[j].UpdatedAt) {
			return values[i].ID > values[j].ID
		}
		return values[i].UpdatedAt.After(values[j].UpdatedAt)
	})
	start := min(offset, len(values))
	end := min(start+limit, len(values))
	return Page{Records: values[start:end], Total: len(values), Unreadable: unreadable, Offset: offset, Limit: limit}, nil
}
func (s *Store) Load(id, expected string) (editor.Project, Summary, []string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return editor.Project{}, Summary{}, nil, errors.New("自动恢复存储已关闭")
	}
	if !validID(id) {
		return editor.Project{}, Summary{}, nil, errors.New("恢复标识无效")
	}
	f, err := openRegular(s.path(id))
	if err != nil {
		return editor.Project{}, Summary{}, nil, err
	}
	defer f.Close()
	contents, err := io.ReadAll(io.LimitReader(f, MaxRecordBytes+1))
	if err != nil || int64(len(contents)) > MaxRecordBytes {
		return editor.Project{}, Summary{}, nil, errors.New("恢复文件读取失败或超过大小限制")
	}
	var record struct {
		Metadata Summary         `json:"metadata"`
		Project  json.RawMessage `json:"project"`
	}
	d := json.NewDecoder(bytes.NewReader(contents))
	d.DisallowUnknownFields()
	if err := d.Decode(&record); err != nil {
		return editor.Project{}, Summary{}, nil, fmt.Errorf("恢复文件格式无效：%w", err)
	}
	if d.Decode(new(any)) != io.EOF {
		return editor.Project{}, Summary{}, nil, errors.New("恢复文件包含多余数据")
	}
	sum := sha256.Sum256(record.Project)
	v := record.Metadata
	if !validSummary(v) || v.ID != id || v.SHA256 != expected || hex.EncodeToString(sum[:]) != expected {
		return editor.Project{}, Summary{}, nil, errors.New("恢复副本已变化或损坏，请刷新后核对")
	}
	p, warnings, err := projectstore.DecodeWithWarnings(record.Project, s.path(id))
	if err != nil {
		return editor.Project{}, Summary{}, nil, err
	}
	if p.ID != v.ProjectID || p.Name != v.Name || p.Revision != v.Revision || len(p.Assets) != v.Assets || len(p.Clips) != v.Clips {
		return editor.Project{}, Summary{}, nil, errors.New("恢复正文与摘要不匹配")
	}
	if v.Sequences > 0 && (v.Sequences != len(p.OtherSequences)+1 || v.SequenceName != editor.CurrentSequenceName(p)) {
		return editor.Project{}, Summary{}, nil, errors.New("恢复点序列摘要与内容不一致")
	}
	return p, v, warnings, nil
}
