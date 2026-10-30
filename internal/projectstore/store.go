package projectstore

import (
	"aividcut/internal/editor"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// 首轮工程格式为可移植 JSON 快照；不会把它标记为 SQLite 或持久撤销日志。
const MaxProjectFileBytes int64 = 64 << 20

// Encode 统一普通保存、版本与收集副本的完整格式、大小和会话地址清理规则。
func Encode(project editor.Project) ([]byte, error) {
	if project.ActiveSequenceProjection {
		return nil, fmt.Errorf("当前序列读模型不包含其它序列，不能保存为完整工程，请通过工程保存接口操作")
	}
	if err := editor.ValidateProject(project); err != nil {
		return nil, fmt.Errorf("保存失败：%w", err)
	}
	value := editor.CloneProject(project)
	for i := range value.Assets {
		// 会话中的访问地址可能包含临时令牌，不进入工程文件。
		value.Assets[i].URL = ""
		value.Assets[i].ThumbnailURL = ""
	}
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("工程序列化失败：%w", err)
	}
	encoded = append(encoded, '\n')
	if int64(len(encoded)) > MaxProjectFileBytes {
		return nil, fmt.Errorf("工程超过当前支持的 64MiB 文件大小")
	}
	return encoded, nil
}

func Save(path string, project editor.Project) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("保存失败：工程路径为空")
	}
	encoded, err := Encode(project)
	if err != nil {
		return err
	}
	parent := filepath.Dir(path)
	if err := os.MkdirAll(parent, 0700); err != nil {
		return fmt.Errorf("创建工程目录失败：%w", err)
	}
	file, err := os.CreateTemp(parent, ".aividcut-save-*")
	if err != nil {
		return fmt.Errorf("创建工程临时文件失败：%w", err)
	}
	temp := file.Name()
	defer os.Remove(temp)
	if _, err := file.Write(encoded); err != nil {
		file.Close()
		return fmt.Errorf("写入工程失败：%w", err)
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return fmt.Errorf("同步工程失败：%w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("关闭工程临时文件失败：%w", err)
	}
	if err := os.Rename(temp, path); err != nil {
		return fmt.Errorf("替换工程文件失败，原文件未主动删除：%w", err)
	}
	return nil
}

func Load(path string) (editor.Project, error) { p, _, err := LoadWithWarnings(path); return p, err }
func LoadWithWarnings(path string) (editor.Project, []string, error) {
	file, err := os.Open(path)
	if err != nil {
		return editor.Project{}, nil, fmt.Errorf("打开工程失败：%w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return editor.Project{}, nil, fmt.Errorf("工程路径不是可读取的普通文件")
	}
	if info.Size() > MaxProjectFileBytes {
		return editor.Project{}, nil, fmt.Errorf("工程超过当前支持的 64MiB 文件大小")
	}
	contents, err := io.ReadAll(io.LimitReader(file, MaxProjectFileBytes+1))
	if err != nil || int64(len(contents)) > MaxProjectFileBytes {
		return editor.Project{}, nil, fmt.Errorf("读取工程失败或文件超过大小限制")
	}
	return DecodeWithWarnings(contents, path)
}

// DecodeWithWarnings 供已校验内容哈希的版本快照复用同一解析器，避免检查后重新读到其它内容。
func DecodeWithWarnings(contents []byte, path string) (editor.Project, []string, error) {
	if int64(len(contents)) > MaxProjectFileBytes {
		return editor.Project{}, nil, fmt.Errorf("工程超过当前支持的 64MiB 文件大小")
	}
	var project editor.Project
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&project); err != nil {
		return editor.Project{}, nil, fmt.Errorf("工程格式无效：%w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return editor.Project{}, nil, fmt.Errorf("工程包含多余数据")
	}
	originalVersion := project.SchemaVersion
	if project.ActiveSequenceProjection {
		return editor.Project{}, nil, fmt.Errorf("此文件是当前序列读模型，不是完整工程")
	}
	project, err := editor.UpgradeProject(project)
	if err != nil {
		return editor.Project{}, nil, fmt.Errorf("工程校验失败：%w", err)
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return editor.Project{}, nil, fmt.Errorf("解析工程路径失败：%w", err)
	}
	for i := range project.Assets {
		asset := &project.Assets[i]
		asset.URL, asset.ThumbnailURL = "", ""
		if !filepath.IsAbs(asset.Path) {
			asset.Path = filepath.Clean(filepath.Join(filepath.Dir(absolute), asset.Path))
		}
		info, err := os.Stat(asset.Path)
		asset.Missing = err != nil || !info.Mode().IsRegular()
	}
	warnings := []string{}
	if originalVersion < editor.SchemaVersion {
		warnings = append(warnings, fmt.Sprintf("旧版工程已在内存升级为v%d；保存后旧客户端不能读取新格式，原文件尚未自动改写。", editor.SchemaVersion))
	}
	return project, warnings, nil
}
