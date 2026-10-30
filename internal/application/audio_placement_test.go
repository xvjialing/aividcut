package application

import (
	"context"
	"encoding/binary"
	"errors"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"aividcut/internal/editor"
	"aividcut/internal/generation"
	"aividcut/internal/media"
	"aividcut/internal/speech"
	"aividcut/internal/speechjobs"
	"aividcut/internal/workflows"
)

func preciseApplicationTools(t *testing.T) media.Toolchain {
	t.Helper()
	if os.Getenv("AIVIDCUT_FFMPEG") == "" || os.Getenv("AIVIDCUT_FFPROBE") == "" {
		t.Skip("精确音尾应用回归需显式配置本机FFmpeg/ffprobe，不依赖其他应用")
	}
	tools := media.Discover()
	if tools.FFmpegPath == "" || tools.FFprobePath == "" {
		t.Fatal("显式媒体工具不可用")
	}
	return tools
}

func preciseApplicationWAV(t *testing.T) string {
	t.Helper()
	const samples = 5777
	data := make([]byte, 44+samples*2)
	copy(data, "RIFF")
	binary.LittleEndian.PutUint32(data[4:], uint32(len(data)-8))
	copy(data[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(data[16:], 16)
	binary.LittleEndian.PutUint16(data[20:], 1)
	binary.LittleEndian.PutUint16(data[22:], 1)
	binary.LittleEndian.PutUint32(data[24:], 48000)
	binary.LittleEndian.PutUint32(data[28:], 96000)
	binary.LittleEndian.PutUint16(data[32:], 2)
	binary.LittleEndian.PutUint16(data[34:], 16)
	copy(data[36:], "data")
	binary.LittleEndian.PutUint32(data[40:], samples*2)
	for i := 0; i < samples; i++ {
		value := int16(16000 * math.Sin(float64(i)*2*math.Pi*660/48000))
		binary.LittleEndian.PutUint16(data[44+i*2:], uint16(value))
	}
	path := filepath.Join(t.TempDir(), "own-5777.wav")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func preciseApplicationCommand(t *testing.T, tools media.Toolchain, args ...string) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	data, err := exec.CommandContext(ctx, tools.FFmpegPath, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("自建媒体验证失败：%v\n%s", err, data)
	}
	return data
}

func preciseApplication(t *testing.T, tools media.Toolchain) *Service {
	t.Helper()
	s, err := New(tools, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	p := s.GetBootstrap().Project
	settings := p.Settings
	settings.Width, settings.Height, settings.FPSNum, settings.FPSDen = 32, 32, 30000, 1001
	if _, err := s.ApplyEdit(editor.EditRequest{BaseRevision: p.Revision, IdempotencyKey: "precise-settings", Commands: []editor.Command{{Type: "update_project", Settings: &settings}}}); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestExactAudioApplicationImportPlacementRetryAndWorkflow(t *testing.T) {
	tools := preciseApplicationTools(t)
	s := preciseApplication(t, tools)
	path := preciseApplicationWAV(t)
	imported, err := s.ImportPaths(context.Background(), []string{path, path})
	if err != nil {
		t.Fatal(err)
	}
	if len(imported.Project.Assets) != 1 || len(imported.Warnings) != 0 || !imported.Capabilities["audioMeasurement"].Available {
		t.Fatal("普通导入未保存实际采样能力或重复扫描产生重复资产")
	}
	asset := imported.Project.Assets[0]
	if asset.AudioSampleCount == nil || *asset.AudioSampleCount != 5777 || asset.AudioSampleRate != 48000 || asset.AudioStartUs != 0 {
		t.Fatalf("普通导入未传播真实样本证据：%+v", asset)
	}
	input := AddAudioInput{ProjectID: imported.Project.ID, ProjectRevision: imported.Project.Revision, AssetID: asset.ID, ClipID: "full-audio", TrackID: "track-audio-1"}
	added, err := s.AddAudioToTimeline(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	clip := added.Project.Clips[0]
	if clip.DurationFrames != 4 || clip.AudioRange == nil || clip.AudioRange.End != 5777 || clip.AudioRange.Unit != "samples" || added.Project.Revision != imported.Project.Revision+1 {
		t.Fatal("上轨仍floor截尾或没有实际源末端")
	}
	again, err := s.AddAudioToTimeline(context.Background(), input)
	if err != nil || again.Project.Revision != added.Project.Revision || len(again.Project.Clips) != 1 {
		t.Fatal("过期revision的相同请求重试重复添加", err)
	}
	changedTrack := input
	changedTrack.TrackID = "track-video-1"
	if _, err := s.AddAudioToTimeline(context.Background(), changedTrack); err == nil {
		t.Fatal("相同ID但不同明确轨道被误报幂等成功")
	}
	newStale := input
	newStale.ClipID = "new-stale"
	if _, err := s.AddAudioToTimeline(context.Background(), newStale); err == nil {
		t.Fatal("新操作使用旧revision覆盖编辑")
	}
	undone, err := s.Undo()
	if err != nil || len(undone.Project.Clips) != 0 || len(undone.Project.Assets) != 1 {
		t.Fatal("完整音频上轨未单组撤销", err)
	}
	if _, err := s.Redo(); err != nil {
		t.Fatal(err)
	}
	if err := s.ConfigureWorkflows(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	before := s.GetBootstrap().Project
	preview, err := s.PreviewWorkflow(context.Background(), workflows.Input{WorkflowID: "voiceover", Parameters: map[string]any{"assetId": asset.ID, "startFrame": 10}})
	if err != nil {
		t.Fatal(err)
	}
	if projectHash(before) != projectHash(s.GetBootstrap().Project) {
		t.Fatal("配音工作流预览修改工程")
	}
	var planned *editor.Clip
	for _, command := range preview.Request.Commands {
		if command.Type == "add_clip" {
			planned = command.Clip
		}
	}
	if planned == nil || planned.DurationFrames != 4 || planned.AudioRange == nil || planned.AudioRange.End != 5777 {
		t.Fatal("配音工作流仍floor实际音尾")
	}
	applied, err := s.ApplyWorkflowPreview(context.Background(), preview.ID)
	if err != nil || len(applied.Project.Clips) != 2 || applied.Project.Revision != before.Revision+1 {
		t.Fatal("配音工作流不能原子应用", err)
	}
	undone, err = s.Undo()
	if err != nil || projectHash(undone.Project) != projectHash(before) {
		t.Fatal("精确音频工作流无法整组撤销", err)
	}
	output := filepath.Join(t.TempDir(), "ordinary-tail.mp4")
	if err := tools.Export(context.Background(), undone.Project, media.ExportOptions{Path: output, Preset: "project"}, nil); err != nil {
		t.Fatal(err)
	}
	pcm := preciseApplicationCommand(t, tools, "-hide_banner", "-loglevel", "error", "-i", output, "-map", "0:a:0", "-ac", "1", "-ar", "48000", "-f", "f32le", "pipe:1")
	if len(pcm)/4 < 5777 {
		t.Fatal("应用到真实导出后仍缺少最后采样")
	}
	var energy float64
	for i := 5500; i < 5777; i++ {
		v := math.Float32frombits(binary.LittleEndian.Uint32(pcm[i*4:]))
		energy += float64(v * v)
	}
	if energy < 1 {
		t.Fatal("普通导入→上轨→MP4的非帧对齐尾音被截断")
	}
}

func TestExactAudioApplicationRelinkMeasuredLockedSourceAndReopen(t *testing.T) {
	s := preciseApplication(t, preciseApplicationTools(t))
	path := preciseApplicationWAV(t)
	imported, err := s.ImportPaths(context.Background(), []string{path})
	if err != nil {
		t.Fatal(err)
	}
	asset := imported.Project.Assets[0]
	added, err := s.AddAudioToTimeline(context.Background(), AddAudioInput{ProjectID: imported.Project.ID, ProjectRevision: imported.Project.Revision, AssetID: asset.ID, ClipID: "relink-precise", TrackID: "track-audio-1"})
	if err != nil {
		t.Fatal(err)
	}
	track := added.Project.Tracks[len(added.Project.Tracks)-1]
	track.Locked = true
	if _, err := s.ApplyEdit(editor.EditRequest{BaseRevision: added.Project.Revision, IdempotencyKey: "lock-precise", Commands: []editor.Command{{Type: "update_track", Track: &track}}}); err != nil {
		t.Fatal(err)
	}
	projectPath := filepath.Join(t.TempDir(), "precise.aividcut")
	if _, err := s.SaveProjectTo(projectPath); err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(t.TempDir(), "moved.wav")
	if err := os.Rename(path, moved); err != nil {
		t.Fatal(err)
	}
	if _, err := s.OpenProjectPath(projectPath); err != nil {
		t.Fatal(err)
	}
	relinked, err := s.RelinkAsset(context.Background(), asset.ID, moved)
	if err != nil {
		t.Fatal("相同SHA但普通Probe无count时错误拒绝精确片段", err)
	}
	if relinked.Project.Assets[0].AudioSampleCount == nil || *relinked.Project.Assets[0].AudioSampleCount != 5777 || !relinked.Project.Tracks[len(relinked.Project.Tracks)-1].Locked || !reflect.DeepEqual(relinked.Project.Clips, added.Project.Clips) {
		t.Fatal("重定位丢失样本证据或改变锁定片段")
	}
	if _, err := s.SaveProject(); err != nil {
		t.Fatal(err)
	}
	reopened, err := s.OpenProjectPath(projectPath)
	if err != nil || reopened.Project.Assets[0].AudioSampleCount == nil || *reopened.Project.Assets[0].AudioSampleCount != 5777 || reopened.Project.Clips[0].AudioRange.End != 5777 {
		t.Fatal("保存重开丢失真实音尾", err)
	}
}

func TestExactAudioPlacementUsesRealOverlapAndExplicitTracksFailAtomically(t *testing.T) {
	tools := preciseApplicationTools(t)
	s := preciseApplication(t, tools)
	wav := preciseApplicationWAV(t)
	late := filepath.Join(t.TempDir(), "late.mov")
	preciseApplicationCommand(t, tools, "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "color=black:s=32x32:r=25:d=2", "-itsoffset", "0.5", "-i", wav, "-map", "0:v:0", "-map", "1:a:0", "-c:v", "libx264", "-c:a", "pcm_s16le", "-output_ts_offset", "3", late)
	imported, err := s.ImportPaths(context.Background(), []string{wav, late})
	if err != nil {
		t.Fatal(err)
	}
	if imported.Project.Assets[1].AudioStartUs != 500000 || imported.Project.Assets[1].AudioSampleCount == nil || *imported.Project.Assets[1].AudioSampleCount != 5777 {
		t.Fatal("普通视频导入没有真实音轨偏移/计数")
	}
	first, err := s.AddAudioToTimeline(context.Background(), AddAudioInput{ProjectID: imported.Project.ID, ProjectRevision: imported.Project.Revision, AssetID: imported.Project.Assets[0].ID, ClipID: "first-carrier", TrackID: "track-audio-1"})
	if err != nil {
		t.Fatal(err)
	}
	lateInput := AddAudioInput{ProjectID: imported.Project.ID, ProjectRevision: first.Project.Revision, AssetID: imported.Project.Assets[1].ID, ClipID: "late-carrier"}
	second, err := s.AddAudioToTimeline(context.Background(), lateInput)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Project.Tracks) != len(imported.Project.Tracks) || second.Project.Clips[1].TrackID != "track-audio-1" || second.Project.Clips[1].StartFrame != 0 || second.Project.Clips[1].AudioRange.OffsetNum != 500000 {
		t.Fatal("自动找轨按ceil占位误判重叠，或把晚起音轨搬到源零点")
	}
	before := s.GetBootstrap().Project
	for _, trackID := range []string{"track-video-1", "unknown-track", "track-audio-1"} {
		_, err := s.AddAudioToTimeline(context.Background(), AddAudioInput{ProjectID: before.ID, ProjectRevision: before.Revision, AssetID: imported.Project.Assets[0].ID, ClipID: "invalid-" + trackID, TrackID: trackID})
		if err == nil || projectHash(s.GetBootstrap().Project) != projectHash(before) {
			t.Fatalf("明确轨道类型/缺失/真实重叠失败没有原子拒绝：%s %v", trackID, err)
		}
	}
	track := before.Tracks[len(before.Tracks)-1]
	track.Locked = true
	locked, err := s.ApplyEdit(editor.EditRequest{BaseRevision: before.Revision, IdempotencyKey: "lock-audio-placement", Commands: []editor.Command{{Type: "update_track", Track: &track}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddAudioToTimeline(context.Background(), AddAudioInput{ProjectID: locked.Project.ID, ProjectRevision: locked.Project.Revision, AssetID: imported.Project.Assets[0].ID, ClipID: "locked-placement", TrackID: track.ID, StartFrame: 100}); err == nil || projectHash(s.GetBootstrap().Project) != projectHash(locked.Project) {
		t.Fatal("明确锁轨的音频添加没有原子拒绝")
	}
}

func TestExactAudioImportGapFailureAndCancellationPublishNoPartialBatch(t *testing.T) {
	tools := preciseApplicationTools(t)
	s := preciseApplication(t, tools)
	wav := preciseApplicationWAV(t)
	gap := filepath.Join(t.TempDir(), "gap.mkv")
	preciseApplicationCommand(t, tools, "-hide_banner", "-loglevel", "error", "-i", wav, "-af", "asetnsamples=n=480:p=0,asetpts=PTS+gte(N\\,2400)*0.1/TB", "-c:a", "pcm_s16le", gap)
	before := s.GetBootstrap().Project
	if _, err := s.ImportPaths(context.Background(), []string{wav, gap}); err == nil || !strings.Contains(err.Error(), "缺口/重叠") || projectHash(s.GetBootstrap().Project) != projectHash(before) {
		t.Fatal("时间不连续失败后发布了同批已测量素材，或压缩了源时间", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.ImportPaths(ctx, []string{wav}); !errors.Is(err, context.Canceled) || projectHash(s.GetBootstrap().Project) != projectHash(before) {
		t.Fatal("取消后继续发布导入", err)
	}
}

func TestExactAudioSpeechRealMP3ProbeAndUndoableImport(t *testing.T) {
	tools := preciseApplicationTools(t)
	wav := preciseApplicationWAV(t)
	mp3 := filepath.Join(t.TempDir(), "own-voice.mp3")
	preciseApplicationCommand(t, tools, "-hide_banner", "-loglevel", "error", "-i", wav, "-ar", "44100", "-c:a", "libmp3lame", "-b:a", "128k", mp3)
	audio, err := os.ReadFile(mp3)
	if err != nil {
		t.Fatal(err)
	}
	decoded := preciseApplicationCommand(t, tools, "-hide_banner", "-loglevel", "error", "-i", mp3, "-map", "0:a:0", "-c:a", "pcm_s16le", "-f", "s16le", "pipe:1")
	count := int64(len(decoded) / 2)
	s := preciseApplication(t, tools)
	control := &applicationSpeechControl{audio: audio}
	var calls atomic.Int32
	options := speechApplicationOptions(t, t.TempDir(), &appSecretStore{values: map[string][]byte{}}, control)
	options.SpeechProbe, options.SpeechPrepare = nil, nil
	options.SpeechFactory = func(secrets generation.SecretSource, files speech.MediaSource) (speech.Provider, error) {
		return &timedApplicationProvider{applicationSpeechProvider: &applicationSpeechProvider{control: control, secrets: secrets, files: files}, calls: &calls}, nil
	}
	if err := s.ConfigureGeneration(options); err != nil {
		t.Fatal(err)
	}
	connection := saveApplicationSpeechConnection(t, s, speech.MultilingualV2)
	input := speechjobs.Input{ID: "real-counted-voice", ConnectionID: connection.ID, Mode: speechjobs.Synthesize, Consent: true, Synthesize: &speech.SynthesizeRequest{Text: "你好", VoiceID: "fixture-voice", WithTiming: true}}
	if _, err := s.StartSpeechJob(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	job := awaitSpeechApplication(t, s, input.ID, speechjobs.Succeeded)
	if job.Audio == nil || job.Audio.AudioSampleCount == nil || *job.Audio.AudioSampleCount != count || job.Audio.AudioSampleRate != 44100 {
		t.Fatal("默认语音Probe未保存真实MP3解码计数")
	}
	before := s.GetBootstrap().Project
	importOptions := ImportSpeechAudioOptions{AddToTimeline: true, TrackID: "track-audio-1", StartFrame: 1, GenerateCaptions: true, CaptionTrackID: "track-caption-1", AlignmentSource: "original"}
	imported, err := s.ImportSpeechAudio(context.Background(), job.ID, importOptions)
	if err != nil {
		t.Fatal(err)
	}
	var clip *editor.Clip
	for i := range imported.Project.Clips {
		if imported.Project.Clips[i].Kind == "audio" {
			clip = &imported.Project.Clips[i]
		}
	}
	if clip == nil || clip.DurationFrames != 4 || clip.AudioRange == nil || clip.AudioRange.End != count || clip.AudioRange.SampleRate != 44100 || len(imported.Project.Transcripts) != 1 || imported.Project.Transcripts[0].Origin != "tts" {
		t.Fatal("真实配音上轨丢尾或来源被伪装成ASR")
	}
	again, err := s.ImportSpeechAudio(context.Background(), job.ID, importOptions)
	if err != nil || again.Project.Revision != imported.Project.Revision || calls.Load() != 1 {
		t.Fatal("精确配音重试重新生成/重复导入", err)
	}
	undone, err := s.Undo()
	if err != nil || projectHash(undone.Project) != projectHash(before) {
		t.Fatal("真实音频、样本证据及字幕没有整组撤销", err)
	}
}

func TestAudioMeasurementBudgetAndFFprobeOnlyImportAreExplicit(t *testing.T) {
	path := preciseApplicationWAV(t)
	asset := editor.Asset{ID: "limited", Name: "限额音频", Kind: "audio", Path: path, HasAudio: true, DurationUs: importAudioMaxDurationUs + 1}
	budget, cancel := newImportAudioBudget(context.Background())
	defer cancel()
	result, warning, err := importedAudioEvidence(context.Background(), media.Toolchain{FFmpegPath: "/must-not-run", FFprobePath: "/must-not-run"}, asset, budget)
	if err != nil || result.AudioSampleCount != nil || !strings.Contains(warning, "上限") {
		t.Fatal("超限未明确保留未知count", err, warning)
	}
	asset.DurationUs = 100000
	budget.remaining = 0
	_, warning, err = importedAudioEvidence(context.Background(), media.Toolchain{FFmpegPath: "/must-not-run", FFprobePath: "/must-not-run"}, asset, budget)
	if err != nil || !strings.Contains(warning, "累计") {
		t.Fatal("累计预算未阻止后续解码", err, warning)
	}
	budget.remaining = importAudioBatchDurationUs
	expired, cancelExpired := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancelExpired()
	budget.ctx = expired
	_, warning, err = importedAudioEvidence(context.Background(), media.Toolchain{FFmpegPath: "/must-not-run", FFprobePath: "/must-not-run"}, asset, budget)
	if err != nil || !strings.Contains(warning, "15秒") {
		t.Fatal("批量测量期限耗尽未显式降级", err, warning)
	}
	ctx, stop := context.WithCancel(context.Background())
	stop()
	if _, _, err := importedAudioEvidence(ctx, media.Toolchain{}, asset, budget); !errors.Is(err, context.Canceled) {
		t.Fatal("用户取消被降级成导入warning", err)
	}
	tools := preciseApplicationTools(t)
	s := preciseApplication(t, media.Toolchain{FFprobePath: tools.FFprobePath})
	imported, err := s.ImportPaths(context.Background(), []string{path})
	if err != nil || len(imported.Warnings) != 1 || imported.Project.Assets[0].AudioSampleCount != nil || imported.Capabilities["audioMeasurement"].Available {
		t.Fatal("仅ffprobe时未清楚返回声明时长降级", err)
	}
	added, err := s.AddAudioToTimeline(context.Background(), AddAudioInput{ProjectID: imported.Project.ID, ProjectRevision: imported.Project.Revision, AssetID: imported.Project.Assets[0].ID, ClipID: "declared-audio"})
	if err != nil || len(added.Warnings) != 1 || added.Project.Clips[0].AudioRange.Unit != "us" || added.Project.Clips[0].DurationFrames != 4 {
		t.Fatal("未知样本数上轨冒充实测或仍截成3帧", err)
	}
	if len(s.GetBootstrap().Warnings) != 0 {
		t.Fatal("一次导入warning写入了持久工程/常驻Bootstrap")
	}
}
