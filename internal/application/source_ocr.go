package application

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"
	"unicode/utf8"

	"aividcut/internal/editor"
	"aividcut/internal/ocr"
)

type SourceOCRInput struct {
	ThumbnailRequest
	ProjectRevision int64 `json:"projectRevision"`
}
type SourceOCRDraft struct {
	ID              string      `json:"id"`
	ProjectID       string      `json:"projectId"`
	ProjectRevision int64       `json:"projectRevision"`
	SourceEpoch     uint64      `json:"sourceEpoch"`
	Frame           SourceFrame `json:"frame"`
	Result          ocr.Result  `json:"result"`
}
type AcceptSourceOCRInput struct {
	ID              string   `json:"id"`
	ProjectID       string   `json:"projectId"`
	ProjectRevision int64    `json:"projectRevision"`
	SourceEpoch     uint64   `json:"sourceEpoch"`
	Lines           []string `json:"lines"`
}
type sourceOCRRecord struct {
	draft         SourceOCRDraft
	asset         editor.Asset
	created       time.Time
	status        string
	acceptedLines []string
}
type sourceOCRState struct {
	closed    bool
	activeID  string
	cancel    context.CancelFunc
	done      chan struct{}
	records   map[string]*sourceOCRRecord
	cancelled map[string]time.Time
}

func (s *Service) GetSourceOCRCapability() ocr.Capability {
	if s.ocrRecognizer != nil {
		return ocr.Capability{Available: true, Engine: "apple-vision", Reason: "测试识别器"}
	}
	capability, _ := ocr.Discover()
	return capability
}
func (s *Service) resetSourceOCR(closed bool) chan struct{} {
	s.ocrMu.Lock()
	defer s.ocrMu.Unlock()
	if s.ocrState == nil {
		s.ocrState = &sourceOCRState{}
	}
	state := s.ocrState
	state.closed = state.closed || closed
	if state.cancel != nil {
		state.cancel()
	}
	state.records = map[string]*sourceOCRRecord{}
	return state.done
}
func (s *Service) closeSourceOCR() {
	if done := s.resetSourceOCR(true); done != nil {
		select {
		case <-done:
		case <-time.After(3 * time.Second):
		}
	}
}
func (s *Service) CancelSourceOCR(id string) {
	if !captureIDPattern.MatchString(id) {
		return
	}
	s.ocrMu.Lock()
	defer s.ocrMu.Unlock()
	if s.ocrState == nil {
		s.ocrState = &sourceOCRState{}
	}
	state := s.ocrState
	if state.activeID == id && state.cancel != nil {
		state.cancel()
	}
	delete(state.records, id)
	if state.cancelled == nil {
		state.cancelled = map[string]time.Time{}
	}
	for key, at := range state.cancelled {
		if time.Since(at) > time.Minute {
			delete(state.cancelled, key)
		}
	}
	if len(state.cancelled) >= 32 {
		for key := range state.cancelled {
			delete(state.cancelled, key)
			break
		}
	}
	state.cancelled[id] = time.Now()
}
func (s *Service) PrepareSourceOCR(parent context.Context, input SourceOCRInput) (SourceOCRDraft, error) {
	return s.prepareSourceOCR(parent, input, true)
}

