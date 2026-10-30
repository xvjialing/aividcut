package application

import (
	"aividcut/internal/editor"
	"context"
	"fmt"
	"golang.org/x/image/font/gofont/goregular"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func batchFixture(t *testing.T) (*Service, Bootstrap, string, string) {
	t.Helper()
	s := newRelinkService(t)
	source := fixtureImage(t)
	old := t.TempDir()
	for _, name := range []string{"one.png", "sub/one.png"} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(old, name)), 0700); err != nil {
			t.Fatal(err)
		}
		data, _ := os.ReadFile(source)
		if err := os.WriteFile(filepath.Join(old, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	b, err := s.ImportPaths(context.Background(), []string{filepath.Join(old, "one.png"), filepath.Join(old, "sub/one.png")})
	if err != nil {
		t.Fatal(err)
	}
	clip := editor.DefaultClip("batch-image", "track-video-1", "image")
	clip.AssetID = b.Project.Assets[0].ID
	track := b.Project.Tracks[2]
	track.Locked = true
	b, err = s.ApplyEdit(editor.EditRequest{BaseRevision: b.Project.Revision, IdempotencyKey: "batch-fixture", Commands: []editor.Command{{Type: "add_clip", Clip: &clip}, {Type: "update_track", Track: &track}}})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "offline.aividcut")
	if _, err = s.SaveProjectTo(path); err != nil {
		t.Fatal(err)
	}
	newRoot := filepath.Join(t.TempDir(), "moved")
	if err = os.Rename(old, newRoot); err != nil {
		t.Fatal(err)
	}
	b, err = s.OpenProjectPath(path)
	if err != nil {
		t.Fatal(err)
	}
	return s, b, old, newRoot
}

