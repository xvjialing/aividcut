package generation

import (
	"context"
	"encoding/base64"
	"net/http"
	"strings"
)

type OpenAIProvider struct{ base }

func NewOpenAI(cfg Config) (*OpenAIProvider, error) {
	b, err := newBase(OpenAI, "gpt-image-2.5-sunburst", "https://api.openai.com/v1", cfg)
	if err != nil {
		return nil, err
	}
	if !contains([]string{"gpt-image-2.5-sunburst", "gpt-image-2.5-flare", "gpt-image-2.5-sunburst-2026-09-08", "gpt-image-2.5-flare-2026-09-08"}, b.model) {
		return nil, failure("model", "当前正式适配器只声明已核对的 GPT Image 2.5 模型；其他模型需单独验证能力")
	}
	return &OpenAIProvider{b}, nil
}

func (p *OpenAIProvider) Capabilities() []Capability {
	return []Capability{{ProviderID: OpenAI, Family: "GPT Image", ModelID: p.model, MediaKind: "image", Modes: []string{ImageGenerate, ImageEdit}, Sizes: []string{"1024x1024", "1536x1024", "1024x1536", "1536x864", "864x1536"}, AspectRatios: []string{"1:1", "3:2", "2:3", "16:9", "9:16"}, Qualities: []string{"auto", "low", "medium", "high", "xhigh", "max"}, OutputFormats: []string{"png", "jpeg"}, MaxReferences: 16, MaxRequestBytes: maxJSONBytes, MaxCount: 10, Note: "已核对接口并通过本地 HTTP fixture；真实账号和收费生成未验证。当前参考图仅开放 PNG/JPEG。", SourceURL: "https://developers.openai.com/api/docs/guides/image-generation"}}
}

func (p *OpenAIProvider) normalize(r Request) Request {
	if r.Size == "" {
		r.Size = map[string]string{"": "1024x1024", "1:1": "1024x1024", "3:2": "1536x1024", "2:3": "1024x1536", "16:9": "1536x864", "9:16": "864x1536"}[r.AspectRatio]
	}
	if r.Count == 0 {
		r.Count = 1
	}
	if r.Quality == "" {
		r.Quality = "auto"
	}
	if r.OutputFormat == "" {
		r.OutputFormat = "png"
	}
	if r.Background == "" {
		r.Background = "auto"
	}
	return r
}

func (p *OpenAIProvider) Validate(r Request) error {
	if err := p.common(r, ImageGenerate, ImageEdit); err != nil {
		return err
	}
	r = p.normalize(r)
	c := p.Capabilities()[0]
	if r.GenerateAudio != nil || r.DurationSeconds != 0 || !contains(c.Sizes, r.Size) || !contains(c.Qualities, r.Quality) || !contains(c.OutputFormats, r.OutputFormat) || !contains([]string{"auto", "opaque", "transparent"}, r.Background) || r.Count < 1 || r.Count > 10 {
		return failure("parameters", "图片尺寸、质量、格式、背景或数量超出已声明能力")
	}
	if r.AspectRatio != "" {
		expected := map[string]string{"1024x1024": "1:1", "1536x1024": "3:2", "1024x1536": "2:3", "1536x864": "16:9", "864x1536": "9:16"}[r.Size]
		if expected != r.AspectRatio {
			return failure("parameters", "图片尺寸与指定画幅不一致")
		}
	}
	if r.Background == "transparent" && r.OutputFormat != "png" {
		return failure("parameters", "当前透明输出需要 PNG 格式")
	}
	if len(r.References) > 16 || (r.Mode == ImageGenerate && len(r.References) > 0) || (r.Mode == ImageEdit && len(r.References) == 0) {
		return failure("reference_mode", "纯文字生成不接收参考图，图片编辑需要 1–16 张参考图")
	}
	for _, ref := range r.References {
		if !contains([]string{"", "reference", "reference_image"}, ref.Role) {
			return failure("reference_role", "图片编辑参考用途不受支持")
		}
	}
	return nil
}

