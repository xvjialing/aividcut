package generation

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

const maxJSONBytes int64 = 64 << 20

var safeID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,160}$`)

type base struct {
	id, model, endpoint string
	client              *http.Client
	secrets             SecretSource
	references          ReferenceSource
	localTest           bool
}

func newBase(id, model, endpoint string, cfg Config) (base, error) {
	if cfg.ModelID != "" {
		model = cfg.ModelID
	}
	if cfg.BaseURL != "" {
		endpoint = cfg.BaseURL
	}
	if err := validateURL(endpoint, cfg.AllowLocalTestHTTP); err != nil {
		return base{}, err
	}
	client := &http.Client{Timeout: 180 * time.Second}
	if cfg.HTTPClient != nil {
		copy := *cfg.HTTPClient
		client = &copy
	}
	if client.Timeout <= 0 {
		client.Timeout = 180 * time.Second
	}
	// 不让鉴权重定向到其他服务，也不自动重试可能已计费的 POST。
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return base{id: id, model: model, endpoint: strings.TrimRight(endpoint, "/"), client: client, secrets: cfg.Secrets, references: cfg.References, localTest: cfg.AllowLocalTestHTTP}, nil
}

func validateURL(raw string, allowLocal bool) error {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return failure("endpoint", "生成服务地址无效或包含不允许的凭据/查询参数")
	}
	host := strings.ToLower(u.Hostname())
	ip := net.ParseIP(host)
	local := host == "localhost" || strings.HasSuffix(host, ".localhost") || (ip != nil && (ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified()))
	testLoopback := host == "localhost" || (ip != nil && ip.IsLoopback())
	if allowLocal && testLoopback && (u.Scheme == "http" || u.Scheme == "https") {
		return nil
	}
	if u.Scheme != "https" || local || strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".internal") {
		return failure("endpoint", "正式生成服务需要非本地的 HTTPS 地址")
	}
	return nil
}

func (b base) common(request Request, modes ...string) error {
	if request.ProviderID != "" && request.ProviderID != b.id {
		return failure("provider", "请求的服务与适配器不一致")
	}
	if request.ModelID != "" && request.ModelID != b.model {
		return failure("model", "请求模型不在当前适配器能力中")
	}
	if request.ID != "" && !safeID.MatchString(request.ID) {
		return failure("request_id", "生成请求标识无效")
	}
	if request.ParentRecipeID != "" && !safeID.MatchString(request.ParentRecipeID) {
		return failure("recipe", "父配方标识无效")
	}
	if !contains(modes, request.Mode) {
		return failure("mode", "当前模型适配器不支持此生成任务")
	}
	if !utf8.ValidString(request.Prompt) || strings.TrimSpace(request.Prompt) == "" || utf8.RuneCountInString(request.Prompt) > 32000 {
		return failure("prompt", "需要非空且不超过 32000 字符的生成描述")
	}
	for _, ref := range request.References {
		if ref.AssetID == "" {
			return failure("reference", "参考素材缺少已登记标识")
		}
	}
	return nil
}

func contains(values []string, value string) bool {
	for _, candidate := range values {
		if value == candidate {
			return true
		}
	}
	return false
}

func (b base) inputs(ctx context.Context, request Request, max int) ([]ReferenceData, error) {
	if len(request.References) > max {
		return nil, failure("reference_count", "参考素材数量超过当前适配器限制")
	}
	if len(request.References) > 0 && b.references == nil {
		return nil, failure("reference_scope", "尚未配置受限素材读取器")
	}
	values := make([]ReferenceData, 0, len(request.References))
	var bytes int64
	for _, reference := range request.References {
		value, err := b.references.Read(ctx, reference)
		if err != nil {
			return nil, failure("reference", "参考素材校验失败，请检查已授权文件、类型、尺寸和内容是否变化")
		}
		if value.AssetID != reference.AssetID {
			return nil, failure("reference_type", "参考读取器没有返回可用的 PNG/JPEG 图像")
		}
		value, err = checkedReference(value)
		if err != nil {
			return nil, err
		}
		value.Role = reference.Role
		bytes += int64(len(value.Data))
		if bytes > 45<<20 {
			return nil, failure("reference_size", "本轮参考数据总量不能超过 45MiB")
		}
		values = append(values, value)
	}
	return values, nil
}

func (b base) task(request Request, inputs []ReferenceData) (Task, error) {
	id := request.ID
	if id == "" {
		var data [16]byte
		if _, err := rand.Read(data[:]); err != nil {
			return Task{}, failure("request_id", "无法创建生成请求标识")
		}
		id = "gen-" + hex.EncodeToString(data[:])
	}
	count := request.Count
	if count == 0 {
		count = 1
	}
	refs := make([]ReferenceRecipe, 0, len(inputs))
	for _, input := range inputs {
		refs = append(refs, ReferenceRecipe{AssetID: input.AssetID, Role: input.Role, MIME: input.MIME, SHA256: input.SHA256})
	}
	recipe := Recipe{ID: id, ParentRecipeID: request.ParentRecipeID, ProviderID: b.id, ModelID: b.model, Mode: request.Mode, Prompt: request.Prompt, Size: request.Size, AspectRatio: request.AspectRatio, DurationSeconds: request.DurationSeconds, GenerateAudio: cloneBool(request.GenerateAudio), Quality: request.Quality, OutputFormat: request.OutputFormat, Background: request.Background, Count: count, References: refs, CreatedAt: time.Now().UTC()}
	return Task{ID: id, ProviderID: b.id, ModelID: b.model, Status: Queued, Recipe: recipe, Outputs: []Output{}}, nil
}

func (b base) request(ctx context.Context, method, path string, body any, target any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	var data []byte
	if body != nil {
		var err error
		data, err = json.Marshal(body)
		if err != nil {
			return failure("request", "无法编码生成请求")
		}
		limit := maxJSONBytes
		if b.id == Google {
			limit = 20_000_000
		}
		if b.id == MiniMax || b.id == Seedance {
			limit = 64_000_000
		}
		if int64(len(data)) > limit {
			return failure("reference_size", "参考素材与文本超过当前接口允许的请求体大小限制")
		}
	}
	if b.secrets == nil {
		return failure("credentials", "尚未配置生成服务凭据")
	}
	key, err := b.secrets.Secret(ctx, b.id)
	if err != nil || strings.TrimSpace(key) == "" || strings.ContainsAny(key, "\r\n") {
		return failure("credentials", "生成服务凭据不可用，请在连接设置中检查")
	}
	key = strings.TrimSpace(key)
	req, err := http.NewRequestWithContext(ctx, method, b.endpoint+path, bytes.NewReader(data))
	if err != nil {
		return failure("request", "无法创建生成服务请求")
	}
	if b.id == Google {
		req.Header.Set("x-goog-api-key", key)
	} else {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	req.Header.Set("Content-Type", "application/json")
	response, err := b.client.Do(req)
	if err != nil {
		return &Error{Code: "transport", Message: "生成服务连接中断；提交结果可能未知，请检查原任务，不要自动重复提交", SubmissionUncertain: method == http.MethodPost}
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return &Error{Code: "provider_http", Message: "生成服务返回错误，请检查连接、权限、余额和参数；未自动重试", HTTPStatus: response.StatusCode, SubmissionUncertain: method == http.MethodPost && (response.StatusCode >= 500 || response.StatusCode == 408 || response.StatusCode == 409 || (response.StatusCode >= 300 && response.StatusCode < 400))}
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxJSONBytes+1))
	if err != nil || int64(len(raw)) > maxJSONBytes {
		return &Error{Code: "response", Message: "生成结果读取失败或超过限制，请检查原任务，不要重复生成", SubmissionUncertain: method == http.MethodPost}
	}
	if err := json.Unmarshal(raw, target); err != nil {
		return &Error{Code: "response", Message: "生成服务返回了无法识别的结果，请核对原任务", SubmissionUncertain: method == http.MethodPost}
	}
	return nil
}

func failTask(task Task, err error) (Task, error) {
	task.Status = Failed
	if value, ok := err.(*Error); ok && value.SubmissionUncertain {
		task.Status = Unknown
		task.SubmissionUncertain = true
	}
	return task, err
}

func (b base) checkTask(task Task) error {
	if task.ProviderID != b.id || task.ModelID != b.model {
		return failure("task", "任务不属于当前模型连接")
	}
	return nil
}
func unsupportedCancel() error {
	return failure("cancel_unsupported", "当前适配器没有已确认的远端取消接口；停止本地等待不等于取消远端任务")
}

// 配方持有值快照，避免调用方后续修改指针改变已核对参数。
func cloneBool(value *bool) *bool {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
