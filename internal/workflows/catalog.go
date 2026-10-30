package workflows

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"

	"aividcut/internal/editor"
)

func bound(value float64) *float64 { return &value }
func parameter(id, name, kind string, value any, min, max float64) Parameter {
	p := Parameter{ID: id, Name: name, Type: kind, Default: value}
	if kind == "integer" || kind == "number" {
		p.Min, p.Max = bound(min), bound(max)
		if kind == "integer" {
			p.Step = bound(1)
		}
	}
	return p
}
func pick(id, name, value string, options ...Option) Parameter {
	return Parameter{ID: id, Name: name, Type: "select", Default: value, Options: options}
}
func definitions() []Definition {
	defs := []Definition{
		{ID: "talking-head", Name: "口播词间停顿粗剪", Description: "依据已接受词的时间与保护边缘压缩词间空隙；不将未识别区间冒充静音或语义错误。", Parameters: []Parameter{
			parameter("gapThresholdMs", "最小候选词间间隔 · ms", "integer", 800, 100, 30000), parameter("keepGapMs", "候选间隔保留 · ms", "integer", 300, 0, 30000), parameter("edgeProtectionMs", "词边缘保护 · ms", "integer", 120, 0, 5000), parameter("maxGapMs", "最大自动候选 · ms", "integer", 5000, 100, 120000), parameter("sentencePauseMs", "句末停顿保护 · ms", "integer", 500, 0, 30000), parameter("preserveSpeakerChanges", "保护说话人切换", "boolean", true, 0, 0), pick("rippleScope", "闭合范围", "selection", Option{Value: "selection", Name: "仅选中完整音画组"}, Option{Value: "all_tracks", Name: "所有轨道（含未选内容）"}),
		}},
		{ID: "captions", Name: "已有字幕整理", Description: "统一已有字幕的明确样式和位置，保留文本与来源，不假造转写。", Parameters: []Parameter{
			pick("scope", "字幕范围", "selection", Option{Value: "selection", Name: "选中字幕"}, Option{Value: "all_captions", Name: "全部字幕"}), parameter("fontSize", "字号", "integer", 48, 8, 400), parameter("color", "文字颜色", "string", "#ffffff", 0, 0), parameter("background", "背景", "string", "transparent", 0, 0), pick("align", "对齐", "center", Option{Value: "left", Name: "左"}, Option{Value: "center", Name: "中"}, Option{Value: "right", Name: "右"}), parameter("offsetX", "水平位置", "number", 0, -16000, 16000), parameter("offsetY", "垂直位置", "number", 0, -16000, 16000),
		}},
		{ID: "voiceover", Name: "已保存配音编排", Description: "使用已导入音频的真实时长编排，可选已有源转写字幕；生成新声音需另行核对收费任务。", Parameters: []Parameter{
			{ID: "assetId", Name: "配音素材", Type: "asset", Required: true, Default: ""}, parameter("startFrame", "开始帧", "integer", 0, 0, 1e9), {ID: "trackId", Name: "音频轨（留空新建）", Type: "track", Default: ""}, parameter("volume", "音量", "number", 1, 0, 4), parameter("generateCaptions", "生成已有词时间字幕", "boolean", false, 0, 0), parameter("transcriptId", "源转写ID", "string", "", 0, 0), {ID: "captionTrackId", Name: "字幕轨（留空新建）", Type: "track", Default: ""},
		}},
		{ID: "broll", Name: "已有补充镜头编排", Description: "把已导入图片或视频放入明确区间，默认新建最上视频轨并静音，不删除原音画。", Parameters: []Parameter{
			{ID: "assetId", Name: "补充镜头素材", Type: "asset", Required: true, Default: ""}, parameter("startFrame", "开始帧", "integer", 0, 0, 1e9), parameter("durationFrames", "持续帧数", "integer", 90, 1, 1e9), parameter("sourceInUs", "源入点 · 微秒", "integer", 0, 0, 1e12), {ID: "trackId", Name: "视频轨（留空置于最上层）", Type: "track", Default: ""}, parameter("muted", "不用补充镜头声音", "boolean", true, 0, 0), parameter("scale", "画面缩放", "number", 1, 0.01, 16), parameter("offsetX", "水平位置", "number", 0, -16000, 16000), parameter("offsetY", "垂直位置", "number", 0, -16000, 16000),
		}},
	}
	defs = append(defs, Definition{ID: "audio-fades", Name: "批量声音淡入淡出", Description: "只更新明确范围内的声音淡化；默认保留已有曲线，不改时间、音量或静音。", Parameters: []Parameter{
		pick("mode", "操作", "set", Option{Value: "set", Name: "设置淡入淡出"}, Option{Value: "clear", Name: "清除已有淡化"}),
		pick("targetKinds", "声音类型", "audio_only", Option{Value: "audio_only", Name: "仅音频片段"}, Option{Value: "audio_and_video", Name: "音频与视频原声"}),
		parameter("audibleOnly", "仅处理当前可听声音", "boolean", true, 0, 0),
		parameter("inMs", "淡入 · ms", "integer", 100, 0, 86400000), parameter("outMs", "淡出 · ms", "integer", 100, 0, 86400000),
		pick("existingPolicy", "已有淡化（设置时）", "keep", Option{Value: "keep", Name: "保留已有曲线"}, Option{Value: "replace", Name: "覆盖并按当前片段重设"}),
		pick("shortPolicy", "短片段（设置时）", "scale", Option{Value: "scale", Name: "两端同比缩短至片段长度"}, Option{Value: "reject", Name: "时长不足时整批拒绝"}),
	}})
	for i := range defs {
		defs[i].Version = "1.0.0"
		defs[i].Builtin = true
		defs[i].Defaults = map[string]any{}
		for _, p := range defs[i].Parameters {
			defs[i].Defaults[p.ID] = p.Default
		}
	}
	return defs
}
func definition(id string) (Definition, error) {
	for _, d := range definitions() {
		if d.ID == id {
			return d, nil
		}
	}
	return Definition{}, errors.New("未声明的内置工作流")
}

