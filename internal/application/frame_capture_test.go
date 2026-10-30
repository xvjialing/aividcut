package application

import (
	"aividcut/internal/media"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func captureApplication(t *testing.T, tools media.Toolchain) (*Service, *appSecretStore, *appProviderControl) {
	t.Helper()
	s, err := New(tools, filepath.Join(t.TempDir(), "previews"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	store := &appSecretStore{values: map[string][]byte{}}
	control := &appProviderControl{}
	if err = s.ConfigureGeneration(GenerationOptions{Directory: t.TempDir(), SecretStore: store, Factory: control.factory}); err != nil {
		t.Fatal(err)
	}
	return s, store, control
}
func TestCaptureSourceNoModelUndoNoReplayAndScope(t *testing.T) {
	s, store, control := captureApplication(t, media.Toolchain{})
	asset := sourceFrameFixture(t, s)
	b := s.GetBootstrap()
	input := FrameCaptureInput{ID: "source-frame", ProjectID: b.Project.ID, ProjectRevision: b.Project.Revision, SourceEpoch: b.SourceEpoch, Kind: "source", AssetID: asset.ID, Fingerprint: asset.Fingerprint}
	result, err := s.CaptureFrameAsset(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Imported || result.Origin.Asset.Kind != "image" || !result.Origin.Approximate || len(result.Bootstrap.Project.Assets) != 2 {
		t.Fatal("未加入真实图片")
	}
	if result.Origin.Asset.Width != 480 || result.Origin.Asset.Height != 270 {
		t.Fatal("实际源取样尺寸错误")
	}
	again, err := s.CaptureFrameAsset(context.Background(), input)
	if err != nil || !again.Duplicate || again.Bootstrap.Project.Revision != result.Bootstrap.Project.Revision {
		t.Fatal("重复取图产生新资产", err)
	}
	undo, err := s.Undo()
	if err != nil || len(undo.Project.Assets) != 1 {
		t.Fatal("无法一次撤销", err)
	}
	again, err = s.CaptureFrameAsset(context.Background(), input)
	if err != nil || again.Imported || again.Bootstrap.Project.Revision != undo.Project.Revision {
		t.Fatal("撤销后旧请求重放", err)
	}
	changed := input
	changed.PositionUs = 1
	if _, err = s.CaptureFrameAsset(context.Background(), changed); err == nil {
		t.Fatal("同ID参数改变通过")
	}
	s.resetThumbnails(false)
	if _, err = s.CaptureFrameAsset(context.Background(), input); err == nil {
		t.Fatal("旧实例请求通过")
	}
	data, err := os.ReadFile(filepath.Join(filepath.Dir(result.Origin.Asset.Path), "origin.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "Bearer ") || strings.Contains(string(data), "base64") || strings.Contains(string(data), "token=") {
		t.Fatal("来源记录泄漏访问能力")
	}
	if store.readCount() != 0 || control.posts.Load() != 0 {
		t.Fatal("取图触发了模型/凭据")
	}
}
func TestCaptureSourceFailureKeepsProject(t *testing.T) {
	s, _, _ := captureApplication(t, media.Toolchain{})
	a := sourceFrameFixture(t, s)
	b := s.GetBootstrap()
	input := FrameCaptureInput{ID: "source-bad", ProjectID: b.Project.ID, ProjectRevision: b.Project.Revision, SourceEpoch: b.SourceEpoch, Kind: "source", AssetID: a.ID, Fingerprint: a.Fingerprint}
	if err := os.WriteFile(a.Path, []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CaptureFrameAsset(context.Background(), input); err == nil {
		t.Fatal("变更源仍被截图")
	}
	if len(s.GetBootstrap().Project.Assets) != 1 || s.GetBootstrap().Project.Revision != b.Project.Revision {
		t.Fatal("失败产生工程变化")
	}
}
