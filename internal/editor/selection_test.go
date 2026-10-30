package editor

import (
	"math"
	"reflect"
	"testing"
)

func selectionFixture(t *testing.T) *Engine {
	p := fixture(t).Snapshot()
	p.Clips[0].DurationFrames = 60
	p.Clips[0].LinkGroup = "pair"
	a := DefaultClip("audio-clip", "track-audio-1", "audio")
	a.AssetID = "audio"
	a.DurationFrames = 60
	a.LinkGroup = "pair"
	text := DefaultClip("title", "track-text-1", "text")
	text.Text = "标题"
	text.StartFrame = 90
	text.DurationFrames = 30
	p.Clips = append(p.Clips, a, text)
	e, err := NewEngine(p)
	if err != nil {
		t.Fatal(err)
	}
	return e
}
func TestSelectionMoveLinkedOnceAndAtomicHistory(t *testing.T) {
	e := selectionFixture(t)
	before := e.Snapshot()
	request := EditRequest{BaseRevision: 0, IdempotencyKey: "batch", Commands: []Command{{Type: "move_clips", ClipIDs: []string{"video-clip", "audio-clip", "title"}, FrameDelta: 30}}}
	moved, err := e.Apply(request)
	if err != nil {
		t.Fatal(err)
	}
	if moved.Clips[0].StartFrame != 30 || moved.Clips[1].StartFrame != 30 || moved.Clips[2].StartFrame != 120 {
		t.Fatal("联动片段重复移动", moved.Clips)
	}
	retried, err := e.Apply(request)
	if err != nil || retried.Revision != moved.Revision {
		t.Fatal("批量幂等失效", err)
	}
	undone, err := e.Undo()
	if err != nil {
		t.Fatal(err)
	}
	undone.Revision = before.Revision
	if !reflect.DeepEqual(undone, before) {
		t.Fatal("一次撤销未恢复完整选区")
	}
	redone, err := e.Redo()
	if err != nil || redone.Clips[2].StartFrame != 120 {
		t.Fatal("重做失败", err)
	}
	deleted := apply(t, e, Command{Type: "delete_clips", ClipIDs: []string{"video-clip", "audio-clip"}})
	if len(deleted.Clips) != 1 || deleted.Clips[0].ID != "title" {
		t.Fatal("删除联动组没有去重")
	}
}
func TestSelectionFailureNeverPartiallyCommits(t *testing.T) {
	for _, test := range []struct {
		name    string
		change  func(*Project)
		command Command
	}{
		{"重复ID", nil, Command{Type: "move_clips", ClipIDs: []string{"title", "title"}, FrameDelta: 1}},
		{"缺失ID", nil, Command{Type: "delete_clips", ClipIDs: []string{"title", "missing"}}},
		{"负时间", nil, Command{Type: "move_clips", ClipIDs: []string{"title", "video-clip"}, FrameDelta: -1}},
		{"溢出", nil, Command{Type: "move_clips", ClipIDs: []string{"title"}, FrameDelta: math.MaxInt64}},
		{"错误类型", nil, Command{Type: "move_clips", ClipIDs: []string{"title", "video-clip"}, TrackDelta: 1}},
		{"轨道越界", nil, Command{Type: "move_clips", ClipIDs: []string{"title"}, TrackDelta: math.MinInt}},
		{"联动锁定", func(p *Project) { p.Tracks[3].Locked = true }, Command{Type: "move_clips", ClipIDs: []string{"title", "video-clip"}, FrameDelta: 1}},
		{"目标锁定", func(p *Project) {
			p.Tracks = append(p.Tracks, Track{ID: "locked-text", Name: "锁定", Kind: "text", Locked: true})
		}, Command{Type: "move_clips", ClipIDs: []string{"title"}, TrackDelta: 3}},
		{"夹带参数", nil, Command{Type: "move_clips", ClipIDs: []string{"title"}, ClipID: "title", FrameDelta: 1}},
		{"字段混用", nil, Command{Type: "delete_clip", ClipID: "title", ClipIDs: []string{"title"}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			p := selectionFixture(t).Snapshot()
			if test.change != nil {
				test.change(&p)
			}
			e, err := NewEngine(p)
			if err != nil {
				t.Fatal(err)
			}
			before := e.Snapshot()
			_, err = e.Apply(EditRequest{BaseRevision: p.Revision, IdempotencyKey: "failed", Commands: []Command{test.command}})
			if err == nil {
				t.Fatal("非法批量操作被接受")
			}
			if !reflect.DeepEqual(e.Snapshot(), before) || e.CanUndo() {
				t.Fatal("失败操作有部分写入或撤销记录")
			}
		})
	}
}
func TestSelectionMoveKeepsPhaseAndCaptions(t *testing.T) {
	e, _ := phaseFixture(t)
	split := apply(t, e, Command{Type: "split_clip", ClipID: "source", AtFrame: 5})
	before := CloneProject(split)
	ids := []string{split.Clips[0].ID, split.Clips[1].ID}
	moved := apply(t, e, Command{Type: "move_clips", ClipIDs: ids, FrameDelta: 7})
	for i, c := range moved.Clips {
		if c.StartFrame != before.Clips[i].StartFrame+7 || c.SourceInUs != before.Clips[i].SourceInUs || c.DurationFrames != before.Clips[i].DurationFrames || !sameAudioRange(c.AudioRange, before.Clips[i].AudioRange) || c.SourcePhase.OffsetFrames != before.Clips[i].SourcePhase.OffsetFrames {
			t.Fatal("批量移动重置源处理输出窗", c)
		}
	}
	captions := transcriptFixture(t)
	p := captions.Snapshot()
	old := boundByWord(t, p, "speech-clip", "hello")
	old.Text = "手工文字"
	p = apply(t, captions, Command{Type: "update_clip", Clip: &old})
	beforeCaption := boundByWord(t, p, "speech-clip", "hello")
	p = apply(t, captions, Command{Type: "move_clips", ClipIDs: []string{"speech-clip", beforeCaption.ID}, FrameDelta: 9})
	after := boundByWord(t, p, "speech-clip", "hello")
	if after.StartFrame != beforeCaption.StartFrame+9 || after.Text != "手工文字" || !after.CaptionSource.TextOverride {
		t.Fatal("字幕被移动两次或丢失人工覆盖", after)
	}
	prior := captions.Snapshot()
	if _, err := captions.Apply(EditRequest{BaseRevision: prior.Revision, IdempotencyKey: "caption-only", Commands: []Command{{Type: "move_clips", ClipIDs: []string{after.ID}, FrameDelta: 1}}}); err == nil {
		t.Fatal("单独移动绑定字幕绕过保护")
	}
}

func TestSelectionFinalCollisionValidationAllowsAdjacentMoves(t *testing.T) {
	p := selectionFixture(t).Snapshot()
	right := cloneClip(p.Clips[0])
	right.ID = "right"
	right.LinkGroup = ""
	right.StartFrame = 60
	p.Clips = append(p.Clips, right)
	e, err := NewEngine(p)
	if err != nil {
		t.Fatal(err)
	}
	moved := apply(t, e, Command{Type: "move_clips", ClipIDs: []string{"video-clip", "right"}, FrameDelta: 60})
	if moved.Clips[0].StartFrame != 60 || moved.Clips[3].StartFrame != 120 {
		t.Fatal("邻接片段中间状态被误当碰撞")
	}
	prior := e.Snapshot()
	_, err = e.Apply(EditRequest{BaseRevision: prior.Revision, IdempotencyKey: "collision", Commands: []Command{{Type: "move_clips", ClipIDs: []string{"video-clip"}, FrameDelta: 60}}})
	if err == nil || !reflect.DeepEqual(e.Snapshot(), prior) {
		t.Fatal("与未选片段碰撞未原子拒绝", err)
	}
}
