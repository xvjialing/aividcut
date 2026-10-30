package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"aividcut/internal/editor"
	"aividcut/internal/media"
	"aividcut/internal/speech"
)

func sourceTranscriptFixture(t *testing.T) (*Service, editor.Asset, speech.Transcription) {
	t.Helper()
	directory := t.TempDir()
	path := filepath.Join(directory, "registered-source.bin")
	data := []byte("a locally registered source for content identity checks")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(data)
	asset := editor.Asset{ID: "speech-source", Name: "已登记源", Kind: "video", Path: path, DurationUs: 3_000_000, HasAudio: true, Fingerprint: "sha256:" + hex.EncodeToString(hash[:])}
	project := editor.NewProject("转写工程")
	project.Assets = []editor.Asset{asset}
	clip := editor.DefaultClip("speech-source-clip", "track-video-1", "video")
	clip.AssetID = asset.ID
	clip.DurationFrames = 90
	project.Clips = []editor.Clip{clip}
	engine, err := editor.NewEngine(project)
	if err != nil {
		t.Fatal(err)
	}
	service, err := New(media.Toolchain{}, filepath.Join(directory, "previews"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(service.Close)
	service.engine = engine
	service.savedHash = projectHash(project)
	start, end := int64(100_000), int64(900_000)
	result := speech.Transcription{Operation: speech.Operation{ID: "speech-result", ProviderID: speech.ElevenLabsID, ModelID: speech.ScribeV2, Status: speech.Succeeded}, AssetID: asset.ID, SourceSHA256: hex.EncodeToString(hash[:]), SourceDurationUs: asset.DurationUs, Language: "zh", Text: "原始话语", TimingComplete: true, Words: []speech.Word{{TimedText: speech.TimedText{Text: "原始话语", StartUs: &start, EndUs: &end}, Type: "word", TimingComplete: true, SpeakerID: "speaker-1"}, {TimedText: speech.TimedText{Text: " ", StartUs: nil, EndUs: nil}, Type: "spacing"}}}
	return service, asset, result
}

func TestAcceptSpeechResultBindCorrectDetachAndUndo(t *testing.T) {
	s, asset, result := sourceTranscriptFixture(t)
	p := s.GetBootstrap().Project
	accepted, err := s.acceptSpeechResult(context.Background(), "speech-result", p.ID, asset.ID, asset.Fingerprint, asset.DurationUs, result, AcceptTranscriptOptions{GenerateCaptions: true, TrackID: "track-caption-1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(accepted.Project.Transcripts) != 1 || len(accepted.Project.Transcripts[0].RawWords) != 1 {
		t.Fatal("原始普通词未接纳或把spacing当字幕词")
	}
	var caption editor.Clip
	for _, clip := range accepted.Project.Clips {
		if clip.CaptionSource != nil {
			caption = clip
		}
	}
	if caption.ID == "" || caption.Text != "原始话语" {
		t.Fatal("没有来源绑定字幕")
	}
	transcript := accepted.Project.Transcripts[0]
	words := append([]editor.SourceWord(nil), transcript.AcceptedWords...)
	words[0].Text = "纠错后的原话"
	corrected, err := s.CorrectSourceTranscript(context.Background(), CorrectTranscriptInput{ProjectID: p.ID, TranscriptID: transcript.ID, TranscriptRevision: transcript.Revision, Words: words})
	if err != nil {
		t.Fatal(err)
	}
	if corrected.Project.Transcripts[0].RawWords[0].Text != "原始话语" || corrected.Project.Transcripts[0].AcceptedWords[0].Text != "纠错后的原话" {
		t.Fatal("纠错覆盖原始证据或接受版本未变化")
	}
	if _, err := s.CorrectSourceTranscript(context.Background(), CorrectTranscriptInput{ProjectID: p.ID, TranscriptID: transcript.ID, TranscriptRevision: transcript.Revision, Words: words}); err == nil {
		t.Fatal("接受旧转写版本覆盖")
	}
	detached, err := s.DetachCaption(context.Background(), DetachCaptionInput{ProjectID: p.ID, ClipID: caption.ID})
	if err != nil {
		t.Fatal(err)
	}
	for _, clip := range detached.Project.Clips {
		if clip.ID == caption.ID && clip.CaptionSource != nil {
			t.Fatal("没有解除来源绑定")
		}
	}
	undone, err := s.Undo()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, clip := range undone.Project.Clips {
		if clip.ID == caption.ID && clip.CaptionSource != nil {
			found = true
		}
	}
	if !found {
		t.Fatal("解除绑定不能撤销")
	}
}

func TestSpeechSourceContentChangeAndForeignProjectAreRejected(t *testing.T) {
	s, asset, result := sourceTranscriptFixture(t)
	p := s.GetBootstrap().Project
	info, err := os.Stat(asset.Path)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(asset.Path)
	if err != nil {
		t.Fatal(err)
	}
	data[0] = 'b'
	if err := os.WriteFile(asset.Path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(asset.Path, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.acceptSpeechResult(context.Background(), "speech-result", p.ID, asset.ID, asset.Fingerprint, asset.DurationUs, result, AcceptTranscriptOptions{}); err == nil {
		t.Fatal("只核对元数据，没有发现等长等时间的内容变化")
	}
	if s.GetBootstrap().Project.Revision != p.Revision {
		t.Fatal("失败改变工程")
	}
	if _, err := s.CorrectSourceTranscript(context.Background(), CorrectTranscriptInput{ProjectID: "other", TranscriptID: "anything", TranscriptRevision: 1}); err == nil {
		t.Fatal("纠错缺少工程范围约束")
	}
	if _, err := s.DetachCaption(context.Background(), DetachCaptionInput{ProjectID: "other", ClipID: "anything"}); err == nil {
		t.Fatal("解除绑定缺少工程范围约束")
	}
}

func TestSpeechAcceptTimingAndCaptionLockAreAtomic(t *testing.T) {
	s, asset, result := sourceTranscriptFixture(t)
	p := s.GetBootstrap().Project
	result.Words[0].StartUs = nil
	if _, err := s.acceptSpeechResult(context.Background(), "missing-time", p.ID, asset.ID, asset.Fingerprint, asset.DurationUs, result, AcceptTranscriptOptions{}); err == nil {
		t.Fatal("伪造缺失时间")
	}
	start := int64(100_000)
	result.Words[0].StartUs = &start
	track := p.Tracks[0]
	track.Locked = true
	if _, err := s.ApplyEdit(editor.EditRequest{BaseRevision: p.Revision, IdempotencyKey: "lock-captions", Commands: []editor.Command{{Type: "update_track", Track: &track}}}); err != nil {
		t.Fatal(err)
	}
	before := s.GetBootstrap().Project
	if _, err := s.acceptSpeechResult(context.Background(), "locked-caption", p.ID, asset.ID, asset.Fingerprint, asset.DurationUs, result, AcceptTranscriptOptions{GenerateCaptions: true, TrackID: track.ID}); err == nil {
		t.Fatal("在锁定字幕轨上隐式生成")
	}
	after := s.GetBootstrap().Project
	if after.Revision != before.Revision || len(after.Transcripts) != 0 {
		t.Fatal("字幕失败仍部分接纳转写")
	}
}

func TestSpeechAcceptedStateCanBeRecreatedAfterUndo(t *testing.T) {
	s, asset, result := sourceTranscriptFixture(t)
	p := s.GetBootstrap().Project
	options := AcceptTranscriptOptions{GenerateCaptions: true, TrackID: "track-caption-1"}
	first, err := s.acceptSpeechResult(context.Background(), "repeat-source", p.ID, asset.ID, asset.Fingerprint, asset.DurationUs, result, options)
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.acceptSpeechResult(context.Background(), "repeat-source", p.ID, asset.ID, asset.Fingerprint, asset.DurationUs, result, options)
	if err != nil || again.Project.Revision != first.Project.Revision {
		t.Fatal("重复接纳产生第二次编辑", err)
	}
	if _, err := s.Undo(); err != nil {
		t.Fatal(err)
	}
	reaccepted, err := s.acceptSpeechResult(context.Background(), "repeat-source", p.ID, asset.ID, asset.Fingerprint, asset.DurationUs, result, options)
	if err != nil || len(reaccepted.Project.Transcripts) != 1 {
		t.Fatal("撤销后不能重新接纳现有本地结果", err)
	}
}
