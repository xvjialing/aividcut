package application

import (
	"aividcut/internal/editor"
	"aividcut/internal/previewproxy"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func proxyCacheFixture(t *testing.T) (*Service, PreviewProxyInput, string) {
	t.Helper()
	s, a, _ := sourceTranscriptFixture(t)
	s.tools.FFmpegPath, s.tools.FFprobePath = "/usr/bin/true", "/usr/bin/true"
	s.proxyToolInfo[0], _ = os.Stat(s.tools.FFmpegPath)
	s.proxyToolInfo[1], _ = os.Stat(s.tools.FFprobePath)
	b := s.GetBootstrap()
	input := PreviewProxyInput{ProjectID: b.Project.ID, SourceEpoch: b.SourceEpoch, AssetID: a.ID, Fingerprint: a.Fingerprint, Profile: "720p"}
	_, _, key, err := s.proxyContext(input)
	if err != nil {
		t.Fatal(err)
	}
	return s, input, key
}
func TestProxyCacheInvalidScopeUnsafeFilesAndBoundedBudget(t *testing.T) {
	s, input, key := proxyCacheFixture(t)
	defer s.Close()
	ctx := context.Background()
	old := input
	old.SourceEpoch++
	if _, err := s.GetPreviewProxy(ctx, old); err == nil {
		t.Fatal("旧实例读取代理")
	}
	s.proxyMu.Lock()
	path, meta, err := s.proxyPathsLocked(key)
	s.proxyMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(s.GetBootstrap().Project.Assets[0].Path, meta); err != nil {
		t.Fatal(err)
	}
	v, err := s.GetPreviewProxy(ctx, input)
	if err != nil || v.Status != "invalid" {
		t.Fatal("符号链接清单被接受", v, err)
	}
	if err = s.ClearPreviewProxy(ctx, input); err == nil {
		t.Fatal("清理越过普通文件保护")
	}
	os.Remove(meta)
	os.WriteFile(path, []byte("orphan"), 0600)
	v, err = s.GetPreviewProxy(ctx, input)
	if err != nil || v.Status != "invalid" {
		t.Fatal("未发布输出仍可用", v, err)
	}
	if err = s.ClearPreviewProxy(ctx, input); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 32; i++ {
		os.WriteFile(filepath.Join(filepath.Dir(path), fmt.Sprintf("%064x.mp4", i)), []byte("x"), 0600)
	}
	s.proxyMu.Lock()
	err = s.proxyBudgetLocked(1, "")
	s.proxyMu.Unlock()
	if err == nil {
		t.Fatal("缓存条目超过上界")
	}
}
func TestProxyManifestMutationInvalidatesWarmProof(t *testing.T) {
	s, input, key := proxyCacheFixture(t)
	defer s.Close()
	asset := s.GetBootstrap().Project.Assets[0]
	s.proxyMu.Lock()
	path, meta, err := s.proxyPathsLocked(key)
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(path, []byte("proxy"), 0600)
	m := previewproxy.Manifest{Version: previewproxy.Version, Key: key, SourceFingerprint: asset.Fingerprint, Profile: input.Profile, Proof: previewproxy.Proof{Frames: 1, Width: 320, Height: 180, TimelineSHA256: strings.Repeat("1", 64), ProxyFingerprint: "sha256:" + strings.Repeat("2", 64), Bytes: 5}}
	data, _ := json.Marshal(m)
	os.WriteFile(meta, data, 0600)
	a, _ := os.Stat(asset.Path)
	b, _ := os.Stat(path)
	s.proxyProofs = map[string]proxyFileProof{key: {epoch: input.SourceEpoch, manifest: m, source: a, derived: b}}
	s.proxyMu.Unlock()
	v, err := s.GetPreviewProxy(context.Background(), input)
	if err != nil || v.Status != "ready" {
		t.Fatal(v, err)
	}
	m.Proof.OriginUs = 1_000_000
	data, _ = json.Marshal(m)
	os.WriteFile(meta, data, 0600)
	v, err = s.GetPreviewProxy(context.Background(), input)
	if err != nil || v.Status != "cached" {
		t.Fatal("清单变更仍使用暖证明", v, err)
	}
}

func TestProxyCleanupWorksAfterToolRemoval(t *testing.T) {
	s, input, key := proxyCacheFixture(t)
	defer s.Close()
	s.proxyMu.Lock()
	path, _, err := s.proxyPathsLocked(key)
	s.proxyMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(path, []byte("unpublished"), 0600)
	s.tools.FFmpegPath = "/missing-tool"
	if err = s.ClearPreviewProxy(context.Background(), input); err != nil {
		t.Fatal("工具缺失使缓存无法清理", err)
	}
	if _, err = os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("未清理代理")
	}
}

func TestProxyCleanupRefusesRegisteredMediaSource(t *testing.T) {
	s, input, key := proxyCacheFixture(t)
	defer s.Close()
	s.proxyMu.Lock()
	path, _, err := s.proxyPathsLocked(key)
	s.proxyMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(path, []byte("cached media"), 0600)
	p := s.GetBootstrap().Project
	p.Assets[0].Path = path
	engine, err := editor.NewEngine(p)
	if err != nil {
		t.Fatal(err)
	}
	s.engine = engine
	if err = s.ClearPreviewProxy(context.Background(), input); err == nil {
		t.Fatal("清理删掉了当前已登记源文件")
	}
	if _, err = os.Stat(path); err != nil {
		t.Fatal("错误清理发生副作用")
	}
}

func TestProxyGlobalCleanupReleasesOrphansAndPreservesUnknownFiles(t *testing.T) {
	s, input, key := proxyCacheFixture(t)
	defer s.Close()
	s.proxyMu.Lock()
	path, _, err := s.proxyPathsLocked(key)
	s.proxyMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Dir(path)
	orphan := filepath.Join(dir, strings.Repeat("b", 64)+".mp4")
	meta := filepath.Join(dir, strings.Repeat("b", 64)+".json")
	unknown := filepath.Join(dir, "user-notes.txt")
	os.WriteFile(orphan, []byte("orphan proxy"), 0600)
	os.WriteFile(meta, []byte("damaged manifest"), 0600)
	os.WriteFile(unknown, []byte("keep"), 0600)
	input.ClearAll = true
	if err = s.ClearPreviewProxy(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(orphan); !os.IsNotExist(err) {
		t.Fatal("孤立代理未清理")
	}
	if _, err = os.Stat(meta); !os.IsNotExist(err) {
		t.Fatal("损坏但属于应用的清单未清理")
	}
	if _, err = os.Stat(unknown); err != nil {
		t.Fatal("未知用户文件被清理")
	}
}
func TestProxyGlobalCleanupRejectsActiveTasks(t *testing.T) {
	s, input, _ := proxyCacheFixture(t)
	defer s.Close()
	release := make(chan struct{})
	defer close(release)
	_, err := s.jobs.Start("export", "fixture", func(ctx context.Context, _ func(float64, string)) (string, error) {
		select {
		case <-release:
			return "", nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	input.ClearAll = true
	if err = s.ClearPreviewProxy(context.Background(), input); err == nil {
		t.Fatal("活跃导出任务期间清理全部代理")
	}
}
