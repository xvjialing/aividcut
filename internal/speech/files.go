package speech

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type RegisteredFile struct {
	ID             string
	Path           string
	Kind           string
	DurationUs     int64
	ExpectedSHA256 string
}
type registeredFile struct {
	value    RegisteredFile
	resolved string
	info     os.FileInfo
}
type FileSource struct{ files map[string]registeredFile }

// NewFileSource 只登记应用已经授权的具体文件；不支持目录漫游和 URL。
func NewFileSource(files []RegisteredFile) (*FileSource, error) {
	source := &FileSource{files: map[string]registeredFile{}}
	for _, value := range files {
		hasExpectedHash := value.ExpectedSHA256 != ""
		value.ExpectedSHA256 = strings.TrimPrefix(strings.ToLower(value.ExpectedSHA256), "sha256:")
		if !safeID.MatchString(value.ID) || !filepath.IsAbs(value.Path) || value.DurationUs < 0 || value.Kind != "audio" && value.Kind != "video" {
			return nil, failure("file_registration", "语音素材登记需要有效标识、绝对本地路径及音频/视频类型")
		}
		if _, found := source.files[value.ID]; found {
			return nil, failure("file_registration", "语音素材标识重复")
		}
		if hasExpectedHash && !sha256Pattern.MatchString(value.ExpectedSHA256) {
			return nil, failure("file_registration", "素材指纹格式无效")
		}
		resolved, err := filepath.EvalSymlinks(value.Path)
		if err != nil {
			return nil, failure("file_registration", "已授权语音素材无法读取")
		}
		info, err := os.Stat(resolved)
		if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > MaxInputBytes {
			return nil, failure("file_registration", "语音素材必须是非空且不超过64MiB的本地普通文件")
		}
		source.files[value.ID] = registeredFile{value: value, resolved: resolved, info: info}
	}
	return source, nil
}

type snapshotReader struct {
	*os.File
	directory string
	once      sync.Once
	closeErr  error
}

func (s *snapshotReader) Close() error {
	s.once.Do(func() { s.closeErr = s.File.Close(); _ = os.RemoveAll(s.directory) })
	return s.closeErr
}

func (source *FileSource) Open(ctx context.Context, id string) (*InputFile, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	value, found := source.files[id]
	if !found {
		return nil, failure("file_scope", "语音素材不在本次已授权的文件集合中")
	}
	resolved, err := filepath.EvalSymlinks(value.value.Path)
	if err != nil || resolved != value.resolved {
		return nil, failure("file_changed", "素材链接已变化，请重新确认")
	}
	f, err := os.Open(resolved)
	if err != nil {
		return nil, failure("file_read", "无法打开已授权语音素材")
	}
	defer f.Close()
	before, err := f.Stat()
	if err != nil || !sameFileSnapshot(value.info, before) {
		return nil, failure("file_changed", "已授权素材已被替换或修改，请重新确认")
	}
	header := make([]byte, 512)
	n, err := f.Read(header)
	if err != nil && err != io.EOF {
		return nil, failure("file_read", "无法读取语音素材头部")
	}
	mime := mediaMIME(header[:n], value.value.Kind)
	if mime == "" {
		return nil, failure("file_type", "当前语音素材仅支持已识别的WAV、MP3、FLAC、Ogg、MP4/M4A、WebM、AVI及AIFF容器")
	}
	if _, err = f.Seek(0, io.SeekStart); err != nil {
		return nil, failure("file_read", "语音素材不能重新定位")
	}
	directory, err := os.MkdirTemp("", "aividcut-speech-source-")
	if err != nil {
		return nil, failure("file_snapshot", "无法创建私有语音素材快照")
	}
	snapshot, err := os.OpenFile(filepath.Join(directory, "input"), os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		_ = os.RemoveAll(directory)
		return nil, failure("file_snapshot", "无法创建私有语音素材快照")
	}
	success := false
	defer func() {
		if !success {
			_ = snapshot.Close()
			_ = os.RemoveAll(directory)
		}
	}()
	hash := sha256.New()
	written, err := io.CopyBuffer(io.MultiWriter(snapshot, hash), &contextReader{ctx: ctx, reader: io.LimitReader(f, MaxInputBytes+1)}, make([]byte, 32<<10))
	if err != nil {
		return nil, &Error{Code: "file_read", Message: "语音素材快照读取失败或已取消", Cause: err}
	}
	after, err := f.Stat()
	if err != nil || written != before.Size() || written > MaxInputBytes || !sameFileSnapshot(before, after) {
		return nil, failure("file_changed", "读取期间语音素材发生变化或超过限制")
	}
	digest := hex.EncodeToString(hash.Sum(nil))
	if value.value.ExpectedSHA256 != "" && !strings.EqualFold(value.value.ExpectedSHA256, digest) {
		return nil, failure("file_changed", "语音素材内容与登记指纹不一致")
	}
	if _, err = snapshot.Seek(0, io.SeekStart); err != nil {
		return nil, failure("file_snapshot", "无法读取私有素材快照")
	}
	success = true
	return &InputFile{AssetID: id, MIME: mime, Kind: value.value.Kind, SizeBytes: written, DurationUs: value.value.DurationUs, SHA256: digest, Reader: &snapshotReader{File: snapshot, directory: directory}}, nil
}

func sameFileSnapshot(before, after os.FileInfo) bool {
	return after.Mode().IsRegular() && os.SameFile(before, after) && before.Size() == after.Size() && before.ModTime().Equal(after.ModTime())
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *contextReader) Read(data []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(data)
}

func mediaMIME(data []byte, kind string) string {
	if len(data) >= 12 && string(data[:4]) == "RIFF" {
		if string(data[8:12]) == "WAVE" && kind == "audio" {
			return "audio/wav"
		}
		if string(data[8:12]) == "AVI " && kind == "video" {
			return "video/x-msvideo"
		}
	}
	if len(data) >= 4 && string(data[:4]) == "fLaC" && kind == "audio" {
		return "audio/flac"
	}
	if len(data) >= 3 && string(data[:3]) == "ID3" && kind == "audio" || len(data) >= 4 && data[0] == 0xff && data[1]&0xe0 == 0xe0 && data[1]&0x06 != 0 && data[1]&0x18 != 0x08 && kind == "audio" {
		return "audio/mpeg"
	}
	if len(data) >= 4 && string(data[:4]) == "OggS" {
		if kind == "audio" {
			return "audio/ogg"
		}
		return "video/ogg"
	}
	if len(data) >= 12 && string(data[4:8]) == "ftyp" {
		if kind == "audio" {
			return "audio/mp4"
		}
		return "video/mp4"
	}
	if len(data) >= 4 && data[0] == 0x1a && data[1] == 0x45 && data[2] == 0xdf && data[3] == 0xa3 {
		if kind == "audio" {
			return "audio/webm"
		}
		return "video/webm"
	}
	if len(data) >= 12 && string(data[:4]) == "FORM" && (string(data[8:12]) == "AIFF" || string(data[8:12]) == "AIFC") && kind == "audio" {
		return "audio/aiff"
	}
	return ""
}
