package application

import (
	"aividcut/internal/editor"
	"aividcut/internal/media"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func shotFixture(t *testing.T) (*Service, ShotIndexInput, string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("本用例是Unix进程夹具；Windows原生取消与缓存需实机验证")
	}
	dir := t.TempDir()
	tool := filepath.Join(dir, "fake-ffmpeg")
	script := `#!/bin/sh
if [ "$1" = "-version" ]; then echo 'ffmpeg version scene-fixture'; exit 0; fi
if [ "$1" = "-hide_banner" ] && [ "$2" = "-filters" ]; then echo ' ... scdet V->V scene detection'; exit 0; fi
case "$*" in *-filter_complex*) printf x >> "$0.count";; esac
if [ -f "$0.slow" ]; then sleep 2; fi
printf '[showinfo] n: 0 pts: 0 pts_time:0 duration:1\n' >&2
printf 'lavfi.scd.time=1\nframe=50\nout_time_us=2000000\nprogress=end\n'
`
	if err := os.WriteFile(tool, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(dir, "registered.bin")
	data := []byte("local source identity, detector is explicit fixture")
	os.WriteFile(source, data, 0600)
	hash := sha256.Sum256(data)
	s, err := New(media.Toolchain{FFmpegPath: tool}, filepath.Join(dir, "cache"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	p := editor.NewProject("镜头缓存夹具")
	p.Assets = []editor.Asset{{ID: "video", Name: "本地源", Kind: "video", Path: source, Fingerprint: fmt.Sprintf("sha256:%x", hash), DurationUs: 2_000_000, Width: 640, Height: 360, FPSNum: 25, FPSDen: 1}}
	s.engine, err = editor.NewEngine(p)
	if err != nil {
		t.Fatal(err)
	}
	s.savedHash = projectHash(p)
	return s, ShotIndexInput{ProjectID: p.ID, AssetID: "video", Fingerprint: p.Assets[0].Fingerprint, Threshold: 10, MinDurationUs: 250000}, source
}
func awaitShots(t *testing.T, s *Service, input ShotIndexInput) ShotIndexView {
	t.Helper()
	until := time.Now().Add(3 * time.Second)
	for time.Now().Before(until) {
		view, err := s.GetShotIndex(context.Background(), input)
		if err != nil {
			t.Fatal(err)
		}
		if view.Status == "ready" {
			return view
		}
		if view.Status == "failed" {
			t.Fatal(view.Reason)
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("镜头任务未完成")
	return ShotIndexView{}
}
func TestShotIndexBackgroundDedupCacheVerifyAndNoProjectEdit(t *testing.T) {
	s, input, source := shotFixture(t)
	ctx := context.Background()
	initial, err := s.GetShotIndex(ctx, input)
	if err != nil || initial.Status != "missing" {
		t.Fatal("没有扫描就出现结果", err)
	}
	first, err := s.StartShotIndex(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.StartShotIndex(ctx, input)
	if err != nil || second.Status == "running" && second.Job.ID != first.Job.ID {
		t.Fatal("重复启动产生不同扫描任务", err)
	}
	ready := awaitShots(t, s, input)
	if len(ready.Index.Shots) != 2 || s.GetBootstrap().Dirty || s.GetBootstrap().Project.Revision != 0 {
		t.Fatal("检测改工程或结果错误")
	}
	s.resetThumbnails(false)
	old := input
	if _, err = s.GetShotIndex(ctx, old); err == nil {
		t.Fatal("旧实例仍可读索引")
	}
	input.SourceEpoch = s.GetBootstrap().SourceEpoch
	cached, err := s.GetShotIndex(ctx, input)
	if err != nil || cached.Status != "cached" || cached.Index != nil {
		t.Fatal("没有重新核对来源就复用旧缓存", err)
	}
	if _, err = s.StartShotIndex(ctx, input); err != nil {
		t.Fatal(err)
	}
	awaitShots(t, s, input)
	count, err := os.ReadFile(s.tools.FFmpegPath + ".count")
	if err != nil || string(count) != "x" {
		t.Fatal("核对已有缓存仍重复解码", err)
	}
	if err = os.WriteFile(source, []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	cached, err = s.GetShotIndex(ctx, input)
	if err != nil || cached.Status != "cached" {
		t.Fatal("文件变化后仍给已核对结果", err)
	}
	if _, err = s.StartShotIndex(ctx, input); err != nil {
		t.Fatal(err)
	}
	until := time.Now().Add(time.Second)
	for time.Now().Before(until) {
		view, _ := s.GetShotIndex(ctx, input)
		if view.Status == "failed" {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("文件指纹变化未失败")
}
func TestMCPShotIndexMissingDoesNotStartDecodeAndScopeRevokes(t *testing.T) {
	s, _, _ := shotFixture(t)
	client := mcpApplicationClient(t, s, false)
	result := callApplicationMCP(t, client, "aividcut_get_shot_index", map[string]any{"assetId": "video"}, false)
	if result["status"] != "missing" || len(s.ListJobs()) != 0 {
		t.Fatal("只读查询自动启动扫描")
	}
	s.StopMCP()
	callApplicationMCP(t, client, "aividcut_get_shot_index", map[string]any{"assetId": "video"}, true)
}

func TestShotIndexCancelAndSwitchDoNotLeaveAutomaticScans(t *testing.T) {
	s, input, _ := shotFixture(t)
	if err := os.WriteFile(s.tools.FFmpegPath+".slow", []byte("wait"), 0600); err != nil {
		t.Fatal(err)
	}
	view, err := s.StartShotIndex(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CancelJob(view.Job.ID); err != nil {
		t.Fatal(err)
	}
	until := time.Now().Add(time.Second)
	for time.Now().Before(until) {
		current, _ := s.GetShotIndex(context.Background(), input)
		if current.Status == "cancelled" {
			break
		}
		time.Sleep(time.Millisecond)
	}
	current, err := s.GetShotIndex(context.Background(), input)
	if err != nil || current.Status != "cancelled" {
		t.Fatal("取消没有终态", err)
	}
	s.resetThumbnails(false)
	if _, err = s.GetShotIndex(context.Background(), input); err == nil {
		t.Fatal("切换实例后旧请求没有失效")
	}
	if s.GetBootstrap().Dirty {
		t.Fatal("取消检测修改工程")
	}
}
