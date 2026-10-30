package generation

import (
	"context"
	"net/http"
	"net/url"
)

// 当前仅声明火山方舟北京区标准 API，其他渠道需独立验证模型与凭据。
const Seedance25Model = "doubao-seedance-2-5-260628"
const seedanceTasksPath = "/api/v3/contents/generations/tasks"

type SeedanceProvider struct{ base }

func NewSeedance(cfg Config) (*SeedanceProvider, error) {
	b, err := newBase(Seedance, Seedance25Model, "https://ark.cn-beijing.volces.com", cfg)
	if err != nil {
		return nil, err
	}
	if b.model != Seedance25Model {
		return nil, failure("model", "当前 Seedance 适配器仅声明火山方舟北京区 Seedance 2.5 指定版本")
	}
	return &SeedanceProvider{b}, nil
}

func (p *SeedanceProvider) Capabilities() []Capability {
	aspects := []string{"16:9", "4:3", "1:1", "3:4", "9:16", "21:9", "adaptive"}
	return []Capability{{
		ProviderID: Seedance, Family: "Seedance 2.5 · 火山方舟北京区", ModelID: p.model,
		MediaKind: "video", Modes: []string{VideoText, VideoImage, VideoReference},
		Sizes: []string{"480p", "720p", "1080p"}, DefaultSize: "720p", AspectRatios: aspects,
		ModeAspectRatios: map[string][]string{VideoImage: {"adaptive"}}, RequiresFirstFrame: true,
		SupportsAudioGeneration: true, DefaultGenerateAudio: true,
		OutputFormats: []string{"mp4"}, MinDurationSeconds: 4, MaxDurationSeconds: 30,
		MaxReferences: 30, MaxCount: 1, MaxRequestBytes: 64_000_000, Async: true,
		Note:      "火山方舟北京区标准 API，账户未验证。当前支持文字、首帧/首尾帧和 PNG/JPEG 参考图；图片需 300–6000 像素、宽高比 0.4–2.5、单张小于 30MB。首帧模式自动匹配画幅。仅开放 MP4；视频/音频参考、MOV、编辑和延长待后续接入。",
		SourceURL: "https://console.volcengine.com/ark/region:cn-beijing/docs/create-video-generation-task-api",
	}}
}

func (p *SeedanceProvider) normalize(r Request) Request {
	if r.Size == "" {
		r.Size = "720p"
	}
	if r.DurationSeconds == 0 {
		r.DurationSeconds = 5
	}
	if r.Count == 0 {
		r.Count = 1
	}
	if r.OutputFormat == "" {
		r.OutputFormat = "mp4"
	}
	if r.AspectRatio == "" {
		r.AspectRatio = "16:9"
		if r.Mode == VideoImage {
			r.AspectRatio = "adaptive"
		}
	}
	if r.GenerateAudio == nil {
		value := true
		r.GenerateAudio = &value
	}
	return r
}

func (p *SeedanceProvider) Validate(r Request) error {
	if err := p.common(r, VideoText, VideoImage, VideoReference); err != nil {
		return err
	}
	r = p.normalize(r)
	capability := p.Capabilities()[0]
	if !contains(capability.Sizes, r.Size) || !contains(capability.AspectRatios, r.AspectRatio) ||
		r.DurationSeconds < 4 || r.DurationSeconds > 30 || r.Count != 1 ||
		r.OutputFormat != "mp4" || r.Background != "" || r.Quality != "" ||
		(r.Mode == VideoImage && r.AspectRatio != "adaptive") {
		return failure("parameters", "参数超出当前 Seedance 2.5 适配器声明能力")
	}
	first, last, reference := 0, 0, 0
	for _, input := range r.References {
		switch input.Role {
		case "first_frame":
			first++
		case "last_frame":
			last++
		case "reference_image":
			reference++
		default:
			return failure("reference_role", "当前 Seedance 仅接收首帧、尾帧或图片参考用途")
		}
	}
	if first > 1 || last > 1 || reference > 30 ||
		(r.Mode == VideoText && len(r.References) != 0) ||
		(r.Mode == VideoImage && (first != 1 || reference != 0)) ||
		(r.Mode == VideoReference && (reference == 0 || first+last != 0)) {
		return failure("reference_mode", "首帧模式必须有一张首帧、可选一张尾帧；参考模式需 1–30 张参考图，不能混用")
	}
	return nil
}

