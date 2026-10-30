package speechjobs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"aividcut/internal/editor"
	"aividcut/internal/speech"
)

func (m *Manager) run(ctx context.Context, e *entry, binding Binding) error {
	m.mu.Lock()
	err := m.stateLocked(e, Preparing, "正在进行本地语音准备，尚未提交收费请求", false)
	m.mu.Unlock()
	if err != nil {
		return m.beforeFailure(e, ctx, "本地任务状态无法安全保存，未提交语音请求")
	}
	if err := ctx.Err(); err != nil {
		return m.beforeFailure(e, ctx, "本地准备已取消")
	}
	if err := m.ensureJobDirectory(e.record.Summary.ID); err != nil {
		return m.beforeFailure(e, ctx, "语音任务输出根目录不可用，未提交请求")
	}
	if e.record.Input.Mode == Transcribe {
		return m.runTranscribe(ctx, e, binding)
	}
	files, err := registeredSources(e.record.Sources)
	if err != nil {
		return m.beforeFailure(e, ctx, "已授权音色样本无法读取，未提交请求")
	}
	provider, err := m.options.Factory(binding.Secrets, files)
	if err != nil {
		return m.beforeFailure(e, ctx, "语音适配器无法创建，未提交请求")
	}
	if e.record.Input.Mode == Synthesize {
		var timedProvider speech.TimedTTSProvider
		if e.record.Input.Synthesize.WithTiming {
			var ok bool
			timedProvider, ok = provider.(speech.TimedTTSProvider)
			if !ok || !provider.Capabilities().SpeechTiming.Supported {
				return m.beforeFailure(e, ctx, "本次适配器不支持带时间配音，未提交且未回退普通端点")
			}
		}
		if err := m.intent(e, -1); err != nil {
			return err
		}
		if ctx.Err() != nil {
			return m.cancelBeforeCall(e, -1)
		}
		var result speech.AudioResult
		var timing *speech.TimedAudioResult
		if timedProvider != nil {
			value, callErr := timedProvider.SynthesizeWithTiming(ctx, *e.record.Input.Synthesize)
			result, err = value.AudioResult, callErr
			// 音频字节由Result单独持有，时间证据只保存元数据，避免多份大响应。
			value.Data = nil
			_, value.TimingComplete, value.TimingReason = speech.AssessTiming(value)
			timing = &value
		} else {
			result, err = provider.Synthesize(ctx, *e.record.Input.Synthesize)
		}
		if err != nil || !validOperation(e.identity, result.Operation, e.record.Input.ID, e.identity.ModelID) || result.VoiceID != e.record.Input.Synthesize.VoiceID || result.MIME != "audio/mpeg" || result.OutputFormat != "mp3_44100_128" || !validMP3(result.Data) {
			return m.callFailure(e, -1, result.Operation, err)
		}
		m.mu.Lock()
		e.audio = &speechAudio{Result: result, Timing: timing}
		applyTimingSummary(&e.record.Summary, timing)
		if timing != nil {
			e.record.TimingSHA256 = timingFingerprint(timing)
			err = m.saveLocked(e)
		}
		m.mu.Unlock()
		if err != nil {
			return m.pendingSave(e, "配音已返回，时间证据摘要尚未保存；只恢复本地保存，不会重发")
		}
		return m.finishLocal(ctx, e)
	}
	if err := m.intent(e, -1); err != nil {
		return err
	}
	if ctx.Err() != nil {
		return m.cancelBeforeCall(e, -1)
	}
	result, err := provider.CloneVoice(ctx, *e.record.Input.Clone)
	if err != nil || !validOperation(e.identity, result.Operation, e.record.Input.ID, "") || !operationID.MatchString(result.Voice.ID) {
		return m.callFailure(e, -1, result.Operation, err)
	}
	result.Voice.VerificationState, result.Voice.PreviewURL = "unverified", ""
	m.mu.Lock()
	e.voice = &result
	m.mu.Unlock()
	return m.finishLocal(ctx, e)
}

func registeredSources(assets []editor.Asset) (speech.MediaSource, error) {
	files := make([]speech.RegisteredFile, 0, len(assets))
	for _, asset := range assets {
		files = append(files, speech.RegisteredFile{ID: asset.ID, Path: asset.Path, Kind: asset.Kind, DurationUs: asset.DurationUs, ExpectedSHA256: hashValue(asset.Fingerprint)})
	}
	return speech.NewFileSource(files)
}

