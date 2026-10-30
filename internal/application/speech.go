package application

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"aividcut/internal/editor"
	"aividcut/internal/speech"
	"aividcut/internal/speechcaptions"
	"aividcut/internal/speechjobs"
)

type SpeechCatalog struct {
	speech.Capabilities
	AudioPreparation Capability `json:"audioPreparation"`
	AudioImport      Capability `json:"audioImport"`
}
type SpeechSummary struct {
	speechjobs.Summary
	AcceptedTranscriptID string `json:"acceptedTranscriptId,omitempty"`
	AudioImported        bool   `json:"audioImported"`
	AudioOnTimeline      bool   `json:"audioOnTimeline"`
	CaptionTimingSource  string `json:"captionTimingSource,omitempty"`
}
type SpeechTimingChoice struct {
	Source    string `json:"source"`
	Available bool   `json:"available"`
	Reason    string `json:"reason"`
	Text      string `json:"text"`
	WordCount int    `json:"wordCount"`
}
type SpeechJob struct {
	SpeechSummary
	Input         speechjobs.Input         `json:"input"`
	Parts         []speechjobs.PartState   `json:"parts"`
	Transcription *speech.Transcription    `json:"transcription,omitempty"`
	PartResults   []speech.Transcription   `json:"partResults,omitempty"`
	Timing        *speech.TimedAudioResult `json:"timing,omitempty"`
	TimingChoices []SpeechTimingChoice     `json:"timingChoices"`
}

func (s *Service) GetSpeechCatalog() (SpeechCatalog, error) {
	provider, err := speech.NewElevenLabs(speech.Config{})
	if err != nil {
		return SpeechCatalog{}, err
	}
	value := SpeechCatalog{Capabilities: provider.Capabilities(), AudioPreparation: Capability{Available: s.mediaReport.FFmpeg.Available && s.mediaReport.FFprobe.Available, Reason: "音轨准备需要可用的 FFmpeg 与 ffprobe"}, AudioImport: Capability{Available: s.mediaReport.FFprobe.Available, Reason: "配音需要 ffprobe 核对真实音频时长"}}
	s.generationMu.Lock()
	defer s.generationMu.Unlock()
	if s.generation != nil && !s.generationClosed {
		value.AudioPreparation = s.generation.audioPreparation
		value.AudioImport = s.generation.audioImport
	}
	if value.AudioPreparation.Available {
		value.AudioPreparation.Reason = ""
	}
	if value.AudioImport.Available {
		value.AudioImport.Reason = ""
	} else {
		value.SpeechSynthesis = speech.Feature{Supported: false, Reason: value.AudioImport.Reason}
	}
	return value, nil
}

func (s *Service) ListVoices(ctx context.Context, connectionID string, request speech.VoiceListRequest) (speech.VoicePage, error) {
	if err := ctx.Err(); err != nil {
		return speech.VoicePage{}, err
	}
	s.generationMu.Lock()
	state, err := s.generationLocked()
	if err != nil {
		s.generationMu.Unlock()
		return speech.VoicePage{}, err
	}
	matched := false
	for _, profile := range state.connections.List() {
		if profile.ID == connectionID && profile.ProviderID == speech.ElevenLabsID {
			matched = true
			break
		}
	}
	if !matched {
		s.generationMu.Unlock()
		return speech.VoicePage{}, errors.New("请选择已保存的语音服务连接")
	}
	state.auxWG.Add(1)
	factory := state.speechFactory
	s.generationMu.Unlock()
	defer state.auxWG.Done()
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	stop := context.AfterFunc(state.ctx, cancel)
	defer stop()
	lease, err := state.connections.Acquire(ctx, connectionID)
	if err != nil {
		return speech.VoicePage{}, err
	}
	defer lease.Close()
	provider, err := factory(lease, nil)
	if err != nil {
		return speech.VoicePage{}, err
	}
	if provider.Capabilities().ProviderID != lease.Profile.ProviderID {
		return speech.VoicePage{}, errors.New("语音连接与提供商不一致")
	}
	page, err := provider.ListVoices(ctx, request)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return speech.VoicePage{}, ctxErr
	}
	return page, err
}

func (s *Service) StartSpeechJob(ctx context.Context, input speechjobs.Input) (SpeechSummary, error) {
	return s.startSpeechForMCP(ctx, input, nil, 0)
}

