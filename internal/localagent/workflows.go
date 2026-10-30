package localagent

import "aividcut/internal/editor"

func Workflows(project editor.Project) []Workflow {
	caption, visual := false, false
	for _, clip := range project.Clips {
		if clip.Kind == "caption" {
			caption = true
		}
	}
	for _, asset := range project.Assets {
		if (asset.Kind == "video" || asset.Kind == "image") && !asset.Missing {
			visual = true
		}
	}
	return []Workflow{
		{ID: "manual-plan", Name: "剪辑计划", Description: "根据已导入素材和明确要求生成可校验的编辑命令。", Available: true},
		{ID: "talking-head", Name: "口播粗剪", Description: "基于已接受词时间生成有证据的删减候选。", Reason: "请使用工作流视图生成词间停顿计划；结构快照规划不会猜测未内联的词时间。"},
		{ID: "captions", Name: "已有字幕整理", Description: "整理已有字幕的样式和编排，不将它当作原始ASR。", Available: caption, Reason: reasonUnless(caption, "需要已有字幕片段；本模块不执行ASR。")},
		{ID: "voiceover", Name: "配音讲解", Description: "语音任务生成真实音频，再使用工作流按实测时长编排。", Reason: "CLI结构计划不直接提交收费语音；请在语音页生成后使用工作流编排。"},
		{ID: "broll", Name: "补充镜头", Description: "使用已有视觉素材安排补充镜头，不伪造已生成媒体。", Available: visual, Reason: reasonUnless(visual, "需要已导入且可用的图片或视频；生成服务另接。")},
	}
}

func reasonUnless(available bool, reason string) string {
	if available {
		return ""
	}
	return reason
}

func workflowFor(input PlanInput) (Workflow, error) {
	id := input.Workflow
	if id == "" {
		id = "manual-plan"
	}
	for _, workflow := range Workflows(input.Project) {
		if workflow.ID == id {
			if !workflow.Available {
				return workflow, failure("missing_prerequisite", workflow.Reason, ErrPrerequisite)
			}
			return workflow, nil
		}
	}
	return Workflow{}, failure("unknown_workflow", "未知的本地工作流", ErrPrerequisite)
}
