package editor

import (
	"fmt"
	"math"
	"reflect"
	"testing"
)

func TestConstructSelectionHistoryAndLinkedDedup(t *testing.T) {
	e := selectionFixture(t)
	before := e.Snapshot()
	req := EditRequest{BaseRevision: before.Revision, IdempotencyKey: "split-many", Commands: []Command{{Type: "split_clips", ClipIDs: []string{"video-clip", "audio-clip", "title"}, AtFrame: 30}}}
	p, err := e.Apply(req)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Clips) != 5 || p.Clips[0].DurationFrames != 30 || p.Clips[1].DurationFrames != 30 || p.Clips[2].StartFrame != 90 {
		t.Fatal("未按交点分割且保留非交点", p.Clips)
	}
	if p.Clips[3].LinkGroup == "pair" || p.Clips[3].LinkGroup == "" || p.Clips[3].LinkGroup != p.Clips[4].LinkGroup {
		t.Fatal("右半未建立独立关联")
	}
	retry, err := e.Apply(req)
	if err != nil || !reflect.DeepEqual(retry, p) {
		t.Fatal("幂等重复构造", err)
	}
	undone, err := e.Undo()
	if err != nil {
		t.Fatal(err)
	}
	undone.Revision = before.Revision
	if !reflect.DeepEqual(undone, before) {
		t.Fatal("一次撤销未恢复分割")
	}
	if _, err = e.Redo(); err != nil {
		t.Fatal(err)
	}
	if _, err = e.Undo(); err != nil {
		t.Fatal(err)
	}
	p = apply(t, e, Command{Type: "duplicate_clips", ClipIDs: []string{"video-clip", "audio-clip", "title"}, AtFrame: 180})
	if len(p.Clips) != 6 || p.Clips[3].StartFrame != 180 || p.Clips[4].StartFrame != 180 || p.Clips[5].StartFrame != 270 {
		t.Fatal("复制没有保留时间间隔")
	}
	if p.Clips[3].LinkGroup == "pair" || p.Clips[3].LinkGroup == "" || p.Clips[3].LinkGroup != p.Clips[4].LinkGroup {
		t.Fatal("副本关联混入原组")
	}
	undone, err = e.Undo()
	if err != nil {
		t.Fatal(err)
	}
	undone.Revision = before.Revision
	if !reflect.DeepEqual(undone, before) {
		t.Fatal("一次撤销未恢复复制")
	}
}

func TestConstructSplitPartitionsStaggeredLinkedMembers(t *testing.T) {
	p := selectionFixture(t).Snapshot()
	p.Clips[2].LinkGroup = "pair"
	e, err := NewEngine(p)
	if err != nil {
		t.Fatal(err)
	}
	p = apply(t, e, Command{Type: "split_clips", ClipIDs: []string{"video-clip"}, AtFrame: 30})
	if p.Clips[2].StartFrame != 90 || p.Clips[2].LinkGroup != p.Clips[3].LinkGroup || p.Clips[0].LinkGroup == p.Clips[2].LinkGroup {
		t.Fatal("完全位于切口后的成员仍随左半联动")
	}
	no := false
	e = selectionFixture(t)
	p = apply(t, e, Command{Type: "split_clips", ClipIDs: []string{"video-clip"}, AtFrame: 30, IncludeLinked: &no})
	if len(p.Clips) != 4 || p.Clips[0].LinkGroup != "" || p.Clips[3].LinkGroup != "" || p.Clips[1].DurationFrames != 60 {
		t.Fatal("不联动分割错误")
	}
}

func TestConstructSelectionFailuresStayAtomic(t *testing.T) {
	tests := []struct {
		name    string
		command Command
		lock    bool
	}{
		{"empty", Command{Type: "split_clips", AtFrame: 30}, false},
		{"missing", Command{Type: "duplicate_clips", ClipIDs: []string{"missing"}, AtFrame: 180}, false},
		{"duplicate-id", Command{Type: "split_clips", ClipIDs: []string{"video-clip", "video-clip"}, AtFrame: 30}, false},
		{"boundary", Command{Type: "split_clips", ClipIDs: []string{"video-clip"}, AtFrame: 60}, false},
		{"locked", Command{Type: "split_clips", ClipIDs: []string{"video-clip"}, AtFrame: 30}, true},
		{"copy-locked", Command{Type: "duplicate_clips", ClipIDs: []string{"video-clip"}, AtFrame: 180}, true},
		{"collision", Command{Type: "duplicate_clips", ClipIDs: []string{"video-clip", "title"}, AtFrame: 20}, false},
		{"track-kind", Command{Type: "duplicate_clips", ClipIDs: []string{"title"}, AtFrame: 180, TrackDelta: 1}, false},
		{"overflow", Command{Type: "duplicate_clips", ClipIDs: []string{"video-clip", "title"}, AtFrame: math.MaxInt64 - 10}, false},
		{"negative", Command{Type: "duplicate_clips", ClipIDs: []string{"title"}, AtFrame: -1}, false},
		{"foreign", Command{Type: "split_clips", ClipIDs: []string{"video-clip"}, AtFrame: 30, FrameDelta: 2}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := selectionFixture(t).Snapshot()
			if tc.lock {
				p.Tracks[trackIndex(&p, "track-audio-1")].Locked = true
			}
			e, err := NewEngine(p)
			if err != nil {
				t.Fatal(err)
			}
			_, err = e.Apply(EditRequest{BaseRevision: p.Revision, IdempotencyKey: tc.name, Commands: []Command{{Type: "update_project", Name: "不得保留"}, tc.command}})
			if err == nil || !reflect.DeepEqual(p, e.Snapshot()) || e.CanUndo() {
				t.Fatal("构造失败没有原子回滚", err)
			}
		})
	}
}

