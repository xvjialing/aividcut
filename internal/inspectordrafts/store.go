package inspectordrafts

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"aividcut/internal/editor"
	"aividcut/internal/projectstore"
	"aividcut/internal/statefilelock"
)

const MaxDraftBytes = 256 << 10
const MaxRecords = 50
const MaxTotalBytes int64 = 512 << 20

type Record struct {
	ID           string    `json:"id"`
	ProjectID    string    `json:"projectId"`
	Name         string    `json:"name"`
	Target       string    `json:"target"`
	OriginalPath string    `json:"originalPath"`
	BaseRevision int64     `json:"baseRevision"`
	UpdatedAt    time.Time `json:"updatedAt"`
	BaseSHA256   string    `json:"baseSha256"`
	Fields       []Field   `json:"fields"`
	SHA256       string    `json:"sha256,omitempty"`
}
type Page struct {
	Records    []Record `json:"records"`
	Unreadable int      `json:"unreadable"`
}
type Store struct {
	mu        sync.Mutex
	root      *os.Root
	directory string
	lock      *statefilelock.Lock
	closed    bool
}

func Open(directory string) (*Store, error) {
	if strings.TrimSpace(directory) == "" {
		return nil, errors.New("属性草稿目录不能为空")
	}
	lock, err := statefilelock.Acquire(filepath.Join(directory, ".lock"))
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		lock.Close()
		return nil, err
	}
	return &Store{root: root, directory: directory, lock: lock}, nil
}
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	s.root.Close()
	return s.lock.Close()
}
func digest(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }
func validID(id string) bool {
	if !strings.HasPrefix(id, "draft-") || len(id) > 96 || len(id) < 7 {
		return false
	}
	for _, c := range id[6:] {
		if c != '-' && (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
func read(root *os.Root, name string, limit int64) ([]byte, error) {
	info, err := root.Lstat(name)
	if err != nil || !info.Mode().IsRegular() || info.Size() > limit {
		return nil, errors.New("草稿资料缺失、类型无效或过大")
	}
	f, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return nil, errors.New("草稿资料读取时已变化")
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || int64(len(data)) > limit {
		return nil, errors.New("草稿资料读取失败或过大")
	}
	return data, nil
}
func write(root *os.Root, name string, data []byte) error {
	temp := ".write-" + editor.NewID("tmp")
	f, err := root.OpenFile(temp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer root.Remove(temp)
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return root.Rename(temp, name)
}
func (s *Store) directoryRoot(id string) (*os.Root, error) {
	if s.closed || !validID(id) {
		return nil, errors.New("草稿身份无效或存储已关闭")
	}
	info, err := s.root.Lstat(id)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("草稿目录无效")
	}
	return s.root.OpenRoot(id)
}
func (s *Store) load(id string) (Record, error) {
	r, err := s.directoryRoot(id)
	if err != nil {
		return Record{}, err
	}
	defer r.Close()
	data, err := read(r, "draft.json", MaxDraftBytes)
	if err != nil {
		return Record{}, err
	}
	var v Record
	if json.Unmarshal(data, &v) != nil || v.ID != id || v.ProjectID == "" || len(v.Fields) == 0 || ValidateFields(v.Fields) != nil || len(v.BaseSHA256) != 64 || v.UpdatedAt.IsZero() {
		return Record{}, errors.New("草稿记录损坏")
	}
	v.SHA256 = digest(data)
	return v, nil
}
func (s *Store) list() (Page, int64, error) {
	page := Page{Records: []Record{}}
	f, err := s.root.Open(".")
	if err != nil {
		return page, 0, err
	}
	defer f.Close()
	rows, err := f.ReadDir(4097)
	if err != nil && err != io.EOF {
		return page, 0, err
	}
	if len(rows) > 4096 {
		return page, 0, errors.New("草稿目录条目过多")
	}
	var total int64
	count := 0
	for _, row := range rows {
		if !validID(row.Name()) {
			continue
		}
		count++
		if count > MaxRecords {
			return page, 0, errors.New("草稿数量超出50条，请先整理资料")
		}
		root, err := s.directoryRoot(row.Name())
		if err != nil {
			page.Unreadable++
			continue
		}
		for _, name := range []string{"base.aividcut", "draft.json"} {
			if info, e := root.Lstat(name); e == nil && info.Mode().IsRegular() {
				total += info.Size()
			}
		}
		root.Close()
		v, err := s.load(row.Name())
		if err != nil {
			page.Unreadable++
		} else {
			page.Records = append(page.Records, v)
		}
	}
	sort.Slice(page.Records, func(i, j int) bool { return page.Records[i].UpdatedAt.After(page.Records[j].UpdatedAt) })
	return page, total, nil
}
func (s *Store) List() (Page, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return Page{}, errors.New("草稿存储已关闭")
	}
	p, _, err := s.list()
	return p, err
}
func (s *Store) Load(id, sha string) (Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, err := s.load(id)
	if err == nil && v.SHA256 != sha {
		return Record{}, errors.New("草稿已变化，请刷新后重新核对")
	}
	return v, err
}

// 更新输入只核对有界目录元信息，不逐次读取其它草稿正文或基础工程。
func (s *Store) usage() (int, int64, error) {
	f, err := s.root.Open(".")
	if err != nil {
		return 0, 0, err
	}
	defer f.Close()
	rows, err := f.ReadDir(4097)
	if err != nil && err != io.EOF {
		return 0, 0, err
	}
	if len(rows) > 4096 {
		return 0, 0, errors.New("草稿目录条目过多")
	}
	count := 0
	var total int64
	for _, row := range rows {
		if !validID(row.Name()) {
			continue
		}
		count++
		if count > MaxRecords {
			return 0, 0, errors.New("草稿数量超过50条")
		}
		root, err := s.directoryRoot(row.Name())
		if err != nil {
			return 0, 0, err
		}
		for _, name := range []string{"base.aividcut", "draft.json"} {
			info, e := root.Lstat(name)
			if e == nil {
				if !info.Mode().IsRegular() || info.Size() > projectstore.MaxProjectFileBytes+MaxDraftBytes {
					root.Close()
					return 0, 0, errors.New("草稿目录文件类型或大小无效")
				}
				total += info.Size()
			}
		}
		root.Close()
	}
	return count, total, nil
}

// Save 首次写入不可变基础工程；后续只原子替换有界的小草稿文件。
func (s *Store) Save(v Record, base *editor.Project) (Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || !validID(v.ID) || len(v.Fields) == 0 {
		return Record{}, errors.New("草稿身份或字段无效")
	}
	if err := ValidateFields(v.Fields); err != nil {
		return Record{}, err
	}
	count, total, err := s.usage()
	if err != nil {
		return Record{}, err
	}
	var root *os.Root
	created := false
	if base != nil {
		if count >= MaxRecords {
			return Record{}, errors.New("草稿已到50条上限，请先整理输入草稿")
		}
		data, err := projectstore.Encode(*base)
		if err != nil {
			return Record{}, err
		}
		if int64(len(data))+MaxDraftBytes > MaxTotalBytes-total {
			return Record{}, errors.New("草稿资料已到512MiB上限")
		}
		if err = s.root.Mkdir(v.ID, 0700); err != nil {
			return Record{}, err
		}
		created = true
		root, err = s.root.OpenRoot(v.ID)
		if err != nil {
			return Record{}, err
		}
		defer root.Close()
		defer func() {
			if created {
				_ = root.Remove("base.aividcut")
				_ = root.Remove("draft.json")
				_ = s.root.Remove(v.ID)
			}
		}()
		if err = write(root, "base.aividcut", data); err != nil {
			return Record{}, err
		}
		v.ProjectID = base.ID
		v.Name = base.Name
		v.BaseRevision = base.Revision
		v.BaseSHA256 = digest(data)
	} else {
		old, err := s.load(v.ID)
		if err != nil {
			return Record{}, err
		}
		if old.SHA256 != v.SHA256 {
			return Record{}, errors.New("草稿已变化，请重试")
		}
		root, err = s.directoryRoot(v.ID)
		if err != nil {
			return Record{}, err
		}
		defer root.Close()
		v.ProjectID = old.ProjectID
		v.Name = old.Name
		v.BaseRevision = old.BaseRevision
		v.BaseSHA256 = old.BaseSHA256
		v.Target = old.Target
		v.OriginalPath = old.OriginalPath
	}
	v.UpdatedAt = time.Now().UTC()
	v.SHA256 = ""
	data, err := json.Marshal(v)
	if err != nil || len(data) > MaxDraftBytes {
		return Record{}, errors.New("属性草稿超过256KiB限制，已有记录保留")
	}
	if total+int64(len(data)) > MaxTotalBytes {
		return Record{}, errors.New("属性草稿资料容量不足")
	}
	if err = write(root, "draft.json", data); err != nil {
		return Record{}, err
	}
	created = false
	v.SHA256 = digest(data)
	return v, nil
}
func (s *Store) Base(id, sha string) (editor.Project, Record, []string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, err := s.load(id)
	if err != nil || v.SHA256 != sha {
		return editor.Project{}, v, nil, errors.New("草稿已变化或无法读取，请刷新后重试")
	}
	root, err := s.directoryRoot(id)
	if err != nil {
		return editor.Project{}, v, nil, err
	}
	defer root.Close()
	data, err := read(root, "base.aividcut", projectstore.MaxProjectFileBytes)
	if err != nil || digest(data) != v.BaseSHA256 {
		return editor.Project{}, v, nil, errors.New("草稿基础工程未通过完整性核对")
	}
	p, w, err := projectstore.DecodeWithWarnings(data, filepath.Join(s.directory, id, "base.aividcut"))
	if err == nil && p.ID != v.ProjectID {
		err = errors.New("草稿基础工程归属不一致")
	}
	return p, v, w, err
}
func (s *Store) Delete(id, sha string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, err := s.load(id)
	if err != nil {
		return err
	}
	if v.SHA256 != sha {
		return errors.New("草稿已变化，请重新核对")
	}
	root, err := s.directoryRoot(id)
	if err != nil {
		return err
	}
	defer root.Close()
	f, err := root.Open(".")
	if err != nil {
		return err
	}
	rows, err := f.ReadDir(4)
	f.Close()
	if err != nil && err != io.EOF {
		return err
	}
	for _, row := range rows {
		if row.Name() != "base.aividcut" && row.Name() != "draft.json" {
			return errors.New("草稿包含未知资料，保留原目录")
		}
	}
	if err = root.Remove("draft.json"); err != nil {
		return err
	}
	if err = root.Remove("base.aividcut"); err != nil {
		return err
	}
	return s.root.Remove(id)
}

// Count 供恢复入口轻量提示，不读取属性正文或基础工程。
func (s *Store) Count() (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return 0, errors.New("草稿存储已关闭")
	}
	count, _, err := s.usage()
	return count, err
}