func (p *OpenAIProvider) Submit(ctx context.Context, r Request) (Task, error) {
	if err := p.Validate(r); err != nil {
		return Task{}, err
	}
	r = p.normalize(r)
	inputs, err := p.inputs(ctx, r, 16)
	if err != nil {
		return Task{}, err
	}
	task, err := p.task(r, inputs)
	if err != nil {
		return Task{}, err
	}
	body := map[string]any{"model": p.model, "prompt": r.Prompt, "size": r.Size, "quality": r.Quality, "output_format": r.OutputFormat, "background": r.Background, "n": r.Count}
	path := "/images/generations"
	if r.Mode == ImageEdit {
		path = "/images/edits"
		images := make([]map[string]string, 0, len(inputs))
		for _, input := range inputs {
			images = append(images, map[string]string{"image_url": dataURL(input)})
		}
		body["images"] = images
	}
	var response struct {
		Data []struct {
			B64 string `json:"b64_json"`
		} `json:"data"`
	}
	if err := p.request(ctx, http.MethodPost, path, body, &response); err != nil {
		return failTask(task, err)
	}
	if len(response.Data) == 0 || len(response.Data) > 10 {
		return failTask(task, &Error{Code: "response", Message: "服务未返回可识别的最终图像，请检查原请求，不要自动重试", SubmissionUncertain: true})
	}
	for _, value := range response.Data {
		output, err := decodeImage(value.B64, "")
		if err != nil {
			return failTask(task, err)
		}
		task.Outputs = append(task.Outputs, output)
	}
	task.Status = Succeeded
	return task, nil
}

func (p *OpenAIProvider) Get(ctx context.Context, task Task) (Task, error) {
	if err := p.checkTask(task); err != nil {
		return task, err
	}
	if err := ctx.Err(); err != nil {
		return task, err
	}
	if task.Status == Succeeded && len(task.Outputs) > 0 {
		return task, nil
	}
	return task, failure("not_pollable", "同步图片接口没有可查询的远端任务；未知提交需核对服务历史，不可自动再次生成")
}
func (p *OpenAIProvider) Cancel(_ context.Context, task Task) (Task, error) {
	return task, unsupportedCancel()
}

type GeminiProvider struct{ base }

func NewGemini(cfg Config) (*GeminiProvider, error) {
	b, err := newBase(Google, "gemini-2.5-flash-image", "https://generativelanguage.googleapis.com/v1beta", cfg)
	if err != nil {
		return nil, err
	}
	if b.model != "gemini-2.5-flash-image" {
		return nil, failure("model", "当前适配器仅验证 Nano Banana 的 gemini-2.5-flash-image；其他版本不可沿用未验证参数")
	}
	return &GeminiProvider{b}, nil
}

func (p *GeminiProvider) Capabilities() []Capability {
	return []Capability{{ProviderID: Google, Family: "Nano Banana", ModelID: p.model, MediaKind: "image", Modes: []string{ImageGenerate, ImageEdit}, AspectRatios: []string{"1:1", "2:3", "3:2", "3:4", "4:3", "4:5", "5:4", "9:16", "16:9", "21:9"}, MaxReferences: 3, MaxRequestBytes: 20_000_000, MaxCount: 1, Note: "当前使用明确支持 generateContent 的 Nano Banana 具体模型；最多 3 张 PNG/JPEG 参考图，输出格式和数量以真实响应为准；账号未验证。", SourceURL: "https://ai.google.dev/gemini-api/docs/models/gemini-2.5-flash-image"}}
}

func (p *GeminiProvider) Validate(r Request) error {
	if err := p.common(r, ImageGenerate, ImageEdit); err != nil {
		return err
	}
	if r.GenerateAudio != nil || r.Size != "" || r.Quality != "" || r.OutputFormat != "" || r.Background != "" || r.DurationSeconds != 0 || r.Count < 0 || r.Count > 1 {
		return failure("parameters", "当前 Nano Banana 适配器不声明这些尺寸、质量、格式、背景或批量控制")
	}
	if r.AspectRatio != "" && !contains(p.Capabilities()[0].AspectRatios, r.AspectRatio) {
		return failure("parameters", "当前模型不声明该画幅")
	}
	if len(r.References) > 3 || (r.Mode == ImageGenerate && len(r.References) > 0) || (r.Mode == ImageEdit && len(r.References) == 0) {
		return failure("reference_mode", "文字生成不带参考图，图片编辑需要 1–3 张参考图")
	}
	for _, ref := range r.References {
		if !contains([]string{"", "reference", "reference_image"}, ref.Role) {
			return failure("reference_role", "当前图片编辑不支持此参考用途")
		}
	}
	return nil
}