func TestConstructCopiesKeepDistinctCaptionParentsAndExclusions(t *testing.T) {
	e := transcriptFixture(t)
	p := e.Snapshot()
	manual := boundByWord(t, p, "speech-clip", "hello")
	manual.Text = "甲镜头字幕"
	p = apply(t, e, Command{Type: "update_clip", Clip: &manual})
	copy := cloneClip(p.Clips[clipIndex(&p, "speech-clip")])
	copy.ID, copy.StartFrame = "second", 240
	p = apply(t, e, Command{Type: "duplicate_clip", ClipID: "speech-clip", Clip: &copy})
	second := boundByWord(t, p, "second", "hello")
	second.Text = "乙镜头字幕"
	world := boundByWord(t, p, "speech-clip", "world")
	p = apply(t, e, Command{Type: "update_clip", Clip: &second}, Command{Type: "delete_clip", ClipID: world.ID})
	p = apply(t, e, Command{Type: "duplicate_clips", ClipIDs: []string{"speech-clip", "second", manual.ID}, AtFrame: 600})
	var a, b string
	for _, c := range p.Clips {
		if c.Kind == "video" {
			if c.StartFrame == 600 {
				a = c.ID
			}
			if c.StartFrame == 810 {
				b = c.ID
			}
		}
	}
	if a == "" || b == "" || boundByWord(t, p, a, "hello").Text != "甲镜头字幕" || boundByWord(t, p, b, "hello").Text != "乙镜头字幕" {
		t.Fatal("同素材不同父源覆盖混淆")
	}
	boundByWord(t, p, b, "world")
	for _, c := range p.Clips {
		if c.CaptionSource != nil && c.CaptionSource.SourceClipID == a && c.CaptionSource.WordIDs[0] == "world" {
			t.Fatal("副本复活排除字幕")
		}
	}
	before := e.Snapshot()
	caption := boundByWord(t, p, a, "hello")
	_, err := e.Apply(EditRequest{BaseRevision: p.Revision, IdempotencyKey: "bound-alone", Commands: []Command{{Type: "duplicate_clips", ClipIDs: []string{caption.ID}, AtFrame: 1000}}})
	if err == nil || !reflect.DeepEqual(before, e.Snapshot()) {
		t.Fatal("独立绑定字幕未拒绝")
	}
}

func TestConstructSplitKeepsProtectedReadableGroupAtomic(t *testing.T) {
	e, p := readableFixture(t, timedWords("你", "好", "世", "界"), &CaptionLayout{Mode: "readable"})
	manual := readingClips(p, "read-clip")[0]
	manual.Text = "保持完整的人工文案"
	p = apply(t, e, Command{Type: "update_clip", Clip: &manual})
	_, err := e.Apply(EditRequest{BaseRevision: p.Revision, IdempotencyKey: "split-readable-many", Commands: []Command{{Type: "split_clips", ClipIDs: []string{"read-clip", manual.ID}, AtFrame: 37}}})
	assertCode(t, err, "caption_override_conflict")
	if !reflect.DeepEqual(p, e.Snapshot()) {
		t.Fatal("手改裁断冲突留下部分分割")
	}
}

func TestConstructSplitDuplicateKeepSourcePhase(t *testing.T) {
	e, _ := phaseFixture(t)
	p := apply(t, e, Command{Type: "split_clips", ClipIDs: []string{"source"}, AtFrame: 2})
	right := p.Clips[1]
	p = apply(t, e, Command{Type: "split_clips", ClipIDs: []string{right.ID}, AtFrame: 5})
	last := p.Clips[2]
	p = apply(t, e, Command{Type: "duplicate_clips", ClipIDs: []string{last.ID}, AtFrame: 300})
	copied := p.Clips[len(p.Clips)-1]
	if last.SourcePhase.OffsetFrames != 5 || !sameSourcePhase(last.SourcePhase, copied.SourcePhase) || !sameAudioRange(last.AudioRange, copied.AudioRange) {
		t.Fatal("分割或复制重新开始了源处理")
	}
}

func TestConstructLargeSelectionCopiesOnceAndRestores(t *testing.T) {
	p := NewProject("万片段构造")
	ids := make([]string, 10000)
	for i := range ids {
		ids[i] = fmt.Sprintf("title-%d", i)
		c := DefaultClip(ids[i], "track-text-1", "text")
		c.StartFrame = int64(i * 4)
		c.DurationFrames = 2
		p.Clips = append(p.Clips, c)
	}
	e, err := NewEngine(p)
	if err != nil {
		t.Fatal(err)
	}
	req := EditRequest{BaseRevision: 0, IdempotencyKey: "copy-10000", Commands: []Command{{Type: "duplicate_clips", ClipIDs: ids, AtFrame: 40000}}}
	after, err := e.Apply(req)
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Clips) != 20000 || after.Clips[10000].StartFrame != 40000 || after.Clips[19999].StartFrame != 79996 {
		t.Fatal("大选区数量或相对位置错误")
	}
	retry, err := e.Apply(req)
	if err != nil || len(retry.Clips) != 20000 || retry.Revision != after.Revision {
		t.Fatal("大选区幂等重复复制", err)
	}
	undone, err := e.Undo()
	if err != nil || len(undone.Clips) != 10000 {
		t.Fatal("大选区没有整组撤销", err)
	}
}
