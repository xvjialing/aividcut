package speechjobs

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"aividcut/internal/editor"
	"aividcut/internal/generation"
	"aividcut/internal/speech"
)

// 所有语音调用均为假 Provider；样本为自建静音 WAV/MP3，不读取系统凭据或真实账户。
type fixtureControl struct {
	asr, tts, clones, resolves, factories, releases, probes atomic.Int32
	transcribe                                              func(context.Context, speech.TranscribeRequest, *speech.InputFile) (speech.Transcription, error)
	synthesize                                              func(context.Context, speech.SynthesizeRequest) (speech.AudioResult, error)
	clone                                                   func(context.Context, speech.CloneRequest) (speech.CloneResult, error)
	prepare                                                 func(context.Context, editor.Asset, string) (Preparation, error)
	probeFails                                              atomic.Bool
	mp3                                                     []byte
}
type fixtureProvider struct {
	control *fixtureControl
	secrets generation.SecretSource
	files   speech.MediaSource
}

func (p *fixtureProvider) Capabilities() speech.Capabilities {
	return speech.Capabilities{ProviderID: speech.ElevenLabsID, ASRModels: []string{speech.ScribeV2}, TTSModels: []string{speech.MultilingualV2}, TimestampGranularities: []string{"word", "character"}, OutputFormats: []string{"mp3_44100_128"}, Transcription: speech.Feature{Supported: true}, SpeechSynthesis: speech.Feature{Supported: true}, InstantCloning: speech.Feature{Supported: true}}
}
func (p *fixtureProvider) checkSecret(ctx context.Context) error {
	_, err := p.secrets.Secret(ctx, speech.ElevenLabsID)
	return err
}
func (p *fixtureProvider) Transcribe(ctx context.Context, r speech.TranscribeRequest) (speech.Transcription, error) {
	p.control.asr.Add(1)
	if err := p.checkSecret(ctx); err != nil {
		return speech.Transcription{}, err
	}
	file, err := p.files.Open(ctx, r.AssetID)
	if err != nil {
		return speech.Transcription{}, err
	}
	defer file.Reader.Close()
	if _, err := io.Copy(io.Discard, file.Reader); err != nil {
		return speech.Transcription{}, err
	}
	if p.control.transcribe != nil {
		return p.control.transcribe(ctx, r, file)
	}
	start, end := int64(100_000), int64(900_000)
	if file.DurationUs < end {
		start, end = 0, file.DurationUs
	}
	return speech.Transcription{Operation: operation(r.ID, r.ModelID), AssetID: r.AssetID, SourceSHA256: file.SHA256, SourceDurationUs: file.DurationUs, Language: "zh", Text: "原始识别词", Granularity: r.Granularity, TimingComplete: true, Words: []speech.Word{{TimedText: speech.TimedText{Text: "原始识别词", StartUs: &start, EndUs: &end}, TimingComplete: true, Type: "word", SpeakerID: "speaker_0"}}, Warnings: []string{}}, nil
}
func (p *fixtureProvider) Synthesize(ctx context.Context, r speech.SynthesizeRequest) (speech.AudioResult, error) {
	p.control.tts.Add(1)
	if err := p.checkSecret(ctx); err != nil {
		return speech.AudioResult{}, err
	}
	if p.control.synthesize != nil {
		return p.control.synthesize(ctx, r)
	}
	return speech.AudioResult{Operation: operation(r.ID, r.ModelID), VoiceID: r.VoiceID, MIME: "audio/mpeg", OutputFormat: r.OutputFormat, Data: bytes.Clone(p.control.mp3)}, nil
}
func (p *fixtureProvider) CloneVoice(ctx context.Context, r speech.CloneRequest) (speech.CloneResult, error) {
	p.control.clones.Add(1)
	if err := p.checkSecret(ctx); err != nil {
		return speech.CloneResult{}, err
	}
	for _, id := range r.AssetIDs {
		file, err := p.files.Open(ctx, id)
		if err != nil {
			return speech.CloneResult{}, err
		}
		_, err = io.Copy(io.Discard, file.Reader)
		file.Reader.Close()
		if err != nil {
			return speech.CloneResult{}, err
		}
	}
	if p.control.clone != nil {
		return p.control.clone(ctx, r)
	}
	verified, required := true, false
	return speech.CloneResult{Operation: operation(r.ID, ""), Voice: speech.Voice{ID: "voice-" + r.ID, Name: r.Name, VerificationState: "verified", ProviderReportedVerified: &verified, RequiresVerification: &required, PreviewURL: "https://remote.invalid/private-preview?signature=hidden"}}, nil
}
func (p *fixtureProvider) ListVoices(context.Context, speech.VoiceListRequest) (speech.VoicePage, error) {
	return speech.VoicePage{}, errors.New("任务协调不应擅自查询音色列表")
}
func operation(id, model string) speech.Operation {
	return speech.Operation{ID: id, ProviderID: speech.ElevenLabsID, ModelID: model, RemoteID: "remote-" + id, Status: speech.Succeeded}
}

