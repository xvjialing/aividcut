package application

import (
	"errors"
	"math"
	"strings"

	"aividcut/internal/editor"
	"aividcut/internal/projectversions"
)

type ProjectVersionScope = ProjectScope

type CreateProjectVersionInput struct {
	ProjectVersionScope
	Name string `json:"name"`
}

type ListProjectVersionsInput struct {
	ProjectVersionScope
	Offset int `json:"offset"`
	Limit  int `json:"limit"`
}

type RestoreProjectVersionInput struct {
	ProjectVersionScope
	ID     string `json:"id"`
	SHA256 string `json:"sha256"`
}

type RestoreProjectVersionResult struct {
	Bootstrap       Bootstrap               `json:"bootstrap"`
	PreviousVersion projectversions.Summary `json:"previousVersion"`
}

func (s *Service) ConfigureProjectVersions(directory string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.versionsClosed {
		return errors.New("工程版本服务已关闭")
	}
	if s.versions != nil {
		return errors.New("工程版本目录已经配置")
	}
	store, err := projectversions.Open(directory)
	if err != nil {
		return err
	}
	s.versions = store
	return nil
}

func (s *Service) checkVersionScopeLocked(input ProjectVersionScope) error {
	if s.versions == nil || s.versionsClosed {
		return errors.New("工程版本服务未配置或已关闭")
	}
	return s.checkProjectScopeLocked(input)
}

func (s *Service) ListProjectVersions(input ListProjectVersionsInput) (projectversions.Page, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkVersionScopeLocked(input.ProjectVersionScope); err != nil {
		return projectversions.Page{}, err
	}
	return s.versions.List(input.ProjectID, input.Offset, input.Limit)
}

func (s *Service) CreateProjectVersion(input CreateProjectVersionInput) (projectversions.Summary, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkVersionScopeLocked(input.ProjectVersionScope); err != nil {
		return projectversions.Summary{}, err
	}
	return s.versions.Create(s.engine.Snapshot(), input.Name, false)
}

func (s *Service) RestoreProjectVersion(input RestoreProjectVersionInput) (RestoreProjectVersionResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkVersionScopeLocked(input.ProjectVersionScope); err != nil {
		return RestoreProjectVersionResult{}, err
	}
	current := s.engine.Snapshot()
	project, warnings, err := s.versions.Load(current.ID, input.ID, input.SHA256)
	if err != nil {
		return RestoreProjectVersionResult{}, err
	}
	if current.Revision == math.MaxInt64 {
		return RestoreProjectVersionResult{}, errors.New("工程修订已达到支持上限")
	}
	project.Revision = current.Revision + 1
	engine, err := editor.NewEngine(project)
	if err != nil {
		return RestoreProjectVersionResult{}, err
	}
	name := []rune(strings.TrimSpace(current.Name))
	if len(name) > 60 {
		name = name[:60]
	}
	previous, err := s.versions.Create(current, "恢复前 · "+string(name), true)
	if err != nil {
		return RestoreProjectVersionResult{}, errors.New("无法保存恢复前版本，当前工程未切换：" + err.Error())
	}
	// 更换引擎和源实例，撤销从恢复点重新开始，旧MCP/预览不能继续编辑新状态。
	_ = s.retireRecoveryLocked()
	s.engine = engine
	s.clipboardCuts, s.clipboardCutOrder = nil, nil
	s.resetWaveforms(false)
	s.resetThumbnails(false)
	s.urls = map[string]mediaURL{}
	s.files.Reset()
	result := s.bootstrapLocked()
	result.Warnings = append(result.Warnings, warnings...)
	result.Warnings = append(result.Warnings, "已恢复工程版本；恢复前状态已保留，撤销历史从此处重新开始。原工程文件尚未覆盖。")
	return RestoreProjectVersionResult{Bootstrap: result, PreviousVersion: previous}, nil
}

func (s *Service) closeProjectVersions() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.versionsClosed = true
	if s.versions != nil {
		_ = s.versions.Close()
	}
}
