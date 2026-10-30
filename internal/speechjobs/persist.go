package speechjobs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var safeID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)
var operationID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,160}$`)

func hashValue(fingerprint string) string {
	value := strings.TrimPrefix(fingerprint, "sha256:")
	data, err := hex.DecodeString(value)
	if err != nil || len(data) != 32 {
		return ""
	}
	return strings.ToLower(value)
}

func signature(projectID string, input Input, sources []sourceSignature) string {
	input.Consent = false
	data, _ := json.Marshal(struct {
		ProjectID string
		Input     Input
		Sources   []sourceSignature
	}{projectID, input, sources})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

type sourceSignature struct {
	ID, Kind, Fingerprint string
	DurationUs            int64
}

func recordSignature(value record) string {
	refs := make([]sourceSignature, 0, len(value.Sources))
	for _, source := range value.Sources {
		refs = append(refs, sourceSignature{source.ID, source.Kind, hashValue(source.Fingerprint), source.DurationUs})
	}
	return signature(value.Summary.ProjectID, value.Input, refs)
}

func atomicWrite(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	if info, err := os.Lstat(path); err == nil && !info.Mode().IsRegular() {
		return errors.New("目标不是普通文件")
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".speech-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
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
	return os.Rename(file.Name(), path)
}

func (m *Manager) writeJSON(path string, value any, limit int) error {
	data, err := json.Marshal(value)
	if err != nil || len(data) > limit {
		return errors.New("语音记录无法编码或超过保存上限")
	}
	if err := m.writeFile(path, data); err != nil {
		return errors.New("语音结果或任务状态保存失败，请保留数据目录后重试本地保存")
	}
	return nil
}

func readJSON(path string, value any, limit int64) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() > limit {
		return errors.New("语音记录不是有效普通文件或超过大小限制")
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, limit+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return errors.New("语音记录格式无效")
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return errors.New("语音记录包含多余数据")
	}
	return nil
}

func fileHash(ctx context.Context, path string, limit int64) (string, int64, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > limit {
		return "", 0, errors.New("语音文件缺失、类型或大小无效")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", 0, errors.New("无法读取语音文件")
	}
	defer file.Close()
	hash := sha256.New()
	buffer := make([]byte, 64<<10)
	var size int64
	for {
		if err := ctx.Err(); err != nil {
			return "", 0, err
		}
		n, readErr := file.Read(buffer)
		if n > 0 {
			size += int64(n)
			if size > limit {
				return "", 0, errors.New("语音文件超过限制")
			}
			_, _ = hash.Write(buffer[:n])
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return "", 0, errors.New("语音文件读取失败")
		}
	}
	after, err := file.Stat()
	if err != nil || !os.SameFile(info, after) || info.Size() != size || info.Size() != after.Size() || !info.ModTime().Equal(after.ModTime()) {
		return "", 0, errors.New("语音文件读取期间发生变化")
	}
	return hex.EncodeToString(hash.Sum(nil)), size, nil
}

func within(base, path string) bool {
	if !filepath.IsAbs(path) {
		return false
	}
	rel, err := filepath.Rel(base, path)
	return err == nil && rel != "." && rel != ".." && !filepath.IsAbs(rel) && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func cloneJSON[T any](value T) T {
	data, _ := json.Marshal(value)
	var copy T
	_ = json.Unmarshal(data, &copy)
	return copy
}

func copyAudio(value *speechAudio) *speechAudio {
	if value == nil {
		return nil
	}
	copy := *value
	// 字节仅由当前 active worker 只读使用，不再复制一份最多64MiB的响应。
	return &copy
}