func (p *GeminiProvider) Submit(ctx context.Context, r Request) (Task, error) {
	if err := p.Validate(r); err != nil {
		return Task{}, err
	}
	inputs, err := p.inputs(ctx, r, 3)
	if err != nil {
		return Task{}, err
	}
	task, err := p.task(r, inputs)
	if err != nil {
		return Task{}, err
	}
	parts := []map[string]any{{"text": r.Prompt}}
	for _, input := range inputs {
		parts = append(parts, map[string]any{"inlineData": map[string]string{"mimeType": input.MIME, "data": base64.StdEncoding.EncodeToString(input.Data)}})
	}
	config := map[string]any{"responseModalities": []string{"TEXT", "IMAGE"}}
	if r.AspectRatio != "" {
		config["imageConfig"] = map[string]string{"aspectRatio": r.AspectRatio}
	}
	body := map[string]any{"contents": []map[string]any{{"role": "user", "parts": parts}}, "generationConfig": config}
	var response struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Thought    bool `json:"thought"`
					InlineData *struct {
						MIME string `json:"mimeType"`
						Data string `json:"data"`
					} `json:"inlineData"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}
	if err := p.request(ctx, http.MethodPost, "/models/"+p.model+":generateContent", body, &response); err != nil {
		return failTask(task, err)
	}
	for _, candidate := range response.Candidates {
		for _, part := range candidate.Content.Parts {
			if part.Thought || part.InlineData == nil {
				continue
			}
			if len(task.Outputs) >= 10 {
				return failTask(task, &Error{Code: "response", Message: "生成返回图像数量超过当前处理限制，请检查原任务", SubmissionUncertain: true})
			}
			output, err := decodeImage(part.InlineData.Data, part.InlineData.MIME)
			if err != nil {
				return failTask(task, err)
			}
			task.Outputs = append(task.Outputs, output)
		}
	}
	if len(task.Outputs) == 0 {
		return failTask(task, &Error{Code: "response", Message: "服务没有返回最终图像，可能被拒绝或接口能力有变化；未自动重试", SubmissionUncertain: true})
	}
	task.Status = Succeeded
	return task, nil
}

func (p *GeminiProvider) Get(ctx context.Context, task Task) (Task, error) {
	if err := p.checkTask(task); err != nil {
		return task, err
	}
	if err := ctx.Err(); err != nil {
		return task, err
	}
	if task.Status == Succeeded && len(task.Outputs) > 0 {
		return task, nil
	}
	return task, failure("not_pollable", "当前同步图像接口不提供可恢复的轮询任务；请检查原生成记录")
}
func (p *GeminiProvider) Cancel(_ context.Context, task Task) (Task, error) {
	return task, unsupportedCancel()
}

func dataURL(input ReferenceData) string {
	return "data:" + input.MIME + ";base64," + base64.StdEncoding.EncodeToString(input.Data)
}

func decodeImage(encoded, mime string) (Output, error) {
	if len(encoded) == 0 || len(encoded) > 72<<20 {
		return Output{}, &Error{Code: "response", Message: "图像返回内容为空或超过处理限制", SubmissionUncertain: true}
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(data) == 0 || len(data) > 50<<20 {
		return Output{}, &Error{Code: "response", Message: "返回图像编码无效或超过 50MiB", SubmissionUncertain: true}
	}
	actual := http.DetectContentType(data)
	if !contains([]string{"image/png", "image/jpeg", "image/webp"}, actual) || (mime != "" && strings.ToLower(mime) != actual) {
		return Output{}, &Error{Code: "response", Message: "返回图像类型无效或与声明不符", SubmissionUncertain: true}
	}
	return Output{Kind: "image", MIME: actual, Data: data}, nil
}
