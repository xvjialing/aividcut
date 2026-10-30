package application

import (
	"aividcut/internal/jobs"
	"aividcut/internal/projectcollect"
	"context"
)

type CollectProjectInput struct {
	ProjectScope
	ParentDirectory string `json:"parentDirectory"`
	FolderName      string `json:"folderName"`
}

func (s *Service) InspectProjectCollection(ctx context.Context, input ProjectScope) (projectcollect.Summary, error) {
	s.mu.Lock()
	if err := s.checkProjectScopeLocked(input); err != nil {
		s.mu.Unlock()
		return projectcollect.Summary{}, err
	}
	project := s.engine.Snapshot()
	s.mu.Unlock()
	return projectcollect.Inspect(ctx, project)
}

func (s *Service) CollectProject(input CollectProjectInput) (jobs.Job, error) {
	// 排队后继续消费固定快照，不随用户后续编辑改变。
	s.mu.Lock()
	if err := s.checkProjectScopeLocked(input.ProjectScope); err != nil {
		s.mu.Unlock()
		return jobs.Job{}, err
	}
	project := s.engine.Snapshot()
	s.mu.Unlock()
	parent, name, err := projectcollect.Destination(input.ParentDirectory, input.FolderName)
	if err != nil {
		return jobs.Job{}, err
	}
	return s.jobs.Start("project-collect", "检查并收集工程 · "+project.Name, func(ctx context.Context, progress func(float64, string)) (string, error) {
		return projectcollect.Collect(ctx, project, parent, name, progress)
	})
}
