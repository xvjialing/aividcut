package application

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"aividcut/internal/editor"
	"aividcut/internal/generation"
	"aividcut/internal/media"
	"aividcut/internal/speech"
	"aividcut/internal/speechjobs"
)

type timedApplicationProvider struct {
	*applicationSpeechProvider
	calls  *atomic.Int32
	change func(*speech.TimedAudioResult)
}

func (p *timedApplicationProvider) SynthesizeWithTiming(ctx context.Context, request speech.SynthesizeRequest) (speech.TimedAudioResult, error) {
	p.calls.Add(1)
	stamp := func(v float64) *float64 { return &v }
	result := speech.TimedAudioResult{AudioResult: speech.AudioResult{Operation: speech.Operation{ID: request.ID, ProviderID: speech.ElevenLabsID, ModelID: speech.MultilingualV2, Status: speech.Succeeded}, VoiceID: request.VoiceID, OutputFormat: request.OutputFormat, MIME: "audio/mpeg", Data: append([]byte{}, p.control.audio...)}, TimingComplete: true, Alignment: &speech.CharacterAlignment{Characters: []string{"你", "好"}, StartTimesSeconds: []*float64{stamp(.01), stamp(.07)}, EndTimesSeconds: []*float64{stamp(.06), stamp(.12)}}, NormalizedAlignment: &speech.CharacterAlignment{Characters: []string{"您", "好"}, StartTimesSeconds: []*float64{stamp(.01), stamp(.07)}, EndTimesSeconds: []*float64{stamp(.06), stamp(.12)}}, Warnings: []string{}}
	if p.change != nil {
		p.change(&result)
	}
	return result, nil
}
func timedApplication(t *testing.T, change func(*speech.TimedAudioResult)) (*Service, *atomic.Int32, SpeechSummary) {
	t.Helper()
	s, err := New(media.Toolchain{}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	control := &applicationSpeechControl{}
	calls := &atomic.Int32{}
	options := speechApplicationOptions(t, t.TempDir(), &appSecretStore{values: map[string][]byte{}}, control)
	options.SpeechFactory = func(secrets generation.SecretSource, files speech.MediaSource) (speech.Provider, error) {
		return &timedApplicationProvider{applicationSpeechProvider: &applicationSpeechProvider{control: control, files: files, secrets: secrets}, calls: calls, change: change}, nil
	}
	if err := s.ConfigureGeneration(options); err != nil {
		t.Fatal(err)
	}
	connection := saveApplicationSpeechConnection(t, s, speech.MultilingualV2)
	input := speechjobs.Input{ID: "app-timed-voice", ConnectionID: connection.ID, Mode: speechjobs.Synthesize, Consent: true, Synthesize: &speech.SynthesizeRequest{VoiceID: "fixture-voice", Text: "你好", WithTiming: true}}
	if _, err := s.StartSpeechJob(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	return s, calls, awaitSpeechApplication(t, s, input.ID, speechjobs.Succeeded)
}
func ttsOptions() ImportSpeechAudioOptions {
	return ImportSpeechAudioOptions{AddToTimeline: true, StartFrame: 90, GenerateCaptions: true, CaptionTrackID: "track-caption-1", CaptionLayout: &editor.CaptionLayout{Mode: "word"}, AlignmentSource: "original"}
}
func TestTimedSpeechAudioAndRealWordCaptionsAreOneUndoableImport(t *testing.T) {
	s, calls, summary := timedApplication(t, nil)
	if !summary.TimingRequested || !summary.TimingComplete || !summary.NormalizedTimingComplete {
		t.Fatal("summary缺少独立时间状态")
	}
	detail, err := s.GetSpeechJob(summary.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.TimingChoices) != 2 || !detail.TimingChoices[0].Available || detail.TimingChoices[0].Text != "你好" || detail.TimingChoices[1].Text != "您好" {
		t.Fatal("两套文本来源混淆", detail.TimingChoices)
	}
	before := s.GetBootstrap().Project
	changed, err := s.ImportSpeechAudio(context.Background(), summary.ID, ttsOptions())
	if err != nil {
		t.Fatal(err)
	}
	if len(changed.Project.Assets) != 1 || len(changed.Project.Transcripts) != 1 || len(changed.Project.Clips) != 3 || changed.Project.Revision != before.Revision+1 {
		t.Fatal("音频/源词/字幕未一次上轨")
	}
	transcript := changed.Project.Transcripts[0]
	if transcript.Origin != "tts" || transcript.TimingSource != "original" || len(transcript.RawWords) != 2 || transcript.RawWords[0].StartUs != 10000 || transcript.RawWords[1].EndUs != 120000 {
		t.Fatal("配音伪装ASR或按文字估时间", transcript)
	}
	for _, clip := range changed.Project.Clips {
		if clip.CaptionSource != nil && clip.StartFrame < 90 {
			t.Fatal("字幕未按配音起点映射")
		}
	}
	again, err := s.ImportSpeechAudio(context.Background(), summary.ID, ttsOptions())
	if err != nil || again.Project.Revision != changed.Project.Revision || calls.Load() != 1 {
		t.Fatal("重复导入重放或重新配音", err)
	}
	wrong := ttsOptions()
	wrong.AlignmentSource = "normalized"
	if _, err := s.ImportSpeechAudio(context.Background(), summary.ID, wrong); err == nil {
		t.Fatal("重复导入覆盖已接受原文来源")
	}
	row, err := s.GetSpeechJob(summary.ID)
	if err != nil || row.AcceptedTranscriptID != transcript.ID || row.CaptionTimingSource != "original" {
		t.Fatal("TTS已接受标记不正确", err)
	}
	undone, err := s.Undo()
	if err != nil || projectHash(undone.Project) != projectHash(before) {
		t.Fatal("未整组撤销配音字幕", err)
	}
	if _, err := s.Redo(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "timed-voice.aividcut")
	if _, err := s.SaveProjectTo(path); err != nil {
		t.Fatal(err)
	}
	reopened, err := s.OpenProjectPath(path)
	if err != nil || reopened.Project.Transcripts[0].Origin != "tts" || reopened.Project.Transcripts[0].TimingSource != "original" {
		t.Fatal("配音来源持久化失效", err)
	}
}
func TestTimedSpeechMissingOriginalCanImportAudioOrExplicitNormalized(t *testing.T) {
	s, calls, summary := timedApplication(t, func(r *speech.TimedAudioResult) { r.Alignment.StartTimesSeconds[0] = nil; r.TimingComplete = false })
	detail, err := s.GetSpeechJob(summary.ID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.TimingChoices[0].Available || !detail.TimingChoices[1].Available {
		t.Fatal("原文缺失时间借用另一套时间")
	}
	before := s.GetBootstrap().Project
	if _, err := s.ImportSpeechAudio(context.Background(), summary.ID, ttsOptions()); err == nil {
		t.Fatal("缺时间仍生成原文字幕")
	}
	if projectHash(s.GetBootstrap().Project) != projectHash(before) {
		t.Fatal("字幕失败部分导入音频")
	}
	if _, err := s.ImportSpeechAudio(context.Background(), summary.ID, ImportSpeechAudioOptions{AddToTimeline: true}); err != nil {
		t.Fatal("缺时间丢弃合法音频", err)
	}
	normalized := ttsOptions()
	normalized.AlignmentSource = "normalized"
	changed, err := s.ImportSpeechAudio(context.Background(), summary.ID, normalized)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, clip := range changed.Project.Clips {
		if clip.Kind == "audio" {
			count++
		}
	}
	if count != 1 || changed.Project.Transcripts[0].TimingSource != "normalized" || calls.Load() != 1 {
		t.Fatal("规范化补字幕重复音频或重新调用")
	}
}
func TestTimedSpeechRejectsTextMismatchAndFileTamperAtomically(t *testing.T) {
	s, _, summary := timedApplication(t, func(r *speech.TimedAudioResult) { r.Alignment.Characters = []string{"错", "词"} })
	detail, err := s.GetSpeechJob(summary.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !summary.TimingComplete || detail.TimingChoices[0].Available || detail.TimingChoices[0].Reason == "" {
		t.Fatal("结构完整被当成文字匹配")
	}
	if _, err := s.ImportSpeechAudio(context.Background(), summary.ID, ttsOptions()); err == nil {
		t.Fatal("不匹配原文仍绑定")
	}
	if err := os.WriteFile(summary.Audio.Path, []byte("changed audio"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ImportSpeechAudio(context.Background(), summary.ID, ImportSpeechAudioOptions{AddToTimeline: true}); err == nil {
		t.Fatal("篡改音频仍可导入")
	}
	if len(s.GetBootstrap().Project.Assets) != 0 {
		t.Fatal("错误结果部分导入")
	}
}
func TestTimedSpeechAlreadySplitAudioGetsCaptionsWithoutDuplicateVoice(t *testing.T) {
	s, calls, summary := timedApplication(t, nil)
	base, err := s.ImportSpeechAudio(context.Background(), summary.ID, ImportSpeechAudioOptions{AddToTimeline: true})
	if err != nil {
		t.Fatal(err)
	}
	clip := base.Project.Clips[0]
	split, err := s.ApplyEdit(editor.EditRequest{BaseRevision: base.Project.Revision, IdempotencyKey: "split-timed-voice", Commands: []editor.Command{{Type: "split_clip", ClipID: clip.ID, AtFrame: 2}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApplyEdit(editor.EditRequest{BaseRevision: split.Project.Revision, IdempotencyKey: "remove-left-timed-voice", Commands: []editor.Command{{Type: "delete_clip", ClipID: clip.ID}}}); err != nil {
		t.Fatal(err)
	}
	changed, err := s.ImportSpeechAudio(context.Background(), summary.ID, ttsOptions())
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, value := range changed.Project.Clips {
		if value.Kind == "audio" {
			count++
		}
	}
	if count != 1 || calls.Load() != 1 || len(changed.Project.Transcripts) != 1 {
		t.Fatal("分割后补字幕重复配音")
	}
}
