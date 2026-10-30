package application

import (
	"aividcut/internal/editor"
	"errors"
)

type ScopedEditInput struct {
	Scope   ProjectScope       `json:"scope"`
	Request editor.EditRequest `json:"request"`
}

// ApplyScopedEdit 用打开编辑窗口时的实例核对后再提交同一个工程核心。
func (s *Service) ApplyScopedEdit(input ScopedEditInput) (Bootstrap, error) {
	if err := validatePublicEdit(input.Request); err != nil {
		return Bootstrap{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkProjectScopeLocked(input.Scope); err != nil {
		return Bootstrap{}, err
	}
	if input.Request.BaseRevision != input.Scope.ProjectRevision {
		return Bootstrap{}, errors.New("编辑修订与窗口范围不一致，请关闭后重新打开")
	}
	if _, err := s.engine.Apply(input.Request); err != nil {
		return Bootstrap{}, err
	}
	return s.bootstrapLocked(), nil
}
