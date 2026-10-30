package application

import (
	"aividcut/internal/media"
	"aividcut/internal/resourceprofile"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

type StartResourceProfileInput struct {
	ProjectScope
	Phase string `json:"phase"`
}
type ExportResourceProfileInput struct {
	ID        string `json:"id"`
	Path      string `json:"path"`
	Overwrite bool   `json:"overwrite"`
}

func (s *Service) resourceContext() resourceprofile.ProjectContext {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, rev, settings, clips, assets, tracks := s.engine.Shape()
	scope := s.projectScopeLocked()
	return resourceprofile.ProjectContext{ProjectID: id, Revision: rev, SourceEpoch: scope.SourceEpoch, Width: settings.Width, Height: settings.Height, FPSNum: settings.FPSNum, FPSDen: settings.FPSDen, Clips: clips, Assets: assets, Tracks: tracks}
}
func (s *Service) GetResourceState(ctx context.Context) (resourceprofile.State, error) {
	return s.resources.State(ctx, true)
}
func (s *Service) StartResourceProfile(input StartResourceProfileInput) (resourceprofile.Summary, error) {
	s.mu.Lock()
	err := s.checkProjectScopeLocked(input.ProjectScope)
	s.mu.Unlock()
	if err != nil {
		return resourceprofile.Summary{}, err
	}
	return s.resources.Start(input.Phase)
}
func (s *Service) StopResourceProfile(id string) (resourceprofile.Summary, error) {
	return s.resources.Stop(id)
}
func (s *Service) ExportResourceProfile(ctx context.Context, input ExportResourceProfileInput) (string, error) {
	s.resourceExportMu.Lock()
	defer s.resourceExportMu.Unlock()
	if strings.TrimSpace(input.Path) == "" || !strings.EqualFold(filepath.Ext(input.Path), ".json") {
		return "", errors.New("诊断报告需要本机.json文件路径")
	}
	report, err := s.resources.Report(input.ID)
	if err != nil {
		return "", err
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return "", err
	}
	if len(data) > 32<<20 {
		return "", errors.New("诊断报告超过32MiB导出上限")
	}
	path, err := filepath.Abs(input.Path)
	if err != nil {
		return "", err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return "", err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".aividcut-resource-*.json")
	if err != nil {
		return "", err
	}
	temp := file.Name()
	defer os.Remove(temp)
	if _, err = file.Write(append(data, '\n')); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return "", err
	}
	if closeErr != nil {
		return "", closeErr
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.engine.Snapshot()
	if outputMatchesSource(path, s.projectPath) {
		return "", errors.New("诊断报告不能覆盖工程文件")
	}
	for _, a := range p.Assets {
		if outputMatchesSource(path, a.Path) {
			return "", errors.New("诊断报告不能覆盖工程素材")
		}
	}
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() {
			return "", errors.New("诊断输出不能为目录、符号链接或特殊文件")
		}
		if !input.Overwrite {
			return "", errors.New("同名报告已存在，请换名称或明确允许替换")
		}
	} else if !os.IsNotExist(err) {
		return "", err
	}
	if err = media.CommitOutput(ctx, temp, path, input.Overwrite); err != nil {
		return "", err
	}
	return path, nil
}

func (s *Service) GetResourceStatus(ctx context.Context) (resourceprofile.State, error) {
	return s.resources.State(ctx, false)
}
