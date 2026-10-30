package projectversions

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"aividcut/internal/editor"
)

func TestImmutableVersionReopenAndPaging(t *testing.T) {
	dir := t.TempDir()
	store, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	project := editor.NewProject("原版本")
	clip := editor.DefaultClip("text-1", "track-text-1", "text")
	clip.Text = "人工标题"
	project.Clips = []editor.Clip{clip}
	first, err := store.Create(project, "粗剪完成", false)
	if err != nil {
		t.Fatal(err)
	}
	project.Name = "更新后"
	project.Revision++
	project.Clips[0].Text = "精修标题"
	second, err := store.Create(project, "精修完成", true)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	page, err := store.List(project.ID, 0, 1)
	if err != nil || page.Total != 2 || len(page.Versions) != 1 || page.Versions[0].ID != second.ID || !page.Versions[0].BeforeRestore {
		t.Fatal(page, err)
	}
	page, err = store.List(project.ID, 1, 1)
	if err != nil || page.Versions[0].ID != first.ID {
		t.Fatal(page, err)
	}
	restored, _, err := store.Load(project.ID, first.ID, first.SHA256)
	if err != nil || restored.Name != "原版本" || restored.Clips[0].Text != "人工标题" {
		t.Fatal(restored, err)
	}
	if project.Clips[0].Text != "精修标题" {
		t.Fatal("加载修改了原对象")
	}
}

func TestVersionProofAndPathBoundaries(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	project := editor.NewProject("版本证明")
	version, err := store.Create(project, "原状态", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ projectID, id, hash string }{{"other", version.ID, version.SHA256}, {project.ID, "../project", version.SHA256}, {project.ID, version.ID, strings.Repeat("0", 64)}} {
		if _, _, err := store.Load(test.projectID, test.id, test.hash); err == nil {
			t.Fatal("错误范围未拒绝", test)
		}
	}
	path := filepath.Join(store.directoryFor(project.ID), version.ID, "project.aividcut")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(data, ' '), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Load(project.ID, version.ID, version.SHA256); err == nil {
		t.Fatal("损坏版本未拒绝")
	}
	page, err := store.List(project.ID, 0, 20)
	if err != nil || page.Total != 1 {
		t.Fatal("列表不应读取整份工程", page, err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(path), "metadata.json"), []byte("bad"), 0600); err != nil {
		t.Fatal(err)
	}
	page, err = store.List(project.ID, 0, 20)
	if err != nil || page.Total != 0 || page.Unreadable != 1 {
		t.Fatal(page, err)
	}
}

func TestVersionWriteFailureSecretsLimitsAndLock(t *testing.T) {
	dir := t.TempDir()
	store, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if extra, err := Open(dir); err == nil {
		extra.Close()
		t.Fatal("并发实例获得版本目录锁")
	}
	project := editor.NewProject("带会话地址")
	assetPath := filepath.Join(t.TempDir(), "source.png")
	if err := os.WriteFile(assetPath, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	project.Assets = []editor.Asset{{ID: "image", Name: "image", Kind: "image", Path: assetPath, Width: 640, Height: 360, URL: "http://local/?token=secret", ThumbnailURL: "http://local/?token=thumb"}}
	version, err := store.Create(project, "去除会话", false)
	if err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(filepath.Join(store.directoryFor(project.ID), version.ID, "project.aividcut"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(contents), "token=") {
		t.Fatal("快照包含会话地址")
	}
	loaded, _, err := store.Load(project.ID, version.ID, version.SHA256)
	if err != nil {
		t.Fatal(err)
	}
	expected := editor.CloneProject(project)
	expected.Assets[0].URL = ""
	expected.Assets[0].ThumbnailURL = ""
	if !reflect.DeepEqual(expected, loaded) {
		t.Fatal("快照没有保留工程属性")
	}
	if _, err := store.Create(project, strings.Repeat("字", 81), false); err == nil {
		t.Fatal("名称无界")
	}
	if _, err := store.List(project.ID, -1, 20); err == nil {
		t.Fatal("负分页未拒绝")
	}
	if _, err := store.List(project.ID, 0, 51); err == nil {
		t.Fatal("无界分页未拒绝")
	}
	other := editor.NewProject("无法写入")
	if err := os.WriteFile(store.directoryFor(other.ID), []byte("blocked"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(other, "不能覆盖", false); err == nil {
		t.Fatal("写入失败未拒绝")
	}
}

func TestVersionPreservesSourcePhaseAudioWindowAndLUT(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	project := editor.NewProject("有理数源配方")
	project.Settings.FPSNum, project.Settings.FPSDen = 30000, 1001
	videoPath, lutPath := filepath.Join(t.TempDir(), "source.mp4"), filepath.Join(t.TempDir(), "grade.cube")
	for _, path := range []string{videoPath, lutPath} {
		if err := os.WriteFile(path, []byte("本地文件存在性夹具"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	count := int64(480000)
	video := editor.Asset{ID: "video", Name: "video", Kind: "video", Path: videoPath, DurationUs: 10_000_000, Width: 640, Height: 360, FPSNum: 30000, FPSDen: 1001, HasAudio: true, AudioSampleRate: 48000, AudioSampleCount: &count, Fingerprint: "sha256:" + strings.Repeat("a", 64)}
	project.Assets = []editor.Asset{video, {ID: "lut", Name: "grade", Kind: "lut", Path: lutPath}}
	clip := editor.DefaultClip("video-clip", "track-video-1", "video")
	clip.AssetID, clip.StartFrame, clip.SourceInUs, clip.SpeedNum, clip.SpeedDen = video.ID, 30, 137000, 3, 2
	clip.Volume = .75
	clip.LUT = &editor.LUT{AssetID: "lut", Intensity: .37}
	clip, err = editor.CaptureSourcePhase(clip, video, project.Settings)
	if err != nil {
		t.Fatal(err)
	}
	project.Clips = []editor.Clip{clip}
	version, err := store.Create(project, "保留取样相位", false)
	if err != nil {
		t.Fatal(err)
	}
	loaded, _, err := store.Load(project.ID, version.ID, version.SHA256)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(project, loaded) {
		t.Fatal("源配方、音频区间或LUT在版本中改变")
	}
	path := filepath.Join(store.directoryFor(project.ID), version.ID, "project.aividcut")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(videoPath, path); err != nil {
		t.Skip("此环境不支持符号链接")
	}
	if _, _, err := store.Load(project.ID, version.ID, version.SHA256); err == nil {
		t.Fatal("版本读取不应跟随符号链接")
	}
}
