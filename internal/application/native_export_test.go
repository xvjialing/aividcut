package application

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"aividcut/internal/editor"
	"aividcut/internal/jobs"
	"aividcut/internal/media"
)

func waitNativeExportJob(t *testing.T, s *Service, id string) jobs.Job {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		for _, j := range s.jobs.List() {
			if j.ID == id && (j.Status == "succeeded" || j.Status == "failed" || j.Status == "cancelled") {
				return j
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("原生导出任务未完成")
	return jobs.Job{}
}
func TestNativeExportApplicationSnapshotAndAutomaticRouting(t *testing.T) {
	if os.Getenv("AIVIDCUT_MLT_WORKER") == "" {
		t.Skip("需要显式原生worker，不能将跳过当作实际导出通过")
	}
	tools := preciseApplicationTools(t)
	s := preciseApplication(t, tools)
	if err := s.ConfigurePlayback(PlaybackOptions{Config: PlaybackEnvironment()}); err != nil {
		t.Fatal(err)
	}
	p := editor.NewProject("快照导出")
	p.Settings.Width, p.Settings.Height = 640, 360
	title := editor.DefaultClip("title", "track-caption-1", "caption")
	title.Text = "原始中文字幕"
	title.DurationFrames = 10
	p.Clips = []editor.Clip{title}
	engine, err := editor.NewEngine(p)
	if err != nil {
		t.Fatal(err)
	}
	s.engine = engine
	s.savedHash = projectHash(p)
	bootstrap := s.GetBootstrap()
	if !bootstrap.Capabilities["exportVideo"].Available || !bootstrap.Capabilities["nativeExport"].Available {
		t.Fatal("缺drawtext时没有提供原生导出路线", bootstrap.Capabilities)
	}
	output := filepath.Join(t.TempDir(), "快照.mp4")
	job, err := s.ExportVideo(media.ExportOptions{Path: output, Preset: "project"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApplyEdit(editor.EditRequest{BaseRevision: p.Revision, IdempotencyKey: "edit-during-export", Commands: []editor.Command{{Type: "delete_clip", ClipID: title.ID}}}); err != nil {
		t.Fatal(err)
	}
	result := waitNativeExportJob(t, s, job.ID)
	if result.Status != "succeeded" {
		t.Fatal(result)
	}
	command := exec.CommandContext(context.Background(), tools.FFmpegPath, "-v", "error", "-i", output, "-frames:v", "1", "-f", "rawvideo", "-pix_fmt", "rgba", "pipe:1")
	data, err := command.Output()
	if err != nil {
		t.Fatal(err)
	}
	white := 0
	for i := 0; i < len(data); i += 4 {
		if data[i] > 200 && data[i+1] > 200 && data[i+2] > 200 {
			white++
		}
	}
	after := s.GetBootstrap()
	if white < 50 || len(after.Project.Clips) != 0 || after.Project.Revision != 1 || !after.Dirty {
		t.Fatal("导出跟随了后来的编辑或改坏当前工程", white, after.Project.Revision, after.Dirty)
	}
	t.Logf("自动原生导出保留旧字幕%d白像素；当前工程已正常删除字幕并到revision1", white)
}
func TestExportTargetPreflightPreservesExistingAndRejectsInvalidRenderer(t *testing.T) {
	s := preciseApplication(t, media.Toolchain{})
	p := s.engine.Snapshot()
	clip := editor.DefaultClip("title", "track-caption-1", "caption")
	clip.Text = "测试"
	clip.DurationFrames = 10
	p.Clips = []editor.Clip{clip}
	engine, err := editor.NewEngine(p)
	if err != nil {
		t.Fatal(err)
	}
	s.engine = engine
	path := filepath.Join(t.TempDir(), "存在.mp4")
	os.WriteFile(path, []byte("old"), 0600)
	// 此处无需真正启动编码器，明确配置标记即可验证任务创建前的范围/目标保护。
	s.tools.FFmpegPath = "configured"
	for _, input := range []media.ExportOptions{{Path: path}, {Path: filepath.Join(t.TempDir(), "bad.mov")}, {Path: filepath.Join(t.TempDir(), "new.mp4"), Renderer: "unknown"}, {Path: filepath.Join(t.TempDir(), "new.mp4"), Renderer: "native"}} {
		if _, err := s.ExportVideo(input); err == nil {
			t.Fatal("非法导出参数被接受", input)
		}
	}
	if len(s.jobs.List()) != 0 {
		t.Fatal("应在创建后台任务前报告可修正错误")
	}
	original, _ := os.ReadFile(path)
	if string(original) != "old" {
		t.Fatal("预检改写目标")
	}
	alias := filepath.Join(t.TempDir(), "alias.mp4")
	if err := os.Link(path, alias); err != nil {
		t.Fatal(err)
	}
	if !outputMatchesSource(alias, path) {
		t.Fatal("未识别源文件别名")
	}
}