func (m *Manager) beforeFailure(e *entry, ctx context.Context, message string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	status := Failed
	if ctx.Err() != nil {
		status = Cancelled
		message = "已取消本地准备，尚未发起新的收费请求"
	}
	_ = m.stateLocked(e, status, message, false)
	return errors.New("语音本地工作未完成")
}

func (m *Manager) intent(e *entry, index int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if index >= 0 {
		e.record.Parts[index].Status, e.record.Parts[index].Message = Submitting, "提交意图已记录，尚待服务回执"
	}
	e.record.Intent = true
	if err := m.stateLocked(e, Submitting, "即将提交语音服务，请勿重复操作", false); err != nil {
		if index >= 0 {
			e.record.Parts[index].Status = Failed
		}
		e.record.Summary.Status, e.record.Summary.Message, e.record.Summary.CanResumeLocal = Failed, "提交意图无法持久化，未发起此请求；已有块证据仍保留", false
		return err
	}
	// 持久意图已在提交前完成；运行态仅用于当前读模型，不依赖百分比推测网络进度。
	e.record.Summary.Status, e.record.Summary.Message = Running, "语音服务处理中；可停止本地等待"
	return nil
}

func (m *Manager) cancelBeforeCall(e *entry, index int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if index >= 0 {
		e.record.Parts[index].Status, e.record.Parts[index].Message = Cancelled, "此块尚未调用服务"
	}
	_ = m.stateLocked(e, Cancelled, "本地等待已停止，未继续提交；已完成块证据保留", false)
	return context.Canceled
}

func validOperation(job Summary, operation speech.Operation, id, model string) bool {
	return operation.ID == id && operation.ProviderID == job.ProviderID && operation.ModelID == model && operation.Status == speech.Succeeded && !operation.SubmissionUncertain && (operation.RemoteID == "" || operationID.MatchString(operation.RemoteID))
}

func (m *Manager) callFailure(e *entry, index int, operation speech.Operation, err error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	status, message := Unknown, "提交结果未知，请核对服务记录；不会自动重发，也不会继续后续块"
	var known *speech.Error
	expectedID, expectedModel := e.record.Input.ID, e.identity.ModelID
	if index >= 0 {
		expectedID = e.record.Parts[index].RequestID
	}
	if e.record.Input.Mode == Clone {
		expectedModel = ""
	}
	if errors.As(err, &known) && !known.SubmissionUncertain && operation.Status == speech.Failed && !operation.SubmissionUncertain && operation.ID == expectedID && operation.ProviderID == e.identity.ProviderID && operation.ModelID == expectedModel {
		status, message = Failed, "语音服务明确拒绝或本地请求验证未通过；未自动重试，已完成证据保留"
		if e.record.Summary.Status == Cancelling {
			status, message = Cancelled, "已停止本地等待，当前调用明确未成功；已有块证据保留"
		}
	}
	if index >= 0 {
		e.record.Parts[index].Status, e.record.Parts[index].Message = status, message
		if operationID.MatchString(operation.RemoteID) {
			e.record.Parts[index].RemoteID = operation.RemoteID
		}
	}
	_ = m.stateLocked(e, status, message, false)
	return errors.New("语音请求没有得到可确认成功的结果")
}

