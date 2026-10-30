package generation

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

const MaxReferenceBytes int64 = 50_000_000

type RegisteredFile struct {
	ID             string
	Path           string
	ExpectedSHA256 string
}

type registeredFile struct {
	original, resolved, expectedHash string
	info                             os.FileInfo
}
type FileReferences struct{ files map[string]registeredFile }

// NewFileReferences 仅登记调用方已经授权的具体文件，不接收 HTTP URL 或任意目录漫游。
func NewFileReferences(files []RegisteredFile) (*FileReferences, error) {
	result := &FileReferences{files: make(map[string]registeredFile)}
	for _, value := range files {
		if value.ID == "" || value.Path == "" {
			return nil, failure("reference", "素材登记需要标识和本地路径")
		}
		if _, exists := result.files[value.ID]; exists {
			return nil, failure("reference", "素材标识重复")
		}
		absolute, err := filepath.Abs(value.Path)
		if err != nil {
			return nil, failure("reference", "素材路径无效")
		}
		resolved, err := filepath.EvalSymlinks(absolute)
		if err != nil {
			return nil, failure("reference", "参考素材不存在或无法读取")
		}
		info, err := os.Stat(resolved)
		if err != nil || !info.Mode().IsRegular() {
			return nil, failure("reference", "参考素材必须是普通本地文件")
		}
		result.files[value.ID] = registeredFile{original: absolute, resolved: resolved, expectedHash: value.ExpectedSHA256, info: info}
	}
	return result, nil
}

func (source *FileReferences) Read(ctx context.Context, reference Reference) (ReferenceData, error) {
	if err := ctx.Err(); err != nil {
		return ReferenceData{}, err
	}
	file, exists := source.files[reference.AssetID]
	if !exists {
		return ReferenceData{}, failure("reference_scope", "参考素材不在已授权的文件集合中")
	}
	resolved, err := filepath.EvalSymlinks(file.original)
	if err != nil || resolved != file.resolved {
		return ReferenceData{}, failure("reference_changed", "参考素材链接已变化，请重新导入或确认")
	}
	f, err := os.Open(resolved)
	if err != nil {
		return ReferenceData{}, failure("reference", "无法读取参考素材")
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() >= MaxReferenceBytes {
		return ReferenceData{}, failure("reference_size", "参考素材为空或达到 50MB 限制")
	}
	if !os.SameFile(file.info, info) || info.Size() != file.info.Size() || !info.ModTime().Equal(file.info.ModTime()) {
		return ReferenceData{}, failure("reference_changed", "参考文件已被替换或修改，请重新确认")
	}
	data, err := io.ReadAll(io.LimitReader(f, MaxReferenceBytes+1))
	if err != nil || len(data) == 0 || int64(len(data)) >= MaxReferenceBytes {
		return ReferenceData{}, failure("reference_size", "参考素材读取失败或超过大小限制")
	}
	if err := ctx.Err(); err != nil {
		return ReferenceData{}, err
	}
	mime := http.DetectContentType(data)
	// 本轮只开放可由标准库验证尺寸与完整性的 PNG/JPEG 参考图。
	if mime != "image/png" && mime != "image/jpeg" {
		return ReferenceData{}, failure("reference_type", "当前参考适配器仅支持 PNG/JPEG 图片，不会把本地 URL 发送到云端")
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width <= 0 || config.Height <= 0 || int64(config.Width)*int64(config.Height) > 32_000_000 {
		return ReferenceData{}, failure("reference_image", "参考图像格式或尺寸无效")
	}
	if _, _, err := image.Decode(bytes.NewReader(data)); err != nil {
		return ReferenceData{}, failure("reference_image", "参考图片内容不完整")
	}
	hash := sha256.Sum256(data)
	digest := hex.EncodeToString(hash[:])
	if file.expectedHash != "" && digest != file.expectedHash {
		return ReferenceData{}, failure("reference_changed", "参考素材内容已变化，请重新确认")
	}
	return ReferenceData{AssetID: reference.AssetID, Role: reference.Role, MIME: mime, Data: data, SHA256: digest, Width: config.Width, Height: config.Height}, nil
}

func checkedReference(value ReferenceData) (ReferenceData, error) {
	if len(value.Data) == 0 || int64(len(value.Data)) >= MaxReferenceBytes {
		return ReferenceData{}, failure("reference_size", "参考图片大小无效")
	}
	value.Data = append([]byte(nil), value.Data...)
	mime := http.DetectContentType(value.Data)
	if (mime != "image/png" && mime != "image/jpeg") || (value.MIME != "" && value.MIME != mime) {
		return ReferenceData{}, failure("reference_type", "参考图片类型无效")
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(value.Data))
	if err != nil || config.Width <= 0 || config.Height <= 0 || int64(config.Width)*int64(config.Height) > 32_000_000 {
		return ReferenceData{}, failure("reference_image", "参考图片尺寸无效")
	}
	hash := sha256.Sum256(value.Data)
	digest := hex.EncodeToString(hash[:])
	if value.SHA256 != "" && value.SHA256 != digest {
		return ReferenceData{}, failure("reference_changed", "参考图片指纹不一致")
	}
	value.MIME, value.Width, value.Height, value.SHA256 = mime, config.Width, config.Height, digest
	return value, nil
}
