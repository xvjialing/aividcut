package speechjobs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"aividcut/internal/speech"
)

func (m *Manager) viewLocked(e *entry) (Job, error) {
	job := Job{Summary: cloneJSON(e.record.Summary), Input: cloneJSON(e.record.Input), Parts: append([]PartState{}, e.record.Parts...)}
	if e.record.Input.Mode == Transcribe {
		for i := range e.record.Parts {
			result, err := m.readPart(e, i)
			if err == nil {
				job.PartResults = append(job.PartResults, result)
			}
		}
		if e.record.Summary.Status == Succeeded {
			var result speech.Transcription
			if err := readJSON(m.output(e.record.Summary.ID, "transcription.json"), &result, 64<<20); err != nil {
				return Job{}, errors.New("完整转写文件缺失或损坏，已完成块证据仍保留")
			}
			if result.ID != e.record.Summary.ID || result.AssetID != e.record.Summary.SourceAssetID || hashValue(result.SourceSHA256) != hashValue(e.record.Summary.SourceFingerprint) || result.SourceDurationUs != e.record.Summary.SourceDurationUs {
				return Job{}, errors.New("完整转写与原素材身份不一致")
			}
			job.Transcription = &result
		}
	}
	if e.record.Input.Mode == Synthesize {
		if e.audio != nil && e.audio.Timing != nil {
			job.Timing = new(speech.TimedAudioResult)
			*job.Timing = cloneJSON(*e.audio.Timing)
			applyTimingSummary(&job.Summary, job.Timing)
		} else if receipt, err := m.readAudio(context.Background(), e, true); err == nil {
			if receipt.Timing != nil {
				value := cloneJSON(*receipt.Timing)
				job.Timing = &value
			}
			applyTimingSummary(&job.Summary, receipt.Timing)
			if probe, probeErr := m.readAudioProbe(e, receipt); probeErr == nil {
				job.AudioProbe = probe
			} else if e.record.Input.Synthesize.WithTiming && e.record.Summary.Status == Succeeded {
				return Job{}, errors.New("带时间配音的探测证据缺失或不一致；可重新核查本地音频，不重发配音")
			}
		} else if e.record.Summary.Status == Succeeded {
			return Job{}, errors.New("配音文件或原始时间回执未通过校验；不会重新提交")
		}
	}
	return job, nil
}

