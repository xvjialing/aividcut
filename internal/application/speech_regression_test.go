package application

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"aividcut/internal/editor"
	"aividcut/internal/speech"
	"aividcut/internal/speechjobs"
)

func TestPublicApplyEditCannotForgeAcceptedSourceTranscript(t *testing.T) {
	service, asset, result := sourceTranscriptFixture(t)
	before := service.GetBootstrap().Project
	data, err := os.ReadFile(asset.Path)
	if err != nil {
		t.Fatal(err)
	}
	data[0] ^= 1
	if err := os.WriteFile(asset.Path, data, 0600); err != nil {
		t.Fatal(err)
	}
	transcript := editor.Transcript{ID: "untrusted-asr", AssetID: asset.ID, SourceFingerprint: asset.Fingerprint, RawWords: []editor.SourceWord{{ID: "untrusted-word", StartUs: 100_000, EndUs: 900_000, Text: "前端伪造原始识别"}}}
	_, err = service.ApplyEdit(editor.EditRequest{BaseRevision: before.Revision, IdempotencyKey: "reject-untrusted-transcript", Commands: []editor.Command{{Type: "accept_transcript", Transcript: &transcript}, {Type: "generate_bound_captions", TranscriptID: transcript.ID, TrackID: "track-caption-1"}}})
	if err == nil {
		t.Fatal("普通编辑桥不能绕过持久任务和实际源内容核查来伪造原始识别")
	}
	if after := service.GetBootstrap().Project; after.Revision != before.Revision || len(after.Transcripts) != 0 {
		t.Fatal("拒绝的伪造转写仍部分改变工程")
	}
	if _, err := service.acceptSpeechResult(context.Background(), "checked-source", before.ID, asset.ID, asset.Fingerprint, asset.DurationUs, result, AcceptTranscriptOptions{}); err == nil {
		t.Fatal("受控接纳也应拒绝实际源已改变的结果")
	}
}

type waitingVoiceCredentialStore struct {
	base    *appSecretStore
	started chan struct{}
	once    sync.Once
}