func wav(samples int64) []byte {
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
	binary.LittleEndian.PutUint32(data[40:], uint32(len(data)-44))
	return data
}
func fingerprint(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func prepareCoverage(ctx context.Context, source editor.Asset, dir string, offset, end int64) (Preparation, error) {
	if err := ctx.Err(); err != nil {
		return Preparation{}, err
	}
	if err := os.Mkdir(dir, 0700); err != nil {
		return Preparation{}, err
	}
	result := Preparation{SourceAssetID: source.ID, SourceFingerprint: source.Fingerprint, SourceDurationUs: source.DurationUs, AudioOffsetUs: offset, CoverageEndUs: end, SampleRate: 16000, Channels: 1, Parts: []Part{}}
	if offset != 0 || end != source.DurationUs {
		result.Warnings = []string{"实际音轨仅覆盖源素材的一部分，未填充静音"}
	}
	for position := offset; position < end; {
		duration := min(int64(1_000_000), end-position)
		samples := (duration*16000 + 999999) / 1_000_000
		physical := (samples*1_000_000 + 15999) / 16000
		data := wav(samples)
		id := fmt.Sprintf("part-%03d", len(result.Parts)+1)
		path := filepath.Join(dir, id+".wav")
		if err := os.WriteFile(path, data, 0600); err != nil {
			return Preparation{}, err
		}
		result.Parts = append(result.Parts, Part{ID: id, Path: path, Fingerprint: fingerprint(data), OffsetUs: position, DurationUs: duration, AudioDurationUs: physical, SampleCount: samples})
		position += duration
	}
	return result, nil
}

func fixture(t *testing.T, duration int64) (*Manager, Options, *fixtureControl, editor.Project) {
	t.Helper()
	control := &fixtureControl{}
	var err error
	control.mp3, err = os.ReadFile(filepath.Join("..", "speech", "testdata", "silence.mp3"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "source.wav")
	data := wav((duration*16000 + 999999) / 1_000_000)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	project := editor.NewProject("语音任务fixture")
	project.Assets = []editor.Asset{{ID: "source", Name: "source.wav", Path: path, Kind: "audio", DurationUs: duration, HasAudio: true, Fingerprint: fingerprint(data)}}
	options := Options{Directory: t.TempDir(), Resolve: func(ctx context.Context, id string) (Binding, error) {
		control.resolves.Add(1)
		model := speech.MultilingualV2
		if id == "asr-connection" {
			model = speech.ScribeV2
		}
		return Binding{ProviderID: speech.ElevenLabsID, ModelID: model, Secrets: generation.SecretFunc(func(ctx context.Context, provider string) (string, error) {
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			if provider != speech.ElevenLabsID {
				return "", errors.New("服务商不匹配")
			}
			return "fixture-only-private-key", nil
		}), Release: func() { control.releases.Add(1) }}, nil
	}, Factory: func(secrets generation.SecretSource, files speech.MediaSource) (speech.Provider, error) {
		control.factories.Add(1)
		return &fixtureProvider{control: control, secrets: secrets, files: files}, nil
	}, Prepare: func(ctx context.Context, asset editor.Asset, dir string) (Preparation, error) {
		if control.prepare != nil {
			return control.prepare(ctx, asset, dir)
		}
		return prepareCoverage(ctx, asset, dir, 0, asset.DurationUs)
	}, Probe: func(ctx context.Context, path string) (editor.Asset, error) {
		control.probes.Add(1)
		if control.probeFails.Load() {
			return editor.Asset{}, errors.New("fixture probe unavailable")
		}
		hash, _, err := fileHash(ctx, path, speech.MaxResponseBytes)
		if err != nil {
			return editor.Asset{}, err
		}
		return editor.Asset{ID: "probe", Name: "probe.mp3", Path: path, Kind: "audio", DurationUs: 156735, HasAudio: true, Fingerprint: "sha256:" + hash}, nil
	}}
	manager, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(manager.Close)
	return manager, options, control, project
}
func transcribeInput(id string) Input {
	return Input{ID: id, ConnectionID: "asr-connection", Mode: Transcribe, Consent: true, Transcribe: &speech.TranscribeRequest{AssetID: "source"}}
}
func synthesizeInput(id string) Input {
	return Input{ID: id, ConnectionID: "tts-connection", Mode: Synthesize, Consent: true, Synthesize: &speech.SynthesizeRequest{VoiceID: "fixture-voice", Text: "仅本地fixture配音"}}
}
func cloneInput(id string) Input {
	return Input{ID: id, ConnectionID: "tts-connection", Mode: Clone, Consent: true, Clone: &speech.CloneRequest{Name: "测试音色", AssetIDs: []string{"source"}, Consent: speech.CloneConsent{Confirmed: true, Subject: "fixture-user", Authority: "self"}}}
}
func await(t *testing.T, m *Manager, id, status string) Job {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		m.mu.Lock()
		e := m.entries[id]
		active := e != nil && e.active
		m.mu.Unlock()
		job, err := m.Get(id)
		if err == nil && job.Status == status && !active {
			return job
		}
		time.Sleep(time.Millisecond)
	}
	job, err := m.Get(id)
	t.Fatalf("未达到 %s，当前 %+v %v", status, job.Summary, err)
	return Job{}
}

func TestASRCoverageReceiptsOffsetMergeAndPersistentDedup(t *testing.T) {
	m, options, control, project := fixture(t, 4_000_000)
	control.prepare = func(ctx context.Context, asset editor.Asset, dir string) (Preparation, error) {
		return prepareCoverage(ctx, asset, dir, 500_000, 3_500_000)
	}
	input := transcribeInput("covered-source")
	if _, err := m.Submit(context.Background(), project, input); err != nil {
		t.Fatal(err)
	}
	job := await(t, m, input.ID, Succeeded)
	if control.asr.Load() != 3 || job.CompletedParts != 3 || job.TotalParts != 3 || len(job.PartResults) != 3 || job.Transcription == nil {
		t.Fatal("分块识别结果不完整")
	}
	result := job.Transcription
	if result.SourceDurationUs != 4_000_000 || result.SourceSHA256 != hashValue(project.Assets[0].Fingerprint) || result.AssetID != "source" || len(result.Words) != 3 {
		t.Fatal("合并结果失去原始源身份")
	}
	for i, word := range result.Words {
		if word.StartUs == nil || *word.StartUs != int64(600_000+i*1_000_000) || word.SpeakerID != fmt.Sprintf("part-%d:speaker_0", i+1) {
			t.Fatal("词时间或跨块说话人范围没有映射回原源")
		}
		if *job.PartResults[i].Words[0].StartUs != 100_000 {
			t.Fatal("原始part证据被合并覆盖")
		}
	}
	data, err := os.ReadFile(filepath.Join(options.Directory, "jobs", input.ID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte(`"words"`)) || bytes.Contains(data, []byte("fixture-only-private-key")) {
		t.Fatal("任务元数据包含巨量词或秘密")
	}
	list, _ := json.Marshal(m.List())
	if bytes.Contains(list, []byte(`"words"`)) || bytes.Contains(list, []byte("原始识别词")) {
		t.Fatal("列表摘要泄漏完整词数据")
	}
	input.Consent = false
	project.Revision++
	if _, err := m.Submit(context.Background(), project, input); err != nil || control.asr.Load() != 3 {
		t.Fatal("重复任务重新计费", err)
	}
	changed := editor.CloneProject(project)
	changed.Assets[0].Fingerprint = fingerprint([]byte("different content"))
	if _, err := m.Submit(context.Background(), changed, input); err == nil {
		t.Fatal("相同ID允许改用不同源指纹")
	}
	m.Close()
	<-m.Done()
	options.Resolve = func(context.Context, string) (Binding, error) {
		t.Error("重启或重复返回不应读取凭据")
		return Binding{}, errors.New("locked")
	}
	restored, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if _, err := restored.Submit(context.Background(), project, input); err != nil || control.asr.Load() != 3 {
		t.Fatal("重启后重新提交", err)
	}
}

func TestFirstASRJobCreatesParentButLeavesPrepareTargetAbsent(t *testing.T) {
	m, _, control, project := fixture(t, 1_000_000)
	var prepared atomic.Bool
	control.prepare = func(ctx context.Context, source editor.Asset, dir string) (Preparation, error) {
		info, err := os.Stat(filepath.Dir(dir))
		if err != nil || !info.IsDir() {
			return Preparation{}, errors.New("调用方未创建父目录")
		}
		if _, err := os.Lstat(dir); !errors.Is(err, os.ErrNotExist) {
			return Preparation{}, errors.New("调用方不应预创建准备目标")
		}
		prepared.Store(true)
		return prepareCoverage(ctx, source, dir, 0, source.DurationUs)
	}
	if _, err := m.Submit(context.Background(), project, transcribeInput("first-prepare-contract")); err != nil {
		t.Fatal(err)
	}
	await(t, m, "first-prepare-contract", Succeeded)
	if !prepared.Load() || control.asr.Load() != 1 {
		t.Fatal("首次任务没有遵守父目录存在、准备目标不存在的契约")
	}
}

func TestUnknownPartStopsLaterCallsAndKeepsCompletedEvidence(t *testing.T) {
	m, options, control, project := fixture(t, 3_000_000)
	control.transcribe = func(_ context.Context, r speech.TranscribeRequest, file *speech.InputFile) (speech.Transcription, error) {
		op := operation(r.ID, r.ModelID)
		if strings.HasSuffix(r.ID, "p0002") {
			op.Status, op.SubmissionUncertain = speech.Unknown, true
			return speech.Transcription{Operation: op}, &speech.Error{Code: "transport", Message: "fixture-only-private-key must not leak", SubmissionUncertain: true}
		}
		start, end := int64(0), int64(500_000)
		return speech.Transcription{Operation: op, AssetID: r.AssetID, SourceSHA256: file.SHA256, SourceDurationUs: file.DurationUs, Language: "zh", Granularity: r.Granularity, Text: "第一块证据", TimingComplete: true, Words: []speech.Word{{TimedText: speech.TimedText{Text: "第一块证据", StartUs: &start, EndUs: &end}, Type: "word", TimingComplete: true}}}, nil
	}
	input := transcribeInput("unknown-second-part")
	if _, err := m.Submit(context.Background(), project, input); err != nil {
		t.Fatal(err)
	}
	job := await(t, m, input.ID, Unknown)
	if control.asr.Load() != 2 || job.CompletedParts != 1 || len(job.PartResults) != 1 || job.Parts[2].Status != Queued || job.CanResumeLocal || strings.Contains(job.Message, "private-key") {
		t.Fatal("未知块没有停止后续或遗失证据/泄漏错误")
	}
	if _, err := m.ResumeLocal(context.Background(), input.ID); err == nil {
		t.Fatal("未知提交不应通过恢复再次POST")
	}
	if _, err := m.AcknowledgeUnknown(input.ID); err != nil {
		t.Fatal(err)
	}
	m.Close()
	<-m.Done()
	restored, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	job, err = restored.Get(input.ID)
	if err != nil || len(job.PartResults) != 1 || !job.Acknowledged {
		t.Fatal("未知块核对及已完成证据未跨重启保留", err)
	}
	if _, err := restored.Submit(context.Background(), project, input); err != nil || control.asr.Load() != 2 {
		t.Fatal("未知任务重启后重新生成", err)
	}
}

func TestASRQuantizationTailIsClippedWithoutChangingRawEvidence(t *testing.T) {
	m, _, control, project := fixture(t, 1_000_001)
	if _, err := m.Submit(context.Background(), project, transcribeInput("quantized-tail")); err != nil {
		t.Fatal(err)
	}
	job := await(t, m, "quantized-tail", Succeeded)
	if control.asr.Load() != 2 || job.Transcription == nil || len(job.Transcription.Words) != 2 {
		t.Fatal("量化样本未完成")
	}
	last := job.Transcription.Words[1]
	if *last.StartUs != 1_000_000 || *last.EndUs != 1_000_001 || *job.PartResults[1].Words[0].EndUs != 63 || len(job.Warnings) == 0 {
		t.Fatal("量化尾没有裁切到逻辑区间或覆盖原始证据")
	}
}

func TestTTSAtomicAudioProbeAndLocalResumeWithoutCredentials(t *testing.T) {
	m, options, control, project := fixture(t, 1_000_000)
	control.probeFails.Store(true)
	input := synthesizeInput("tts-local-resume")
	if _, err := m.Submit(context.Background(), project, input); err != nil {
		t.Fatal(err)
	}
	job := await(t, m, input.ID, Saving)
	if !job.CanResumeLocal || job.Audio != nil || control.tts.Load() != 1 {
		t.Fatal("未探测配音被伪报成功")
	}
	if _, err := os.Stat(m.audioPath(input.ID)); err != nil {
		t.Fatal("已付费响应字节未保存", err)
	}
	m.Close()
	<-m.Done()
	control.probeFails.Store(false)
	options.Resolve = func(context.Context, string) (Binding, error) {
		t.Error("本地恢复不能读取凭据")
		return Binding{}, errors.New("locked")
	}
	factories := control.factories.Load()
	restored, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if _, err := restored.ResumeLocal(context.Background(), input.ID); err != nil {
		t.Fatal(err)
	}
	job = await(t, restored, input.ID, Succeeded)
	if job.Audio == nil || job.Audio.Kind != "audio" || job.Audio.DurationUs != 156735 || job.Audio.ID != input.ID+"-audio" || job.Audio.URL != "" || job.Audio.Fingerprint != fingerprint(control.mp3) || control.tts.Load() != 1 || control.factories.Load() != factories {
		t.Fatal("本地配音恢复的真实媒体身份或无重发语义不正确")
	}
}

func TestCloneConsentAndUnverifiedVoiceArePreserved(t *testing.T) {
	m, options, control, project := fixture(t, 1_000_000)
	input := cloneInput("clone-unverified")
	missing := cloneJSON(input)
	missing.Clone.Consent.Confirmed = false
	if _, err := m.Submit(context.Background(), project, missing); err == nil || control.clones.Load() != 0 {
		t.Fatal("没有声明仍创建音色")
	}
	if _, err := m.Submit(context.Background(), project, input); err != nil {
		t.Fatal(err)
	}
	job := await(t, m, input.ID, Succeeded)
	if job.Voice == nil || job.Voice.VerificationState != "unverified" || job.Input.Clone.Consent.Subject != "fixture-user" || job.Input.Clone.Consent.Authority != "self" || job.Voice.ProviderReportedVerified == nil || !*job.Voice.ProviderReportedVerified {
		t.Fatal("创建回执与身份验证/用户声明被混淆")
	}
	if err := filepath.WalkDir(options.Directory, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		if filepath.Ext(path) != ".json" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err == nil && (bytes.Contains(data, []byte("fixture-only-private-key")) || bytes.Contains(data, []byte("remote.invalid"))) {
			t.Error("凭据或远端预览地址落盘")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func TestASRFinalSaveFailureRestartsLocallyWithoutAnotherPost(t *testing.T) {
	m, options, control, project := fixture(t, 2_000_000)
	writer := m.writeFile
	m.writeFile = func(path string, data []byte) error {
		if filepath.Base(path) == "transcription.json" {
			return errors.New("fixture full disk")
		}
		return writer(path, data)
	}
	input := transcribeInput("merge-save-failure")
	if _, err := m.Submit(context.Background(), project, input); err != nil {
		t.Fatal(err)
	}
	job := await(t, m, input.ID, Saving)
	if !job.CanResumeLocal || job.CompletedParts != 2 {
		t.Fatal("全部原始块未保留恢复能力")
	}
	m.Close()
	<-m.Done()
	options.Resolve = func(context.Context, string) (Binding, error) {
		t.Error("本地合并不可读凭据")
		return Binding{}, errors.New("locked")
	}
	factoryCalls := control.factories.Load()
	restored, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if _, err := restored.ResumeLocal(context.Background(), input.ID); err != nil {
		t.Fatal(err)
	}
	job = await(t, restored, input.ID, Succeeded)
	if job.Transcription == nil || len(job.Transcription.Words) != 2 || control.asr.Load() != 2 || control.factories.Load() != factoryCalls {
		t.Fatal("本地合并恢复重新POST或丢词")
	}
}

func TestUnpersistedSynchronousPartBecomesUnknownAfterRestart(t *testing.T) {
	m, options, control, project := fixture(t, 2_000_000)
	writer := m.writeFile
	m.writeFile = func(path string, data []byte) error {
		if filepath.Base(path) == "part-0001.json" {
			return errors.New("fixture cannot save response")
		}
		return writer(path, data)
	}
	input := transcribeInput("lost-inline-response")
	if _, err := m.Submit(context.Background(), project, input); err != nil {
		t.Fatal(err)
	}
	job := await(t, m, input.ID, Saving)
	if !job.CanResumeLocal || len(job.PartResults) != 1 || control.asr.Load() != 1 {
		t.Fatal("同进程中应保留已取得响应并停止后续提交")
	}
	m.Close()
	<-m.Done()
	restored, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	job, err = restored.Get(input.ID)
	if err != nil || job.Status != Unknown || job.CanResumeLocal || len(job.PartResults) != 0 {
		t.Fatal("未落盘同步内容重启后应直接未知，不能伪造恢复", err)
	}
	if _, err := restored.Submit(context.Background(), project, input); err != nil || control.asr.Load() != 1 {
		t.Fatal("丢失同步内容后自动重发", err)
	}
}

func TestLocalResumeOfPartialResponseSavesEvidenceWithoutContinuingCalls(t *testing.T) {
	m, _, control, project := fixture(t, 2_000_000)
	var fail atomic.Bool
	fail.Store(true)
	writer := m.writeFile
	m.writeFile = func(path string, data []byte) error {
		if fail.Load() && filepath.Base(path) == "part-0001.json" {
			return errors.New("fixture disk failed")
		}
		return writer(path, data)
	}
	input := transcribeInput("partial-local-save")
	if _, err := m.Submit(context.Background(), project, input); err != nil {
		t.Fatal(err)
	}
	await(t, m, input.ID, Saving)
	fail.Store(false)
	if _, err := m.ResumeLocal(context.Background(), input.ID); err != nil {
		t.Fatal(err)
	}
	job := await(t, m, input.ID, Failed)
	if len(job.PartResults) != 1 || job.Parts[0].Status != Succeeded || job.CompletedParts != 1 || control.asr.Load() != 1 || job.Transcription != nil {
		t.Fatal("本地保存恢复不应继续后续POST或冒充完整识别")
	}
}

func TestPaidIntentMustBeDurableAndCorruptIDStaysReserved(t *testing.T) {
	m, options, control, project := fixture(t, 2_000_000)
	writer := m.writeFile
	m.writeFile = func(path string, data []byte) error {
		if filepath.Dir(path) == m.journal && bytes.Contains(data, []byte(`"intent":true`)) {
			return errors.New("fixture intent write failure")
		}
		return writer(path, data)
	}
	input := transcribeInput("intent-not-durable")
	if _, err := m.Submit(context.Background(), project, input); err != nil {
		t.Fatal(err)
	}
	await(t, m, input.ID, Failed)
	if control.asr.Load() != 0 {
		t.Fatal("意图未落盘仍发起收费调用")
	}
	m.Close()
	<-m.Done()
	if err := os.WriteFile(filepath.Join(options.Directory, "jobs", "corrupt-id.json"), []byte(`{`), 0600); err != nil {
		t.Fatal(err)
	}
	restored, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if _, err := restored.Submit(context.Background(), project, transcribeInput("corrupt-id")); err == nil || control.asr.Load() != 0 {
		t.Fatal("损坏历史被当作全新请求提交")
	}
}

func TestQueuedAndInFlightCancelKeepCostStateDistinct(t *testing.T) {
	m, _, control, project := fixture(t, 2_000_000)
	started := make(chan struct{})
	control.transcribe = func(ctx context.Context, r speech.TranscribeRequest, _ *speech.InputFile) (speech.Transcription, error) {
		close(started)
		<-ctx.Done()
		op := operation(r.ID, r.ModelID)
		op.Status, op.SubmissionUncertain = speech.Unknown, true
		return speech.Transcription{Operation: op}, ctx.Err()
	}
	if _, err := m.Submit(context.Background(), project, transcribeInput("active-cancel")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("假识别未开始")
	}
	if _, err := m.Submit(context.Background(), project, transcribeInput("queued-cancel")); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Cancel("queued-cancel"); err != nil {
		t.Fatal(err)
	}
	await(t, m, "queued-cancel", Cancelled)
	if _, err := m.Cancel("active-cancel"); err != nil {
		t.Fatal(err)
	}
	await(t, m, "active-cancel", Unknown)
	if control.asr.Load() != 1 {
		t.Fatal("取消排队任务仍然提交")
	}
}

func TestConcurrentDuplicateOnlyCallsProviderOncePerPart(t *testing.T) {
	m, _, control, project := fixture(t, 2_000_000)
	var group sync.WaitGroup
	for range 12 {
		group.Add(1)
		go func() {
			defer group.Done()
			if _, err := m.Submit(context.Background(), project, transcribeInput("duplicate-concurrent")); err != nil {
				t.Error(err)
			}
		}()
	}
	group.Wait()
	await(t, m, "duplicate-concurrent", Succeeded)
	if control.asr.Load() != 2 {
		t.Fatal("并发相同请求重复收费")
	}
}

func TestModelSourceAndPreparationValidationBeforeCalls(t *testing.T) {
	for _, scenario := range []string{"model", "source", "fingerprint", "coverage"} {
		t.Run(scenario, func(t *testing.T) {
			m, _, control, project := fixture(t, 2_000_000)
			input := transcribeInput("invalid-" + scenario)
			switch scenario {
			case "model":
				input.ConnectionID = "tts-connection"
			case "source":
				input.Transcribe.AssetID = "outside"
			case "fingerprint":
				project.Assets[0].Fingerprint = ""
			case "coverage":
				control.prepare = func(ctx context.Context, source editor.Asset, dir string) (Preparation, error) {
					p, err := prepareCoverage(ctx, source, dir, 0, source.DurationUs)
					if err == nil {
						p.Parts[1].OffsetUs++
					}
					return p, err
				}
			}
			_, err := m.Submit(context.Background(), project, input)
			if scenario == "coverage" {
				if err != nil {
					t.Fatal(err)
				}
				await(t, m, input.ID, Failed)
			} else if err == nil {
				t.Fatal("无效请求被受理")
			}
			if control.asr.Load() != 0 {
				t.Fatal("身份或覆盖不正确仍提交")
			}
		})
	}
}

func TestCancelCannotReplaceAlreadyPublishedSuccess(t *testing.T) {
	m, _, _, _ := fixture(t, 1_000_000)
	m.mu.Lock()
	m.entries["observer-handoff"] = &entry{active: true, record: record{Summary: Summary{ID: "observer-handoff", Status: Succeeded}}, partResults: map[int]speech.Transcription{}}
	m.mu.Unlock()
	job, err := m.Cancel("observer-handoff")
	if err != nil || job.Status != Succeeded {
		t.Fatal("终态已发布但观察者尚未退出时取消破坏成功结果", err)
	}
}

func TestSlowWorkerKeepsDirectoryLockAndDoneUntilExit(t *testing.T) {
	m, options, control, project := fixture(t, 1_000_000)
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	control.synthesize = func(_ context.Context, r speech.SynthesizeRequest) (speech.AudioResult, error) {
		close(started)
		<-release
		return speech.AudioResult{Operation: operation(r.ID, r.ModelID), VoiceID: r.VoiceID, MIME: "audio/mpeg", OutputFormat: r.OutputFormat, Data: bytes.Clone(control.mp3)}, nil
	}
	if _, err := m.Submit(context.Background(), project, synthesizeInput("slow-worker")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("慢假服务未开始")
	}
	m.Close()
	select {
	case <-m.Done():
		t.Fatal("慢worker未退出却提前释放目录")
	default:
	}
	if other, err := New(options); err == nil {
		other.Close()
		t.Fatal("两个实例接管仍在处理的目录")
	}
	once.Do(func() { close(release) })
	select {
	case <-m.Done():
	case <-time.After(4 * time.Second):
		t.Fatal("worker退出后Done未结束")
	}
	restored, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if control.tts.Load() != 1 {
		t.Fatal("关闭恢复重新生成配音")
	}
}

func TestSuccessfulASRCleansOnlyPreparedAudioAndRebuildsFromReceipts(t *testing.T) {
	m, options, control, project := fixture(t, 2_000_000)
	input := transcribeInput("clean-prepared-success")
	if _, err := m.Submit(context.Background(), project, input); err != nil {
		t.Fatal(err)
	}
	completed := await(t, m, input.ID, Succeeded)
	if _, err := os.Lstat(m.output(input.ID, "prepared")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("成功后仍保留准备WAV目录")
	}
	if _, err := os.Stat(project.Assets[0].Path); err != nil {
		t.Fatal("清理删除了原始素材", err)
	}
	if len(completed.PartResults) != 2 || completed.Transcription == nil {
		t.Fatal("清理影响了完整证据读取")
	}
	// 聚合JSON丢失后，重开仍可只用原始part receipts重建，绝不重新准备或计费。
	if err := os.Remove(m.output(input.ID, "transcription.json")); err != nil {
		t.Fatal(err)
	}
	m.Close()
	<-m.Done()
	options.Resolve = func(context.Context, string) (Binding, error) {
		t.Error("原始证据恢复不读取凭据")
		return Binding{}, errors.New("locked")
	}
	options.Prepare = func(context.Context, editor.Asset, string) (Preparation, error) {
		t.Error("已经清理WAV的任务不能重新Prepare")
		return Preparation{}, errors.New("must not prepare")
	}
	posts := control.asr.Load()
	factories := control.factories.Load()
	restored, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	job, err := restored.Get(input.ID)
	if err != nil || job.Status != Saving || !job.CanResumeLocal || len(job.PartResults) != 2 {
		t.Fatal("没有准备WAV时无法恢复原始证据", err)
	}
	if _, err := restored.ResumeLocal(context.Background(), input.ID); err != nil {
		t.Fatal(err)
	}
	job = await(t, restored, input.ID, Succeeded)
	if job.Transcription == nil || job.Transcription.SourceSHA256 != completed.Transcription.SourceSHA256 || len(job.Transcription.Words) != 2 || control.asr.Load() != posts || control.factories.Load() != factories {
		t.Fatal("本地重建重新调用了服务或丢失源绑定")
	}
}

func TestPreparedCleanupFailureOnlyWarnsAfterDurableSuccess(t *testing.T) {
	m, options, _, project := fixture(t, 1_000_000)
	var deletedPath string
	m.removePrepared = func(path string) error { deletedPath = path; return errors.New("fixture file busy") }
	input := transcribeInput("cleanup-warning")
	if _, err := m.Submit(context.Background(), project, input); err != nil {
		t.Fatal(err)
	}
	job := await(t, m, input.ID, Succeeded)
	if len(job.Warnings) == 0 || job.CanResumeLocal || deletedPath != m.output(input.ID, "prepared") {
		t.Fatal("清理失败影响成功状态或使用了未受限删除目标")
	}
	m.mu.Lock()
	m.entries[input.ID].record.Preparation.Parts[0].Path = project.Assets[0].Path
	m.mu.Unlock()
	m.removePrepared = os.RemoveAll
	if err := m.cleanupPrepared(input.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(project.Assets[0].Path); err != nil {
		t.Fatal("清理使用持久化的外部路径删除原素材")
	}
	// 磁盘上的成功状态/警告应存在，清理不会把它退回失败。
	m.Close()
	<-m.Done()
	restored, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	job, err = restored.Get(input.ID)
	if err != nil || job.Status != Succeeded || len(job.Warnings) == 0 {
		t.Fatal("真实成功和清理警告未保留", err)
	}
}

func TestPartialAndUnknownPreparationRemainForReview(t *testing.T) {
	m, _, control, project := fixture(t, 2_000_000)
	control.transcribe = func(_ context.Context, r speech.TranscribeRequest, _ *speech.InputFile) (speech.Transcription, error) {
		op := operation(r.ID, r.ModelID)
		op.Status, op.SubmissionUncertain = speech.Unknown, true
		return speech.Transcription{Operation: op}, &speech.Error{Code: "transport", SubmissionUncertain: true}
	}
	input := transcribeInput("retain-unknown-evidence")
	if _, err := m.Submit(context.Background(), project, input); err != nil {
		t.Fatal(err)
	}
	await(t, m, input.ID, Unknown)
	if info, err := os.Stat(m.output(input.ID, "prepared")); err != nil || !info.IsDir() {
		t.Fatal("未知任务不应静默清理准备证据")
	}
}

func TestMissingWordTimestampsStayNilAcrossMergeAndRestart(t *testing.T) {
	m, options, control, project := fixture(t, 2_000_000)
	control.transcribe = func(_ context.Context, r speech.TranscribeRequest, file *speech.InputFile) (speech.Transcription, error) {
		return speech.Transcription{Operation: operation(r.ID, r.ModelID), AssetID: r.AssetID, SourceSHA256: file.SHA256, SourceDurationUs: file.DurationUs, Language: "zh", Text: "缺时间词", Granularity: r.Granularity, TimingComplete: false, Words: []speech.Word{{TimedText: speech.TimedText{Text: "缺时间词"}, Type: "word", TimingComplete: false, Characters: []speech.TimedText{{Text: "缺"}}}}}, nil
	}
	input := transcribeInput("missing-timestamps")
	if _, err := m.Submit(context.Background(), project, input); err != nil {
		t.Fatal(err)
	}
	job := await(t, m, input.ID, Succeeded)
	if job.Transcription == nil || job.Transcription.TimingComplete || len(job.Transcription.Words) != 2 {
		t.Fatal("未保留缺失时间状态")
	}
	for _, word := range job.Transcription.Words {
		if word.StartUs != nil || word.EndUs != nil || word.TimingComplete || word.Characters[0].StartUs != nil {
			t.Fatal("按零或文本长度补造了时间")
		}
	}
	m.Close()
	<-m.Done()
	restored, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	job, err = restored.Get(input.ID)
	if err != nil || job.Transcription.Words[0].StartUs != nil {
		t.Fatal("重开后缺失时间被补造", err)
	}
}

func TestSymlinkJobDirectoryCannotWriteAudioOutsideTask(t *testing.T) {
	m, _, control, project := fixture(t, 1_000_000)
	outside := t.TempDir()
	input := synthesizeInput("outside-job-directory")
	if err := os.Symlink(outside, m.output(input.ID, "")); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Submit(context.Background(), project, input); err != nil {
		t.Fatal(err)
	}
	await(t, m, input.ID, Failed)
	files, err := os.ReadDir(outside)
	if err != nil || len(files) != 0 || control.tts.Load() != 0 {
		t.Fatal("受控任务目录链接导致外部写入或收费请求", err)
	}
}

func TestObserverKeepsDurableJobsBeyondQueuePruning(t *testing.T) {
	m, options, control, project := fixture(t, 1_000_000)
	for index := range 105 {
		id := fmt.Sprintf("retained-%03d", index)
		if _, err := m.Submit(context.Background(), project, cloneInput(id)); err != nil {
			t.Fatal(err)
		}
		await(t, m, id, Succeeded)
	}
	if len(m.List()) != 105 || len(m.queue.List()) > 100 || control.clones.Load() != 105 {
		t.Fatal("业务历史错误地依赖短期队列缓存")
	}
	first, err := m.Get("retained-000")
	if err != nil || first.Status != Succeeded || first.Voice == nil {
		t.Fatal("队列清理丢失已成功音色任务", err)
	}
	m.Close()
	<-m.Done()
	restored, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if len(restored.List()) != 105 {
		t.Fatal("超过短期队列的任务未跨重启持久化")
	}
}

func TestKnownLocalCancellationDoesNotBecomeUnknownOrFailure(t *testing.T) {
	m, _, control, project := fixture(t, 1_000_000)
	started := make(chan struct{})
	control.transcribe = func(ctx context.Context, r speech.TranscribeRequest, _ *speech.InputFile) (speech.Transcription, error) {
		close(started)
		<-ctx.Done()
		op := operation(r.ID, r.ModelID)
		op.Status = speech.Failed
		return speech.Transcription{Operation: op}, &speech.Error{Code: "input_read", Message: "明确的本地上传前取消", Cause: ctx.Err()}
	}
	if _, err := m.Submit(context.Background(), project, transcribeInput("known-local-cancel")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("假调用未开始")
	}
	if _, err := m.Cancel("known-local-cancel"); err != nil {
		t.Fatal(err)
	}
	job := await(t, m, "known-local-cancel", Cancelled)
	if job.CanResumeLocal || job.CompletedParts != 0 {
		t.Fatal("明确的本地取消被伪造成可恢复结果")
	}
}