func (s *Service) startSpeechForMCP(ctx context.Context, input speechjobs.Input, scope *mcpProjectScope, revision int64) (SpeechSummary, error) {
	s.generationMu.Lock()
	defer s.generationMu.Unlock()
	state, err := s.generationLocked()
	if err != nil {
		return SpeechSummary{}, err
	}
	if input.Mode == speechjobs.Transcribe && !state.audioPreparation.Available {
		return SpeechSummary{}, errors.New(state.audioPreparation.Reason)
	}
	if input.Mode == speechjobs.Synthesize && !state.audioImport.Available {
		return SpeechSummary{}, errors.New(state.audioImport.Reason)
	}
	s.mu.Lock()
	project := s.engine.Snapshot()
	if scope != nil {
		if err := scope.checkRequestLocked(s); err != nil {
			s.mu.Unlock()
			return SpeechSummary{}, err
		}
		if project.Revision != revision {
			s.mu.Unlock()
			return SpeechSummary{}, errors.New("工程已变化，请重新核对本次语音请求")
		}
	}
	s.mu.Unlock()
	job, err := state.speech.Submit(ctx, project, input)
	if err != nil {
		return SpeechSummary{}, err
	}
	return s.speechViewLocked(state, job.Summary), nil
}

func speechAudioPath(state *generationState, summary speechjobs.Summary) (string, error) {
	if summary.Audio == nil || summary.Audio.Kind != "audio" || summary.Audio.ID != summary.ID+"-audio" || summary.Audio.Fingerprint == "" || summary.Audio.DurationUs <= 0 {
		return "", errors.New("语音结果没有核查过的音频元数据")
	}
	path := summary.Audio.Path
	if !filepath.IsAbs(path) {
		return "", errors.New("语音结果路径无效")
	}
	base := filepath.Join(state.speechOutputs, summary.ID)
	within := func(value string) bool {
		rel, err := filepath.Rel(base, value)
		return err == nil && rel != "." && rel != ".." && !filepath.IsAbs(rel) && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
	}
	if !within(path) {
		return "", errors.New("语音结果不在原任务目录中")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > speech.MaxResponseBytes {
		return "", errors.New("语音结果缺失、类型或大小不符")
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || !within(resolved) {
		return "", errors.New("语音结果路径已离开原任务目录")
	}
	return path, nil
}

func (s *Service) speechViewLocked(state *generationState, summary speechjobs.Summary) SpeechSummary {
	value := SpeechSummary{Summary: summary}
	s.mu.Lock()
	defer s.mu.Unlock()
	project := s.engine.Snapshot()
	if summary.Audio != nil {
		audio := *summary.Audio
		audio.URL, audio.ThumbnailURL = "", ""
		mediaID := "speech-preview-" + summary.Audio.ID
		if path, err := speechAudioPath(state, summary); err == nil {
			audio.URL, _ = s.files.Register(mediaID, path)
		} else {
			s.files.Unregister(mediaID)
		}
		value.Audio = &audio
	}
	if project.ID != summary.ProjectID {
		return value
	}
	for _, transcript := range project.Transcripts {
		sourceID, fingerprint := summary.SourceAssetID, summary.SourceFingerprint
		if summary.Mode == speechjobs.Synthesize && summary.Audio != nil {
			sourceID, fingerprint = summary.Audio.ID, summary.Audio.Fingerprint
		}
		if transcript.ID == speechTranscriptID(summary.ID) && transcript.AssetID == sourceID && transcript.SourceFingerprint == fingerprint && !transcript.Stale {
			value.AcceptedTranscriptID = transcript.ID
			if transcript.Origin == "tts" {
				value.CaptionTimingSource = transcript.TimingSource
			}
			break
		}
	}
	if summary.Audio != nil {
		index := assetIndex(project, summary.Audio.ID)
		if index >= 0 {
			audio := project.Assets[index]
			if audio.Kind == "audio" && audio.Fingerprint == summary.Audio.Fingerprint && !audio.Missing {
				if info, err := os.Stat(audio.Path); err == nil && info.Mode().IsRegular() {
					value.AudioImported = true
				}
			}
		}
		if value.AudioImported {
			for _, clip := range project.Clips {
				if clip.AssetID == summary.Audio.ID && clip.Kind == "audio" {
					value.AudioOnTimeline = true
					break
				}
			}
		}
	}
	if value.Status == speechjobs.Succeeded {
		if value.Mode == speechjobs.Synthesize && value.AudioOnTimeline {
			value.Message = "配音已在当前时间线上使用，可继续编辑；导入不会重复添加已有片段"
		} else if value.Mode == speechjobs.Synthesize && value.AudioImported {
			value.Message = "配音已加入当前素材库，可继续放入时间线"
		} else if value.Mode == speechjobs.Transcribe && value.AcceptedTranscriptID != "" {
			value.Message = "源转写已接纳到当前工程，人工修订与字幕由工程保存"
		}
	}
	return value
}

func (s *Service) ListSpeechJobs() ([]SpeechSummary, error) {
	s.generationMu.Lock()
	defer s.generationMu.Unlock()
	state, err := s.generationLocked()
	if err != nil {
		return nil, err
	}
	result := []SpeechSummary{}
	for _, job := range state.speech.List() {
		result = append(result, s.speechViewLocked(state, job))
	}
	return result, nil
}
func (s *Service) GetSpeechJob(id string) (SpeechJob, error) {
	s.generationMu.Lock()
	defer s.generationMu.Unlock()
	state, err := s.generationLocked()
	if err != nil {
		return SpeechJob{}, err
	}
	job, err := state.speech.Get(id)
	if err != nil {
		return SpeechJob{}, err
	}
	return SpeechJob{SpeechSummary: s.speechViewLocked(state, job.Summary), Input: job.Input, Parts: job.Parts, Transcription: job.Transcription, PartResults: job.PartResults, Timing: job.Timing, TimingChoices: speechTimingChoices(job)}, nil
}

func speechTimingChoices(job speechjobs.Job) []SpeechTimingChoice {
	choices := []SpeechTimingChoice{}
	if job.Mode != speechjobs.Synthesize {
		return choices
	}
	for _, source := range []string{"original", "normalized"} {
		choice := SpeechTimingChoice{Source: source}
		if job.Status != speechjobs.Succeeded || job.Audio == nil || job.Input.Synthesize == nil || !job.Input.Synthesize.WithTiming || job.Timing == nil {
			choice.Reason = "本次配音没有已核对的时间结果，仍可单独导入音频"
		} else {
			converted, err := speechcaptions.Convert(job.ID, job.Input.Synthesize.Text, *job.Timing, job.Audio.DurationUs, source)
			if err != nil {
				choice.Reason = err.Error()
			} else {
				choice.Available = true
				choice.Text = converted.Text
				choice.WordCount = len(converted.Words)
			}
		}
		choices = append(choices, choice)
	}
	return choices
}
func (s *Service) CancelSpeechJob(id string) (SpeechSummary, error) {
	s.generationMu.Lock()
	defer s.generationMu.Unlock()
	state, err := s.generationLocked()
	if err != nil {
		return SpeechSummary{}, err
	}
	job, err := state.speech.Cancel(id)
	if err != nil {
		return SpeechSummary{}, err
	}
	return s.speechViewLocked(state, job.Summary), nil
}
func (s *Service) ResumeSpeechLocal(ctx context.Context, id string) (SpeechSummary, error) {
	s.generationMu.Lock()
	defer s.generationMu.Unlock()
	state, err := s.generationLocked()
	if err != nil {
		return SpeechSummary{}, err
	}
	job, err := state.speech.ResumeLocal(ctx, id)
	if err != nil {
		return SpeechSummary{}, err
	}
	return s.speechViewLocked(state, job.Summary), nil
}
func (s *Service) AcknowledgeSpeechUnknown(id string) (SpeechSummary, error) {
	s.generationMu.Lock()
	defer s.generationMu.Unlock()
	state, err := s.generationLocked()
	if err != nil {
		return SpeechSummary{}, err
	}
	job, err := state.speech.AcknowledgeUnknown(id)
	if err != nil {
		return SpeechSummary{}, err
	}
	return s.speechViewLocked(state, job.Summary), nil
}

func (s *Service) AcceptSpeechTranscript(ctx context.Context, id string, options AcceptTranscriptOptions) (Bootstrap, error) {
	s.generationMu.Lock()
	defer s.generationMu.Unlock()
	state, err := s.generationLocked()
	if err != nil {
		return Bootstrap{}, err
	}
	job, err := state.speech.Get(id)
	if err != nil {
		return Bootstrap{}, err
	}
	if job.Status != speechjobs.Succeeded || job.Mode != speechjobs.Transcribe || job.Transcription == nil {
		return Bootstrap{}, errors.New("只有成功保存且有完整转写结果的任务可以接纳")
	}
	return s.acceptSpeechResult(ctx, job.ID, job.ProjectID, job.SourceAssetID, job.SourceFingerprint, job.SourceDurationUs, *job.Transcription, options)
}

func (s *Service) ImportSpeechAudio(ctx context.Context, id string, options ImportSpeechAudioOptions) (Bootstrap, error) {
	if err := ctx.Err(); err != nil {
		return Bootstrap{}, err
	}
	if options.StartFrame < 0 {
		return Bootstrap{}, errors.New("配音起点不能为负帧")
	}
	if !options.GenerateCaptions && (options.CaptionLayout != nil || options.CaptionTrackID != "" || options.AlignmentSource != "") {
		return Bootstrap{}, errors.New("未选择添加字幕时不能附带字幕来源或排版参数")
	}
	s.generationMu.Lock()
	state, err := s.generationLocked()
	if err != nil {
		s.generationMu.Unlock()
		return Bootstrap{}, err
	}
	state.auxWG.Add(1)
	s.generationMu.Unlock()
	defer state.auxWG.Done()
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	stop := context.AfterFunc(state.ctx, cancel)
	defer stop()
	job, err := state.speech.Get(id)
	if err != nil {
		return Bootstrap{}, err
	}
	if job.Status != speechjobs.Succeeded || job.Mode != speechjobs.Synthesize {
		return Bootstrap{}, errors.New("只有成功保存的配音可以导入")
	}
	path, err := speechAudioPath(state, job.Summary)
	if err != nil {
		return Bootstrap{}, err
	}
	s.mu.Lock()
	engine, initial := s.engine, s.engine.Snapshot()
	s.mu.Unlock()
	if initial.ID != job.ProjectID {
		return Bootstrap{}, errors.New("请先打开配音请求对应的原工程")
	}
	if err := verifySourceFingerprint(ctx, path, job.Audio.Fingerprint); err != nil {
		return Bootstrap{}, err
	}
	// 已导入素材可能被用户重定位；字幕必须核查工程实际播放的文件，而不只信任务原副本。
	verifiedProjectPath := path
	initialAudioIndex := assetIndex(initial, job.Audio.ID)
	if initialAudioIndex >= 0 {
		actual := initial.Assets[initialAudioIndex]
		if actual.Kind != "audio" || actual.Missing || actual.Fingerprint != job.Audio.Fingerprint || actual.DurationUs != job.Audio.DurationUs {
			return Bootstrap{}, errors.New("工程中的配音素材与本次结果身份不一致")
		}
		verifiedProjectPath = actual.Path
		if verifiedProjectPath != path {
			if err := verifySourceFingerprint(ctx, verifiedProjectPath, actual.Fingerprint); err != nil {
				return Bootstrap{}, err
			}
		}
	}
	var converted speechcaptions.Result
	var layout *editor.CaptionLayout
	if options.GenerateCaptions {
		if job.Input.Synthesize == nil || !job.Input.Synthesize.WithTiming || job.Timing == nil {
			return Bootstrap{}, errors.New("本次配音没有取得服务商时间，仍可单独导入音频；不会按文字估算字幕")
		}
		if options.AlignmentSource == "" {
			options.AlignmentSource = "original"
		}
		converted, err = speechcaptions.Convert(job.ID, job.Input.Synthesize.Text, *job.Timing, job.Audio.DurationUs, options.AlignmentSource)
		if err != nil {
			return Bootstrap{}, err
		}
		layout, err = editor.NormalizeCaptionLayout(options.CaptionLayout)
		if err != nil {
			return Bootstrap{}, err
		}
	}
	s.generationMu.Lock()
	defer s.generationMu.Unlock()
	if s.generationClosed || s.generation != state {
		return Bootstrap{}, errors.New("语音结果服务已关闭")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	project := s.engine.Snapshot()
	if s.engine != engine || project.ID != job.ProjectID {
		return Bootstrap{}, errors.New("核对配音期间工程已切换，请在原工程重新导入")
	}
	audio := *job.Audio
	audio.URL, audio.ThumbnailURL = "", ""
	audio.Missing = false
	commands := []editor.Command{}
	if index := assetIndex(project, audio.ID); index >= 0 {
		if project.Assets[index].Fingerprint != audio.Fingerprint || project.Assets[index].Kind != "audio" || project.Assets[index].DurationUs != audio.DurationUs || project.Assets[index].Missing || project.Assets[index].Path != verifiedProjectPath {
			return Bootstrap{}, errors.New("同标识已有不同素材，未覆盖工程")
		}
	} else {
		if initialAudioIndex >= 0 {
			return Bootstrap{}, errors.New("核对期间配音素材已被移除，请重新检查导入范围")
		}
		commands = append(commands, editor.Command{Type: "add_asset", Asset: &audio})
	}
	hasAudioClip := false
	for _, clip := range project.Clips {
		if clip.Kind == "audio" && clip.AssetID == audio.ID {
			hasAudioClip = true
			break
		}
	}
	if options.AddToTimeline {
		clipID := job.ID + "-audio-clip"
		exists := false
		for _, clip := range project.Clips {
			if clip.ID == clipID {
				if clip.AssetID != audio.ID {
					return Bootstrap{}, errors.New("配音片段标识已被其它内容使用")
				}
			}
			if clip.Kind == "audio" && clip.AssetID == audio.ID {
				exists = true
			}
		}
		if !exists {
			trackID := options.TrackID
			if trackID == "" {
				trackID = editor.NewID("voice-track")
				track := editor.Track{ID: trackID, Name: "口播配音", Kind: "audio"}
				commands = append(commands, editor.Command{Type: "add_track", Track: &track})
			}
			clip := editor.DefaultClip(clipID, trackID, "audio")
			clip.AssetID, clip.Name, clip.StartFrame = audio.ID, audio.Name, options.StartFrame
			clip, err = editor.AudioClipToSourceEnd(clip, audio, project.Settings)
			if err != nil {
				return Bootstrap{}, err
			}
			commands = append(commands, editor.Command{Type: "add_clip", Clip: &clip})
			hasAudioClip = true
		}
	}
	if options.GenerateCaptions {
		if !hasAudioClip {
			return Bootstrap{}, errors.New("请先将配音放入时间线，或选择与字幕一起上轨")
		}
		transcriptID := speechTranscriptID(job.ID)
		var existing *editor.Transcript
		for i := range project.Transcripts {
			if project.Transcripts[i].ID == transcriptID {
				existing = &project.Transcripts[i]
				break
			}
		}
		if existing == nil {
			for _, transcript := range project.Transcripts {
				if transcript.AssetID == audio.ID && transcript.CaptionTrackID != "" {
					return Bootstrap{}, errors.New("该配音已有另一份活动字幕，请先核对现有来源，未覆盖人工编辑")
				}
			}
			transcript := editor.Transcript{ID: transcriptID, AssetID: audio.ID, SourceFingerprint: audio.Fingerprint, SourceDurationUs: audio.DurationUs, RawWords: converted.Words, Origin: "tts", TimingSource: options.AlignmentSource}
			commands = append(commands, editor.Command{Type: "accept_transcript", Transcript: &transcript})
		} else if existing.Stale || existing.AssetID != audio.ID || existing.SourceFingerprint != audio.Fingerprint || existing.SourceDurationUs != audio.DurationUs || existing.Origin != "tts" || existing.TimingSource != options.AlignmentSource {
			return Bootstrap{}, errors.New("这份配音已接纳不同文本来源或来源已变化；未覆盖现有字幕与人工修改")
		}
		trackID := options.CaptionTrackID
		if existing != nil && existing.CaptionTrackID != "" {
			if trackID != "" && trackID != existing.CaptionTrackID {
				return Bootstrap{}, errors.New("已有字幕需要通过排版预览调整目标轨，重复导入不会改动布局")
			}
			if layout != nil {
				current, err := editor.NormalizeCaptionLayout(existing.CaptionLayout)
				if err != nil {
					return Bootstrap{}, err
				}
				if current == nil {
					current = &editor.CaptionLayout{Mode: "word"}
				}
				if !reflect.DeepEqual(current, layout) {
					return Bootstrap{}, errors.New("已有字幕需要通过分句预览调整排版，重复导入不会覆盖人工修改")
				}
			}
		} else {
			if trackID == "" {
				trackID = editor.NewID("voice-caption-track")
				track := editor.Track{ID: trackID, Name: "配音字幕", Kind: "caption"}
				commands = append(commands, editor.Command{Type: "add_track", Track: &track})
			}
			commands = append(commands, editor.Command{Type: "generate_bound_captions", TranscriptID: transcriptID, TrackID: trackID, CaptionLayout: layout})
		}
	}
	if err := ctx.Err(); err != nil {
		return Bootstrap{}, err
	}
	if len(commands) > 0 {
		if _, err := s.engine.Apply(editor.EditRequest{BaseRevision: project.Revision, IdempotencyKey: editor.NewID("import-speech-audio"), Label: "导入配音", Commands: commands}); err != nil {
			return Bootstrap{}, err
		}
	}
	return s.bootstrapLocked(), nil
}
