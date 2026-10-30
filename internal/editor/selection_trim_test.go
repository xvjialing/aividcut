package editor

import (
	"math"
	"reflect"
	"testing"
)

func TestTrimSelectionKeepsOppositeEdgesAndOneHistoryUnit(t *testing.T) {
	e := selectionFixture(t)
	before := e.Snapshot()
	request := EditRequest{BaseRevision: 0, IdempotencyKey: "trim-head", Commands: []Command{{Type: "trim_clips", ClipIDs: []string{"video-clip", "audio-clip", "title"}, Edge: "start", FrameDelta: 5}}}
	p, err := e.Apply(request)
	if err != nil {
		t.Fatal(err)
	}
	for i, c := range p.Clips {
		if c.StartFrame != before.Clips[i].StartFrame+5 || c.DurationFrames != before.Clips[i].DurationFrames-5 || c.StartFrame+c.DurationFrames != before.Clips[i].StartFrame+before.Clips[i].DurationFrames {
			t.Fatal("起点不是同量修剪或联动重复", c)
		}
	}
	if p.Clips[2].SourceInUs != 0 || p.Clips[0].SourcePhase.OffsetFrames != 5 || p.Clips[1].SourcePhase.OffsetFrames != 5 {
		t.Fatal("静态入点或媒体相位错误")
	}
	retried, err := e.Apply(request)
	if err != nil || !reflect.DeepEqual(retried, p) {
		t.Fatal("重试重复修剪", err)
	}
	p = apply(t, e, Command{Type: "trim_clips", ClipIDs: []string{"video-clip", "title"}, Edge: "end", FrameDelta: -5})
	if p.Clips[0].DurationFrames != 50 || p.Clips[1].DurationFrames != 50 || p.Clips[2].DurationFrames != 20 {
		t.Fatal("终点修剪错误")
	}
	if _, err = e.Undo(); err != nil {
		t.Fatal(err)
	}
	restored, err := e.Undo()
	if err != nil {
		t.Fatal(err)
	}
	restored.Revision = before.Revision
	if !reflect.DeepEqual(restored, before) {
		t.Fatal("每次修剪未整组撤销")
	}
	if _, err = e.Redo(); err != nil {
		t.Fatal(err)
	}
}
func TestTrimSelectionNegativeHeadRestoresRecipeWindow(t *testing.T) {
	e, _ := phaseFixture(t)
	p := apply(t, e, Command{Type: "trim_clips", ClipIDs: []string{"source"}, Edge: "start", FrameDelta: 5})
	p = apply(t, e, Command{Type: "trim_clips", ClipIDs: []string{"source"}, Edge: "start", FrameDelta: -2})
	if p.Clips[0].StartFrame != 3 || p.Clips[0].DurationFrames != 67 || p.Clips[0].SourcePhase.OffsetFrames != 3 {
		t.Fatal("负向延长累计了相位误差")
	}
	want, _ := SourceDurationUs(3, p.Settings, 1, 1)
	if p.Clips[0].SourceInUs != 12345+want {
		t.Fatal("源入点不是原配方位置")
	}
}
func TestTrimSelectionRejectsWholeRequest(t *testing.T) {
	cases := []struct {
		name string
		c    Command
		lock bool
	}{
		{"missing", Command{Type: "trim_clips", ClipIDs: []string{"missing"}, Edge: "end", FrameDelta: -1}, false},
		{"repeat", Command{Type: "trim_clips", ClipIDs: []string{"title", "title"}, Edge: "end", FrameDelta: -1}, false},
		{"edge", Command{Type: "trim_clips", ClipIDs: []string{"title"}, Edge: "other", FrameDelta: 1}, false},
		{"zero", Command{Type: "trim_clips", ClipIDs: []string{"title"}, Edge: "start"}, false},
		{"empty", Command{Type: "trim_clips", ClipIDs: []string{"title"}, Edge: "start", FrameDelta: 30}, false},
		{"negative-time", Command{Type: "trim_clips", ClipIDs: []string{"video-clip"}, Edge: "start", FrameDelta: -1}, false},
		{"overflow", Command{Type: "trim_clips", ClipIDs: []string{"title"}, Edge: "end", FrameDelta: math.MaxInt64}, false},
		{"underflow", Command{Type: "trim_clips", ClipIDs: []string{"title"}, Edge: "end", FrameDelta: math.MinInt64}, false},
		{"source-end", Command{Type: "trim_clips", ClipIDs: []string{"video-clip", "title"}, Edge: "end", FrameDelta: 900}, false},
		{"locked-linked", Command{Type: "trim_clips", ClipIDs: []string{"video-clip"}, Edge: "end", FrameDelta: -1}, true},
		{"foreign", Command{Type: "trim_clips", ClipIDs: []string{"title"}, Edge: "start", FrameDelta: 1, TrackDelta: 1}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := selectionFixture(t).Snapshot()
			if tc.lock {
				p.Tracks[trackIndex(&p, "track-audio-1")].Locked = true
			}
			e, err := NewEngine(p)
			if err != nil {
				t.Fatal(err)
			}
			_, err = e.Apply(EditRequest{BaseRevision: p.Revision, IdempotencyKey: tc.name, Commands: []Command{{Type: "update_project", Name: "不应保留"}, tc.c}})
			if err == nil || !reflect.DeepEqual(e.Snapshot(), p) || e.CanUndo() {
				t.Fatal("失败未整体回滚", err)
			}
		})
	}
}
func TestTrimSelectionRejectsFinalCollision(t *testing.T) {
	p := selectionFixture(t).Snapshot()
	other := cloneClip(p.Clips[0])
	other.ID, other.LinkGroup, other.StartFrame, other.DurationFrames = "next", "", 60, 30
	p.Clips = append(p.Clips, other)
	e, err := NewEngine(p)
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.Apply(EditRequest{BaseRevision: 0, IdempotencyKey: "trim-collision", Commands: []Command{{Type: "trim_clips", ClipIDs: []string{"video-clip", "next"}, Edge: "end", FrameDelta: 1}}})
	if err == nil || !reflect.DeepEqual(e.Snapshot(), p) {
		t.Fatal("最终重叠未整体拒绝", err)
	}
}
func TestTrimSelectionAudioCeilEndAndExistingSilentBox(t *testing.T) {
	_, p := phaseFixture(t)
	c := p.Clips[0]
	c.SourceInUs = 0
	var err error
	c, err = AudioClipToSourceEnd(c, p.Assets[0], p.Settings)
	if err != nil {
		t.Fatal(err)
	}
	p.Clips = []Clip{c}
	e, err := NewEngine(p)
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.Apply(EditRequest{BaseRevision: 0, IdempotencyKey: "past-exact-end", Commands: []Command{{Type: "trim_clips", ClipIDs: []string{c.ID}, Edge: "end", FrameDelta: 1}}})
	assertCode(t, err, "source_out_of_range")
	c.DurationFrames += 10
	p.Clips = []Clip{c}
	e, err = NewEngine(p)
	if err != nil {
		t.Fatal(err)
	}
	after := apply(t, e, Command{Type: "trim_clips", ClipIDs: []string{c.ID}, Edge: "end", FrameDelta: -1})
	if after.Clips[0].DurationFrames != c.DurationFrames-1 {
		t.Fatal("已有无声占位不能收短")
	}
	p.Assets[0].Kind, p.Assets[0].DurationUs = "video", 20_000_000
	p.Assets[0].Width, p.Assets[0].Height, p.Assets[0].FPSNum, p.Assets[0].FPSDen = 1920, 1080, 30000, 1001
	c.DurationFrames -= 10
	p.Clips = []Clip{c}
	e, err = NewEngine(p)
	if err != nil {
		t.Fatal(err)
	}
	carrier := apply(t, e, Command{Type: "trim_clips", ClipIDs: []string{c.ID}, Edge: "end", FrameDelta: 1})
	if carrier.Clips[0].DurationFrames != c.DurationFrames+1 || carrier.Clips[0].AudioRange.End != c.AudioRange.End {
		t.Fatal("视频声音载体不能保留容器内静音，或伪造了样本")
	}
}
func TestTrimSelectionCaptionsDerivedOnceAndProtected(t *testing.T) {
	e := transcriptFixture(t)
	p := e.Snapshot()
	manual := boundByWord(t, p, "speech-clip", "hello")
	manual.Text = "保留手改"
	p = apply(t, e, Command{Type: "update_clip", Clip: &manual})
	p = apply(t, e, Command{Type: "trim_clips", ClipIDs: []string{"speech-clip", manual.ID}, Edge: "start", FrameDelta: 3})
	got := boundByWord(t, p, "speech-clip", "hello")
	if got.Text != "保留手改" || got.StartFrame != manual.StartFrame {
		t.Fatal("字幕重复修剪或手改丢失")
	}
	before := e.Snapshot()
	_, err := e.Apply(EditRequest{BaseRevision: p.Revision, IdempotencyKey: "caption-alone-trim", Commands: []Command{{Type: "trim_clips", ClipIDs: []string{manual.ID}, Edge: "end", FrameDelta: -1}}})
	assertCode(t, err, "caption_binding")
	if !reflect.DeepEqual(before, e.Snapshot()) {
		t.Fatal("独立绑定失败有部分结果")
	}
	reading, project := readableFixture(t, timedWords("你", "好", "世", "界"), &CaptionLayout{Mode: "readable"})
	caption := readingClips(project, "read-clip")[0]
	caption.Text = "不可裁断手改"
	project = apply(t, reading, Command{Type: "update_clip", Clip: &caption})
	_, err = reading.Apply(EditRequest{BaseRevision: project.Revision, IdempotencyKey: "protected-trim", Commands: []Command{{Type: "trim_clips", ClipIDs: []string{"read-clip", caption.ID}, Edge: "start", FrameDelta: 7}}})
	assertCode(t, err, "caption_override_conflict")
	if !reflect.DeepEqual(project, reading.Snapshot()) {
		t.Fatal("手改裁断未整体拒绝")
	}
}
