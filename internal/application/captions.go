package application

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"aividcut/internal/captions"
	"aividcut/internal/editor"
)

func (s *Service) ImportCaptions(path, trackID string) (Bootstrap, error) {
	s.mu.Lock()
	projectID := s.engine.Snapshot().ID
	s.mu.Unlock()
	if !strings.EqualFold(filepath.Ext(path), ".srt") {
		return Bootstrap{}, errors.New("当前字幕导入仅支持 .srt")
	}
	f, err := os.Open(path)
	if err != nil {
		return Bootstrap{}, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, (32<<20)+1))
	if err != nil {
		return Bootstrap{}, err
	}
	if len(data) > 32<<20 {
		return Bootstrap{}, errors.New("字幕文件超过 32 MiB")
	}
	cues, err := captions.ParseSRT(string(data))
	if err != nil {
		return Bootstrap{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.engine.Snapshot()
	if p.ID != projectID {
		return Bootstrap{}, errors.New("读取字幕期间工程已切换，请在目标工程重新导入")
	}
	commands, err := captions.ImportCommands(p, trackID, cues)
	if err != nil {
		return Bootstrap{}, err
	}
	if len(commands) == 0 {
		return Bootstrap{}, errors.New("字幕文件没有可导入的内容")
	}
	if _, err := s.engine.Apply(editor.EditRequest{BaseRevision: p.Revision, IdempotencyKey: editor.NewID("caption-import"), Label: "导入字幕", Commands: commands}); err != nil {
		return Bootstrap{}, err
	}
	return s.bootstrapLocked(), nil
}

func (s *Service) ExportCaptionsTo(path, trackID string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("请选择字幕输出位置")
	}
	if filepath.Ext(path) == "" {
		path += ".srt"
	}
	if !strings.EqualFold(filepath.Ext(path), ".srt") {
		return "", errors.New("字幕输出文件需要 .srt 后缀")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	p := s.engine.Snapshot()
	projectPath := s.projectPath
	s.mu.Unlock()
	if absolute == projectPath {
		return "", errors.New("字幕输出不能覆盖工程文件")
	}
	for _, asset := range p.Assets {
		if absolute == asset.Path {
			return "", errors.New("字幕输出不能覆盖源素材")
		}
	}
	content, err := captions.ExportProjectSRT(p, trackID)
	if err != nil {
		return "", err
	}
	if content == "" {
		return "", errors.New("没有可导出的字幕片段")
	}
	if err := os.MkdirAll(filepath.Dir(absolute), 0755); err != nil {
		return "", err
	}
	temporary, err := os.CreateTemp(filepath.Dir(absolute), ".aividcut-srt-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(temporary.Name())
	if _, err := temporary.WriteString(content); err != nil {
		temporary.Close()
		return "", err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return "", err
	}
	if err := temporary.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(temporary.Name(), absolute); err != nil {
		return "", err
	}
	return absolute, nil
}