func (m *Manager) runTranscribe(ctx context.Context, e *entry, binding Binding) error {
	prepDir := m.output(e.record.Summary.ID, "prepared")
	prepared, err := m.options.Prepare(ctx, e.record.Sources[0], prepDir)
	if err != nil {
		return m.beforeFailure(e, ctx, "本地音轨提取或分块失败，未提交识别请求")
	}
	if err := validatePreparation(ctx, e.record.Sources[0], prepDir, prepared); err != nil {
		return m.beforeFailure(e, ctx, "准备音轨的源指纹、覆盖区间或分块文件校验失败，未提交请求")
	}
	if len(prepared.Warnings) == 0 && (prepared.AudioOffsetUs > 0 || prepared.CoverageEndUs < prepared.SourceDurationUs) {
		prepared.Warnings = []string{"识别仅覆盖实际存在的音轨区间，没有为源视频的无音轨部分填充静音"}
	}
	m.mu.Lock()
	e.record.Preparation = &prepared
	e.record.Summary.TotalParts = len(prepared.Parts)
	e.record.Summary.Warnings = append([]string{}, prepared.Warnings...)
	e.record.Parts = make([]PartState, len(prepared.Parts))
	for i, part := range prepared.Parts {
		e.record.Parts[i] = PartState{ID: part.ID, RequestID: fmt.Sprintf("%s-p%04d", e.record.Summary.ID, i+1), Fingerprint: part.Fingerprint, OffsetUs: part.OffsetUs, DurationUs: part.DurationUs, AudioDurationUs: part.AudioDurationUs, Status: Queued, Message: "本地块已核对，尚未提交"}
	}
	err = m.saveLocked(e)
	m.mu.Unlock()
	if err != nil {
		return m.beforeFailure(e, ctx, "分块计划无法持久化，未提交识别请求")
	}
	for i, part := range prepared.Parts {
		if ctx.Err() != nil {
			return m.cancelBeforeCall(e, i)
		}
		files, err := speech.NewFileSource([]speech.RegisteredFile{{ID: part.ID, Path: part.Path, Kind: "audio", DurationUs: part.AudioDurationUs, ExpectedSHA256: hashValue(part.Fingerprint)}})
		if err != nil {
			return m.beforeFailure(e, ctx, "识别块读取校验失败，未继续提交")
		}
		provider, err := m.options.Factory(binding.Secrets, files)
		if err != nil {
			return m.beforeFailure(e, ctx, "识别适配器未能创建，未继续提交")
		}
		if err := m.intent(e, i); err != nil {
			return err
		}
		if ctx.Err() != nil {
			return m.cancelBeforeCall(e, i)
		}
		request := *e.record.Input.Transcribe
		request.ID, request.AssetID = e.record.Parts[i].RequestID, part.ID
		result, err := provider.Transcribe(ctx, request)
		if err != nil || !validOperation(e.identity, result.Operation, request.ID, e.identity.ModelID) || result.Granularity != request.Granularity || !validPartResult(result, part) {
			return m.callFailure(e, i, result.Operation, err)
		}
		m.mu.Lock()
		e.partResults[i] = result
		m.mu.Unlock()
		if err := m.savePart(e, i, result); err != nil {
			return m.pendingSave(e, "已收到识别块，但本地保存未完成；可以仅恢复本地保存")
		}
		m.mu.Lock()
		delete(e.partResults, i)
		e.record.Parts[i].Status, e.record.Parts[i].Message, e.record.Parts[i].RemoteID = Succeeded, "原始块响应已原子保存", result.RemoteID
		e.record.Summary.CompletedParts = i + 1
		err = m.stateLocked(e, Saving, "识别块证据已保存", false)
		m.mu.Unlock()
		if err != nil {
			return m.pendingSave(e, "识别块已保存，任务状态待恢复；不会继续提交后续块")
		}
	}
	return m.finishLocal(ctx, e)
}

func validatePreparation(ctx context.Context, source editor.Asset, dir string, prepared Preparation) error {
	if prepared.SourceAssetID != source.ID || hashValue(prepared.SourceFingerprint) == "" || hashValue(prepared.SourceFingerprint) != hashValue(source.Fingerprint) || prepared.SourceDurationUs != source.DurationUs || prepared.AudioOffsetUs < 0 || prepared.CoverageEndUs <= prepared.AudioOffsetUs || prepared.CoverageEndUs > source.DurationUs || len(prepared.Parts) < 1 || len(prepared.Parts) > 1024 {
		return errors.New("音轨准备身份或真实覆盖范围不一致")
	}
	expected, ids := prepared.AudioOffsetUs, map[string]bool{}
	for i, part := range prepared.Parts {
		if !operationID.MatchString(part.ID) || ids[part.ID] || part.DurationUs <= 0 || part.OffsetUs != expected || part.OffsetUs > math.MaxInt64-part.DurationUs || part.AudioDurationUs < part.DurationUs || part.AudioDurationUs-part.DurationUs > 63 || i < len(prepared.Parts)-1 && part.AudioDurationUs != part.DurationUs || !within(dir, part.Path) || hashValue(part.Fingerprint) == "" {
			return errors.New("准备分块必须在受限目录中连续、无重叠，且仅末块允许采样量化差")
		}
		resolved, err := filepath.EvalSymlinks(part.Path)
		if err != nil || !within(dir, resolved) {
			return errors.New("准备分块越出受限目录")
		}
		hash, _, err := fileHash(ctx, part.Path, speech.MaxInputBytes)
		if err != nil || hash != hashValue(part.Fingerprint) {
			return errors.New("准备分块指纹不匹配")
		}
		ids[part.ID] = true
		expected = part.OffsetUs + part.DurationUs
	}
	if expected != prepared.CoverageEndUs {
		return errors.New("准备分块没有精确覆盖真实音轨区间")
	}
	return nil
}