func TestBatchRelinkFontLUTImageAndExactAudioKeepAllMetadata(t *testing.T) {
	ctx := context.Background()
	tools := preciseApplicationTools(t)
	s, err := New(tools, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	old := t.TempDir()
	files := map[string][]byte{"font.ttf": goregular.TTF, "look.cube": []byte("LUT_3D_SIZE 2\n0 0 0\n0 0 1\n0 1 0\n0 1 1\n1 0 0\n1 0 1\n1 1 0\n1 1 1\n")}
	for name, source := range map[string]string{"image.png": fixtureImage(t), "voice.wav": preciseApplicationWAV(t)} {
		data, e := os.ReadFile(source)
		if e != nil {
			t.Fatal(e)
		}
		files[name] = data
	}
	paths := []string{}
	for _, name := range []string{"font.ttf", "look.cube", "image.png", "voice.wav"} {
		path := filepath.Join(old, name)
		if err = os.WriteFile(path, files[name], 0600); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, path)
	}
	b, err := s.ImportPaths(ctx, paths)
	if err != nil {
		t.Fatal(err)
	}
	if b.Project.Assets[1].Fingerprint == "" {
		t.Fatal("新导入LUT必须保留内容指纹")
	}
	image := editor.DefaultClip("graded", "track-video-1", "image")
	image.AssetID = b.Project.Assets[2].ID
	image.LUT = &editor.LUT{AssetID: b.Project.Assets[1].ID, Intensity: .35}
	title := editor.DefaultClip("font-title", "track-text-1", "text")
	title.Text = "restore"
	title.TextStyle.FontAssetID = b.Project.Assets[0].ID
	b, err = s.ApplyEdit(editor.EditRequest{BaseRevision: b.Project.Revision, IdempotencyKey: "placed", Commands: []editor.Command{{Type: "add_clip", Clip: &image}, {Type: "add_clip", Clip: &title}}})
	if err != nil {
		t.Fatal(err)
	}
	b, err = s.AddAudioToTimeline(ctx, AddAudioInput{ProjectID: b.Project.ID, ProjectRevision: b.Project.Revision, AssetID: b.Project.Assets[3].ID, ClipID: "exact-voice", TrackID: "track-audio-1"})
	if err != nil {
		t.Fatal(err)
	}
	transcript := editor.Transcript{ID: "batch-source-words", AssetID: b.Project.Assets[3].ID, SourceFingerprint: b.Project.Assets[3].Fingerprint, RawWords: []editor.SourceWord{{ID: "word", StartUs: 10000, EndUs: 100000, Text: "原词"}}}
	// 夹具在核心构造已接受词，不把普通应用编辑接口当作ASR原始结果写入入口。
	s.mu.Lock()
	_, err = s.engine.Apply(editor.EditRequest{BaseRevision: b.Project.Revision, IdempotencyKey: "source-captions", Commands: []editor.Command{{Type: "accept_transcript", Transcript: &transcript}, {Type: "generate_bound_captions", TranscriptID: transcript.ID, TrackID: "track-caption-1"}}})
	s.mu.Unlock()
	b = s.GetBootstrap()
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Project.Transcripts) != 1 || len(b.Project.Clips) != 4 {
		t.Fatal("夹具没有真实派生字幕")
	}
	commands := []editor.Command{}
	for _, track := range b.Project.Tracks {
		track.Locked = true
		commands = append(commands, editor.Command{Type: "update_track", Track: &track})
	}
	b, err = s.ApplyEdit(editor.EditRequest{BaseRevision: b.Project.Revision, IdempotencyKey: "all-locked", Commands: commands})
	if err != nil {
		t.Fatal(err)
	}
	projectPath := filepath.Join(t.TempDir(), "all.aividcut")
	if _, err = s.SaveProjectTo(projectPath); err != nil {
		t.Fatal(err)
	}
	newRoot := filepath.Join(t.TempDir(), "moved")
	if err = os.Rename(old, newRoot); err != nil {
		t.Fatal(err)
	}
	b, err = s.OpenProjectPath(projectPath)
	if err != nil {
		t.Fatal(err)
	}
	before := editor.CloneProject(b.Project)
	input := batchInput(b, "four-kinds", old, newRoot)
	preview, err := s.PreviewBatchRelink(ctx, input)
	if err != nil || preview.Matched != 4 {
		t.Fatal(preview, err)
	}
	after, err := s.ApplyBatchRelink(ctx, BatchRelinkRef{ProjectScope: input.ProjectScope, ID: input.ID})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before.Clips, after.Project.Clips) || !reflect.DeepEqual(before.Tracks, after.Project.Tracks) || !reflect.DeepEqual(before.Transcripts, after.Project.Transcripts) {
		t.Fatal("改变了LUT/字体/精确音尾/字幕或锁轨")
	}
	for i, a := range after.Project.Assets {
		original := before.Assets[i]
		original.Path, original.Missing, original.URL, original.ThumbnailURL = a.Path, a.Missing, a.URL, a.ThumbnailURL
		if !reflect.DeepEqual(original, a) {
			t.Fatal("业务元数据改变", a)
		}
	}
	if count := after.Project.Assets[3].AudioSampleCount; count == nil || *count != 5777 {
		t.Fatal("精确音频计数丢失", count)
	}
}
func batchInput(b Bootstrap, id, old, new string) BatchRelinkInput {
	return BatchRelinkInput{ProjectScope: versionScope(b), ID: id, OldRoot: old, NewRoot: new}
}
func TestBatchRelinkRealMovedImagesAtomicUndoLockedAndReopen(t *testing.T) {
	s, b, old, new := batchFixture(t)
	before := editor.CloneProject(b.Project)
	input := batchInput(b, "real-batch", old, new)
	preview, err := s.PreviewBatchRelink(context.Background(), input)
	if err != nil || preview.Matched != 2 {
		t.Fatal(preview, err)
	}
	if got := s.GetBootstrap(); got.Dirty || got.Project.Revision != b.Project.Revision {
		t.Fatal("核对不能修改工程")
	}
	after, err := s.ApplyBatchRelink(context.Background(), BatchRelinkRef{ProjectScope: input.ProjectScope, ID: input.ID})
	if err != nil {
		t.Fatal(err)
	}
	if !after.Dirty || after.Project.Revision != before.Revision+1 || !reflect.DeepEqual(after.Project.Clips, before.Clips) || !reflect.DeepEqual(after.Project.Tracks, before.Tracks) {
		t.Fatal("未保持原子编排/锁轨")
	}
	for _, a := range after.Project.Assets {
		if a.Missing || !filepath.IsAbs(a.Path) {
			t.Fatal(a)
		}
	}
	if _, err := s.ApplyBatchRelink(context.Background(), BatchRelinkRef{ProjectScope: input.ProjectScope, ID: input.ID}); err == nil {
		t.Fatal("不得重复提交")
	}
	undo, err := s.Undo()
	if err != nil {
		t.Fatal(err)
	}
	for i, a := range undo.Project.Assets {
		if a.Path != before.Assets[i].Path || !a.Missing {
			t.Fatal("一次Undo应恢复全部原路径")
		}
	}
	if _, err = s.Redo(); err != nil {
		t.Fatal(err)
	}
	saved, err := s.SaveProject()
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := s.OpenProjectPath(saved.ProjectPath)
	if err != nil || reopened.Dirty {
		t.Fatal(err)
	}
	for _, a := range reopened.Project.Assets {
		if a.Missing {
			t.Fatal("重开丢失修复")
		}
	}
}
func TestBatchRelinkChangedFileScopeAndEarlyCancelRejectWholeBatch(t *testing.T) {
	t.Run("cancelled-records-do-not-use-plan-quota", func(t *testing.T) {
		s, b, old, new := batchFixture(t)
		for i := 0; i < 40; i++ {
			if err := s.CancelBatchRelink(BatchRelinkRef{ProjectScope: versionScope(b), ID: fmt.Sprintf("cancelled-%d", i)}); err != nil {
				t.Fatal(err)
			}
		}
		if p, err := s.PreviewBatchRelink(context.Background(), batchInput(b, "after-repeated-cancel", old, new)); err != nil || p.Matched != 2 {
			t.Fatal("短取消凭据不能耗尽准备计划额度", p, err)
		}
	})
	t.Run("expired", func(t *testing.T) {
		s, b, old, new := batchFixture(t)
		input := batchInput(b, "expired-plan", old, new)
		if _, err := s.PreviewBatchRelink(context.Background(), input); err != nil {
			t.Fatal(err)
		}
		s.mu.Lock()
		s.batchRelinks[input.ID].created = time.Now().Add(-11 * time.Minute)
		s.mu.Unlock()
		if _, err := s.ApplyBatchRelink(context.Background(), BatchRelinkRef{ProjectScope: input.ProjectScope, ID: input.ID}); err == nil {
			t.Fatal("过期核对不能应用")
		}
		if s.engine.Snapshot().Revision != b.Project.Revision {
			t.Fatal("过期拒绝不得修改工程")
		}
	})
	t.Run("cancel-prepared", func(t *testing.T) {
		s, b, old, new := batchFixture(t)
		input := batchInput(b, "cancel-plan", old, new)
		if _, err := s.PreviewBatchRelink(context.Background(), input); err != nil {
			t.Fatal(err)
		}
		ref := BatchRelinkRef{ProjectScope: input.ProjectScope, ID: input.ID}
		if err := s.CancelBatchRelink(ref); err != nil {
			t.Fatal(err)
		}
		if _, err := s.ApplyBatchRelink(context.Background(), ref); err == nil {
			t.Fatal("取消后的计划不能应用")
		}
	})
	t.Run("file", func(t *testing.T) {
		s, b, old, new := batchFixture(t)
		input := batchInput(b, "changed-file", old, new)
		if _, err := s.PreviewBatchRelink(context.Background(), input); err != nil {
			t.Fatal(err)
		}
		os.WriteFile(filepath.Join(new, "sub/one.png"), []byte("changed"), 0600)
		if _, err := s.ApplyBatchRelink(context.Background(), BatchRelinkRef{ProjectScope: input.ProjectScope, ID: input.ID}); err == nil {
			t.Fatal("候选变化应整批拒绝")
		}
		if s.engine.Snapshot().Revision != b.Project.Revision {
			t.Fatal("不得部分应用")
		}
	})
	t.Run("reopen", func(t *testing.T) {
		s, b, old, new := batchFixture(t)
		input := batchInput(b, "old-instance", old, new)
		if _, err := s.PreviewBatchRelink(context.Background(), input); err != nil {
			t.Fatal(err)
		}
		s.OpenProjectPath(b.ProjectPath)
		if _, err := s.ApplyBatchRelink(context.Background(), BatchRelinkRef{ProjectScope: input.ProjectScope, ID: input.ID}); err == nil {
			t.Fatal("同ID新实例应拒绝")
		}
	})
	t.Run("revision", func(t *testing.T) {
		s, b, old, new := batchFixture(t)
		input := batchInput(b, "old-revision", old, new)
		if _, err := s.PreviewBatchRelink(context.Background(), input); err != nil {
			t.Fatal(err)
		}
		track := b.Project.Tracks[0]
		track.Name = "changed"
		if _, err := s.ApplyEdit(editor.EditRequest{BaseRevision: b.Project.Revision, IdempotencyKey: "external-edit", Commands: []editor.Command{{Type: "update_track", Track: &track}}}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.ApplyBatchRelink(context.Background(), BatchRelinkRef{ProjectScope: input.ProjectScope, ID: input.ID}); err == nil {
			t.Fatal("旧修订应拒绝")
		}
	})
	t.Run("cancel-before-preview", func(t *testing.T) {
		s, b, old, new := batchFixture(t)
		input := batchInput(b, "early-cancel", old, new)
		s.CancelBatchRelink(BatchRelinkRef{ProjectScope: input.ProjectScope, ID: input.ID})
		if _, err := s.PreviewBatchRelink(context.Background(), input); err == nil {
			t.Fatal("取消后晚到请求不得复活")
		}
	})
}
