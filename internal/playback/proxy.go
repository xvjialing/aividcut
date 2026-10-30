package playback

import (
	"aividcut/internal/editor"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

func validateVisualSources(project editor.Project, sources map[string]VisualSource, caps Capabilities) (map[string]VisualSource, error) {
	out := map[string]VisualSource{}
	if len(sources) == 0 {
		return out, nil
	}
	if len(sources) > 32 || !caps.SourceProxies.Supported {
		return nil, errors.New("当前worker不支持已核查源代理，请更新worker或关闭代理")
	}
	assets := map[string]editor.Asset{}
	for _, a := range project.Assets {
		assets[a.ID] = a
	}
	for id, v := range sources {
		a, ok := assets[id]
		fingerprint, err := hex.DecodeString(strings.TrimPrefix(v.Fingerprint, "sha256:"))
		if !ok || a.Kind != "video" || v.SourceFingerprint != a.Fingerprint || v.Path == a.Path || !filepath.IsAbs(v.Path) || strings.ContainsAny(v.Path, "\x00\r\n\t?") || strings.ToLower(filepath.Ext(v.Path)) != ".mp4" || !strings.HasPrefix(v.Fingerprint, "sha256:") || err != nil || len(fingerprint) != 32 || v.OriginUs < -86_400_000_000 || v.OriginUs > 86_400_000_000 {
			return nil, errors.New("画面代理与原素材不匹配")
		}
		info, err := os.Lstat(v.Path)
		if err != nil || !info.Mode().IsRegular() || info.Size() < 1 {
			return nil, errors.New("画面代理已缺失或不是普通文件")
		}
		out[id] = v
	}
	return out, nil
}