func Catalog(project editor.Project) []Definition {
	defs := definitions()
	soundAssets := map[string]bool{}
	for _, a := range project.Assets {
		soundAssets[a.ID] = a.HasAudio
	}
	caption, audio, visual, accepted := false, false, false, false
	for _, clip := range project.Clips {
		if clip.Kind == "caption" {
			caption = true
		}
	}
	for _, asset := range project.Assets {
		if asset.Missing {
			continue
		}
		if asset.Kind == "audio" && asset.DurationUs > 0 {
			audio = true
		}
		if asset.Kind == "video" || asset.Kind == "image" {
			visual = true
		}
	}
	for _, t := range project.Transcripts {
		if t.Stale || len(t.AcceptedWords) < 2 {
			continue
		}
		for _, clip := range project.Clips {
			if clip.AssetID == t.AssetID && (clip.Kind == "video" || clip.Kind == "audio") {
				accepted = true
			}
		}
	}
	for i := range defs {
		d := &defs[i]
		if d.ID == "talking-head" {
			for j := range d.Parameters {
				if d.Parameters[j].ID == "rippleScope" {
					d.Parameters[j].Description = "默认仅闭合选中源组；全轨会裁剪配乐、标题和其它内容并前移所有后段。停顿证据仍只来自候选来源。"
				}
			}
		}
		switch d.ID {
		case "talking-head":
			d.Available = accepted
			d.Reason = "需要已接纳且来源未陈旧的词时间，以及已上轨的对应源片段"
		case "captions":
			d.Available = caption
			d.Reason = "需要已有字幕片段"
		case "audio-fades":
			d.Available = false
			for _, c := range project.Clips {
				if soundAssets[c.AssetID] && (c.Kind == "audio" || c.Kind == "video") {
					d.Available = true
					break
				}
			}
			d.Reason = "需要已上轨且元数据声明具有声音的音频或视频"
			for j := range d.Parameters {
				if d.Parameters[j].ID == "audibleOnly" {
					d.Parameters[j].Description = "跳过静音片段、静音轨和零音量；隐藏画面不等于声音静音。"
				}
				if d.Parameters[j].ID == "mode" {
					d.Parameters[j].Description = "清除只移除匹配声音上的既有曲线，下面的时长/已有策略/短片段设置不适用。"
				}
			}
		case "voiceover":
			d.Available = audio
			d.Reason = "需要已导入且已知真实时长的音频"
		case "broll":
			d.Available = visual
			d.Reason = "需要已导入的可用图片或视频"
		}
		if d.Available {
			d.Reason = ""
		}
		for j := range d.Parameters {
			p := &d.Parameters[j]
			if p.Type == "asset" {
				for _, asset := range project.Assets {
					if asset.Missing {
						continue
					}
					if d.ID == "voiceover" && asset.Kind == "audio" || d.ID == "broll" && (asset.Kind == "image" || asset.Kind == "video") {
						p.Options = append(p.Options, Option{Value: asset.ID, Name: asset.Name})
					}
				}
			}
			if p.Type == "track" {
				kind := "audio"
				if p.ID == "captionTrackId" {
					kind = "caption"
				} else if d.ID == "broll" {
					kind = "video"
				}
				for _, track := range project.Tracks {
					if track.Kind == kind && !track.Locked {
						p.Options = append(p.Options, Option{Value: track.ID, Name: track.Name})
					}
				}
			}
		}
	}
	return defs
}

