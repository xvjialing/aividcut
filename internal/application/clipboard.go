package application

import (
	"aividcut/internal/editor"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

type ClipboardStatus struct {
	SourceEpoch      uint64 `json:"sourceEpoch"`
	ProjectID        string `json:"projectId"`
	Version          uint64 `json:"version"`
	Available        bool   `json:"available"`
	ID               string `json:"id,omitempty"`
	ClipCount        int    `json:"clipCount"`
	DetachedCaptions int    `json:"detachedCaptions"`
	Reason           string `json:"reason,omitempty"`
}
type ClipboardSelectionInput struct {
	ProjectID     string   `json:"projectId"`
	SourceEpoch   uint64   `json:"sourceEpoch"`
	BaseRevision  int64    `json:"baseRevision"`
	ClipIDs       []string `json:"clipIds"`
	IncludeLinked *bool    `json:"includeLinked,omitempty"`
}
type ClipboardCutInput struct {
	ClipboardSelectionInput
	IdempotencyKey string `json:"idempotencyKey"`
}
type ClipboardPasteInput struct {
	ProjectID      string `json:"projectId"`
	SourceEpoch    uint64 `json:"sourceEpoch"`
	BaseRevision   int64  `json:"baseRevision"`
	ClipboardID    string `json:"clipboardId"`
	IdempotencyKey string `json:"idempotencyKey"`
	AtFrame        int64  `json:"atFrame"`
	TrackDelta     int    `json:"trackDelta,omitempty"`
}
type ClipboardResult struct {
	Bootstrap       Bootstrap       `json:"bootstrap"`
	Clipboard       ClipboardStatus `json:"clipboard"`
	SelectedClipIDs []string        `json:"selectedClipIds,omitempty"`
}

func (s *Service) clipboardStatusLocked(projectID string) ClipboardStatus {
	status := ClipboardStatus{SourceEpoch: s.clipboardEpochLocked(), ProjectID: projectID, Version: s.clipboardVersion, Reason: "先在时间线复制或剪切片段"}
	if s.clipboard == nil {
		return status
	}
	info := s.clipboard.Info()
	if info.ProjectID != projectID {
		status.Reason = "剪贴板属于其它工程，请返回对应工程或重新复制"
		return status
	}
	sequenceID, _ := s.engine.SequenceIdentity()
	if info.SequenceID != sequenceID {
		status.Reason = "剪贴板来自其它序列，请返回来源序列或重新复制"
		return status
	}
	status.Available, status.ID, status.ClipCount, status.DetachedCaptions, status.Reason = true, info.ID, info.ClipCount, info.DetachedCaptions, ""
	return status
}

// 轮询只读身份及摘要，不构造整个工程快照。
func (s *Service) GetClipboardStatus() ClipboardStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, _ := s.engine.Identity()
	return s.clipboardStatusLocked(id)
}
func (s *Service) clipboardEpochLocked() uint64 {
	s.thumbMu.Lock()
	defer s.thumbMu.Unlock()
	return s.thumbEpoch
}
func (s *Service) checkClipboardScopeLocked(projectID string, epoch uint64) error {
	id, _ := s.engine.Identity()
	if projectID != id || epoch != s.clipboardEpochLocked() {
		return errors.New("工程已切换或重开，请核对当前剪贴板操作")
	}
	return nil
}
func (s *Service) publishClipboardLocked(c *editor.TimelineClipboard) {
	s.clipboard = c
	s.clipboardVersion++
}
func (s *Service) CopyTimelineSelection(input ClipboardSelectionInput) (ClipboardResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.copyClipboardLocked(s.engine.Snapshot(), input)
}
func (s *Service) copyClipboardLocked(p editor.Project, input ClipboardSelectionInput) (ClipboardResult, error) {
	if err := s.checkClipboardScopeLocked(input.ProjectID, input.SourceEpoch); err != nil {
		return ClipboardResult{}, err
	}
	if input.BaseRevision != p.Revision {
		return ClipboardResult{}, errors.New("复制前工程版本已变化，请重新核对")
	}
	snapshot, err := editor.CaptureTimelineClipboard(p, input.ClipIDs, input.IncludeLinked)
	if err != nil {
		return ClipboardResult{}, err
	}
	s.publishClipboardLocked(snapshot)
	return ClipboardResult{Bootstrap: s.bootstrapLocked(), Clipboard: s.clipboardStatusLocked(p.ID)}, nil
}
func (s *Service) CutTimelineSelection(input ClipboardCutInput) (ClipboardResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cutClipboardLocked(s.engine.Snapshot(), input)
}
func (s *Service) cutClipboardLocked(p editor.Project, input ClipboardCutInput) (ClipboardResult, error) {
	if err := s.checkClipboardScopeLocked(input.ProjectID, input.SourceEpoch); err != nil {
		return ClipboardResult{}, err
	}
	if strings.TrimSpace(input.IdempotencyKey) == "" || len(input.IdempotencyKey) > 256 {
		return ClipboardResult{}, errors.New("剪切需要有效幂等标识")
	}
	encoded, err := json.Marshal(input)
	if err != nil {
		return ClipboardResult{}, err
	}
	hash := sha256.Sum256(encoded)
	key := fmt.Sprintf("%d/%s", input.SourceEpoch, input.IdempotencyKey)
	if prior, ok := s.clipboardCuts[key]; ok {
		if prior != hash {
			return ClipboardResult{}, errors.New("相同剪切标识不能用于不同请求")
		}
		return ClipboardResult{Bootstrap: s.bootstrapLocked(), Clipboard: s.clipboardStatusLocked(p.ID)}, nil
	}
	if p.Revision != input.BaseRevision {
		return ClipboardResult{}, errors.New("剪切前工程版本已变化，请重新核对")
	}
	snapshot, err := editor.CaptureTimelineClipboard(p, input.ClipIDs, input.IncludeLinked)
	if err != nil {
		return ClipboardResult{}, err
	}
	_, err = s.engine.Apply(editor.EditRequest{BaseRevision: input.BaseRevision, IdempotencyKey: input.IdempotencyKey, Label: "剪切片段", Commands: []editor.Command{{Type: "delete_clips", ClipIDs: input.ClipIDs, IncludeLinked: input.IncludeLinked}}})
	if err != nil {
		return ClipboardResult{}, err
	}
	s.publishClipboardLocked(snapshot)
	if s.clipboardCuts == nil {
		s.clipboardCuts = map[string][32]byte{}
	}
	s.clipboardCuts[key] = hash
	s.clipboardCutOrder = append(s.clipboardCutOrder, key)
	if len(s.clipboardCutOrder) > 128 {
		delete(s.clipboardCuts, s.clipboardCutOrder[0])
		s.clipboardCutOrder = s.clipboardCutOrder[1:]
	}
	return ClipboardResult{Bootstrap: s.bootstrapLocked(), Clipboard: s.clipboardStatusLocked(p.ID)}, nil
}
func (s *Service) PasteTimelineSelection(input ClipboardPasteInput) (ClipboardResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pasteClipboardLocked(input)
}
func (s *Service) pasteClipboardLocked(input ClipboardPasteInput) (ClipboardResult, error) {
	if err := s.checkClipboardScopeLocked(input.ProjectID, input.SourceEpoch); err != nil {
		return ClipboardResult{}, err
	}
	var snapshot *editor.TimelineClipboard
	if s.clipboard != nil && s.clipboard.Info().ID == input.ClipboardID {
		snapshot = s.clipboard
	}
	p, err := s.engine.Apply(editor.EditRequest{BaseRevision: input.BaseRevision, IdempotencyKey: input.IdempotencyKey, Label: "粘贴片段", Commands: []editor.Command{editor.ClipboardPasteCommand(snapshot, input.ClipboardID, input.AtFrame, input.TrackDelta)}})
	if err != nil {
		return ClipboardResult{}, err
	}
	return ClipboardResult{Bootstrap: s.bootstrapLocked(), Clipboard: s.clipboardStatusLocked(p.ID), SelectedClipIDs: editor.ClipboardPasteSelection(p, input.ClipboardID, input.IdempotencyKey)}, nil
}
