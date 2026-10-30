package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"aividcut/internal/editor"
	"aividcut/internal/speech"
)

type AcceptTranscriptOptions struct {
	TrackID          string `json:"trackId"`
	GenerateCaptions bool   `json:"generateCaptions"`
}
type CorrectTranscriptInput struct {
	ProjectID          string              `json:"projectId"`
	TranscriptID       string              `json:"transcriptId"`
	TranscriptRevision int64               `json:"transcriptRevision"`
	Words              []editor.SourceWord `json:"words"`
}
type DetachCaptionInput struct {
	ProjectID string `json:"projectId"`
	ClipID    string `json:"clipId"`
}
type ImportSpeechAudioOptions struct {
	TrackID          string                `json:"trackId"`
	StartFrame       int64                 `json:"startFrame"`
	AddToTimeline    bool                  `json:"addToTimeline"`
	GenerateCaptions bool                  `json:"generateCaptions"`
	CaptionTrackID   string                `json:"captionTrackId"`
	CaptionLayout    *editor.CaptionLayout `json:"captionLayout,omitempty"`
	AlignmentSource  string                `json:"alignmentSource,omitempty"`
}

func speechTranscriptID(jobID string) string { return jobID + "-transcript" }

// 从已核查的持久任务转写接纳，不开放给前端任意传入的“ASR成功”对象。
func (s *Service) acceptSpeechResult(ctx context.Context, jobID, projectID, assetID, fingerprint string, durationUs int64, result speech.Transcription, options AcceptTranscriptOptions) (Bootstrap, error) {
	if err := ctx.Err(); err != nil {
		return Bootstrap{}, err
	}
	if result.Status != speech.Succeeded || result.SubmissionUncertain || !result.TimingComplete || result.AssetID != assetID || result.SourceDurationUs != durationUs || strings.TrimPrefix(result.SourceSHA256, "sha256:") != strings.TrimPrefix(fingerprint, "sha256:") {
		return Bootstrap{}, errors.New("转写缺少完整且一致的源词时间，原始结果保留供核对，不能伪造字幕")
	}
	words := []editor.SourceWord{}
	for i, word := range result.Words {
		if word.Type != "word" {
			continue
		}
		if !word.TimingComplete || word.StartUs == nil || word.EndUs == nil {
			return Bootstrap{}, errors.New("转写有缺失时间的普通词，请先核对原始结果")
		}
		words = append(words, editor.SourceWord{ID: fmt.Sprintf("%s-word-%d", jobID, i+1), StartUs: *word.StartUs, EndUs: *word.EndUs, Text: word.Text, SpeakerID: word.SpeakerID})
	}
	if len(words) == 0 {
		return Bootstrap{}, errors.New("转写没有可接纳的普通词，未生成空字幕")
	}
	if err := editor.ValidateSourceWords(words, durationUs); err != nil {
		return Bootstrap{}, err
	}
	s.mu.Lock()
	project := s.engine.Snapshot()
	if project.ID != projectID {
		s.mu.Unlock()
		return Bootstrap{}, errors.New("请先打开转写对应的原工程")
	}
	index := assetIndex(project, assetID)
	if index < 0 || project.Assets[index].Fingerprint != fingerprint || project.Assets[index].DurationUs != durationUs || project.Assets[index].Missing {
		s.mu.Unlock()
		return Bootstrap{}, errors.New("源素材已经变化或缺失，请重新核对转写来源")
	}
	engine, source := s.engine, project.Assets[index]
	s.mu.Unlock()
	if err := verifySourceFingerprint(ctx, source.Path, fingerprint); err != nil {
		return Bootstrap{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	project = s.engine.Snapshot()
	index = assetIndex(project, assetID)
	if s.engine != engine || project.ID != projectID || index < 0 || project.Assets[index].Fingerprint != fingerprint || project.Assets[index].DurationUs != durationUs || project.Assets[index].Path != source.Path {
		return Bootstrap{}, errors.New("核对源文件期间工程或素材已变化，请重新接纳")
	}
	transcriptID := speechTranscriptID(jobID)
	commands := []editor.Command{}
	var existing *editor.Transcript
	for i := range project.Transcripts {
		if project.Transcripts[i].ID == transcriptID {
			existing = &project.Transcripts[i]
			break
		}
	}
	if existing == nil {
		transcript := editor.Transcript{ID: transcriptID, AssetID: assetID, SourceFingerprint: fingerprint, SourceDurationUs: durationUs, Language: result.Language, RawWords: words, Origin: "asr"}
		commands = append(commands, editor.Command{Type: "accept_transcript", Transcript: &transcript})
	} else if existing.Stale || existing.AssetID != assetID || existing.SourceFingerprint != fingerprint {
		return Bootstrap{}, errors.New("同标识转写已关联不同来源，未覆盖工程")
	}
	if options.GenerateCaptions {
		trackID := options.TrackID
		if existing != nil && existing.CaptionTrackID != "" && trackID == "" {
			trackID = existing.CaptionTrackID
		}
		if trackID == "" {
			trackID = editor.NewID("caption-track")
			track := editor.Track{ID: trackID, Name: "识别字幕", Kind: "caption"}
			commands = append(commands, editor.Command{Type: "add_track", Track: &track})
		}
		if existing == nil || existing.CaptionTrackID != trackID {
			commands = append(commands, editor.Command{Type: "generate_bound_captions", TranscriptID: transcriptID, TrackID: trackID})
		}
	}
	if err := ctx.Err(); err != nil {
		return Bootstrap{}, err
	}
	if len(commands) > 0 {
		if _, err := s.engine.Apply(editor.EditRequest{BaseRevision: project.Revision, IdempotencyKey: editor.NewID("accept-speech"), Label: "接纳源转写与字幕", Commands: commands}); err != nil {
			return Bootstrap{}, err
		}
	}
	return s.bootstrapLocked(), nil
}

type checkedSourceReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r checkedSourceReader) Read(data []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(data)
}

// 登记指纹之外还核对实际文件内容；流式读取，不将长视频全部放进内存。
func verifySourceFingerprint(ctx context.Context, path, fingerprint string) error {
	expected := strings.TrimPrefix(fingerprint, "sha256:")
	if decoded, err := hex.DecodeString(expected); err != nil || len(decoded) != sha256.Size {
		return errors.New("素材缺少可核查的SHA-256指纹")
	}
	f, err := os.Open(path)
	if err != nil {
		return errors.New("源素材无法读取，请重新定位后再试")
	}
	defer f.Close()
	before, err := f.Stat()
	if err != nil || !before.Mode().IsRegular() {
		return errors.New("源素材不是有效本地文件")
	}
	digest := sha256.New()
	if _, err := io.CopyBuffer(digest, checkedSourceReader{ctx: ctx, reader: f}, make([]byte, 64<<10)); err != nil {
		return err
	}
	after, err := f.Stat()
	current, pathErr := os.Stat(path)
	if err != nil || pathErr != nil || !os.SameFile(before, after) || !os.SameFile(before, current) || before.Size() != after.Size() || before.Size() != current.Size() || !before.ModTime().Equal(after.ModTime()) || !before.ModTime().Equal(current.ModTime()) || hex.EncodeToString(digest.Sum(nil)) != strings.ToLower(expected) {
		return errors.New("源文件内容已经变化，旧语音结果未应用，请重新核对来源")
	}
	return ctx.Err()
}

func (s *Service) CorrectSourceTranscript(ctx context.Context, input CorrectTranscriptInput) (Bootstrap, error) {
	if err := ctx.Err(); err != nil {
		return Bootstrap{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	project := s.engine.Snapshot()
	words := append([]editor.SourceWord(nil), input.Words...)
	if project.ID != input.ProjectID {
		return Bootstrap{}, errors.New("当前工程已变化，请重新读取源转写后修改")
	}
	if _, err := s.engine.Apply(editor.EditRequest{BaseRevision: project.Revision, IdempotencyKey: editor.NewID("correct-source"), Label: "修订源转写", Commands: []editor.Command{{Type: "correct_transcript", TranscriptID: input.TranscriptID, TranscriptRevision: input.TranscriptRevision, Words: words}}}); err != nil {
		return Bootstrap{}, err
	}
	return s.bootstrapLocked(), nil
}

func (s *Service) DetachCaption(ctx context.Context, input DetachCaptionInput) (Bootstrap, error) {
	if err := ctx.Err(); err != nil {
		return Bootstrap{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	project := s.engine.Snapshot()
	if project.ID != input.ProjectID {
		return Bootstrap{}, errors.New("当前工程已变化，请重新选择字幕")
	}
	if _, err := s.engine.Apply(editor.EditRequest{BaseRevision: project.Revision, IdempotencyKey: editor.NewID("detach-caption"), Label: "解除字幕来源绑定", Commands: []editor.Command{{Type: "detach_caption", ClipID: input.ClipID}}}); err != nil {
		return Bootstrap{}, err
	}
	return s.bootstrapLocked(), nil
}
