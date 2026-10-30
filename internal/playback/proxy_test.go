package playback

import (
	"aividcut/internal/editor"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProxyCompilationKeepsOriginalAudioAndSourcePhase(t *testing.T) {
	p := editor.NewProject("代理编译")
	p.Settings.Width, p.Settings.Height = 320, 180
	source := filepath.Join(t.TempDir(), "source.mp4")
	derived := filepath.Join(t.TempDir(), "proxy.mp4")
	os.WriteFile(source, []byte("source"), 0600)
	os.WriteFile(derived, []byte("proxy"), 0600)
	a := editor.Asset{ID: "v", Name: "源", Kind: "video", Path: source, Fingerprint: "sha256:" + strings.Repeat("a", 64), Width: 1920, Height: 1080, DurationUs: 4_000_000, HasAudio: true}
	p.Assets = []editor.Asset{a}
	v := editor.DefaultClip("v1", "track-video-1", "video")
	v.AssetID = a.ID
	v.DurationFrames = 60
	v.SourceInUs = 137000
	v.SpeedNum, v.SpeedDen = 3, 2
	p.Clips = []editor.Clip{v}
	caps := graphTestCaps()
	caps.SourceProxies = Feature{Supported: true}
	proxy := VisualSource{Path: derived, Fingerprint: "sha256:" + strings.Repeat("b", 64), SourceFingerprint: a.Fingerprint, OriginUs: -1000}
	sources := map[string]VisualSource{a.ID: proxy}
	if _, err := validateVisualSources(p, sources, caps); err != nil {
		t.Fatal(err)
	}
	xml, _, err := compileScaledSelection(p, p.Clips, caps, 0, false, 1, sources)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(xml, prop("resource", derived)) || !strings.Contains(xml, prop("resource", source)) || !strings.Contains(xml, prop("source_origin_us", "-1000")) || !strings.Contains(xml, prop("source_in_us", "137000")) {
		t.Fatal("代理改变音频路径/源相位或遗漏原格式起点")
	}
	caps.SourceProxies = Feature{}
	if _, err := validateVisualSources(p, sources, caps); err == nil {
		t.Fatal("旧worker仍可接受代理")
	}
	if _, err := Start(context.Background(), Config{OfflineRender: true, VisualSources: sources}); err == nil || !strings.Contains(err.Error(), "代理") {
		t.Fatal("独立离线会话接受画面代理", err)
	}
	proxy.SourceFingerprint = "wrong"
	sources[a.ID] = proxy
	caps.SourceProxies.Supported = true
	if _, err := validateVisualSources(p, sources, caps); err == nil {
		t.Fatal("其它源代理被接受")
	}
}
