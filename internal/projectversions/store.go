// Package projectversions 保存不可变的本地工程版本；不复制源媒体，不重放生成任务。
package projectversions

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"aividcut/internal/editor"
	"aividcut/internal/projectstore"
	"aividcut/internal/snapshotstorage"
	"aividcut/internal/statefilelock"
)

const MaxVersions = 200

type Summary struct {
	Sequences     int       `json:"sequences,omitempty"`
	SequenceName  string    `json:"sequenceName,omitempty"`
	ID            string    `json:"id"`
	ProjectID     string    `json:"projectId"`
	Name          string    `json:"name"`
	CreatedAt     time.Time `json:"createdAt"`
	Revision      int64     `json:"revision"`
	Assets        int       `json:"assets"`
	Tracks        int       `json:"tracks"`
	Clips         int       `json:"clips"`
	SHA256        string    `json:"sha256"`
	BeforeRestore bool      `json:"beforeRestore"`
}

type Page struct {
	Versions   []Summary `json:"versions"`
	Total      int       `json:"total"`
	Unreadable int       `json:"unreadable"`
	Offset     int       `json:"offset"`
	Limit      int       `json:"limit"`
}

type Store struct {
	mu        sync.Mutex
	directory string
	lock      *statefilelock.Lock
	closed    bool
}

func Open(directory string) (*Store, error) {
	if strings.TrimSpace(directory) == "" {
		return nil, errors.New("请指定工程版本目录")
	}
	absolute, err := filepath.Abs(directory)
	if err != nil {
		return nil, err
	}
	lock, err := statefilelock.Acquire(filepath.Join(absolute, ".lock"))
	if err != nil {
		return nil, err
	}
	return &Store{directory: absolute, lock: lock}, nil
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

func projectKey(id string) string {
	sum := sha256.Sum256([]byte(id))
	return hex.EncodeToString(sum[:])
}

func validID(id string) bool {
	if !strings.HasPrefix(id, "version-") || len(id) > 96 {
		return false
	}
	for _, c := range strings.TrimPrefix(id, "version-") {
		if c != '-' && (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return len(id) > len("version-")
}

func (s *Store) directoryFor(projectID string) string {
	return filepath.Join(s.directory, projectKey(projectID))
}

func (s *Store) entries(projectID string) ([]os.DirEntry, error) {
	info, err := os.Lstat(s.directoryFor(projectID))
	if errors.Is(err, os.ErrNotExist) {
		return []os.DirEntry{}, nil
	}
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("工程版本目录无效")
	}
	f, err := os.Open(s.directoryFor(projectID))
	if errors.Is(err, os.ErrNotExist) {
		return []os.DirEntry{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	entries := []os.DirEntry{}
	scanned := 0
	for {
		rows, err := f.ReadDir(64)
		if err != nil && err != io.EOF {
			return nil, err
		}
		for _, row := range rows {
			scanned++
			if scanned > 4096 {
				return nil, errors.New("版本目录超过有界扫描范围")
			}
			if !validID(row.Name()) {
				continue
			}
			entries = append(entries, row)
			if len(entries) > MaxVersions {
				return nil, fmt.Errorf("单个工程最多保留%d个活动版本", MaxVersions)
			}
		}
		if err == io.EOF {
			break
		}
	}
	return entries, nil
}

func (s *Store) StorageItems(projectID, view string) ([]snapshotstorage.Item, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, errors.New("工程版本服务已关闭")
	}
	return s.storageItems(projectID, view)
}
func (s *Store) storageItems(projectID, view string) ([]snapshotstorage.Item, error) {
	return snapshotstorage.Catalog(s.directoryFor(projectID), view, true, validID, func(base, id string) (snapshotstorage.Label, error) {
		v, err := readSummary(filepath.Join(base, id), id, projectID)
		return snapshotstorage.Label{Name: v.Name, ProjectID: v.ProjectID, Revision: v.Revision, Time: v.CreatedAt, Readable: err == nil}, err
	}, MaxVersions)
}
func (s *Store) ChangeStorage(projectID string, p snapshotstorage.Proof, action string, protected []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("工程版本服务已关闭")
	}
	if p.Root != s.directoryFor(projectID) || !p.Directory || !validID(p.Filename) || !snapshotstorage.ValidView(p.View) {
		return errors.New("版本资料身份无效")
	}
	if action == "restore" {
		rows, err := s.entries(projectID)
		if err != nil {
			return err
		}
		if len(rows) >= MaxVersions {
			return errors.New("活动版本已达200条，请先整理其它活动版本")
		}
	}
	if action == "trash" {
		rows, err := s.storageItems(projectID, "trash")
		if err != nil {
			return err
		}
		if len(rows) >= MaxVersions {
			return errors.New("版本回收站已达200条，请先永久清理确认不再需要的资料")
		}
	}
	return snapshotstorage.Apply(p, action, protected)
}

func regularBytes(path string, limit int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > limit {
		return nil, errors.New("版本文件缺失、类型无效或超过大小限制")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return nil, errors.New("读取期间版本文件已变化")
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || int64(len(data)) > limit {
		return nil, errors.New("版本文件读取失败或超过大小限制")
	}
	return data, nil
}

func readSummary(directory, id, projectID string) (Summary, error) {
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return Summary{}, errors.New("版本目录无效")
	}
	data, err := regularBytes(filepath.Join(directory, "metadata.json"), 16<<10)
	if err != nil {
		return Summary{}, err
	}
	var value Summary
	if err := json.Unmarshal(data, &value); err != nil {
		return Summary{}, errors.New("版本摘要损坏")
	}
	if value.ID != id || value.ProjectID != projectID || value.CreatedAt.IsZero() || len(value.SHA256) != 64 || utf8.RuneCountInString(value.Name) > 80 {
		return Summary{}, errors.New("版本摘要不匹配")
	}
	return value, nil
}

func (s *Store) Create(project editor.Project, name string, beforeRestore bool) (Summary, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return Summary{}, errors.New("工程版本服务已关闭")
	}
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > 80 {
		return Summary{}, errors.New("版本名称需为1到80个字符")
	}
	if err := editor.ValidateProject(project); err != nil {
		return Summary{}, err
	}
	entries, err := s.entries(project.ID)
	if err != nil {
		return Summary{}, err
	}
	if len(entries) >= MaxVersions {
		return Summary{}, fmt.Errorf("工程已达到%d个版本上限；为保护已有版本，本次未写入", MaxVersions)
	}
	directory := s.directoryFor(project.ID)
	if err := os.MkdirAll(directory, 0700); err != nil {
		return Summary{}, err
	}
	staging, err := os.MkdirTemp(directory, ".pending-")
	if err != nil {
		return Summary{}, err
	}
	defer os.RemoveAll(staging)
	path := filepath.Join(staging, "project.aividcut")
	if err := projectstore.Save(path, project); err != nil {
		return Summary{}, err
	}
	contents, err := regularBytes(path, projectstore.MaxProjectFileBytes)
	if err != nil {
		return Summary{}, err
	}
	sum := sha256.Sum256(contents)
	value := Summary{Sequences: len(project.OtherSequences) + 1, SequenceName: editor.CurrentSequenceName(project), ID: editor.NewID("version"), ProjectID: project.ID, Name: name, CreatedAt: time.Now().UTC(), Revision: project.Revision, Assets: len(project.Assets), Tracks: len(project.Tracks), Clips: len(project.Clips), SHA256: hex.EncodeToString(sum[:]), BeforeRestore: beforeRestore}
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return Summary{}, err
	}
	f, err := os.OpenFile(filepath.Join(staging, "metadata.json"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return Summary{}, err
	}
	_, writeErr := f.Write(encoded)
	if writeErr == nil {
		writeErr = f.Sync()
	}
	closeErr := f.Close()
	if writeErr != nil {
		return Summary{}, writeErr
	}
	if closeErr != nil {
		return Summary{}, closeErr
	}
	if err := os.Rename(staging, filepath.Join(directory, value.ID)); err != nil {
		return Summary{}, err
	}
	return value, nil
}

func (s *Store) List(projectID string, offset, limit int) (Page, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return Page{}, errors.New("工程版本服务已关闭")
	}
	if offset < 0 || limit < 1 || limit > 50 {
		return Page{}, errors.New("版本分页范围无效")
	}
	entries, err := s.entries(projectID)
	if err != nil {
		return Page{}, err
	}
	values := []Summary{}
	unreadable := 0
	for _, entry := range entries {
		if !validID(entry.Name()) {
			continue
		}
		value, err := readSummary(filepath.Join(s.directoryFor(projectID), entry.Name()), entry.Name(), projectID)
		if err != nil {
			unreadable++
			continue
		}
		values = append(values, value)
	}
	sort.Slice(values, func(i, j int) bool {
		if values[i].CreatedAt.Equal(values[j].CreatedAt) {
			return values[i].ID > values[j].ID
		}
		return values[i].CreatedAt.After(values[j].CreatedAt)
	})
	end := len(values)
	start := min(offset, end)
	end = min(start+limit, end)
	return Page{Versions: values[start:end], Total: len(values), Unreadable: unreadable, Offset: offset, Limit: limit}, nil
}

