package editor

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestClipboardSnapshotSurvivesOriginalEditsAndDeletion(t *testing.T) {
	e := selectionFixture(t)
	before := e.Snapshot()
	c, err := CaptureTimelineClipboard(before, []string{"video-clip", "title"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, e.Snapshot()) || e.CanUndo() {
		t.Fatal("复制修改了工程")
	}
	video := before.Clips[0]
	video.Name = "后来改名"
	video.Transform.X = 88
	p := apply(t, e, Command{Type: "update_clip", Clip: &video}, Command{Type: "delete_clips", ClipIDs: []string{"video-clip", "title"}})
	if len(p.Clips) != 0 {
		t.Fatal("未删除完整来源")
	}
	req := EditRequest{BaseRevision: p.Revision, IdempotencyKey: "paste-old-snapshot", Commands: []Command{ClipboardPasteCommand(c, c.Info().ID, 180, 0)}}
	after, err := e.Apply(req)
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Clips) != 3 || after.Clips[0].Name != before.Clips[0].Name || after.Clips[0].Transform.X != before.Clips[0].Transform.X || after.Clips[0].StartFrame != 180 || after.Clips[2].StartFrame != 270 {
		t.Fatal("粘贴没有保留旧属性及间距")
	}
	if after.Clips[0].LinkGroup == "pair" || after.Clips[0].LinkGroup == "" || after.Clips[0].LinkGroup != after.Clips[1].LinkGroup {
		t.Fatal("新旧链接组混淆")
	}
	selected := ClipboardPasteSelection(after, c.Info().ID, req.IdempotencyKey)
	if len(selected) != 3 {
		t.Fatal("没有找到本次副本")
	}
	req.Commands = []Command{ClipboardPasteCommand(nil, c.Info().ID, 180, 0)}
	retry, err := e.Apply(req)
	if err != nil || !reflect.DeepEqual(retry, after) {
		t.Fatal("快照被替换后旧请求不能幂等恢复", err)
	}
	undone, err := e.Undo()
	if err != nil || len(undone.Clips) != 0 {
		t.Fatal("粘贴没有整组撤销", err)
	}
	redone, err := e.Redo()
	if err != nil || !reflect.DeepEqual(ClipboardPasteSelection(redone, c.Info().ID, req.IdempotencyKey), selected) {
		t.Fatal("重做副本身份改变", err)
	}
}
func TestClipboardKeepsDeletedSourceCaptionOverridesAndExclusions(t *testing.T) {
	e := transcriptFixture(t)
	p := e.Snapshot()
	manual := boundByWord(t, p, "speech-clip", "hello")
	manual.Text = "复制时的文案"
	manual.TextStyle.FontSize = 67
	world := boundByWord(t, p, "speech-clip", "world")
	p = apply(t, e, Command{Type: "update_clip", Clip: &manual}, Command{Type: "delete_clip", ClipID: world.ID})
	c, err := CaptureTimelineClipboard(p, []string{"speech-clip"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	manual.Text = "后来改掉的文案"
	words := append([]SourceWord{}, p.Transcripts[0].AcceptedWords...)
	words[1].Text = "更新后的源词"
	p = apply(t, e, Command{Type: "update_clip", Clip: &manual}, Command{Type: "correct_transcript", TranscriptID: "transcript", TranscriptRevision: p.Transcripts[0].Revision, Words: words}, Command{Type: "delete_clip", ClipID: "speech-clip"})
	p = apply(t, e, ClipboardPasteCommand(c, c.Info().ID, 300, 0))
	var source string
	for _, clip := range p.Clips {
		if clip.Kind == "video" {
			source = clip.ID
		}
	}
	if source == "" {
		t.Fatal("旧源被删后未粘贴新源")
	}
	got := boundByWord(t, p, source, "hello")
	if got.Text != "复制时的文案" || got.TextStyle.FontSize != 67 || !got.CaptionSource.TextOverride {
		t.Fatal("旧手改没有通过独立父源继承")
	}
	if boundByWord(t, p, source, "cut").Text != "更新后的源词" {
		t.Fatal("自动字幕没有使用当前源转写")
	}
	for _, clip := range p.Clips {
		if clip.CaptionSource != nil && clip.CaptionSource.SourceClipID == source && clip.CaptionSource.WordIDs[0] == "world" {
			t.Fatal("被排除词复活")
		}
	}
	if p.Transcripts[0].RawWords[1].Text != "左边界" {
		t.Fatal("粘贴伪造了原始识别证据")
	}
}
func TestClipboardStandaloneCaptionIsIndependentWithoutChangingSource(t *testing.T) {
	e := transcriptFixture(t)
	p := e.Snapshot()
	caption := boundByWord(t, p, "speech-clip", "hello")
	c, err := CaptureTimelineClipboard(p, []string{caption.ID}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if c.Info().DetachedCaptions != 1 {
		t.Fatal("摘要未解释独立字幕")
	}
	p = apply(t, e, ClipboardPasteCommand(c, c.Info().ID, 300, 0))
	var found bool
	for _, clip := range p.Clips {
		if clip.StartFrame == 300 && clip.Kind == "caption" {
			found = true
			if clip.CaptionSource != nil || clip.Text != caption.Text {
				t.Fatal("独立字幕不正确")
			}
		}
	}
	if !found || boundByWord(t, p, "speech-clip", "hello").ID != caption.ID {
		t.Fatal("复制改变了原字幕")
	}
}
func TestClipboardRejectsUnsafeRestoreAtomically(t *testing.T) {
	for _, kind := range []string{"collision", "foreign-project", "asset", "clock", "locked", "nil-snapshot"} {
		t.Run(kind, func(t *testing.T) {
			e := selectionFixture(t)
			p := e.Snapshot()
			c, err := CaptureTimelineClipboard(p, []string{"video-clip", "title"}, nil)
			if err != nil {
				t.Fatal(err)
			}
			at := int64(180)
			switch kind {
			case "collision":
				at = 0
			case "foreign-project":
				p.ID = "other"
			case "asset":
				p.Assets[0].DurationUs++
			case "clock":
				p.Clips = nil
				p.Settings.FPSNum = 24
			case "locked":
				p.Tracks[trackIndex(&p, "track-audio-1")].Locked = true
			}
			e, err = NewEngine(p)
			if err != nil {
				t.Fatal(err)
			}
			prior := e.Snapshot()
			snapshot := c
			if kind == "nil-snapshot" {
				snapshot = nil
			}
			_, err = e.Apply(EditRequest{BaseRevision: p.Revision, IdempotencyKey: kind, Commands: []Command{ClipboardPasteCommand(snapshot, c.Info().ID, at, 0)}})
			if err == nil || !reflect.DeepEqual(prior, e.Snapshot()) || e.CanUndo() {
				t.Fatal("不安全恢复没有整体拒绝", err)
			}
		})
	}
}
func TestClipboardRejectsRemovedManualWordAfterOriginalWasDeleted(t *testing.T) {
	e := transcriptFixture(t)
	p := e.Snapshot()
	caption := boundByWord(t, p, "speech-clip", "hello")
	caption.Text = "不能丢失的手改"
	p = apply(t, e, Command{Type: "update_clip", Clip: &caption})
	c, err := CaptureTimelineClipboard(p, []string{"speech-clip"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	p = apply(t, e, Command{Type: "delete_clip", ClipID: "speech-clip"})
	words := []SourceWord{}
	for _, w := range p.Transcripts[0].AcceptedWords {
		if w.ID != "hello" {
			words = append(words, w)
		}
	}
	p = apply(t, e, Command{Type: "correct_transcript", TranscriptID: "transcript", TranscriptRevision: p.Transcripts[0].Revision, Words: words})
	before := e.Snapshot()
	_, err = e.Apply(EditRequest{BaseRevision: p.Revision, IdempotencyKey: "missing-manual", Commands: []Command{ClipboardPasteCommand(c, c.Info().ID, 300, 0)}})
	assertCode(t, err, "caption_override_conflict")
	if !reflect.DeepEqual(before, e.Snapshot()) {
		t.Fatal("失败留下部分副本")
	}
}

func TestClipboardBudgetRejectsOversizedSnapshot(t *testing.T) {
	p := NewProject("剪贴板预算")
	ids := make([]string, 12000)
	content := strings.Repeat("a", 6000)
	for i := range ids {
		ids[i] = fmt.Sprintf("budget-%d", i)
		c := DefaultClip(ids[i], "track-text-1", "text")
		c.Text = content
		c.StartFrame = int64(i * 3)
		c.DurationFrames = 2
		p.Clips = append(p.Clips, c)
	}
	if _, err := CaptureTimelineClipboard(p, ids, nil); err == nil {
		t.Fatal("超过64MiB的快照未被拒绝")
	} else {
		assertCode(t, err, "clipboard_budget")
	}
}

func TestClipboardExplicitUnlinkedCopyKeepsOriginalLinks(t *testing.T) {
	e := selectionFixture(t)
	p := e.Snapshot()
	no := false
	c, err := CaptureTimelineClipboard(p, []string{"video-clip"}, &no)
	if err != nil {
		t.Fatal(err)
	}
	pasted := apply(t, e, ClipboardPasteCommand(c, c.Info().ID, 180, 0))
	if len(pasted.Clips) != 4 || pasted.Clips[3].LinkGroup != "" || pasted.Clips[0].LinkGroup != "pair" || pasted.Clips[1].LinkGroup != "pair" {
		t.Fatal("不联动复制改写了原组或重新关联副本")
	}
}
