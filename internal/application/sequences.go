package application

import (
	"aividcut/internal/editor"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
)

type SequenceChangeInput struct {
	ProjectScope
	Action         string           `json:"action"`
	SequenceID     string           `json:"sequenceId"`
	Name           string           `json:"name,omitempty"`
	Settings       *editor.Settings `json:"settings,omitempty"`
	IdempotencyKey string           `json:"idempotencyKey"`
	ConfirmDelete  bool             `json:"confirmDelete,omitempty"`
}

func (s *Service) sequenceContextChangedLocked(before string) {
	current, _ := s.engine.SequenceIdentity()
	if current == before {
		return
	}
	s.sequenceGeneration++
	s.resetThumbnails(false)
	s.resetWaveforms(false)
	s.files.Reset()
	s.urls = map[string]mediaURL{}
	s.playbackEvidence.Store(nil)
}
func (s *Service) ChangeSequence(input SequenceChangeInput) (Bootstrap, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkProjectScopeLocked(input.ProjectScope); err != nil {
		return Bootstrap{}, err
	}
	before, _ := s.engine.SequenceIdentity()
	if input.Action == "switch" {
		if input.Name != "" || input.Settings != nil || input.ConfirmDelete {
			return Bootstrap{}, errors.New("切换序列不能携带编辑参数")
		}
		if _, err := s.engine.ActivateSequence(input.SequenceID); err != nil {
			return Bootstrap{}, err
		}
		s.sequenceContextChangedLocked(before)
		return s.bootstrapLocked(), nil
	}
	if strings.TrimSpace(input.IdempotencyKey) == "" || len(input.IdempotencyKey) > 128 {
		return Bootstrap{}, errors.New("序列编辑需要有效的幂等标识")
	}
	c := editor.Command{Name: strings.TrimSpace(input.Name), Settings: input.Settings, SequenceID: input.SequenceID}
	label := ""
	switch input.Action {
	case "create", "duplicate":
		if input.ConfirmDelete {
			return Bootstrap{}, errors.New("新建或复制不接受删除确认")
		}
		h := sha256.Sum256([]byte(input.ProjectID + "/" + input.IdempotencyKey))
		c.SequenceID = "sequence-" + hex.EncodeToString(h[:12])
		if input.Action == "create" {
			if input.SequenceID != "" {
				return Bootstrap{}, errors.New("空序列不接受来源序列")
			}
			c.Type = "create_sequence"
			label = "新建序列"
		} else {
			c.Type = "duplicate_sequence"
			c.SourceSequenceID = input.SequenceID
			label = "复制序列"
		}
	case "rename":
		if input.ConfirmDelete || input.Settings != nil {
			return Bootstrap{}, errors.New("重命名仅修改序列名称")
		}
		c.Type = "rename_sequence"
		label = "重命名序列"
	case "delete":
		if !input.ConfirmDelete || input.Name != "" || input.Settings != nil {
			return Bootstrap{}, errors.New("删除需要明确核对目标序列")
		}
		c.Type = "delete_sequence"
		label = "删除序列"
	default:
		return Bootstrap{}, errors.New("未知序列管理操作")
	}
	if _, err := s.engine.Apply(editor.EditRequest{BaseRevision: input.ProjectRevision, IdempotencyKey: "sequence/" + input.IdempotencyKey, Label: label, Commands: []editor.Command{c}}); err != nil {
		return Bootstrap{}, err
	}
	s.sequenceContextChangedLocked(before)
	return s.bootstrapLocked(), nil
}
