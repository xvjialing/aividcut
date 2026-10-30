package application

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"aividcut/internal/connections"
	"aividcut/internal/editor"
	"aividcut/internal/generation"
	"aividcut/internal/media"
	"aividcut/internal/speech"
	"aividcut/internal/speechjobs"
)

type applicationSpeechControl struct {
	transcribes, syntheses, clones, lists atomic.Int32
	audio                                 []byte
	list                                  func(context.Context) (speech.VoicePage, error)
}

func TestRealMediaSpeechApplicationFlow(t *testing.T) {
	tools := media.Discover()
	if tools.FFmpegPath == "" || tools.FFprobePath == "" {
		t.Skip("未显式配置媒体工具，真实音轨/时长应用验证未执行")
	}
	directory := t.TempDir()
	sourcePath := filepath.Join(directory, "own-source.wav")
	if err := writeApplicationWAV(sourcePath, 3_000_000); err != nil {
		t.Fatal(err)
	}
	s, err := New(tools, filepath.Join(directory, "previews"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	control := &applicationSpeechControl{}
	options := speechApplicationOptions(t, filepath.Join(directory, "state"), &appSecretStore{values: map[string][]byte{}}, control)
	options.SpeechPrepare, options.SpeechProbe = nil, nil
	if err := s.ConfigureGeneration(options); err != nil {
		t.Fatal(err)
	}
	bootstrap, err := s.ImportPaths(context.Background(), []string{sourcePath})
	if err != nil {
		t.Fatal(err)
	}
	asset := bootstrap.Project.Assets[0]
	clip := editor.DefaultClip("real-audio-source", "track-audio-1", "audio")
	clip.AssetID = asset.ID
	clip.DurationFrames = 90
	if _, err := s.ApplyEdit(editor.EditRequest{BaseRevision: bootstrap.Project.Revision, IdempotencyKey: "real-source-on-timeline", Commands: []editor.Command{{Type: "add_clip", Clip: &clip}}}); err != nil {
		t.Fatal(err)
	}
	asr := saveApplicationSpeechConnection(t, s, speech.ScribeV2)
	if _, err := s.StartSpeechJob(context.Background(), speechjobs.Input{ID: "real-media-asr", ConnectionID: asr.ID, Mode: speechjobs.Transcribe, Consent: true, Transcribe: &speech.TranscribeRequest{AssetID: asset.ID, Granularity: "word"}}); err != nil {
		t.Fatal(err)
	}
	awaitSpeechApplication(t, s, "real-media-asr", speechjobs.Succeeded)
	if _, err := s.AcceptSpeechTranscript(context.Background(), "real-media-asr", AcceptTranscriptOptions{GenerateCaptions: true, TrackID: "track-caption-1"}); err != nil {
		t.Fatal(err)
	}
	tts := saveApplicationSpeechConnection(t, s, speech.MultilingualV2)
	if _, err := s.StartSpeechJob(context.Background(), speechjobs.Input{ID: "real-media-tts", ConnectionID: tts.ID, Mode: speechjobs.Synthesize, Consent: true, Synthesize: &speech.SynthesizeRequest{VoiceID: "fixture-voice", Text: "只调用本地假提供商，媒体链路使用真实工具"}}); err != nil {
		t.Fatal(err)
	}
	job := awaitSpeechApplication(t, s, "real-media-tts", speechjobs.Succeeded)
	if job.Audio == nil || job.Audio.DurationUs <= 0 || job.Audio.URL == "" {
		t.Fatal("真实ffprobe时长或本地URL未生成")
	}
	response, err := http.Get(job.Audio.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatal("试听素材不能读取", response.StatusCode)
	}
	if _, err := s.ImportSpeechAudio(context.Background(), job.ID, ImportSpeechAudioOptions{AddToTimeline: true, StartFrame: 90}); err != nil {
		t.Fatal(err)
	}
	if control.transcribes.Load() != 1 || control.syntheses.Load() != 1 {
		t.Fatal("媒体接纳/导入重新调用提供商")
	}
}

type applicationSpeechProvider struct {
	control *applicationSpeechControl
	files   speech.MediaSource
	secrets generation.SecretSource
}

func (p *applicationSpeechProvider) Capabilities() speech.Capabilities {
	provider, _ := speech.NewElevenLabs(speech.Config{})
	return provider.Capabilities()
}
func (p *applicationSpeechProvider) Transcribe(ctx context.Context, request speech.TranscribeRequest) (speech.Transcription, error) {
	p.control.transcribes.Add(1)
	file, err := p.files.Open(ctx, request.AssetID)
	if err != nil {
		return speech.Transcription{}, err
	}
	defer file.Reader.Close()
	if _, err := io.Copy(io.Discard, file.Reader); err != nil {
		return speech.Transcription{}, err
	}
	start, end := int64(100_000), int64(500_000)
	return speech.Transcription{Operation: speech.Operation{ID: request.ID, ProviderID: speech.ElevenLabsID, ModelID: speech.ScribeV2, Status: speech.Succeeded}, AssetID: request.AssetID, SourceSHA256: file.SHA256, SourceDurationUs: file.DurationUs, Text: "真实任务链路测试", Language: "zh", Granularity: "word", TimingComplete: true, Words: []speech.Word{{TimedText: speech.TimedText{Text: "真实任务链路测试", StartUs: &start, EndUs: &end}, Type: "word", TimingComplete: true, SpeakerID: "speaker-1"}}}, nil
}
func (p *applicationSpeechProvider) Synthesize(_ context.Context, request speech.SynthesizeRequest) (speech.AudioResult, error) {
	p.control.syntheses.Add(1)
	return speech.AudioResult{Operation: speech.Operation{ID: request.ID, ProviderID: speech.ElevenLabsID, ModelID: speech.MultilingualV2, Status: speech.Succeeded}, VoiceID: request.VoiceID, OutputFormat: request.OutputFormat, MIME: "audio/mpeg", Data: append([]byte(nil), p.control.audio...)}, nil
}
func (p *applicationSpeechProvider) CloneVoice(_ context.Context, request speech.CloneRequest) (speech.CloneResult, error) {
	p.control.clones.Add(1)
	return speech.CloneResult{Operation: speech.Operation{ID: request.ID, ProviderID: speech.ElevenLabsID, Status: speech.Succeeded}, Voice: speech.Voice{ID: "fixture-custom", Name: request.Name, VerificationState: "unverified"}}, nil
}
func (p *applicationSpeechProvider) ListVoices(ctx context.Context, _ speech.VoiceListRequest) (speech.VoicePage, error) {
	p.control.lists.Add(1)
	if p.control.list != nil {
		return p.control.list(ctx)
	}
	if _, err := p.secrets.Secret(ctx, speech.ElevenLabsID); err != nil {
		return speech.VoicePage{}, err
	}
	return speech.VoicePage{Voices: []speech.Voice{{ID: "fixture-voice", Name: "测试音色", VerificationState: "unverified"}}, HasMore: true, NextPageToken: "next-fixture"}, nil
}

func writeApplicationWAV(path string, durationUs int64) error {
	samples := durationUs * 16000 / 1_000_000
	data := make([]byte, 44+samples*2)
	copy(data, "RIFF")
	binary.LittleEndian.PutUint32(data[4:], uint32(len(data)-8))
	copy(data[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(data[16:], 16)
	binary.LittleEndian.PutUint16(data[20:], 1)
	binary.LittleEndian.PutUint16(data[22:], 1)
	binary.LittleEndian.PutUint32(data[24:], 16000)
	binary.LittleEndian.PutUint32(data[28:], 32000)
	binary.LittleEndian.PutUint16(data[32:], 2)
	binary.LittleEndian.PutUint16(data[34:], 16)
	copy(data[36:], "data")
	binary.LittleEndian.PutUint32(data[40:], uint32(samples*2))
	return os.WriteFile(path, data, 0600)
}
func fileDigest(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}
func speechApplicationOptions(t *testing.T, directory string, store connections.SecretStore, control *applicationSpeechControl) GenerationOptions {
	t.Helper()
	if control.audio == nil {
		data, err := os.ReadFile(filepath.Join("..", "speech", "testdata", "silence.mp3"))
		if err != nil {
			t.Fatal(err)
		}
		control.audio = data
	}
	return GenerationOptions{Directory: directory, SecretStore: store, StorageName: "fixture-memory", SpeechFactory: func(secrets generation.SecretSource, files speech.MediaSource) (speech.Provider, error) {
		return &applicationSpeechProvider{control: control, files: files, secrets: secrets}, nil
	}, SpeechPrepare: func(ctx context.Context, asset editor.Asset, dir string) (speechjobs.Preparation, error) {
		if err := verifySourceFingerprint(ctx, asset.Path, asset.Fingerprint); err != nil {
			return speechjobs.Preparation{}, err
		}
		if err := os.Mkdir(dir, 0700); err != nil {
			return speechjobs.Preparation{}, err
		}
		path := filepath.Join(dir, "part.wav")
		if err := writeApplicationWAV(path, asset.DurationUs); err != nil {
			return speechjobs.Preparation{}, err
		}
		return speechjobs.Preparation{SourceAssetID: asset.ID, SourceFingerprint: asset.Fingerprint, SourceDurationUs: asset.DurationUs, SampleRate: 16000, Channels: 1, AudioOffsetUs: 0, CoverageEndUs: asset.DurationUs, Parts: []speechjobs.Part{{ID: "prepared-source", Path: path, Fingerprint: fileDigest(t, path), OffsetUs: 0, DurationUs: asset.DurationUs, AudioDurationUs: asset.DurationUs, SampleCount: asset.DurationUs * 16000 / 1_000_000}}}, nil
	}, SpeechProbe: func(ctx context.Context, path string) (editor.Asset, error) {
		if err := ctx.Err(); err != nil {
			return editor.Asset{}, err
		}
		return editor.Asset{ID: "probe-result", Name: "试听配音.mp3", Kind: "audio", Path: path, DurationUs: 156735, HasAudio: true, Fingerprint: fileDigest(t, path)}, nil
	}}
}
func saveApplicationSpeechConnection(t *testing.T, s *Service, model string) ConnectionProfile {
	t.Helper()
	profile, err := s.SaveConnection(context.Background(), connections.SaveInput{ProviderID: speech.ElevenLabsID, Name: "测试语音连接", ModelID: model, APIKey: "fixture-speech-key"})
	if err != nil {
		t.Fatal(err)
	}
	return profile
}
func awaitSpeechApplication(t *testing.T, s *Service, id, status string) SpeechSummary {
	t.Helper()
	until := time.Now().Add(4 * time.Second)
	for time.Now().Before(until) {
		rows, err := s.ListSpeechJobs()
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range rows {
			if row.ID == id && row.Status == status {
				return row
			}
		}
		time.Sleep(time.Millisecond)
	}
	job, _ := s.GetSpeechJob(id)
	t.Fatalf("语音任务未到达 %s: %+v", status, job.SpeechSummary)
	return SpeechSummary{}
}

func TestSpeechApplicationASRAcceptCorrectAndSavedProject(t *testing.T) {
	s, asset, _ := sourceTranscriptFixture(t)
	store := &appSecretStore{values: map[string][]byte{}}
	control := &applicationSpeechControl{}
	directory := t.TempDir()
	options := speechApplicationOptions(t, directory, store, control)
	if err := s.ConfigureGeneration(options); err != nil {
		t.Fatal(err)
	}
	profile := saveApplicationSpeechConnection(t, s, speech.ScribeV2)
	input := speechjobs.Input{ID: "application-asr", ConnectionID: profile.ID, Mode: speechjobs.Transcribe, Consent: true, Transcribe: &speech.TranscribeRequest{AssetID: asset.ID, ModelID: speech.ScribeV2, Granularity: "word"}}
	if _, err := s.StartSpeechJob(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	summary := awaitSpeechApplication(t, s, input.ID, speechjobs.Succeeded)
	if summary.AcceptedTranscriptID != "" {
		t.Fatal("转写自动改了工程")
	}
	detail, err := s.GetSpeechJob(input.ID)
	if err != nil || detail.Transcription == nil || len(detail.Transcription.Words) != 1 {
		t.Fatal("显式详情没有原始词", err)
	}
	accepted, err := s.AcceptSpeechTranscript(context.Background(), input.ID, AcceptTranscriptOptions{GenerateCaptions: true, TrackID: "track-caption-1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(accepted.Project.Transcripts) != 1 {
		t.Fatal("结果未进入统一源转写核心")
	}
	rows, err := s.ListSpeechJobs()
	if err != nil || rows[0].AcceptedTranscriptID == "" {
		t.Fatal("未校准接纳状态", err)
	}
	if _, err := s.Undo(); err != nil {
		t.Fatal(err)
	}
	rows, _ = s.ListSpeechJobs()
	if rows[0].AcceptedTranscriptID != "" {
		t.Fatal("撤销后仍显示已接纳")
	}
	if _, err := s.AcceptSpeechTranscript(context.Background(), input.ID, AcceptTranscriptOptions{}); err != nil {
		t.Fatal(err)
	}
	projectPath := filepath.Join(t.TempDir(), "speech-project.aividcut")
	if _, err := s.SaveProjectTo(projectPath); err != nil {
		t.Fatal(err)
	}
	s.Close()
	restored, _, _ := sourceTranscriptFixture(t)
	if err := restored.ConfigureGeneration(options); err != nil {
		t.Fatal(err)
	}
	if _, err := restored.OpenProjectPath(projectPath); err != nil {
		t.Fatal(err)
	}
	if _, err := restored.StartSpeechJob(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	if control.transcribes.Load() != 1 {
		t.Fatal("重开后重新转写扣费")
	}
	rows, _ = restored.ListSpeechJobs()
	if rows[0].AcceptedTranscriptID == "" {
		t.Fatal("重开丢失接纳状态")
	}
}

func TestSpeechApplicationAudioPreviewImportUndoAndOriginalProject(t *testing.T) {
	s, _, _ := sourceTranscriptFixture(t)
	control := &applicationSpeechControl{}
	if err := s.ConfigureGeneration(speechApplicationOptions(t, t.TempDir(), &appSecretStore{values: map[string][]byte{}}, control)); err != nil {
		t.Fatal(err)
	}
	profile := saveApplicationSpeechConnection(t, s, speech.MultilingualV2)
	input := speechjobs.Input{ID: "application-tts", ConnectionID: profile.ID, Mode: speechjobs.Synthesize, Consent: true, Synthesize: &speech.SynthesizeRequest{VoiceID: "fixture-voice", Text: "一段明确配音", ModelID: speech.MultilingualV2, OutputFormat: "mp3_44100_128"}}
	if _, err := s.StartSpeechJob(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	job := awaitSpeechApplication(t, s, input.ID, speechjobs.Succeeded)
	if job.Audio == nil || job.Audio.DurationUs != 156735 || job.Audio.URL == "" || job.AudioImported {
		t.Fatal("没有真实元数据或本地试听URL")
	}
	options := ImportSpeechAudioOptions{TrackID: "track-audio-1", StartFrame: 30, AddToTimeline: true}
	imported, err := s.ImportSpeechAudio(context.Background(), job.ID, options)
	if err != nil {
		t.Fatal(err)
	}
	if len(imported.Project.Assets) != 2 {
		t.Fatal("未导入配音资产")
	}
	rows, _ := s.ListSpeechJobs()
	if !rows[0].AudioImported || !rows[0].AudioOnTimeline {
		t.Fatal("导入/时间线状态未校准")
	}
	again, err := s.ImportSpeechAudio(context.Background(), job.ID, options)
	if err != nil || again.Project.Revision != imported.Project.Revision {
		t.Fatal("重复导入产生双份片段", err)
	}
	if _, err := s.Undo(); err != nil {
		t.Fatal(err)
	}
	rows, _ = s.ListSpeechJobs()
	if rows[0].AudioImported || rows[0].AudioOnTimeline {
		t.Fatal("撤销后配音导入状态错误")
	}
	if _, err := s.NewProject("另一工程"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ImportSpeechAudio(context.Background(), job.ID, options); err == nil {
		t.Fatal("配音被导入其它工程")
	}
	if control.syntheses.Load() != 1 {
		t.Fatal("导入重新生成语音")
	}
}

func TestVoiceQueryAndCloneKeepExplicitConsentAndUnverifiedState(t *testing.T) {
	s, asset, _ := sourceTranscriptFixture(t)
	control := &applicationSpeechControl{}
	// 自定义音色样本为用户显式登记的音频，测试只走注入提供商。
	s.mu.Lock()
	p := s.engine.Snapshot()
	p.Assets[0].Kind = "audio"
	p.Clips = nil
	engine, err := editor.NewEngine(p)
	if err != nil {
		t.Fatal(err)
	}
	s.engine = engine
	s.mu.Unlock()
	if err := s.ConfigureGeneration(speechApplicationOptions(t, t.TempDir(), &appSecretStore{values: map[string][]byte{}}, control)); err != nil {
		t.Fatal(err)
	}
	profile := saveApplicationSpeechConnection(t, s, speech.MultilingualV2)
	page, err := s.ListVoices(context.Background(), profile.ID, speech.VoiceListRequest{PageSize: 10})
	if err != nil || !page.HasMore || page.NextPageToken == "" || page.Voices[0].VerificationState != "unverified" {
		t.Fatal("音色分页/状态错误", err)
	}
	input := speechjobs.Input{ID: "application-clone", ConnectionID: profile.ID, Mode: speechjobs.Clone, Consent: true, Clone: &speech.CloneRequest{Name: "本人音色", AssetIDs: []string{asset.ID}, Consent: speech.CloneConsent{Subject: "本人", Authority: "self", Confirmed: false}}}
	if _, err := s.StartSpeechJob(context.Background(), input); err == nil {
		t.Fatal("未确认来源仍创建音色")
	}
	input.Clone.Consent.Confirmed = true
	if _, err := s.StartSpeechJob(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	job := awaitSpeechApplication(t, s, input.ID, speechjobs.Succeeded)
	if job.Voice == nil || job.Voice.VerificationState != "unverified" || control.clones.Load() != 1 {
		t.Fatal("将创建音色伪装成验证成功")
	}
}

func TestVoiceQueryCancellationAndWrongConnectionDoNotEscapeState(t *testing.T) {
	s, _, _ := sourceTranscriptFixture(t)
	control := &applicationSpeechControl{}
	if err := s.ConfigureGeneration(speechApplicationOptions(t, t.TempDir(), &appSecretStore{values: map[string][]byte{}}, control)); err != nil {
		t.Fatal(err)
	}
	profile := saveApplicationSpeechConnection(t, s, speech.MultilingualV2)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.ListVoices(ctx, profile.ID, speech.VoiceListRequest{}); !errors.Is(err, context.Canceled) {
		t.Fatal("取消未传播", err)
	}
	if control.lists.Load() != 0 {
		t.Fatal("已取消仍查询账户")
	}
	if _, err := s.ListVoices(context.Background(), "wrong-connection", speech.VoiceListRequest{}); err == nil {
		t.Fatal("未知连接仍查询")
	}
	started, returned := make(chan struct{}), make(chan error, 1)
	control.list = func(ctx context.Context) (speech.VoicePage, error) {
		close(started)
		<-ctx.Done()
		return speech.VoicePage{}, ctx.Err()
	}
	go func() {
		_, err := s.ListVoices(context.Background(), profile.ID, speech.VoiceListRequest{})
		returned <- err
	}()
	<-started
	s.Close()
	select {
	case err := <-returned:
		if !errors.Is(err, context.Canceled) {
			t.Fatal("关闭未取消查询", err)
		}
	case <-time.After(time.Second):
		t.Fatal("关闭后仍查询账户")
	}
}