func numeric(value any) (float64, bool) {
	switch n := value.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case int32:
		return float64(n), true
	case json.Number:
		v, err := n.Float64()
		return v, err == nil
	}
	return 0, false
}
func normalizeParameters(def Definition, input map[string]any, required bool) (map[string]any, error) {
	allowed := map[string]Parameter{}
	result := map[string]any{}
	for _, p := range def.Parameters {
		allowed[p.ID] = p
		result[p.ID] = p.Default
	}
	for id, value := range input {
		p, ok := allowed[id]
		if !ok {
			return nil, fmt.Errorf("工作流不支持参数：%s", id)
		}
		switch p.Type {
		case "integer", "number":
			n, ok := numeric(value)
			if !ok || math.IsNaN(n) || math.IsInf(n, 0) || p.Type == "integer" && math.Trunc(n) != n || p.Min != nil && n < *p.Min || p.Max != nil && n > *p.Max {
				return nil, fmt.Errorf("参数%s的数值或范围无效", p.Name)
			}
			result[id] = n
		case "boolean":
			if _, ok := value.(bool); !ok {
				return nil, fmt.Errorf("参数%s需要布尔值", p.Name)
			}
			result[id] = value
		default:
			text, ok := value.(string)
			if !ok || len(text) > 1024 {
				return nil, fmt.Errorf("参数%s需要有效文本或稳定ID", p.Name)
			}
			if p.Type == "select" {
				found := false
				for _, option := range p.Options {
					if text == option.Value {
						found = true
					}
				}
				if !found {
					return nil, fmt.Errorf("参数%s选项未支持", p.Name)
				}
			}
			result[id] = text
		}
	}
	for _, p := range def.Parameters {
		if required && p.Required {
			if text, ok := result[p.ID].(string); ok && strings.TrimSpace(text) == "" {
				return nil, fmt.Errorf("请选择%s", p.Name)
			}
		}
	}
	return result, nil
}
func normalizeInput(project editor.Project, input Input) (Definition, map[string]any, error) {
	if err := editor.ValidateProject(project); err != nil {
		return Definition{}, nil, err
	}
	def, err := definition(input.WorkflowID)
	if err != nil {
		return def, nil, err
	}
	for _, item := range Catalog(project) {
		if item.ID == def.ID {
			def = item
			break
		}
	}
	if !def.Available {
		return def, nil, errors.New(def.Reason)
	}
	if len(input.Selection) > 1000 {
		return def, nil, errors.New("选择片段超过工作流当前限制")
	}
	seen := map[string]bool{}
	for _, id := range input.Selection {
		if seen[id] {
			return def, nil, errors.New("选择的片段重复")
		}
		seen[id] = true
		found := false
		for _, clip := range project.Clips {
			if clip.ID == id {
				found = true
				break
			}
		}
		if !found {
			return def, nil, errors.New("选择的片段已不在当前工程")
		}
	}
	parameters, err := normalizeParameters(def, input.Parameters, true)
	return def, parameters, err
}
