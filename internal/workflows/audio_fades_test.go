package workflows

import (
	"aividcut/internal/editor"
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func fadeInput() Input {
	return Input{WorkflowID: "audio-fades", Scope: "selection", Selection: []string{"audio"}}
}

func TestAudioFadeWorkflowDefaultsScopeAndAtomicHistory(t *testing.T) {
	p := algorithmProject(t, false)
	before := editor.CloneProject(p)
	plan, err := Preview(p, fadeInput())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(p, before) || plan.AudioFadeSummary.Changed != 1 || len(plan.Request.Commands) != 1 || *plan.Request.Commands[0].IncludeLinked || plan.SavedFrames != 0 {
		t.Fatal("范围/预览/链接策略错误")
	}
	e, after := applyAlgorithm(t, p, plan)
	got := clipWithID(after, "audio").AudioFade
	if got == nil || got.InUs != 100000 || got.OutUs != 100000 || clipWithID(after, "video").AudioFade != nil || !reflect.DeepEqual(before.Transcripts, after.Transcripts) {
		t.Fatal("默认扩大范围或改原话")
	}
	undo, err := e.Undo()
	if err != nil || !reflect.DeepEqual(noRevision(p), noRevision(undo)) {
		t.Fatal("批量非一次撤销", err)
	}
	redo, err := e.Redo()
	if err != nil || !reflect.DeepEqual(noRevision(after), noRevision(redo)) {
		t.Fatal("重做不同", err)
	}
	for _, in := range []Input{{WorkflowID: "audio-fades", Selection: []string{"audio"}}, {WorkflowID: "audio-fades", Scope: "selection"}, {WorkflowID: "audio-fades", Scope: "project", Selection: []string{"audio"}}} {
		if _, err := Preview(p, in); err == nil {
			t.Fatal("未明确或冲突范围被接纳", in)
		}
	}
}

func TestAudioFadeWorkflowPoliciesMutedVideoLocksAndShortClip(t *testing.T) {
	p := algorithmProject(t, true)
	for i := range p.Tracks {
		if p.Tracks[i].ID == "music-track" {
			p.Tracks[i].Locked = false
			p.Tracks[i].Muted = true
		}
	}
	for i := range p.Clips {
		if p.Clips[i].ID == "audio" {
			p.Clips[i].AudioFade = editor.NewAudioFade(p.Clips[i], 200000, 300000)
		}
	}
	in := Input{WorkflowID: "audio-fades", Scope: "project"}
	if _, err := Preview(p, in); err == nil || !strings.Contains(err.Error(), "保留已有1") {
		t.Fatal("默认未保留曲线/跳过静音", err)
	}
	in.Parameters = map[string]any{"existingPolicy": "replace", "targetKinds": "audio_and_video", "audibleOnly": false}
	plan, err := Preview(p, in)
	if err != nil || plan.AudioFadeSummary.Changed != 3 {
		t.Fatal("明确包括视频/静音没有完整处理", err)
	}
	_, after := applyAlgorithm(t, p, plan)
	in.Parameters = map[string]any{"mode": "clear", "targetKinds": "audio_and_video", "audibleOnly": false}
	cleared, err := Preview(after, in)
	if err != nil || cleared.AudioFadeSummary.Changed != 3 {
		t.Fatal("清除被设置时保留策略阻止", err)
	}
	_, clean := applyAlgorithm(t, after, cleared)
	for _, c := range clean.Clips {
		if c.AudioFade != nil {
			t.Fatal("未完整清除")
		}
	}
	for i := range p.Tracks {
		if p.Tracks[i].ID == "track-video-1" {
			p.Tracks[i].Locked = true
		}
	}
	in.Parameters = map[string]any{"existingPolicy": "replace", "targetKinds": "audio_and_video"}
	if _, err := Preview(p, in); err == nil {
		t.Fatal("改变锁轨仍产生计划")
	}
	in = fadeInput()
	in.Parameters = map[string]any{"existingPolicy": "replace"}
	if _, err := Preview(p, in); err != nil {
		t.Fatal("未选链接视频锁轨阻断音频淡化", err)
	}
	p = algorithmProject(t, false)
	for i := range p.Clips {
		if p.Clips[i].ID == "audio" {
			p.Clips[i].DurationFrames = 1
			p.Clips[i].LinkGroup = ""
		}
	}
	in = fadeInput()
	in.Parameters = map[string]any{"inMs": 100, "outMs": 200}
	plan, err = Preview(p, in)
	if err != nil || plan.AudioFadeSummary.Scaled != 1 {
		t.Fatal("短片段未同比缩短", err)
	}
	f := plan.Request.Commands[0].Clip.AudioFade
	if f.InUs != 11122 || f.OutUs != 22244 {
		t.Fatal("29.97帧率微秒分配错误", f)
	}
	in.Parameters["shortPolicy"] = "reject"
	if _, err := Preview(p, in); err == nil {
		t.Fatal("明确拒绝短片段策略未生效")
	}
}

func TestAudioFadeWorkflowBoundedDetailsFullCommandsAndPresets(t *testing.T) {
	p := editor.NewProject("批量声音")
	p.Assets = []editor.Asset{{ID: "a", Name: "声音", Kind: "audio", Path: "/fixture.wav", HasAudio: true, DurationUs: 60_000_000, Missing: true}}
	for i := 0; i < 1001; i++ {
		c := editor.DefaultClip(fmt.Sprintf("audio-%d", i), "track-audio-1", "audio")
		c.AssetID = "a"
		c.StartFrame = int64(i * 3)
		c.DurationFrames = 2
		p.Clips = append(p.Clips, c)
	}
	in := Input{WorkflowID: "audio-fades", Scope: "project"}
	if _, err := Preview(p, in); err == nil {
		t.Fatal("超过上限只截断执行")
	}
	p.Clips = p.Clips[:1000]
	plan, err := Preview(p, in)
	if err != nil || plan.AudioFadeSummary.Changed != 1000 || len(plan.Effects) != 200 || len(plan.Request.Commands) != 1000 || len(plan.AffectedClipIDs) != 1000 {
		t.Fatal("展示上限替代实际范围", err)
	}
	if !strings.Contains(strings.Join(plan.Warnings, " "), "离线") {
		t.Fatal("离线元数据操作没有说明")
	}
	store, err := NewPresetStore(filepath.Join(t.TempDir(), "presets.json"))
	if err != nil {
		t.Fatal(err)
	}
	saved, err := store.Save(context.Background(), PresetInput{Name: "配乐淡化", WorkflowID: "audio-fades", Parameters: map[string]any{"inMs": 1000, "existingPolicy": "replace"}})
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := NewPresetStore(store.path)
	if err != nil {
		t.Fatal(err)
	}
	got, err := reopened.Get(saved.ID)
	if err != nil || !reflect.DeepEqual(got.Parameters, saved.Parameters) {
		t.Fatal("预设重启丢参数", err)
	}
	if skill, err := BuiltinSkill("audio-fades"); err != nil || !strings.Contains(skill, "scope") {
		t.Fatal("内置方法未打包", err)
	}
}

func TestAudioFadeWorkflowKnownEmptyRangeAndHiddenPicture(t *testing.T) {
	p := algorithmProject(t, false)
	for i := range p.Clips {
		if p.Clips[i].ID == "audio" {
			p.Clips[i].AudioRange = &editor.AudioRange{Unit: "us", Start: 0, End: 0}
		}
	}
	if _, err := Preview(p, fadeInput()); err == nil || !strings.Contains(err.Error(), "无声范围1") {
		t.Fatal("已知空声音范围未被跳过", err)
	}
	p = algorithmProject(t, false)
	for i := range p.Tracks {
		if p.Tracks[i].ID == "track-video-1" {
			p.Tracks[i].Hidden = true
		}
	}
	in := fadeInput()
	in.Selection = []string{"video"}
	in.Parameters = map[string]any{"targetKinds": "audio_and_video"}
	plan, err := Preview(p, in)
	if err != nil || plan.AudioFadeSummary.Changed != 1 {
		t.Fatal("隐藏画面被错误当成静音", err)
	}
}