func (m *Manager) load() error {
	files, err := os.ReadDir(m.journal)
	if err != nil {
		return errors.New("无法读取语音任务目录")
	}
	count := 0
	for _, file := range files {
		if file.IsDir() || !strings.HasSuffix(file.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(file.Name(), ".json")
		if !safeID.MatchString(id) {
			continue
		}
		count++
		if count > 2000 {
			return errors.New("语音任务历史超过当前读取上限")
		}
		e := &entry{partResults: map[int]speech.Transcription{}}
		err := readJSON(filepath.Join(m.journal, file.Name()), &e.record, 2<<20)
		r := &e.record
		if err != nil || r.Version != 1 || r.Summary.ID != id || r.Input.ID != id || r.Summary.Mode != r.Input.Mode || r.Summary.ConnectionID != r.Input.ConnectionID || r.Summary.ProjectID == "" || r.Summary.CreatedAt.IsZero() || r.Signature != recordSignature(*r) || !validRecordShape(*r) {
			now := time.Now().UTC()
			e.record = record{Version: 1, Summary: Summary{ID: id, Status: Unknown, Message: "语音任务记录损坏，请核对服务记录；相同ID不会重新提交", CreatedAt: now, UpdatedAt: now, Warnings: []string{}}}
			e.corrupt = true
			m.entries[id] = e
			continue
		}
		e.identity = r.Summary
		m.recoverLocalState(e)
		m.entries[id] = e
	}
	return nil
}

func validRecordShape(r record) bool {
	if r.Summary.ProviderID == "" || r.Summary.ModelID == "" || len(r.Parts) > 1024 {
		return false
	}
	switch r.Input.Mode {
	case Transcribe:
		if r.Input.Transcribe == nil || r.Input.Synthesize != nil || r.Input.Clone != nil || r.Input.Transcribe.ID != r.Input.ID || r.Input.Transcribe.ModelID != r.Summary.ModelID || len(r.Sources) != 1 || r.Summary.SourceAssetID != r.Sources[0].ID || r.Summary.SourceDurationUs != r.Sources[0].DurationUs || hashValue(r.Summary.SourceFingerprint) == "" || hashValue(r.Summary.SourceFingerprint) != hashValue(r.Sources[0].Fingerprint) {
			return false
		}
		if r.Preparation == nil {
			return len(r.Parts) == 0
		}
		if len(r.Parts) != len(r.Preparation.Parts) || r.Preparation.SourceAssetID != r.Sources[0].ID || r.Preparation.SourceDurationUs != r.Summary.SourceDurationUs || hashValue(r.Preparation.SourceFingerprint) != hashValue(r.Sources[0].Fingerprint) || r.Preparation.TimestampToleranceUs < 0 {
			return false
		}
		expected := r.Preparation.AudioOffsetUs
		if expected < 0 || r.Preparation.CoverageEndUs <= expected || r.Preparation.CoverageEndUs > r.Summary.SourceDurationUs {
			return false
		}
		for i, part := range r.Parts {
			prepared := r.Preparation.Parts[i]
			if !operationID.MatchString(part.ID) || !operationID.MatchString(part.RequestID) || part.ID != prepared.ID || part.OffsetUs != expected || part.OffsetUs != prepared.OffsetUs || part.DurationUs <= 0 || part.DurationUs > r.Summary.SourceDurationUs-expected || part.DurationUs != prepared.DurationUs || part.AudioDurationUs < part.DurationUs || part.AudioDurationUs-part.DurationUs > 63 || i < len(r.Parts)-1 && part.AudioDurationUs != part.DurationUs || part.AudioDurationUs != prepared.AudioDurationUs || hashValue(part.Fingerprint) == "" || hashValue(part.Fingerprint) != hashValue(prepared.Fingerprint) {
				return false
			}
			expected += part.DurationUs
		}
		return expected == r.Preparation.CoverageEndUs
	case Synthesize:
		return r.Input.Synthesize != nil && r.Input.Transcribe == nil && r.Input.Clone == nil && r.Input.Synthesize.ID == r.Input.ID && r.Input.Synthesize.ModelID == r.Summary.ModelID && len(r.Sources) == 0 && r.Summary.TimingRequested == r.Input.Synthesize.WithTiming
	case Clone:
		return r.Input.Clone != nil && r.Input.Transcribe == nil && r.Input.Synthesize == nil && r.Input.Clone.ID == r.Input.ID && r.Input.Clone.Consent.Confirmed && len(r.Sources) > 0
	}
	return false
}

func (m *Manager) recoverLocalState(e *entry) {
	r := &e.record
	s := &r.Summary
	if r.Input.Mode == Transcribe && len(r.Parts) > 0 {
		completed, unknown := 0, false
		for i := range r.Parts {
			if result, err := m.readPart(e, i); err == nil {
				r.Parts[i].Status, r.Parts[i].RemoteID = Succeeded, result.RemoteID
				completed++
			} else if r.Parts[i].Status == Submitting || r.Parts[i].Status == Running || r.Parts[i].Status == Unknown || r.Parts[i].Status == Succeeded {
				unknown = true
			}
		}
		s.CompletedParts, s.TotalParts = completed, len(r.Parts)
		if completed == len(r.Parts) {
			if s.Status == Succeeded && regularResult(m.output(s.ID, "transcription.json"), 64<<20) {
				s.CanResumeLocal = false
				return
			}
			s.Status, s.Message, s.CanResumeLocal = Saving, "原始块已在本机，可仅恢复合并保存，不会重发识别", true
			return
		}
		if unknown {
			s.Status, s.Message, s.CanResumeLocal = Unknown, "部分块提交结果未知，已完成证据保留；不会继续提交或自动重试", false
			return
		}
		if s.Status == Failed || s.Status == Cancelled {
			return
		}
		s.Status, s.Message, s.CanResumeLocal = Cancelled, "上次本地等待已中断，未继续提交后续块；已完成证据保留", false
		return
	}
	if r.Input.Mode == Synthesize {
		if receipt, err := m.readAudio(context.Background(), e, true); err == nil {
			applyTimingSummary(s, receipt.Timing)
			probeValid := true
			if r.Input.Synthesize.WithTiming {
				_, probeErr := m.readAudioProbe(e, receipt)
				probeValid = probeErr == nil
			}
			if s.Status == Succeeded && s.Audio != nil && s.Audio.Kind == "audio" && s.Audio.DurationUs > 0 && hashValue(s.Audio.Fingerprint) == receipt.SHA256 && s.Audio.Path == m.audioPath(s.ID) && probeValid {
				s.Audio.URL, s.Audio.ThumbnailURL = "", ""
				s.CanResumeLocal = false
				return
			}
			s.Status, s.Message, s.CanResumeLocal = Saving, "配音已原子保存在本机，可仅恢复真实媒体探测", true
			return
		}
		if r.Input.Synthesize.WithTiming {
			applyTimingSummary(s, nil)
			s.TimingReason = "配音或原始时间回执缺失、身份或指纹校验未通过"
		}
	}
	if r.Input.Mode == Clone {
		var receipt cloneReceipt
		if err := readJSON(m.output(s.ID, "voice.json"), &receipt, 2<<20); err == nil && receipt.JobID == s.ID && validOperation(e.identity, receipt.Result.Operation, s.ID, "") && operationID.MatchString(receipt.Result.Voice.ID) {
			if s.Status == Succeeded {
				receipt.Result.Voice.VerificationState, receipt.Result.Voice.PreviewURL = "unverified", ""
				s.Voice = &receipt.Result.Voice
				s.CanResumeLocal = false
				return
			}
			s.Status, s.Message, s.CanResumeLocal = Saving, "音色回执已保存在本机，可恢复元数据保存，验证仍未完成", true
			return
		}
	}
	if s.Status == Failed || s.Status == Cancelled || s.Status == Unknown {
		s.CanResumeLocal = false
		return
	}
	if r.Intent {
		s.Status, s.Message, s.CanResumeLocal = Unknown, "上次语音提交结果未知或同步内容未可靠落盘；不会重新提交", false
	} else {
		s.Status, s.Message, s.CanResumeLocal = Cancelled, "上次本地准备已结束，尚无收费提交意图", false
	}
}

func regularResult(path string, limit int64) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode().IsRegular() && info.Size() > 0 && info.Size() <= limit
}
