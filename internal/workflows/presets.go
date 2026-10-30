package workflows

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"aividcut/internal/editor"
)

type PresetStore struct {
	mu      sync.Mutex
	path    string
	entries map[string]Preset
}

func NewPresetStore(path string) (*PresetStore, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("预设资料路径不能为空")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	store := &PresetStore{path: abs, entries: map[string]Preset{}}
	info, err := os.Lstat(abs)
	if errors.Is(err, os.ErrNotExist) {
		return store, nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Size() > 2<<20 {
		return nil, errors.New("预设文件不是有效普通文件或超过限制")
	}
	file, err := os.Open(abs)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, (2<<20)+1))
	if err != nil || len(data) > 2<<20 {
		return nil, errors.New("预设内容读取失败或超过限制")
	}
	var saved struct {
		Version int      `json:"version"`
		Presets []Preset `json:"presets"`
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if d.Decode(&saved) != nil {
		return nil, errors.New("预设资料格式无效")
	}
	var extra any
	if d.Decode(&extra) != io.EOF || saved.Version != 1 || len(saved.Presets) > 500 {
		return nil, errors.New("预设资料版本、内容或数量无效")
	}
	for _, p := range saved.Presets {
		def, err := definition(p.WorkflowID)
		if err != nil || !strings.HasPrefix(p.ID, "preset-") || store.entries[p.ID].ID != "" || !validPresetName(p.Name) || p.Version < 1 || p.WorkflowVersion != def.Version || p.CreatedAt.IsZero() || p.UpdatedAt.Before(p.CreatedAt) {
			return nil, errors.New("预设身份、版本或名称无效")
		}
		normalized, err := normalizeParameters(def, p.Parameters, false)
		if err != nil {
			return nil, err
		}
		p.Parameters = normalized
		store.entries[p.ID] = p
	}
	return store, nil
}
func validPresetName(name string) bool {
	if strings.TrimSpace(name) == "" || !utf8.ValidString(name) || utf8.RuneCountInString(name) > 120 {
		return false
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}
func clonePreset(p Preset) Preset {
	data, _ := json.Marshal(p)
	var out Preset
	_ = json.Unmarshal(data, &out)
	return out
}
func (s *PresetStore) List() []Preset {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []Preset{}
	for _, p := range s.entries {
		out = append(out, clonePreset(p))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out
}
func (s *PresetStore) Get(id string) (Preset, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.entries[id]
	if !ok {
		return Preset{}, errors.New("预设不存在")
	}
	return clonePreset(p), nil
}
func (s *PresetStore) write(values map[string]Preset) error {
	saved := struct {
		Version int      `json:"version"`
		Presets []Preset `json:"presets"`
	}{Version: 1, Presets: []Preset{}}
	for _, p := range values {
		saved.Presets = append(saved.Presets, p)
	}
	sort.Slice(saved.Presets, func(i, j int) bool { return saved.Presets[i].ID < saved.Presets[j].ID })
	data, err := json.Marshal(saved)
	if err != nil || len(data) > 2<<20 {
		return errors.New("预设内容超过保存限制")
	}
	if err = os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		return err
	}
	if info, err := os.Lstat(s.path); err == nil && !info.Mode().IsRegular() {
		return errors.New("预设目标不是普通文件")
	}
	f, err := os.CreateTemp(filepath.Dir(s.path), ".workflow-presets-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), s.path)
}
func (s *PresetStore) Save(ctx context.Context, input PresetInput) (Preset, error) {
	if err := ctx.Err(); err != nil {
		return Preset{}, err
	}
	input.Name = strings.TrimSpace(input.Name)
	if !validPresetName(input.Name) {
		return Preset{}, errors.New("预设名称需为1至120字的有效文字")
	}
	def, err := definition(input.WorkflowID)
	if err != nil {
		return Preset{}, err
	}
	parameters, err := normalizeParameters(def, input.Parameters, false)
	if err != nil {
		return Preset{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return Preset{}, err
	}
	now := time.Now().UTC()
	p := Preset{ID: input.ID, Name: input.Name, WorkflowID: input.WorkflowID, WorkflowVersion: def.Version, Parameters: parameters, Version: 1, CreatedAt: now, UpdatedAt: now}
	if p.ID == "" {
		if len(s.entries) >= 500 {
			return Preset{}, errors.New("预设已达到500份上限")
		}
		if input.BaseVersion != 0 {
			return Preset{}, errors.New("新预设不能带旧版本")
		}
		p.ID = editor.NewID("preset")
	} else {
		old, ok := s.entries[p.ID]
		if !ok {
			return Preset{}, errors.New("不能覆盖内置或不存在的预设")
		}
		if old.Version != input.BaseVersion || old.Version == math.MaxInt64 {
			return Preset{}, errors.New("预设版本已经变化，请重新读取")
		}
		p.CreatedAt, p.Version = old.CreatedAt, old.Version+1
		if p.UpdatedAt.Before(old.UpdatedAt) {
			p.UpdatedAt = old.UpdatedAt
		}
	}
	values := map[string]Preset{}
	for id, value := range s.entries {
		values[id] = value
	}
	values[p.ID] = p
	if err := s.write(values); err != nil {
		return Preset{}, err
	}
	s.entries = values
	return clonePreset(p), nil
}
func (s *PresetStore) Delete(ctx context.Context, id string, version int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	p, ok := s.entries[id]
	if !ok {
		return errors.New("不能删除内置或不存在的预设")
	}
	if p.Version != version {
		return errors.New("预设版本已变化，请重新读取")
	}
	values := map[string]Preset{}
	for key, value := range s.entries {
		if key != id {
			values[key] = value
		}
	}
	if err := s.write(values); err != nil {
		return err
	}
	s.entries = values
	return nil
}
