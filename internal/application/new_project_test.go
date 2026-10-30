package application

import (
	"aividcut/internal/editor"
	"aividcut/internal/media"
	"reflect"
	"testing"
)

func setupService(t *testing.T) *Service {
	t.Helper()
	s, err := New(media.Toolchain{}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s
}

func setupInput(b Bootstrap) NewProjectInput {
	return NewProjectInput{ProjectID: b.Project.ID, ProjectRevision: b.Project.Revision, SourceEpoch: b.SourceEpoch, Name: "竖屏新工程", Settings: editor.Settings{Width: 1080, Height: 1920, FPSNum: 60000, FPSDen: 2002, SampleRate: 48000}}
}
func TestNewProjectSettingsAtomicResetAndFraction(t *testing.T) {
	s := setupService(t)
	before := s.GetBootstrap()
	in := setupInput(before)
	after, err := s.NewProjectWithSettings(in)
	if err != nil {
		t.Fatal(err)
	}
	v := after.Project.Settings
	if v.Width != 1080 || v.Height != 1920 || v.FPSNum != 30000 || v.FPSDen != 1001 || v.SampleRate != 48000 {
		t.Fatal("规格未按有理数保存", v)
	}
	if after.Project.ID == before.Project.ID || after.SourceEpoch == before.SourceEpoch || after.Dirty || after.CanUndo || after.CanRedo || after.ProjectPath != "" || len(after.Project.Tracks) != 4 || len(after.Project.Clips) != 0 || len(after.Project.Assets) != 0 {
		t.Fatal("新工程重置范围不正确", after)
	}
	if _, err := s.NewProjectWithSettings(in); err == nil {
		t.Fatal("重复旧窗口再次创建工程")
	}
	legacy, err := s.NewProject("兼容入口")
	if err != nil || legacy.Project.Settings != editor.NewProject("").Settings {
		t.Fatal("旧入口默认被改变", err)
	}
}
func TestInvalidOrStaleNewProjectNeverDropsCurrentWork(t *testing.T) {
	s := setupService(t)
	b := s.GetBootstrap()
	c := editor.DefaultClip("preserve-title", "track-text-1", "text")
	c.Text = "要保留的当前编辑"
	in := setupInput(b)
	edited, err := s.ApplyEdit(editor.EditRequest{BaseRevision: b.Project.Revision, IdempotencyKey: "old-work", Commands: []editor.Command{{Type: "add_clip", Clip: &c}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.NewProjectWithSettings(in); err == nil {
		t.Fatal("过期窗口清空新编辑")
	}
	if !reflect.DeepEqual(edited, s.GetBootstrap()) {
		t.Fatal("过期请求改变原状态")
	}
	for _, change := range []func(*NewProjectInput){func(v *NewProjectInput) { v.Settings.Width = 1919 }, func(v *NewProjectInput) { v.Settings.Width = 7680; v.Settings.Height = 4320 }, func(v *NewProjectInput) { v.Settings.FPSDen = 0 }, func(v *NewProjectInput) { v.Settings.FPSNum = 121; v.Settings.FPSDen = 1 }, func(v *NewProjectInput) { v.Settings.SampleRate = 44100 }, func(v *NewProjectInput) { v.Name = "\n" }, func(v *NewProjectInput) { v.Name = "坏\x00名" }} {
		v := setupInput(edited)
		change(&v)
		if _, err := s.NewProjectWithSettings(v); err == nil {
			t.Fatal("无效设置被创建", v)
		}
		if !reflect.DeepEqual(edited, s.GetBootstrap()) {
			t.Fatal("无效请求改变当前工程/撤销/资源")
		}
	}
	in = setupInput(edited)
	s.resetThumbnails(false)
	state := s.GetBootstrap()
	if _, err := s.NewProjectWithSettings(in); err == nil {
		t.Fatal("同ID同修订旧实例窗口未拒绝")
	}
	if !reflect.DeepEqual(state, s.GetBootstrap()) {
		t.Fatal("旧实例请求仍改变状态")
	}
}