func (store *waitingVoiceCredentialStore) Set(ctx context.Context, id string, value []byte) error {
	return store.base.Set(ctx, id, value)
}
func (store *waitingVoiceCredentialStore) Delete(ctx context.Context, id string) error {
	return store.base.Delete(ctx, id)
}
func (store *waitingVoiceCredentialStore) Get(ctx context.Context, _ string) ([]byte, error) {
	store.once.Do(func() { close(store.started) })
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestCloseCancelsVoiceCredentialWaitBeforeProviderGET(t *testing.T) {
	service, _, _ := sourceTranscriptFixture(t)
	store := &waitingVoiceCredentialStore{base: &appSecretStore{values: map[string][]byte{}}, started: make(chan struct{})}
	control := &applicationSpeechControl{}
	if err := service.ConfigureGeneration(speechApplicationOptions(t, t.TempDir(), store, control)); err != nil {
		t.Fatal(err)
	}
	profile := saveApplicationSpeechConnection(t, service, speech.MultilingualV2)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	returned := make(chan error, 1)
	go func() { _, err := service.ListVoices(ctx, profile.ID, speech.VoiceListRequest{}); returned <- err }()
	select {
	case <-store.started:
	case <-time.After(time.Second):
		t.Fatal("没有开始凭据读取")
	}
	closed := make(chan struct{})
	go func() { service.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("关闭应主动取消凭据Get的context，不能被共享锁阻塞")
	}
	select {
	case err := <-returned:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("关闭未取消凭据等待：%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("关闭后凭据等待未退出")
	}
	if control.lists.Load() != 0 {
		t.Fatal("凭据尚未取得就进入Provider音色GET")
	}
}

func TestRepeatedSpeechImportKeepsRemainingSplitClipWithoutAddingFullTake(t *testing.T) {
	service, _, _ := sourceTranscriptFixture(t)
	control := &applicationSpeechControl{}
	if err := service.ConfigureGeneration(speechApplicationOptions(t, t.TempDir(), &appSecretStore{values: map[string][]byte{}}, control)); err != nil {
		t.Fatal(err)
	}
	profile := saveApplicationSpeechConnection(t, service, speech.MultilingualV2)
	input := speechjobs.Input{ID: "split-speech-reimport", ConnectionID: profile.ID, Mode: speechjobs.Synthesize, Consent: true, Synthesize: &speech.SynthesizeRequest{VoiceID: "fixture-voice", Text: "仅本地fixture配音"}}
	if _, err := service.StartSpeechJob(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	job := awaitSpeechApplication(t, service, input.ID, speechjobs.Succeeded)
	options := ImportSpeechAudioOptions{StartFrame: 30, AddToTimeline: true}
	first, err := service.ImportSpeechAudio(context.Background(), job.ID, options)
	if err != nil {
		t.Fatal(err)
	}
	canonicalID := job.ID + "-audio-clip"
	cut, err := service.ApplyEdit(editor.EditRequest{BaseRevision: first.Project.Revision, IdempotencyKey: "split-imported-speech", Commands: []editor.Command{{Type: "split_clip", ClipID: canonicalID, AtFrame: 32}}})
	if err != nil {
		t.Fatal(err)
	}
	remaining, err := service.ApplyEdit(editor.EditRequest{BaseRevision: cut.Project.Revision, IdempotencyKey: "remove-left-speech", Commands: []editor.Command{{Type: "delete_clip", ClipID: canonicalID}}})
	if err != nil {
		t.Fatal(err)
	}
	rows, err := service.ListSpeechJobs()
	if err != nil || !rows[0].AudioOnTimeline {
		t.Fatal("右半配音仍应具有任务来源", err)
	}
	again, err := service.ImportSpeechAudio(context.Background(), job.ID, options)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, clip := range again.Project.Clips {
		if clip.Kind == "audio" && clip.AssetID == job.Audio.ID {
			count++
		}
	}
	if count != 1 || again.Project.Revision != remaining.Project.Revision || control.syntheses.Load() != 1 {
		t.Fatal("重复应用配音不能在已有右段之外添加完整新片段、消耗撤销或重新生成")
	}
	undone, err := service.Undo()
	if err != nil {
		t.Fatal(err)
	}
	restoredLeft := false
	for _, clip := range undone.Project.Clips {
		if clip.ID == canonicalID {
			restoredLeft = true
		}
	}
	if !restoredLeft {
		t.Fatal("无操作重复导入不应抢占前一次手动删除的撤销")
	}
}

func TestExplicitSpeechCaptionTrackChangeMovesExistingBindingAndKeepsOverrides(t *testing.T) {
	service, asset, result := sourceTranscriptFixture(t)
	original := service.GetBootstrap().Project
	first, err := service.acceptSpeechResult(context.Background(), "retarget-speech", original.ID, asset.ID, asset.Fingerprint, asset.DurationUs, result, AcceptTranscriptOptions{GenerateCaptions: true, TrackID: "track-caption-1"})
	if err != nil {
		t.Fatal(err)
	}
	var caption editor.Clip
	for _, clip := range first.Project.Clips {
		if clip.CaptionSource != nil {
			caption = clip
			break
		}
	}
	caption.Text, caption.TextStyle.FontSize, caption.Transform.Y = "人工覆盖保留", 72, -100
	track := editor.Track{ID: "speech-caption-target", Name: "新字幕轨", Kind: "caption"}
	if _, err := service.ApplyEdit(editor.EditRequest{BaseRevision: first.Project.Revision, IdempotencyKey: "prepare-speech-retarget", Commands: []editor.Command{{Type: "update_clip", Clip: &caption}, {Type: "add_track", Track: &track}}}); err != nil {
		t.Fatal(err)
	}
	moved, err := service.acceptSpeechResult(context.Background(), "retarget-speech", original.ID, asset.ID, asset.Fingerprint, asset.DurationUs, result, AcceptTranscriptOptions{GenerateCaptions: true, TrackID: track.ID})
	if err != nil {
		t.Fatal(err)
	}
	if moved.Project.Transcripts[0].CaptionTrackID != track.ID {
		t.Fatal("转写目标轨没有更新")
	}
	found := false
	for _, clip := range moved.Project.Clips {
		if clip.ID == caption.ID {
			found = true
			if clip.TrackID != track.ID || clip.Text != caption.Text || clip.TextStyle != caption.TextStyle || clip.Transform != caption.Transform || clip.CaptionSource == nil {
				t.Fatal("显式改轨没有移动既有绑定或丢失人工文本/样式")
			}
		}
	}
	if !found {
		t.Fatal("换轨更换或丢失了字幕稳定ID")
	}
	undone, err := service.Undo()
	if err != nil {
		t.Fatal(err)
	}
	if undone.Project.Transcripts[0].CaptionTrackID != "track-caption-1" {
		t.Fatal("换轨不能整组撤销")
	}
	for _, clip := range undone.Project.Clips {
		if clip.ID == caption.ID && clip.TrackID != "track-caption-1" {
			t.Fatal("换轨撤销未恢复旧展示轨")
		}
	}
}