func (s *Store) Load(projectID, id, expectedHash string) (editor.Project, []string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return editor.Project{}, nil, errors.New("工程版本服务已关闭")
	}
	if !validID(id) || len(expectedHash) != 64 {
		return editor.Project{}, nil, errors.New("版本标识无效，请刷新列表")
	}
	directory := filepath.Join(s.directoryFor(projectID), id)
	meta, err := readSummary(directory, id, projectID)
	if err != nil {
		return editor.Project{}, nil, err
	}
	path := filepath.Join(directory, "project.aividcut")
	contents, err := regularBytes(path, projectstore.MaxProjectFileBytes)
	if err != nil {
		return editor.Project{}, nil, err
	}
	sum := sha256.Sum256(contents)
	if meta.SHA256 != expectedHash || hex.EncodeToString(sum[:]) != expectedHash {
		return editor.Project{}, nil, errors.New("版本内容已变化或损坏，未恢复工程")
	}
	project, warnings, err := projectstore.DecodeWithWarnings(contents, path)
	if err != nil {
		return editor.Project{}, nil, err
	}
	if project.ID != projectID || project.Revision != meta.Revision || len(project.Assets) != meta.Assets || len(project.Tracks) != meta.Tracks || len(project.Clips) != meta.Clips {
		return editor.Project{}, nil, errors.New("版本内容与摘要不匹配")
	}
	if meta.Sequences > 0 && (meta.Sequences != len(project.OtherSequences)+1 || meta.SequenceName != editor.CurrentSequenceName(project)) {
		return editor.Project{}, nil, errors.New("版本序列摘要与内容不一致")
	}
	return project, warnings, nil
}
