// Package mediatools 只保存本机工具路径，不执行配置文件或搜索其它应用。
package mediatools

import (
	"aividcut/internal/statefilelock"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
)

const MaxConfigBytes = 32 << 10

type Config struct {
	UseBundled bool   `json:"useBundled,omitempty"`
	FFmpeg     string `json:"ffmpeg"`
	FFprobe    string `json:"ffprobe"`
	Worker     string `json:"worker"`
	Repository string `json:"repository"`
	Profiles   string `json:"profiles"`
}
type document struct {
	Version  int    `json:"version"`
	Settings Config `json:"settings"`
}
type Store struct {
	mu        sync.Mutex
	directory string
	lock      *statefilelock.Lock
	closed    bool
}

func Validate(input Config) error {
	if input.UseBundled && (input.FFmpeg != "" || input.FFprobe != "" || input.Worker != "" || input.Repository != "" || input.Profiles != "") {
		return errors.New("随包自动模式不能同时保存手动路径")
	}
	for _, path := range []string{input.FFmpeg, input.FFprobe, input.Worker, input.Repository, input.Profiles} {
		if len(path) > 4096 || strings.ContainsRune(path, 0) || strings.TrimSpace(path) != path || path != "" && !filepath.IsAbs(path) {
			return errors.New("工具设置需要不超过4096字节的完整本机路径，空值表示自动或未配置")
		}
	}
	return nil
}
func Open(directory string) (*Store, error) {
	if strings.TrimSpace(directory) == "" {
		return nil, errors.New("媒体设置目录不能为空")
	}
	lock, err := statefilelock.Acquire(filepath.Join(directory, ".lock"))
	if err != nil {
		return nil, err
	}
	return &Store{directory: directory, lock: lock}, nil
}
func (s *Store) Load() (Config, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return Config{}, "媒体设置已关闭"
	}
	path := filepath.Join(s.directory, "settings.json")
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return Config{UseBundled: true}, ""
	}
	if err != nil || !info.Mode().IsRegular() || info.Size() > MaxConfigBytes {
		return Config{}, "媒体设置文件不可读取或类型/大小无效，原资料已保留，请先保留资料并修复该文件后重新检测。"
	}
	file, err := os.Open(path)
	if err != nil {
		return Config{}, "媒体设置无法读取，原资料已保留。"
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, MaxConfigBytes+1))
	if err != nil || len(data) > MaxConfigBytes {
		return Config{}, "媒体设置读取失败或过大，原资料已保留。"
	}
	var value document
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&value) != nil || (value.Version != 1 && value.Version != 2) || (value.Version == 1 && value.Settings.UseBundled) || Validate(value.Settings) != nil {
		return Config{}, "媒体设置格式无效，已使用环境/PATH；原资料保留，可重新检测保存。"
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return Config{}, "媒体设置包含多余数据，原资料保留。"
	}
	return value.Settings, ""
}
func (s *Store) Save(settings Config) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("媒体设置已关闭")
	}
	if err := Validate(settings); err != nil {
		return err
	}
	path := filepath.Join(s.directory, "settings.json")
	if info, err := os.Lstat(path); err == nil && !info.Mode().IsRegular() {
		return errors.New("设置目标不是普通文件，未覆盖")
	}
	data, err := json.MarshalIndent(document{Version: 2, Settings: settings}, "", "  ")
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(s.directory, ".media-settings-*")
	if err != nil {
		return err
	}
	temp := file.Name()
	defer os.Remove(temp)
	if _, err = file.Write(append(data, '\n')); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(temp, path)
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
func Equal(a, b Config) bool { return reflect.DeepEqual(a, b) }

// Requested 不作隐式安装；用途固定的环境变量覆盖保存设置，PATH仍由工具发现器解析。
func Requested(saved Config, getenv func(string) string) (Config, map[string]string) {
	out := saved
	origins := map[string]string{}
	entries := []struct {
		key, env string
		value    *string
	}{{"ffmpeg", "AIVIDCUT_FFMPEG", &out.FFmpeg}, {"ffprobe", "AIVIDCUT_FFPROBE", &out.FFprobe}, {"worker", "AIVIDCUT_MLT_WORKER", &out.Worker}, {"repository", "AIVIDCUT_MLT_REPOSITORY", &out.Repository}, {"profiles", "AIVIDCUT_MLT_PROFILES", &out.Profiles}}
	for _, entry := range entries {
		if value := getenv(entry.env); value != "" {
			*entry.value = value
			origins[entry.key] = "environment"
		} else if *entry.value != "" {
			origins[entry.key] = "saved"
		} else if entry.key == "ffmpeg" || entry.key == "ffprobe" {
			origins[entry.key] = "path"
		} else {
			origins[entry.key] = "unset"
		}
	}
	return out, origins
}

// Stamp 绑定原始设置文件，损坏内容也不能被另一个窗口的旧检测静默覆盖。
func (s *Store) Stamp() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return "", errors.New("媒体设置已关闭")
	}
	path := filepath.Join(s.directory, "settings.json")
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return "missing", nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Size() > MaxConfigBytes {
		return "", errors.New("媒体设置原文件不可确认，请先保留资料后修复")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, MaxConfigBytes+1))
	if err != nil || len(data) > MaxConfigBytes {
		return "", errors.New("媒体设置原文件无法读取")
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}
