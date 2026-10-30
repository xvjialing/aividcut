package application

import (
	"aividcut/internal/editor"
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"
)

type NewProjectInput struct {
	ProjectID       string          `json:"projectId"`
	ProjectRevision int64           `json:"projectRevision"`
	SourceEpoch     uint64          `json:"sourceEpoch"`
	Name            string          `json:"name"`
	Settings        editor.Settings `json:"settings"`
}

// ValidateNewProjectSettings是当前建项范围，不扩大核心模型或媒体引擎的能力声明。
func ValidateNewProjectSettings(v editor.Settings) error {
	if err := editor.ValidateSettings(v); err != nil {
		return err
	}
	if v.Width < 16 || v.Height < 16 || v.Width > 8192 || v.Height > 8192 || v.Width%2 != 0 || v.Height%2 != 0 || int64(v.Width)*int64(v.Height) > 3840*2160 {
		return errors.New("新建尺寸需要16–8192的偶数，总像素不超过3840×2160")
	}
	if v.FPSNum > 240000 || v.FPSDen > 120000 || int64(v.FPSNum) < 2*int64(v.FPSDen) || int64(v.FPSNum) > 120*int64(v.FPSDen) {
		return errors.New("新建帧率需要2–120fps，分子最多240000、分母最多120000")
	}
	if v.SampleRate != 48000 {
		return errors.New("当前新建工程使用48000Hz音频采样率")
	}
	return nil
}
func (s *Service) NewProjectWithSettings(input NewProjectInput) (Bootstrap, error) {
	name := strings.TrimSpace(input.Name)
	if !utf8.ValidString(name) || name == "" || utf8.RuneCountInString(name) > 120 || strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return Bootstrap{}, errors.New("工程名称需要1–120个字符，不能包含控制字符")
	}
	if err := ValidateNewProjectSettings(input.Settings); err != nil {
		return Bootstrap{}, err
	}
	project, err := editor.NewProjectWithSettings(name, input.Settings)
	if err != nil {
		return Bootstrap{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.thumbMu.Lock()
	epoch := s.thumbEpoch
	s.thumbMu.Unlock()
	id, revision := s.engine.Identity()
	if input.ProjectID != id || input.ProjectRevision != revision || input.SourceEpoch != epoch {
		return Bootstrap{}, errors.New("新建窗口对应的原工程已变化，请关闭并重新核对未保存状态后新建")
	}
	return s.newProjectLocked(project)
}