func (p *SeedanceProvider) Submit(ctx context.Context, r Request) (Task, error) {
	if err := p.Validate(r); err != nil {
		return Task{}, err
	}
	r = p.normalize(r)
	inputs, err := p.inputs(ctx, r, 30)
	if err != nil {
		return Task{}, err
	}
	for _, input := range inputs {
		if len(input.Data) >= 30_000_000 || input.Width < 300 || input.Width > 6000 ||
			input.Height < 300 || input.Height > 6000 || int64(input.Width)*10 < int64(input.Height)*4 ||
			int64(input.Width)*10 > int64(input.Height)*25 {
			return Task{}, failure("reference_image", "Seedance 图片需 300–6000 像素、宽高比 0.4–2.5 且单张小于 30MB")
		}
	}
	task, err := p.task(r, inputs)
	if err != nil {
		return Task{}, err
	}
	content := []map[string]any{{"type": "text", "text": r.Prompt}}
	for _, input := range inputs {
		content = append(content, map[string]any{"type": "image_url", "image_url": map[string]string{"url": dataURL(input)}, "role": input.Role})
	}
	body := map[string]any{"model": p.model, "content": content, "resolution": r.Size,
		"ratio": r.AspectRatio, "duration": r.DurationSeconds, "output_format": r.OutputFormat,
		"generate_audio": *r.GenerateAudio}
	if r.Mode == VideoReference {
		body["omni_reference_task_type"] = "reference"
	}
	var response struct {
		ID string `json:"id"`
	}
	if err := p.request(ctx, http.MethodPost, seedanceTasksPath, body, &response); err != nil {
		return failTask(task, err)
	}
	if !safeID.MatchString(response.ID) {
		return failTask(task, &Error{Code: "response", Message: "未返回可查询任务 ID，提交结果待核实；不可自动重复提交", SubmissionUncertain: true})
	}
	task.RemoteID, task.Status = response.ID, Queued
	return task, nil
}

func (p *SeedanceProvider) Get(ctx context.Context, task Task) (Task, error) {
	if err := p.checkTask(task); err != nil {
		return task, err
	}
	if !safeID.MatchString(task.RemoteID) {
		return task, failure("unknown_submission", "缺少远端任务 ID，需核实原提交；查询不会重新生成")
	}
	var response struct {
		ID      string `json:"id"`
		Model   string `json:"model"`
		Status  string `json:"status"`
		Format  string `json:"output_format"`
		Content struct {
			URL string `json:"video_url"`
		} `json:"content"`
	}
	if err := p.request(ctx, http.MethodGet, seedanceTasksPath+"/"+url.PathEscape(task.RemoteID), nil, &response); err != nil {
		return task, err
	}
	if response.ID != task.RemoteID || response.Model != p.model {
		return task, failure("response", "查询响应的任务或模型与原提交不一致")
	}
	switch response.Status {
	case Queued, Running, Failed, Cancelled:
		task.Status, task.Outputs = response.Status, []Output{}
	case "expired":
		task.Status, task.Outputs = Failed, []Output{}
	case Succeeded:
		if response.Content.URL == "" || (response.Format != "" && response.Format != "mp4") {
			return task, failure("response", "成功任务缺少 MP4 结果，保留原任务供核实")
		}
		if err := validateURL(response.Content.URL, p.localTest); err != nil {
			// 下载签名可出现在查询参数，不在日志或工程中暴露，交由结果导入器继续校验。
			parsed, parseErr := url.Parse(response.Content.URL)
			if parseErr != nil {
				return task, failure("response", "结果地址无效")
			}
			parsed.RawQuery = ""
			if validateURL(parsed.String(), p.localTest) != nil {
				return task, failure("response", "结果地址无效")
			}
		}
		task.Status = Succeeded
		task.Outputs = []Output{{Kind: "video", MIME: "video/mp4", URL: response.Content.URL}}
	default:
		return task, failure("response", "远端任务状态未知，保留原任务继续查询")
	}
	task.SubmissionUncertain = false
	return task, nil
}

func (p *SeedanceProvider) Cancel(_ context.Context, task Task) (Task, error) {
	return task, unsupportedCancel()
}
