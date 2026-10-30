package workflows

import (
	"errors"
	"fmt"
	"reflect"
	"sort"

	"aividcut/internal/editor"
)

func Preview(project editor.Project, input Input) (Plan, error) {
	def, parameters, err := normalizeInput(project, input)
	if err != nil {
		return Plan{}, err
	}
	id := editor.NewID("workflow")
	plan := Plan{ID: id, WorkflowID: def.ID, WorkflowVersion: def.Version, ProjectID: project.ID, Request: editor.EditRequest{BaseRevision: project.Revision, IdempotencyKey: id, Label: def.Name, Commands: []editor.Command{}}, Effects: []Effect{}, Evidence: []Evidence{}, Warnings: []string{}, AffectedClipIDs: []string{}}
	selectedRemoved := int64(0)
	switch def.ID {
	case "talking-head":
		selectedRemoved, err = previewTalkingHead(project, input.Selection, parameters, &plan)
	case "captions":
		err = previewCaptions(project, input.Selection, parameters, &plan)
	case "voiceover":
		err = previewVoiceover(project, input.Selection, parameters, &plan)
	case "broll":
		err = previewBroll(project, parameters, &plan)
	case "audio-fades":
		err = previewAudioFades(project, input, parameters, &plan)
	default:
		err = errors.New("未实现的工作流")
	}
	if err != nil {
		return Plan{}, err
	}
	return finalizePreview(project, plan, selectedRemoved)
}

func finalizePreview(project editor.Project, plan Plan, selectedRemoved int64) (Plan, error) {
	if len(plan.Request.Commands) == 0 {
		return Plan{}, errors.New("当前范围和参数没有产生需要应用的编辑")
	}
	engine, err := editor.NewEngine(project)
	if err != nil {
		return Plan{}, err
	}
	after, err := engine.Apply(plan.Request)
	if err != nil {
		return Plan{}, fmt.Errorf("工作流无法安全原子应用，请检查锁定、选区与轨道重叠：%w", err)
	}
	plan.SavedFrames = editor.DurationFrames(project) - editor.DurationFrames(after)
	if selectedRemoved > 0 && selectedRemoved > plan.SavedFrames {
		plan.Warnings = append(plan.Warnings, fmt.Sprintf("选中源组共移除%d帧，但工程总长仅减少%d帧；未选轨的位置和长尾保持不变", selectedRemoved, plan.SavedFrames))
	}
	affected := map[string]bool{}
	for _, id := range plan.AffectedClipIDs {
		affected[id] = true
	}
	afterByID := map[string]editor.Clip{}
	beforeIDs := map[string]bool{}
	for _, clip := range project.Clips {
		beforeIDs[clip.ID] = true
	}
	for _, clip := range after.Clips {
		afterByID[clip.ID] = clip
		if !beforeIDs[clip.ID] {
			affected[clip.ID] = true
		}
	}
	for _, before := range project.Clips {
		if next, exists := afterByID[before.ID]; !exists || !reflect.DeepEqual(before, next) {
			affected[before.ID] = true
		}
	}
	plan.AffectedClipIDs = nil
	for id := range affected {
		plan.AffectedClipIDs = append(plan.AffectedClipIDs, id)
	}
	sort.Strings(plan.AffectedClipIDs)
	if plan.RangeSummary != nil {
		describeRipple(project, after, &plan)
	}
	if plan.SpeechRippleSummary != nil {
		if err := describeSpeechRipple(project, after, &plan); err != nil {
			return Plan{}, err
		}
	}
	if plan.SavedFrames < 0 {
		plan.Warnings = append(plan.Warnings, fmt.Sprintf("本次工程总长增加%d帧，请核对时间线末端。", -plan.SavedFrames))
	}
	return plan, nil
}

func numberValue(parameters map[string]any, id string) float64 {
	value, _ := numeric(parameters[id])
	return value
}
func integerValue(parameters map[string]any, id string) int64 {
	return int64(numberValue(parameters, id))
}
func stringValue(parameters map[string]any, id string) string {
	value, _ := parameters[id].(string)
	return value
}
func boolValue(parameters map[string]any, id string) bool {
	value, _ := parameters[id].(bool)
	return value
}
func boolPointer(value bool) *bool { return &value }

func ownedClip(value editor.Clip) editor.Clip {
	copy := editor.CloneProject(editor.Project{Clips: []editor.Clip{value}})
	return copy.Clips[0]
}

func assetByID(project editor.Project, id string) (editor.Asset, error) {
	for _, asset := range project.Assets {
		if asset.ID == id {
			if asset.Missing || asset.Fingerprint == "" {
				return editor.Asset{}, errors.New("素材缺失或尚无已核对的内容指纹")
			}
			return asset, nil
		}
	}
	return editor.Asset{}, errors.New("素材不属于当前工程，请先保存生成结果并导入素材库")
}
func trackByID(project editor.Project, id, kind string) (editor.Track, error) {
	for _, track := range project.Tracks {
		if track.ID == id {
			if track.Kind != kind || track.Locked {
				return editor.Track{}, errors.New("目标轨类型不符或已锁定")
			}
			return track, nil
		}
	}
	return editor.Track{}, errors.New("目标轨已不存在")
}
func currentTranscript(project editor.Project, asset editor.Asset, id string) (editor.Transcript, error) {
	candidates := []editor.Transcript{}
	for _, transcript := range project.Transcripts {
		if transcript.AssetID == asset.ID && !transcript.Stale && transcript.SourceFingerprint == asset.Fingerprint && transcript.SourceDurationUs == asset.DurationUs && (id == "" || transcript.ID == id) {
			candidates = append(candidates, transcript)
		}
	}
	if len(candidates) == 0 {
		return editor.Transcript{}, errors.New("需要已接纳且对应当前源指纹/时长的真实词时间")
	}
	if id != "" || len(candidates) == 1 {
		return candidates[0], nil
	}
	for _, candidate := range candidates {
		if candidate.CaptionTrackID != "" {
			return candidate, nil
		}
	}
	return editor.Transcript{}, errors.New("同一素材存在多份已接受转写，请先明确启用需要使用的源字幕版本")
}

func selectedMediaGroup(project editor.Project, clip editor.Clip) ([]editor.Clip, error) {
	group := []editor.Clip{}
	for _, other := range project.Clips {
		if other.ID != clip.ID && (clip.LinkGroup == "" || other.LinkGroup != clip.LinkGroup) {
			continue
		}
		if other.Kind != "video" && other.Kind != "audio" {
			return nil, errors.New("首轮源工作流不切剪包含标题或静态字幕的自定义链接组，请先整理链接")
		}
		group = append(group, other)
	}
	return group, nil
}
