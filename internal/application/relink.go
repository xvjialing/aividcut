package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"aividcut/internal/editor"
	"aividcut/internal/media"
	"aividcut/internal/projectfonts"
)

type relinkProbeFunc func(context.Context, string) (editor.Asset, error)

// RelinkAsset 恢复同一个素材的地址，不将不同内容伪装成重定位。
// 已知同指纹时保留原始业务元数据，锁定轨道也能恢复离线源；旧无指纹项目需通过类型/源范围校验。
func (s *Service) RelinkAsset(ctx context.Context, assetID, path string) (Bootstrap, error) {
	s.mu.Lock()
	tools := s.tools
	s.mu.Unlock()
	return s.relinkAssetWithProbe(ctx, assetID, path, tools.Probe)
}

func (s *Service) relinkAssetWithProbe(ctx context.Context, assetID, path string, probe relinkProbeFunc) (Bootstrap, error) {
	if err := ctx.Err(); err != nil {
		return Bootstrap{}, err
	}
	if strings.TrimSpace(assetID) == "" || strings.TrimSpace(path) == "" {
		return Bootstrap{}, errors.New("请选择需要重定位的素材和本地文件")
	}
	s.mu.Lock()
	engine := s.engine
	initial := engine.Snapshot()
	originalIndex := assetIndex(initial, assetID)
	if originalIndex < 0 {
		s.mu.Unlock()
		return Bootstrap{}, errors.New("需要重定位的素材不存在")
	}
	original := initial.Assets[originalIndex]
	tools := s.tools
	s.mu.Unlock()

	absolute, err := filepath.Abs(path)
	if err != nil {
		return Bootstrap{}, fmt.Errorf("重定位路径无效：%w", err)
	}
	before, err := os.Stat(absolute)
	if err != nil || !before.Mode().IsRegular() || before.Size() <= 0 {
		return Bootstrap{}, errors.New("重定位需要非空且可读取的本地普通文件")
	}
	var verified editor.Asset
	if original.Kind == "font" {
		file, err := projectfonts.Read(absolute)
		if err != nil {
			return Bootstrap{}, err
		}
		info := file.Info
		verified = original
		verified.Path = absolute
		verified.Fingerprint = "sha256:" + file.SHA256
		verified.Font = &info
	} else if original.Kind == "lut" {
		if _, err := media.ValidateCube(absolute); err != nil {
			return Bootstrap{}, fmt.Errorf("重定位 LUT 校验失败：%w", err)
		}
		fingerprint, err := fingerprintRelinkFile(ctx, absolute)
		if err != nil {
			return Bootstrap{}, fmt.Errorf("读取 LUT 指纹失败：%w", err)
		}
		verified = original
		verified.Fingerprint = fingerprint
		verified.Path = absolute
	} else {
		verified, err = probe(ctx, absolute)
		if err != nil {
			return Bootstrap{}, fmt.Errorf("重定位素材探测失败：%w", err)
		}
	}
	if err := ctx.Err(); err != nil {
		return Bootstrap{}, err
	}
	after, err := os.Stat(absolute)
	if err != nil || !after.Mode().IsRegular() || !os.SameFile(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return Bootstrap{}, errors.New("重定位读取期间文件发生变化，请重新选择并确认")
	}
	if verified.Kind != original.Kind {
		return Bootstrap{}, errors.New("所选文件与原素材类型不一致，不能作为重定位源")
	}
	if verified.Fingerprint == "" {
		return Bootstrap{}, errors.New("无法验证新文件内容指纹，未修改工程")
	}
	if original.Fingerprint != "" && verified.Fingerprint != original.Fingerprint {
		return Bootstrap{}, errors.New("所选文件指纹与原素材不一致；重定位只恢复同一内容，不会偷偷替换为不同素材")
	}
	var warnings []string
	if original.Fingerprint != "" {
		// 同内容SHA已验证，可沿用原解码计数；不能因普通Probe没有count而拒绝精确区间。
		verified.AudioSampleRate, verified.AudioSampleCount, verified.AudioStartUs = original.AudioSampleRate, original.AudioSampleCount, original.AudioStartUs
	} else if verified.HasAudio {
		budget, cancel := newImportAudioBudget(ctx)
		defer cancel()
		var warning string
		verified, warning, err = importedAudioEvidence(ctx, tools, verified, budget)
		if err != nil {
			return Bootstrap{}, err
		}
		if warning != "" {
			warnings = append(warnings, warning)
		}
	}
	verified.ID, verified.Name = original.ID, original.Name
	verified.Path, verified.Missing = absolute, false
	verified.URL, verified.ThumbnailURL = "", ""

	s.mu.Lock()
	defer s.mu.Unlock()
	current := s.engine.Snapshot()
	if s.engine != engine || current.ID != initial.ID {
		return Bootstrap{}, errors.New("重定位读取期间工程已切换，请在目标工程重新选择素材")
	}
	index := assetIndex(current, assetID)
	if index < 0 || !sameRelinkSource(original, current.Assets[index]) {
		return Bootstrap{}, errors.New("读取期间原素材已被修改或重定位，请刷新后重试")
	}
	// 使用最新工程的所有片段验证真实探测到的源边界，不能仅检查读取前的旧快照。
	validation := editor.CloneProject(current)
	validation.Assets[index] = verified
	if err := editor.ValidateProject(validation); err != nil {
		return Bootstrap{}, fmt.Errorf("重定位文件无法满足当前工程的全部素材引用：%w", err)
	}
	replacement := verified
	if original.Fingerprint != "" {
		// 同内容修复沿用原 ID、名称、时长、帧率等；只修地址、缺失状态和会话地址。
		replacement = current.Assets[index]
		replacement.Path, replacement.Missing = absolute, false
		replacement.URL, replacement.ThumbnailURL = "", ""
	}
	if err := ctx.Err(); err != nil {
		return Bootstrap{}, err
	}
	if _, err := s.engine.Apply(editor.EditRequest{BaseRevision: current.Revision, IdempotencyKey: editor.NewID("relink"), Label: "重定位素材", Commands: []editor.Command{{Type: "update_asset", Asset: &replacement}}}); err != nil {
		return Bootstrap{}, err
	}
	result := s.bootstrapLocked()
	result.Warnings = warnings
	return result, nil
}

func assetIndex(project editor.Project, id string) int {
	for i, asset := range project.Assets {
		if asset.ID == id {
			return i
		}
	}
	return -1
}

func sameRelinkSource(before, current editor.Asset) bool {
	before.URL, current.URL = "", ""
	before.ThumbnailURL, current.ThumbnailURL = "", ""
	return reflect.DeepEqual(before, current)
}

func fingerprintRelinkFile(ctx context.Context, path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	buffer := make([]byte, 128<<10)
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		n, err := file.Read(buffer)
		if n > 0 {
			_, _ = hash.Write(buffer[:n])
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", err
		}
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}
