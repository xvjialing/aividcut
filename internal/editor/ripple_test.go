package editor

import (
	"reflect"
	"testing"
)

func TestRippleRangeLinkedCutsAllTracksAndOneUndo(t *testing.T) {
	e := selectionFixture(t)
	before := e.Snapshot()
	request := EditRequest{BaseRevision: before.Revision, IdempotencyKey: "range", Commands: []Command{{Type: "ripple_delete_range", FromFrame: 15, ToFrame: 30}}}
	after, err := e.Apply(request)
	if err != nil {
		t.Fatal(err)
	}
	if DurationFrames(after) != 105 {
		t.Fatal("全局末端未闭合", DurationFrames(after))
	}
	left, right := map[string]Clip{}, map[string]Clip{}
	for _, c := range after.Clips {
		if c.ID == "title" {
			if c.StartFrame != 75 {
				t.Fatal("后段文字未移动")
			}
			continue
		}
		if c.StartFrame == 0 {
			left[c.Kind] = c
		}
		if c.StartFrame == 15 {
			right[c.Kind] = c
		}
	}
	if left["video"].DurationFrames != 15 || left["audio"].DurationFrames != 15 || right["video"].DurationFrames != 30 || right["audio"].DurationFrames != 30 || right["video"].SourceInUs != 1_000_000 {
		t.Fatal("音画保留窗错误", after.Clips)
	}
	if left["video"].LinkGroup == right["video"].LinkGroup || right["video"].LinkGroup != right["audio"].LinkGroup {
		t.Fatal("切点两侧串组或声音失去链接")
	}
	sandbox, _ := NewEngine(before)
	expected, err := sandbox.Apply(request)
	if err != nil || !reflect.DeepEqual(expected, after) {
		t.Fatal("预览和实际结果ID不一致", err)
	}
	again, err := e.Apply(request)
	if err != nil || again.Revision != after.Revision {
		t.Fatal("重复执行", err)
	}
	restored, err := e.Undo()
	restored.Revision = before.Revision
	if err != nil || !reflect.DeepEqual(restored, before) {
		t.Fatal("无法一次恢复", err)
	}
	redone, err := e.Redo()
	redone.Revision = after.Revision
	if err != nil || !reflect.DeepEqual(redone, after) {
		t.Fatal("重做不一致", err)
	}
}
func TestRippleRangeEdgesGapsAndFailures(t *testing.T) {
	for _, test := range []struct{ from, to, duration int64 }{{0, 15, 105}, {45, 60, 105}, {60, 90, 90}, {0, 120, 0}, {110, 120, 110}} {
		e := selectionFixture(t)
		after, err := e.Apply(EditRequest{BaseRevision: 0, IdempotencyKey: "edge", Commands: []Command{{Type: "ripple_delete_range", FromFrame: test.from, ToFrame: test.to}}})
		if err != nil || DurationFrames(after) != test.duration {
			t.Fatalf("范围%+v失败 %v", test, err)
		}
	}
	for _, c := range []Command{{Type: "ripple_delete_range", FromFrame: -1, ToFrame: 10}, {Type: "ripple_delete_range", FromFrame: 20, ToFrame: 20}, {Type: "ripple_delete_range", ToFrame: 121}, {Type: "ripple_delete_range", ToFrame: 10, ClipID: "video-clip"}, {Type: "delete_clip", ClipID: "video-clip", ToFrame: 10}} {
		e := selectionFixture(t)
		before := e.Snapshot()
		if _, err := e.Apply(EditRequest{BaseRevision: 0, IdempotencyKey: "bad", Commands: []Command{c}}); err == nil {
			t.Fatal("非法范围/夹带字段通过", c)
		}
		if !reflect.DeepEqual(before, e.Snapshot()) || e.CanUndo() {
			t.Fatal("失败产生部分写入或历史")
		}
	}
	e := selectionFixture(t)
	p := e.Snapshot()
	p.Tracks[3].Locked = true
	e, _ = NewEngine(p)
	if _, err := e.Apply(EditRequest{BaseRevision: 0, IdempotencyKey: "locked", Commands: []Command{{Type: "ripple_delete_range", FromFrame: 15, ToFrame: 30}}}); err == nil {
		t.Fatal("锁定音轨被移动")
	}
	// 锁轨中所有内容都在范围之前，可保持不动。
	e = selectionFixture(t)
	p = e.Snapshot()
	p.Tracks[3].Locked = true
	e, _ = NewEngine(p)
	if _, err := e.Apply(EditRequest{BaseRevision: 0, IdempotencyKey: "unaffected-lock", Commands: []Command{{Type: "ripple_delete_range", FromFrame: 60, ToFrame: 90}}}); err != nil {
		t.Fatal("未受影响锁轨阻断", err)
	}
}
func TestRippleProtectedCaptionRejectsPartialButMovesWhole(t *testing.T) {
	e, p := readableFixture(t, timedWords("你", "好", "世", "界"), &CaptionLayout{Mode: "readable"})
	manual := readingClips(p, "read-clip")[0]
	manual.Text = "人工整句"
	p = apply(t, e, Command{Type: "update_clip", Clip: &manual})
	before := e.Snapshot()
	if _, err := e.Apply(EditRequest{BaseRevision: p.Revision, IdempotencyKey: "protected", Commands: []Command{{Type: "ripple_delete_range", FromFrame: 33, ToFrame: 36}}}); err == nil {
		t.Fatal("跨切口人工整句仍被拆分")
	}
	if !reflect.DeepEqual(e.Snapshot(), before) {
		t.Fatal("拒绝后写入")
	}
	after, err := e.Apply(EditRequest{BaseRevision: p.Revision, IdempotencyKey: "move-all", Commands: []Command{{Type: "ripple_delete_range", FromFrame: 0, ToFrame: 15}}})
	if err != nil {
		t.Fatal(err)
	}
	captions := readingClips(after, "read-clip")
	if len(captions) != 1 || captions[0].Text != "人工整句" || captions[0].StartFrame != manual.StartFrame-15 {
		t.Fatal("人工整句整体前移丢失")
	}
	if !reflect.DeepEqual(after.Transcripts[0].AcceptedWords, before.Transcripts[0].AcceptedWords) {
		t.Fatal("媒体操作改了原话")
	}
}