// retain仅用于人工核对；MCP读取不注册预览凭据或占用待核对候选名额。
func (s *Service) prepareSourceOCR(parent context.Context, input SourceOCRInput, retain bool) (SourceOCRDraft, error) {
	if !captureIDPattern.MatchString(input.RequestID) {
		return SourceOCRDraft{}, errors.New("识别操作标识无效")
	}
	capability, worker := ocr.Discover()
	recognizer := s.ocrRecognizer
	if recognizer == nil {
		if !capability.Available {
			return SourceOCRDraft{}, errors.New(capability.Reason)
		}
		recognizer = worker
	}
	ctx, cancel := context.WithTimeout(parent, 50*time.Second)
	defer cancel()
	s.ocrMu.Lock()
	if s.ocrState == nil {
		s.ocrState = &sourceOCRState{}
	}
	state := s.ocrState
	if state.closed || state.activeID != "" {
		s.ocrMu.Unlock()
		return SourceOCRDraft{}, errors.New("文字识别已关闭或仍有任务运行，请先取消")
	}
	if at, ok := state.cancelled[input.RequestID]; ok && time.Since(at) < time.Minute {
		s.ocrMu.Unlock()
		return SourceOCRDraft{}, context.Canceled
	}
	if state.records == nil {
		state.records = map[string]*sourceOCRRecord{}
	}
	for key, r := range state.records {
		if time.Since(r.created) > 10*time.Minute {
			delete(state.records, key)
		}
	}
	pending := 0
	for _, record := range state.records {
		if record.status != "accepted" {
			pending++
		}
	}
	for len(state.records) >= 32 {
		oldestKey := ""
		var oldest time.Time
		for key, record := range state.records {
			if record.status == "accepted" && (oldestKey == "" || record.created.Before(oldest)) {
				oldestKey = key
				oldest = record.created
			}
		}
		if oldestKey == "" {
			break
		}
		delete(state.records, oldestKey)
	}
	if retain && pending >= 8 {
		s.ocrMu.Unlock()
		return SourceOCRDraft{}, errors.New("待核对识别结果已达8份，请先放弃旧结果")
	}
	if _, ok := state.records[input.RequestID]; ok {
		s.ocrMu.Unlock()
		return SourceOCRDraft{}, errors.New("识别标识已使用")
	}
	state.activeID = input.RequestID
	state.cancel = cancel
	state.done = make(chan struct{})
	done := state.done
	s.ocrMu.Unlock()
	defer func() {
		s.ocrMu.Lock()
		state.activeID = ""
		state.cancel = nil
		close(done)
		state.done = nil
		s.ocrMu.Unlock()
	}()
	s.mu.Lock()
	p := s.engine.Snapshot()
	s.mu.Unlock()
	index := assetIndex(p, input.AssetID)
	if p.ID != input.ProjectID || p.Revision != input.ProjectRevision || index < 0 {
		return SourceOCRDraft{}, errors.New("识别所属工程或版本已变化")
	}
	asset := p.Assets[index]
	frame, err := s.readSourceFrame(ctx, input.ThumbnailRequest, "detail", retain, nil, true)
	if err != nil {
		return SourceOCRDraft{}, err
	}
	result, err := recognizer.Recognize(ctx, frame.JPEG)
	if err != nil {
		return SourceOCRDraft{}, err
	}
	if err = ocr.Validate(result); err != nil {
		return SourceOCRDraft{}, err
	}
	// 模型运行期间源文件也可能被替换，再次核对实际字节及当前工程。
	if _, err = s.verifyFrameSource(ctx, asset, input.SourceEpoch); err != nil {
		return SourceOCRDraft{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current := s.engine.Snapshot()
	s.thumbMu.Lock()
	epoch := s.thumbEpoch
	s.thumbMu.Unlock()
	idx := assetIndex(current, asset.ID)
	if ctx.Err() != nil || current.ID != input.ProjectID || current.Revision != input.ProjectRevision || epoch != input.SourceEpoch || idx < 0 || current.Assets[idx].Path != asset.Path || current.Assets[idx].Fingerprint != asset.Fingerprint {
		return SourceOCRDraft{}, errors.New("识别期间工程或源素材已变化，请重新读取")
	}
	draft := SourceOCRDraft{ID: input.RequestID, ProjectID: p.ID, ProjectRevision: p.Revision, SourceEpoch: epoch, Frame: SourceFrame{Evidence: frame.Evidence, URL: frame.URL}, Result: result}
	s.ocrMu.Lock()
	defer s.ocrMu.Unlock()
	if state.closed || ctx.Err() != nil {
		return SourceOCRDraft{}, context.Canceled
	}
	if retain {
		state.records[input.RequestID] = &sourceOCRRecord{draft: draft, asset: asset, created: time.Now(), status: "prepared"}
	}
	return draft, nil
}
func (s *Service) AcceptSourceOCR(ctx context.Context, input AcceptSourceOCRInput) (Bootstrap, error) {
	input.Lines = append([]string{}, input.Lines...)
	if !captureIDPattern.MatchString(input.ID) || len(input.Lines) < 1 || len(input.Lines) > 50 {
		return Bootstrap{}, errors.New("请提供1–50条识别文字")
	}
	for _, line := range input.Lines {
		if !utf8.ValidString(line) || strings.TrimSpace(line) == "" || utf8.RuneCountInString(line) > 1000 || strings.ContainsRune(line, 0) {
			return Bootstrap{}, errors.New("每条识别文字须为1–1000字")
		}
	}

	for i := range input.Lines {
		input.Lines[i] = strings.TrimSpace(input.Lines[i])
	}
	s.ocrMu.Lock()
	state := s.ocrState
	var record *sourceOCRRecord
	if state != nil {
		record = state.records[input.ID]
	}
	if record == nil || state.closed || time.Since(record.created) > 10*time.Minute {
		s.ocrMu.Unlock()
		return Bootstrap{}, errors.New("识别结果已失效，请重新识别")
	}
	draft, asset, status := record.draft, record.asset, record.status
	if status == "accepted" && !reflect.DeepEqual(record.acceptedLines, input.Lines) {
		s.ocrMu.Unlock()
		return Bootstrap{}, errors.New("该识别已接纳其他文字，请重新识别以保存修改")
	}
	if len(draft.Result.Lines) == 0 {
		s.ocrMu.Unlock()
		return Bootstrap{}, errors.New("当前取样没有识别文字，不能接纳空结果")
	}
	if status == "submitting" {
		s.ocrMu.Unlock()
		return Bootstrap{}, errors.New("正在接纳这份识别结果")
	}
	if input.ProjectID != draft.ProjectID || input.SourceEpoch != draft.SourceEpoch || input.ProjectRevision != draft.ProjectRevision {
		s.ocrMu.Unlock()
		return Bootstrap{}, errors.New("识别核对的工程上下文不匹配")
	}
	record.status = "submitting"
	s.ocrMu.Unlock()
	success := false
	defer func() {
		s.ocrMu.Lock()
		defer s.ocrMu.Unlock()
		if success {
			record.status = "accepted"
			record.acceptedLines = append([]string{}, input.Lines...)
		} else {
			record.status = status
		}
	}()
	proof, err := s.verifyFrameSource(ctx, asset, input.SourceEpoch)
	if err != nil {
		return Bootstrap{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.engine.Snapshot()
	s.thumbMu.Lock()
	epoch := s.thumbEpoch
	s.thumbMu.Unlock()
	index := assetIndex(p, asset.ID)
	if p.ID != input.ProjectID || epoch != input.SourceEpoch || index < 0 || p.Assets[index].Path != asset.Path || p.Assets[index].Fingerprint != asset.Fingerprint {
		return Bootstrap{}, errors.New("识别源工程或素材已变化")
	}
	s.ocrMu.Lock()
	valid := !state.closed && state.records[input.ID] == record
	s.ocrMu.Unlock()
	if !valid {
		return Bootstrap{}, errors.New("识别结果已放弃")
	}
	if status == "accepted" {
		success = true
		return s.bootstrapLocked(), nil
	}
	if p.Revision != input.ProjectRevision {
		return Bootstrap{}, errors.New("核对后工程已变化，请重新识别")
	}
	if err = checkFrameProof(asset.Path, proof); err != nil {
		return Bootstrap{}, err
	}
	if err = ctx.Err(); err != nil {
		return Bootstrap{}, err
	}
	producer, method, err := ocr.SourceIdentity(draft.Result.Engine)
	if err != nil {
		return Bootstrap{}, err
	}
	value := editor.VisualAnalysis{ID: input.ID, AssetID: asset.ID, SourceKind: asset.Kind, SourceFingerprint: asset.Fingerprint, SourceDurationUs: asset.DurationUs, Producer: producer, MethodVersion: method, Coverage: "sampled_frames", Frames: []editor.SourceFrameEvidence{draft.Frame.Evidence}}
	for i, line := range input.Lines {
		value.Notes = append(value.Notes, editor.VisualNote{ID: fmt.Sprintf("%s-line-%d", input.ID, i+1), Kind: "observation", Text: strings.TrimSpace(line), Tags: []string{"画面文字", "OCR"}, EvidenceIDs: []string{draft.Frame.Evidence.ID}})
	}
	_, err = s.engine.Apply(editor.EditRequest{BaseRevision: p.Revision, IdempotencyKey: "accept-ocr-" + input.ID, Label: "接纳画面文字识别", Commands: []editor.Command{{Type: "accept_visual_analysis", VisualAnalysis: &value}}})
	if err != nil {
		return Bootstrap{}, err
	}
	success = true
	return s.bootstrapLocked(), nil
}