func validPartResult(result speech.Transcription, part Part) bool {
	if result.AssetID != part.ID || hashValue(result.SourceSHA256) != hashValue(part.Fingerprint) || result.SourceDurationUs != part.AudioDurationUs || !utf8.ValidString(result.Text) {
		return false
	}
	if result.LanguageProbability != nil && (math.IsNaN(*result.LanguageProbability) || math.IsInf(*result.LanguageProbability, 0)) {
		return false
	}
	validTime := func(value speech.TimedText) bool {
		return utf8.ValidString(value.Text) && (value.StartUs == nil || *value.StartUs >= 0 && *value.StartUs <= part.AudioDurationUs) && (value.EndUs == nil || *value.EndUs >= 0 && *value.EndUs <= part.AudioDurationUs) && (value.StartUs == nil || value.EndUs == nil || *value.EndUs >= *value.StartUs)
	}
	for _, word := range result.Words {
		if word.LogProbability != nil && (math.IsNaN(*word.LogProbability) || math.IsInf(*word.LogProbability, 0)) {
			return false
		}
		if !validTime(word.TimedText) || word.TimingComplete && (word.StartUs == nil || word.EndUs == nil || *word.EndUs <= *word.StartUs) {
			return false
		}
		for _, char := range word.Characters {
			if !validTime(char) {
				return false
			}
		}
	}
	return true
}

func (m *Manager) savePart(e *entry, index int, result speech.Transcription) error {
	part := e.record.Parts[index]
	return m.writeJSON(m.partPath(e.record.Summary.ID, index), partReceipt{JobID: e.record.Summary.ID, PartID: part.ID, RequestID: part.RequestID, Fingerprint: part.Fingerprint, Result: result}, 16<<20)
}

func (m *Manager) readPart(e *entry, index int) (speech.Transcription, error) {
	if result, ok := e.partResults[index]; ok {
		return cloneJSON(result), nil
	}
	var receipt partReceipt
	if err := readJSON(m.partPath(e.record.Summary.ID, index), &receipt, 16<<20); err != nil {
		return speech.Transcription{}, err
	}
	part := e.record.Parts[index]
	if e.record.Preparation == nil || index >= len(e.record.Preparation.Parts) || receipt.JobID != e.record.Summary.ID || receipt.PartID != part.ID || receipt.RequestID != part.RequestID || hashValue(receipt.Fingerprint) != hashValue(part.Fingerprint) || !validOperation(e.identity, receipt.Result.Operation, part.RequestID, e.identity.ModelID) || receipt.Result.Granularity != e.record.Input.Transcribe.Granularity || !validPartResult(receipt.Result, e.record.Preparation.Parts[index]) {
		return speech.Transcription{}, errors.New("已保存识别块与原请求身份不一致")
	}
	return receipt.Result, nil
}

func (m *Manager) pendingSave(e *entry, message string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	_ = m.stateLocked(e, Saving, message, true)
	return errors.New("已取得语音结果，仅本地保存待恢复")
}

func (m *Manager) finishLocal(ctx context.Context, e *entry) error {
	if err := m.ensureJobDirectory(e.record.Summary.ID); err != nil {
		return m.pendingSave(e, "受控语音结果目录无法访问；已有结果仍保留，不会重新提交")
	}
	switch e.record.Input.Mode {
	case Transcribe:
		return m.finishTranscription(e)
	case Synthesize:
		return m.finishAudio(ctx, e)
	case Clone:
		return m.finishVoice(e)
	}
	return errors.New("未知语音模式")
}

