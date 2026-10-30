package application

import "errors"

// ProjectScope 约束只消费某次工程读模型的应用操作，不授予文件或MCP写权限。
type ProjectScope struct {
	ProjectID       string `json:"projectId"`
	ProjectRevision int64  `json:"projectRevision"`
	SourceEpoch     uint64 `json:"sourceEpoch"`
}

func (s *Service) projectScopeLocked() ProjectScope {
	s.thumbMu.Lock()
	epoch := s.thumbEpoch
	s.thumbMu.Unlock()
	id, revision := s.engine.Identity()
	return ProjectScope{ProjectID: id, ProjectRevision: revision, SourceEpoch: epoch}
}
func (s *Service) checkProjectScopeLocked(input ProjectScope) error {
	if s.projectScopeLocked() != input {
		return errors.New("工程或实例已变化，请重新打开操作窗口并核对")
	}
	return nil
}
