package application

import (
	"bytes"
	"context"
	"os"
	"testing"

	"aividcut/internal/editor"
	"aividcut/internal/playback"
)

func TestSelectionConstructAndMoveKeepActualPCMAndPictures(t *testing.T) {
	if os.Getenv("AIVIDCUT_MLT_WORKER") == "" {
		t.Skip("需要显式原生worker，不把跳过当作真实批量媒体通过")
	}
	tools := preciseApplicationTools(t)
	s := preciseApplication(t, tools)
	state, err := s.ImportPaths(context.Background(), []string{preciseApplicationWAV(t)})
	if err != nil {
		t.Fatal(err)
	}
	p := state.Project
	p.Settings.Width, p.Settings.Height = 320, 180
	p.Settings.FPSNum, p.Settings.FPSDen = 30000, 1001
	audio := editor.DefaultClip("audio", "track-audio-1", "audio")
	audio.AssetID = p.Assets[0].ID
	audio, err = editor.AudioClipToSourceEnd(audio, p.Assets[0], p.Settings)
	if err != nil {
		t.Fatal(err)
	}
	title := editor.DefaultClip("title", "track-text-1", "text")
	title.Text = "成组编辑"
	title.TextStyle.FontSize = 24
	title.DurationFrames = 10
	settings := p.Settings
	before, err := s.ApplyEdit(editor.EditRequest{BaseRevision: p.Revision, IdempotencyKey: "selection-media", Commands: []editor.Command{{Type: "update_project", Settings: &settings}, {Type: "add_clip", Clip: &audio}, {Type: "add_clip", Clip: &title}}})
	if err != nil {
		t.Fatal(err)
	}
	collect := func(p editor.Project, start int64) ([]byte, []byte) {
		t.Helper()
		session, err := playback.Start(context.Background(), PlaybackEnvironment())
		if err != nil {
			t.Fatal(err)
		}
		defer session.Close()
		if _, err = session.Load(context.Background(), p); err != nil {
			t.Fatal(err)
		}
		if _, err = session.Preview(context.Background(), start); err != nil {
			t.Fatal(err)
		}
		if err = session.Play(context.Background()); err != nil {
			t.Fatal(err)
		}
		var pcm, image []byte
		for at := start; at < start+10; at++ {
			frame, err := session.Next(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if frame.Position != at {
				t.Fatal("帧序变化")
			}
			pcm = append(pcm, frame.PCM...)
			image = append(image, frame.RGBA...)
		}
		return pcm, image
	}
	pcm, image := collect(before.Project, 0)
	after, err := s.ApplyEdit(editor.EditRequest{BaseRevision: before.Project.Revision, IdempotencyKey: "move-media-group", Commands: []editor.Command{{Type: "move_clips", ClipIDs: []string{audio.ID, title.ID}, FrameDelta: 30}}})
	if err != nil {
		t.Fatal(err)
	}
	movedPCM, movedImage := collect(after.Project, 30)
	if !bytes.Equal(pcm, movedPCM) || !bytes.Equal(image, movedImage) {
		t.Fatal("统一批量移动改变实际取样或画面", len(pcm), len(movedPCM))
	}
	undone, err := s.Undo()
	if err != nil {
		t.Fatal(err)
	}
	if undone.Project.Clips[0].StartFrame != 0 || undone.Project.Clips[1].StartFrame != 0 {
		t.Fatal("一次撤销未恢复全部片段")
	}
	split, err := s.ApplyEdit(editor.EditRequest{BaseRevision: undone.Project.Revision, IdempotencyKey: "split-media-selection", Commands: []editor.Command{{Type: "split_clips", ClipIDs: []string{audio.ID, title.ID}, AtFrame: 2}}})
	if err != nil {
		t.Fatal(err)
	}
	splitPCM, splitImage := collect(split.Project, 0)
	if !bytes.Equal(pcm, splitPCM) || !bytes.Equal(image, splitImage) {
		t.Fatal("成组分割改变实际音画")
	}
	ids := []string{}
	for _, c := range split.Project.Clips {
		ids = append(ids, c.ID)
	}
	copied, err := s.ApplyEdit(editor.EditRequest{BaseRevision: split.Project.Revision, IdempotencyKey: "copy-media-selection", Commands: []editor.Command{{Type: "duplicate_clips", ClipIDs: ids, AtFrame: 30}}})
	if err != nil {
		t.Fatal(err)
	}
	copiedPCM, copiedImage := collect(copied.Project, 30)
	if !bytes.Equal(pcm, copiedPCM) || !bytes.Equal(image, copiedImage) {
		t.Fatal("分割后成组复制改变实际音画")
	}
	undoCopy, err := s.Undo()
	if err != nil || len(undoCopy.Project.Clips) != 4 {
		t.Fatal("一次撤销未恢复复制前的4个片段", err)
	}
	undoSplit, err := s.Undo()
	if err != nil || len(undoSplit.Project.Clips) != 2 {
		t.Fatal("一次撤销未恢复分割前的2个片段", err)
	}
	cut, err := s.CutTimelineSelection(ClipboardCutInput{ClipboardSelectionInput: ClipboardSelectionInput{ProjectID: undoSplit.Project.ID, SourceEpoch: undoSplit.SourceEpoch, BaseRevision: undoSplit.Project.Revision, ClipIDs: []string{audio.ID, title.ID}}, IdempotencyKey: "cut-actual-media"})
	if err != nil || len(cut.Bootstrap.Project.Clips) != 0 {
		t.Fatal("真实媒体剪切失败", err)
	}
	pasted, err := s.PasteTimelineSelection(ClipboardPasteInput{ProjectID: cut.Bootstrap.Project.ID, SourceEpoch: cut.Bootstrap.SourceEpoch, BaseRevision: cut.Bootstrap.Project.Revision, ClipboardID: cut.Clipboard.ID, IdempotencyKey: "paste-actual-media", AtFrame: 30})
	if err != nil {
		t.Fatal(err)
	}
	clipboardPCM, clipboardImage := collect(pasted.Bootstrap.Project, 30)
	if !bytes.Equal(pcm, clipboardPCM) || !bytes.Equal(image, clipboardImage) {
		t.Fatal("原片段删去后剪贴板改变实际声音或画面")
	}
	undoPaste, err := s.Undo()
	if err != nil || len(undoPaste.Project.Clips) != 0 {
		t.Fatal("粘贴没有一次撤销", err)
	}
	undoCut, err := s.Undo()
	if err != nil || len(undoCut.Project.Clips) != 2 {
		t.Fatal("剪切没有一次撤销", err)
	}
	t.Logf("真实剪切后粘贴：画面%d字节、PCM%d字节完全一致，剪切和粘贴各一次撤销", len(image), len(pcm))
	t.Logf("成组分割/复制后对应10帧：画面%d字节、PCM%d字节完全一致，分别一次撤销", len(image), len(pcm))
	t.Logf("实际整体平移30帧，10帧画面%d字节与PCM%d字节完全一致；一次撤销恢复", len(image), len(pcm))
}