func (m *Manager) finishTranscription(e *entry) error {
	m.mu.Lock()
	volatile := make(map[int]speech.Transcription, len(e.partResults))
	for index, result := range e.partResults {
		volatile[index] = cloneJSON(result)
	}
	m.mu.Unlock()
	for index, result := range volatile {
		if err := m.savePart(e, index, result); err != nil {
			return m.pendingSave(e, "识别块本地保存仍未完成，可继续恢复已有结果")
		}
		m.mu.Lock()
		delete(e.partResults, index)
		m.mu.Unlock()
	}
	parts := []speech.Transcription{}
	for i := range e.record.Parts {
		m.mu.Lock()
		result, err := m.readPart(e, i)
		m.mu.Unlock()
		if err != nil {
			m.mu.Lock()
			e.record.Summary.CompletedParts = len(parts)
			_ = m.stateLocked(e, Failed, "已取得块已保存；原任务仍有未取得结果的块，恢复不会自动重发", false)
			m.mu.Unlock()
			return errors.New("没有全部识别块结果，不能冒充完整转写")
		}
		m.mu.Lock()
		e.record.Parts[i].Status, e.record.Parts[i].Message, e.record.Parts[i].RemoteID = Succeeded, "原始块证据已保存", result.RemoteID
		m.mu.Unlock()
		parts = append(parts, result)
	}
	if len(parts) == 0 {
		return m.pendingSave(e, "没有可保存的本地转写结果")
	}
	m.mu.Lock()
	snapshot := cloneJSON(e.record)
	m.mu.Unlock()
	merged := mergeTranscription(snapshot, parts)
	if err := m.writeJSON(m.output(e.record.Summary.ID, "transcription.json"), merged, 64<<20); err != nil {
		return m.pendingSave(e, "全部块已取得，合并文件保存待恢复；不会重发请求")
	}
	m.mu.Lock()
	for i := range e.record.Parts {
		e.record.Parts[i].Status, e.record.Parts[i].Message = Succeeded, "原始块证据已保存"
	}
	e.record.Summary.CompletedParts = len(parts)
	e.record.Summary.Warnings = append([]string{}, merged.Warnings...)
	if err := m.stateLocked(e, Succeeded, "转写原始证据已保存；尚未接纳为工程字幕", false); err != nil {
		e.record.Summary.Status, e.record.Summary.CanResumeLocal = Saving, true
		m.mu.Unlock()
		return err
	}
	m.mu.Unlock()
	if err := m.cleanupPrepared(e.record.Summary.ID); err != nil {
		m.mu.Lock()
		e.record.Summary.Warnings = append(e.record.Summary.Warnings, "转写已成功保存，但准备音频清理未完成；可保留证据后手动整理任务数据目录")
		_ = m.saveLocked(e)
		m.mu.Unlock()
	}
	return nil
}

func (m *Manager) ensureJobDirectory(id string) error {
	dir := m.output(id, "")
	if err := os.Mkdir(dir, 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return errors.New("无法创建受控语音任务目录")
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("语音输出根目录不是安全目录")
	}
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil || resolved != dir {
		return errors.New("语音输出根目录链接发生变化")
	}
	return nil
}

func (m *Manager) cleanupPrepared(id string) error {
	// 只删除由任务ID推导出的固定子目录，绝不读取持久化Parts.Path来决定删除位置。
	dir := m.output(id, "prepared")
	info, err := os.Lstat(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("准备目录不是安全目录")
	}
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil || resolved != dir || !within(m.output(id, ""), resolved) {
		return errors.New("准备目录越出任务范围")
	}
	return m.removePrepared(dir)
}

