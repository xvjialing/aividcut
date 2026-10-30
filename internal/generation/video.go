package generation

import (
	"context"
	"net/http"
	"net/url"
)

type MiniMaxProvider struct{ base }

func NewMiniMax(cfg Config) (*MiniMaxProvider, error) {
	b, err := newBase(MiniMax, "MiniMax-H3", "https://api.minimax.io", cfg)
	if err != nil {
		return nil, err
	}
	if !contains([]string{"MiniMax-H3", "MiniMax-H3-Highspeed", "MiniMax-H3-Lite", "MiniMax-H3-Max"}, b.model) {
		return nil, failure("model", "未声明此 MiniMax 视频模型的真实参数能力")
	}
	return &MiniMaxProvider{b}, nil
}

func (p *MiniMaxProvider) Capabilities() []Capability {
	min := 4
	sizes := []string{"768P", "2K"}
	if p.model == "MiniMax-H3-Highspeed" {
		sizes = []string{"768P"}
	}
	if p.model == "MiniMax-H3-Lite" || p.model == "MiniMax-H3-Max" {
		sizes = []string{"480P", "768P"}
	}
	if p.model == "MiniMax-H3-Max" {
		min = 5
	}
	return []Capability{{ProviderID: MiniMax, Family: "MiniMax H3", ModelID: p.model, MediaKind: "video", Modes: []string{VideoText, VideoImage, VideoReference}, Sizes: sizes, AspectRatios: []string{"21:9", "16:9", "4:3", "1:1", "3:4", "9:16", "adaptive"}, MinDurationSeconds: min, MaxDurationSeconds: 15, MaxCount: 1, MaxReferences: 9, MaxRequestBytes: 64_000_000, Async: true, Note: "已核对真实创建/查询契约并通过 fixture，账号未验证。当前只开放 PNG/JPEG 图片参考，视频/音频参考待真实编解码元数据验证；首尾帧和参考模式互斥。", SourceURL: "https://platform.minimax.io/docs/api-reference/video-generation-v2-create"}}
}

func (p *MiniMaxProvider) normalize(r Request) Request {
	if r.Size == "" {
		r.Size = "768P"
	}
	if r.DurationSeconds == 0 {
		r.DurationSeconds = 5
	}
	if r.AspectRatio == "" {
		r.AspectRatio = "16:9"
		if r.Mode == VideoImage {
			r.AspectRatio = "adaptive"
		}
	}
	if r.Count == 0 {
		r.Count = 1
	}
	return r
}

func (p *MiniMaxProvider) Validate(r Request) error {
	if err := p.common(r, VideoText, VideoImage, VideoReference); err != nil {
		return err
	}
	r = p.normalize(r)
	cap := p.Capabilities()[0]
	if !contains(cap.Sizes, r.Size) || !contains(cap.AspectRatios, r.AspectRatio) || r.DurationSeconds < cap.MinDurationSeconds || r.DurationSeconds > 15 || r.Count != 1 || r.Quality != "" || r.OutputFormat != "" || r.Background != "" || r.GenerateAudio != nil {
		return failure("parameters", "视频参数超出当前 H3 具体模型已声明能力")
	}
	if (r.Mode == VideoText && r.AspectRatio == "adaptive") || (r.Mode == VideoImage && r.AspectRatio != "adaptive") || (p.model == "MiniMax-H3-Lite" && r.Mode == VideoReference && r.AspectRatio == "adaptive") {
		return failure("parameters", "当前视频模式与画幅组合不受支持")
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
			return failure("reference_role", "当前 H3 适配器只开放首帧、尾帧和图片参考；不接受未知或本地 URL 参考")
		}
	}
	if (first > 0 || last > 0) && reference > 0 {
		return failure("reference_mode", "H3 首尾帧与参考素材模式不能混用")
	}
	if first > 1 || last > 1 || reference > 9 || (r.Mode == VideoText && len(r.References) > 0) || (r.Mode == VideoImage && (first+last == 0 || reference > 0)) || (r.Mode == VideoReference && (reference == 0 || first+last > 0)) {
		return failure("reference_mode", "参考数量或用途不符合当前 H3 生成模式")
	}
	return nil
}

func (p *MiniMaxProvider) Submit(ctx context.Context, r Request) (Task, error) {
	if err := p.Validate(r); err != nil {
		return Task{}, err
	}
	r = p.normalize(r)
	inputs, err := p.inputs(ctx, r, 9)
	if err != nil {
		return Task{}, err
	}
	for _, input := range inputs {
		if len(input.Data) > 30_000_000 || input.Width < 256 || input.Width > 5760 || input.Height < 256 || input.Height > 5760 || int64(input.Width)*10 < int64(input.Height)*4 || int64(input.Width)*10 > int64(input.Height)*25 {
			return Task{}, failure("reference_image", "H3 图片参考需要 256–5760 像素、宽高比 0.4–2.5 且单文件不超过 30MB")
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
	body := map[string]any{"model": p.model, "content": content, "resolution": r.Size, "duration": r.DurationSeconds, "ratio": r.AspectRatio}
	var response struct {
		ID string `json:"task_id"`
	}
	if err := p.request(ctx, http.MethodPost, "/v2/video_generation", body, &response); err != nil {
		return failTask(task, err)
	}
	if !safeID.MatchString(response.ID) {
		return failTask(task, &Error{Code: "response", Message: "服务未返回可查询任务 ID，提交结果待核实；不可自动重复提交", SubmissionUncertain: true})
	}
	task.RemoteID = response.ID
	task.Status = Queued
	return task, nil
}

func (p *MiniMaxProvider) Get(ctx context.Context, task Task) (Task, error) {
	if err := p.checkTask(task); err != nil {
		return task, err
	}
	if !safeID.MatchString(task.RemoteID) {
		return task, failure("unknown_submission", "缺少远端任务 ID，需人工核实原提交；不能通过查询函数重新生成")
	}
	var response struct {
		Task struct {
			ID      string `json:"id"`
			Model   string `json:"model"`
			Status  string `json:"status"`
			Content struct {
				URL string `json:"url"`
			} `json:"content"`
		} `json:"task"`
	}
	if err := p.request(ctx, http.MethodGet, "/v2/query/video_generation/"+url.PathEscape(task.RemoteID), nil, &response); err != nil {
		return task, err
	}
	if response.Task.ID != task.RemoteID || (response.Task.Model != "" && response.Task.Model != task.ModelID) {
		return task, failure("response", "查询响应的任务或模型与原任务不一致")
	}
	switch response.Task.Status {
	case Queued, Running, Failed, Cancelled:
		task.Status = response.Task.Status
	case "processing", "preparing":
		task.Status = Running
	case Succeeded:
		if response.Task.Content.URL == "" {
			return task, failure("response", "成功任务缺少结果地址，请重新查询同一任务")
		}
		task.Status = Succeeded
		task.Outputs = []Output{{Kind: "video", MIME: "video/mp4", URL: response.Task.Content.URL}}
	default:
		return task, failure("response", "远端任务状态未知，保留原任务供继续核实")
	}
	task.SubmissionUncertain = false
	return task, nil
}

func (p *MiniMaxProvider) Cancel(_ context.Context, task Task) (Task, error) {
	return task, unsupportedCancel()
}
