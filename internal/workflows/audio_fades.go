package workflows

import (
	"aividcut/internal/editor"
	"errors"
	"fmt"
	"reflect"
)

type AudioFadeSummary struct {
	Scope        string `json:"scope"`
	Mode         string `json:"mode"`
	Examined     int    `json:"examined"`
	Changed      int    `json:"changed"`
	Preserved    int    `json:"preserved"`
	SkippedMuted int    `json:"skippedMuted"`
	SkippedType  int    `json:"skippedType"`
	Unchanged    int    `json:"unchanged"`
	Scaled       int    `json:"scaled"`
}

func previewAudioFades(project editor.Project, input Input, p map[string]any, plan *Plan) error {
	if input.Scope != "selection" && input.Scope != "project" {
		return errors.New("批量声音淡化需要明确操作范围：selection或project")
	}
	if input.Scope == "selection" && len(input.Selection) == 0 || input.Scope == "project" && len(input.Selection) > 0 {
		return errors.New("所选操作范围与片段不一致，请重新明确范围")
	}
	selected := map[string]bool{}
	for _, id := range input.Selection {
		selected[id] = true
	}
	assets := map[string]editor.Asset{}
	tracks := map[string]editor.Track{}
	for _, a := range project.Assets {
		assets[a.ID] = a
	}
	for _, t := range project.Tracks {
		tracks[t.ID] = t
	}
	s := &AudioFadeSummary{Scope: input.Scope, Mode: stringValue(p, "mode")}
	plan.AudioFadeSummary = s
	in, out := integerValue(p, "inMs")*1000, integerValue(p, "outMs")*1000
	offline := false
	for _, c := range project.Clips {
		if input.Scope == "selection" && !selected[c.ID] {
			continue
		}
		s.Examined++
		a, t := assets[c.AssetID], tracks[c.TrackID]
		if c.Kind != "audio" && (c.Kind != "video" || stringValue(p, "targetKinds") != "audio_and_video") || !a.HasAudio {
			s.SkippedType++
			continue
		}
		if boolValue(p, "audibleOnly") && (c.Muted || t.Muted || c.Volume == 0) {
			s.SkippedMuted++
			continue
		}
		if boolValue(p, "audibleOnly") {
			phase, err := editor.ResolveSourceAudioPhase(c, a, project.Settings)
			if err != nil {
				return err
			}
			if phase.Current.Empty {
				s.SkippedMuted++
				continue
			}
		}
		if s.Mode == "set" && c.AudioFade != nil && stringValue(p, "existingPolicy") == "keep" {
			s.Preserved++
			continue
		}
		after := ownedClip(c)
		if s.Mode == "clear" {
			after.AudioFade = nil
		} else {
			if stringValue(p, "shortPolicy") == "scale" {
				var err error
				after.AudioFade, err = editor.FittedAudioFade(c, project.Settings, in, out)
				if err != nil {
					return err
				}
			} else {
				after.AudioFade = editor.NewAudioFade(c, in, out)
			}
			if err := editor.ValidateAudioFade(after, a, project.Settings); err != nil {
				return fmt.Errorf("片段%s的淡化无法应用：%w", c.Name, err)
			}
		}
		if reflect.DeepEqual(c.AudioFade, after.AudioFade) {
			s.Unchanged++
			continue
		}
		if t.Locked {
			return fmt.Errorf("声音片段%s所在轨道%s已锁定，整批未应用", c.Name, t.Name)
		}
		if s.Changed >= 1000 {
			return errors.New("单次批量淡化最多变更1000个片段，请缩小范围；整批未应用")
		}
		if after.AudioFade != nil && (after.AudioFade.InUs != in || after.AudioFade.OutUs != out) {
			s.Scaled++
		}
		s.Changed++
		offline = offline || a.Missing
		plan.Request.Commands = append(plan.Request.Commands, editor.Command{Type: "update_clip", Clip: &after, IncludeLinked: boolPointer(false)})
		if len(plan.Effects) < 200 {
			label := "清除两端淡化"
			if s.Mode == "set" {
				label = fmt.Sprintf("淡入%sms、淡出%sms；按当前片段重设", fadeMs(after.AudioFade, true), fadeMs(after.AudioFade, false))
			}
			plan.Effects = append(plan.Effects, Effect{Kind: "audio-fades", ClipID: c.ID, TrackID: c.TrackID, StartFrame: c.StartFrame, EndFrame: c.StartFrame + c.DurationFrames, Description: c.Name + " · " + label})
		}
	}
	if s.Changed == 0 {
		return fmt.Errorf("当前范围没有需要更新的声音：保留已有%d，跳过静音或无声范围%d/其它类型%d，已相同%d；请核对范围、类型和已有曲线策略", s.Preserved, s.SkippedMuted, s.SkippedType, s.Unchanged)
	}
	verb := "设置"
	if s.Mode == "clear" {
		verb = "清除"
	}
	plan.Request.Label = "批量" + verb + "声音淡化"
	plan.Summary = fmt.Sprintf("%s%d个声音片段的淡化；保留已有%d，跳过静音或无声范围%d/其它类型%d，已相同%d，同比缩短%d", verb, s.Changed, s.Preserved, s.SkippedMuted, s.SkippedType, s.Unchanged, s.Scaled)
	plan.Warnings = append(plan.Warnings, "仅更新明确范围内匹配的声音，不自动扩展音画链接；音量、静音、源范围和时间位置保持。淡化按显示片段范围，不是交叉混音或去爆音保证。")
	if s.Mode == "set" {
		plan.Warnings = append(plan.Warnings, "设置会按每个当前片段重建包络；选择覆盖时会替换原有/继承的曲线，请核对实际两端时长。")
	}
	if s.Scaled > 0 {
		plan.Warnings = append(plan.Warnings, fmt.Sprintf("%d个短片段已按原比例缩短两端，请核对明细中的实际微秒精度时长", s.Scaled))
	}
	if offline {
		plan.Warnings = append(plan.Warnings, "部分素材离线：本次只更新工程元数据，没有读取/试听源文件；试听与导出需恢复素材。")
	}
	if s.Changed > len(plan.Effects) {
		plan.Warnings = append(plan.Warnings, "影响明细最多显示200项，统计和应用仍覆盖全部变更。")
	}
	return nil
}
func fadeMs(f *editor.AudioFade, in bool) string {
	if f == nil {
		return "0"
	}
	v := f.OutUs
	if in {
		v = f.InUs
	}
	return fmt.Sprintf("%.3f", float64(v)/1000)
}