func mergeTranscription(record record, parts []speech.Transcription) speech.Transcription {
	result := speech.Transcription{Operation: speech.Operation{ID: record.Summary.ID, ProviderID: record.Summary.ProviderID, ModelID: record.Summary.ModelID, Status: speech.Succeeded}, AssetID: record.Summary.SourceAssetID, SourceSHA256: hashValue(record.Summary.SourceFingerprint), SourceDurationUs: record.Summary.SourceDurationUs, Granularity: record.Input.Transcribe.Granularity, TimingComplete: true, Words: []speech.Word{}, Warnings: append([]string{}, record.Preparation.Warnings...)}
	texts := []string{}
	clipped, speakers := false, false
	for i, part := range parts {
		segment := record.Preparation.Parts[i]
		if i == 0 {
			result.Language = part.Language
		} else if result.Language != part.Language {
			result.Warnings = append(result.Warnings, "分块返回了不同语言，未推测统一语言概率")
		}
		if !part.TimingComplete {
			result.TimingComplete = false
		}
		texts = append(texts, part.Text)
		for _, warning := range part.Warnings {
			result.Warnings = append(result.Warnings, segment.ID+"："+warning)
		}
		shift := func(value speech.TimedText) speech.TimedText {
			if value.StartUs != nil {
				stamp := *value.StartUs
				if stamp > segment.DurationUs {
					stamp = segment.DurationUs
					clipped = true
				}
				stamp += segment.OffsetUs
				value.StartUs = &stamp
			}
			if value.EndUs != nil {
				stamp := *value.EndUs
				if stamp > segment.DurationUs {
					stamp = segment.DurationUs
					clipped = true
				}
				stamp += segment.OffsetUs
				value.EndUs = &stamp
			}
			return value
		}
		for _, raw := range part.Words {
			word := cloneJSON(raw)
			word.TimedText = shift(word.TimedText)
			if word.StartUs == nil || word.EndUs == nil || *word.EndUs <= *word.StartUs {
				word.TimingComplete = false
				if word.Type == "word" {
					result.TimingComplete = false
				}
			}
			for j := range word.Characters {
				word.Characters[j] = shift(word.Characters[j])
			}
			if len(parts) > 1 && word.SpeakerID != "" {
				word.SpeakerID = fmt.Sprintf("part-%d:%s", i+1, word.SpeakerID)
				speakers = true
			}
			result.Words = append(result.Words, word)
		}
	}
	result.Text = strings.Join(texts, "\n")
	if clipped {
		result.Warnings = append(result.Warnings, "末块采样量化超出逻辑覆盖的时间已裁切；原始块响应保持不变")
	}
	if speakers {
		result.Warnings = append(result.Warnings, "跨块说话人标识添加块前缀，不代表已完成跨块说话人匹配")
	}
	return result
}

func validMP3(data []byte) bool {
	return len(data) > 3 && int64(len(data)) <= speech.MaxResponseBytes && (string(data[:3]) == "ID3" || data[0] == 0xff && data[1]&0xe0 == 0xe0 && data[1]&0x06 != 0 && data[1]&0x18 != 0x08)
}

func (m *Manager) audioPath(id string) string {
	return m.output(id, filepath.Join("audio", "result.mp3"))
}

func (m *Manager) writeAudio(e *entry, pending speechAudio) error {
	result := pending.Result
	jobID := e.record.Summary.ID
	if _, err := os.Lstat(m.output(jobID, "audio")); err == nil {
		receipt, err := m.readAudio(context.Background(), e, true)
		sum := sha256.Sum256(result.Data)
		if err != nil || receipt.SHA256 != hex.EncodeToString(sum[:]) || !sameTiming(receipt.Timing, pending.Timing) {
			return errors.New("同任务已有不同或损坏音频，不覆盖")
		}
		return nil
	}
	if err := os.MkdirAll(m.output(jobID, ""), 0700); err != nil {
		return errors.New("无法创建配音结果目录")
	}
	dir, err := os.MkdirTemp(m.output(jobID, ""), ".audio-*")
	if err != nil {
		return errors.New("无法创建私有配音暂存")
	}
	defer os.RemoveAll(dir)
	sum := sha256.Sum256(result.Data)
	receipt := audioReceipt{JobID: jobID, Operation: result.Operation, VoiceID: result.VoiceID, MIME: result.MIME, OutputFormat: result.OutputFormat, Bytes: int64(len(result.Data)), SHA256: hex.EncodeToString(sum[:]), Timing: pending.Timing, TimingSHA256: timingFingerprint(pending.Timing)}
	if err := m.writeFile(filepath.Join(dir, "result.mp3"), result.Data); err != nil {
		return errors.New("配音字节保存失败")
	}
	if err := m.writeJSON(filepath.Join(dir, "receipt.json"), receipt, 16<<20); err != nil {
		return err
	}
	if err := os.Rename(dir, m.output(jobID, "audio")); err != nil {
		return errors.New("发布配音结果失败，未覆盖原文件")
	}
	return nil
}

func (m *Manager) readAudio(ctx context.Context, e *entry, verify bool) (audioReceipt, error) {
	var receipt audioReceipt
	jobID := e.record.Summary.ID
	if err := readJSON(m.output(jobID, filepath.Join("audio", "receipt.json")), &receipt, 16<<20); err != nil {
		return receipt, err
	}
	if receipt.JobID != jobID || !validOperation(e.identity, receipt.Operation, jobID, e.identity.ModelID) || receipt.VoiceID != e.record.Input.Synthesize.VoiceID || receipt.MIME != "audio/mpeg" || receipt.OutputFormat != "mp3_44100_128" || receipt.Bytes < 1 || receipt.Bytes > speech.MaxResponseBytes || hashValue(receipt.SHA256) == "" {
		return receipt, errors.New("配音回执与原请求身份不一致")
	}
	if err := validateTimingReceipt(e, receipt); err != nil {
		return receipt, err
	}
	path := m.audioPath(jobID)
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || resolved != path {
		return receipt, errors.New("配音文件链接已变化")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() != receipt.Bytes {
		return receipt, errors.New("配音文件缺失或大小变化")
	}
	if verify {
		hash, size, err := fileHash(ctx, path, speech.MaxResponseBytes)
		if err != nil || size != receipt.Bytes || hash != receipt.SHA256 {
			return receipt, errors.New("配音文件指纹不匹配")
		}
	}
	return receipt, nil
}

func (m *Manager) finishAudio(ctx context.Context, e *entry) error {
	m.mu.Lock()
	pending := copyAudio(e.audio)
	m.mu.Unlock()
	if pending != nil {
		if err := m.writeAudio(e, *pending); err != nil {
			return m.pendingSave(e, "配音已返回，但字节或回执尚未保存，可恢复本地保存")
		}
		m.mu.Lock()
		clear(e.audio.Result.Data)
		e.audio = nil
		m.mu.Unlock()
	}
	receipt, err := m.readAudio(ctx, e, true)
	if err != nil {
		return m.pendingSave(e, "本地配音回执/文件尚未通过验证，不会重新生成")
	}
	if m.options.Probe == nil && m.options.ProbeAudio == nil {
		return m.pendingSave(e, "配音已落盘，等待配置真实媒体探测")
	}
	probe, err := m.probeAudio(ctx, e)
	asset := probe.Asset
	if err != nil || asset.Kind != "audio" || asset.DurationUs <= 0 || hashValue(asset.Fingerprint) != receipt.SHA256 {
		return m.pendingSave(e, "配音已落盘，真实时长或指纹探测未通过；可仅重试本地探测")
	}
	asset.ID, asset.Name, asset.Path = e.record.Summary.ID+"-audio", e.record.Summary.ID+".mp3", m.audioPath(e.record.Summary.ID)
	asset.URL, asset.ThumbnailURL, asset.Missing = "", "", false
	probe.Asset = asset
	if err := m.writeJSON(m.output(e.record.Summary.ID, "audio-probe.json"), probe, 2<<20); err != nil {
		return m.pendingSave(e, "音频已核对，但原始探测证据尚未保存；可仅恢复本地保存")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	e.record.Summary.Audio = &asset
	applyTimingSummary(&e.record.Summary, receipt.Timing)
	if err := m.stateLocked(e, Succeeded, "配音已保存并核对真实媒体时长；尚未加入工程", false); err != nil {
		e.record.Summary.Status, e.record.Summary.CanResumeLocal = Saving, true
		return err
	}
	return nil
}

func (m *Manager) finishVoice(e *entry) error {
	m.mu.Lock()
	var pending *speech.CloneResult
	if e.voice != nil {
		copy := cloneJSON(*e.voice)
		pending = &copy
	}
	m.mu.Unlock()
	if pending != nil {
		if err := m.writeJSON(m.output(e.record.Summary.ID, "voice.json"), cloneReceipt{JobID: e.record.Summary.ID, Result: *pending}, 2<<20); err != nil {
			return m.pendingSave(e, "音色服务已返回，尚待保存本地回执；不会重新克隆")
		}
		m.mu.Lock()
		e.voice = nil
		m.mu.Unlock()
	}
	var receipt cloneReceipt
	if err := readJSON(m.output(e.record.Summary.ID, "voice.json"), &receipt, 2<<20); err != nil || receipt.JobID != e.record.Summary.ID || !validOperation(e.identity, receipt.Result.Operation, e.record.Summary.ID, "") || !operationID.MatchString(receipt.Result.Voice.ID) {
		return m.pendingSave(e, "音色回执需要核对，不会重新克隆")
	}
	receipt.Result.Voice.VerificationState, receipt.Result.Voice.PreviewURL = "unverified", ""
	m.mu.Lock()
	defer m.mu.Unlock()
	e.record.Summary.Voice = &receipt.Result.Voice
	if err := m.stateLocked(e, Succeeded, "音色ID已保存；用户声明保留，身份/音色验证仍未完成", false); err != nil {
		e.record.Summary.Status, e.record.Summary.CanResumeLocal = Saving, true
		return err
	}
	return nil
}
