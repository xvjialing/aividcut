package generation

import (
	"context"
	"fmt"
	"net/http"
	"reflect"
	"time"
)

const (
	OpenAI   = "openai"
	Google   = "google"
	MiniMax  = "minimax"
	Seedance = "seedance"

	ImageGenerate  = "image.generate"
	ImageEdit      = "image.edit"
	VideoText      = "video.text"
	VideoImage     = "video.image"
	VideoReference = "video.reference"

	Queued    = "queued"
	Running   = "running"
	Succeeded = "succeeded"
	Failed    = "failed"
	Cancelled = "cancelled"
	Unknown   = "unknown"
)

type Capability struct {
	ProviderID              string              `json:"providerId"`
	Family                  string              `json:"family"`
	ModelID                 string              `json:"modelId"`
	MediaKind               string              `json:"mediaKind"`
	Modes                   []string            `json:"modes"`
	Sizes                   []string            `json:"sizes,omitempty"`
	DefaultSize             string              `json:"defaultSize,omitempty"`
	ModeAspectRatios        map[string][]string `json:"modeAspectRatios,omitempty"`
	RequiresFirstFrame      bool                `json:"requiresFirstFrame,omitempty"`
	SupportsAudioGeneration bool                `json:"supportsAudioGeneration,omitempty"`
	DefaultGenerateAudio    bool                `json:"defaultGenerateAudio,omitempty"`
	AspectRatios            []string            `json:"aspectRatios,omitempty"`
	Qualities               []string            `json:"qualities,omitempty"`
	OutputFormats           []string            `json:"outputFormats,omitempty"`
	MaxReferences           int                 `json:"maxReferences"`
	MaxRequestBytes         int64               `json:"maxRequestBytes,omitempty"`
	MaxCount                int                 `json:"maxCount"`
	MinDurationSeconds      int                 `json:"minDurationSeconds,omitempty"`
	MaxDurationSeconds      int                 `json:"maxDurationSeconds,omitempty"`
	Async                   bool                `json:"async"`
	SupportsCancel          bool                `json:"supportsCancel"`
	NeedsModelConfiguration bool                `json:"needsModelConfiguration"`
	AccountVerified         bool                `json:"accountVerified"`
	Note                    string              `json:"note"`
	SourceURL               string              `json:"sourceUrl"`
}

type Reference struct {
	AssetID string `json:"assetId"`
	Role    string `json:"role"`
}

type Request struct {
	ID              string      `json:"id"`
	ProviderID      string      `json:"providerId"`
	ModelID         string      `json:"modelId"`
	Mode            string      `json:"mode"`
	Prompt          string      `json:"prompt"`
	Size            string      `json:"size,omitempty"`
	AspectRatio     string      `json:"aspectRatio,omitempty"`
	GenerateAudio   *bool       `json:"generateAudio,omitempty"`
	DurationSeconds int         `json:"durationSeconds,omitempty"`
	Quality         string      `json:"quality,omitempty"`
	OutputFormat    string      `json:"outputFormat,omitempty"`
	Background      string      `json:"background,omitempty"`
	Count           int         `json:"count,omitempty"`
	References      []Reference `json:"references,omitempty"`
	ParentRecipeID  string      `json:"parentRecipeId,omitempty"`
}

type ReferenceData struct {
	AssetID string
	Role    string
	MIME    string
	Data    []byte
	SHA256  string
	Width   int
	Height  int
}

// 只由应用层提供凭据和已登记素材，不在请求/工程/配方中携带密钥。
type SecretSource interface {
	Secret(context.Context, string) (string, error)
}

type SecretFunc func(context.Context, string) (string, error)

func (f SecretFunc) Secret(ctx context.Context, provider string) (string, error) {
	return f(ctx, provider)
}

type ReferenceSource interface {
	Read(context.Context, Reference) (ReferenceData, error)
}

type ReferenceRecipe struct {
	AssetID string `json:"assetId"`
	Role    string `json:"role"`
	MIME    string `json:"mime"`
	SHA256  string `json:"sha256"`
}

type Recipe struct {
	ID              string            `json:"id"`
	ParentRecipeID  string            `json:"parentRecipeId,omitempty"`
	ProviderID      string            `json:"providerId"`
	ModelID         string            `json:"modelId"`
	Mode            string            `json:"mode"`
	Prompt          string            `json:"prompt"`
	Size            string            `json:"size,omitempty"`
	AspectRatio     string            `json:"aspectRatio,omitempty"`
	GenerateAudio   *bool             `json:"generateAudio,omitempty"`
	DurationSeconds int               `json:"durationSeconds,omitempty"`
	Quality         string            `json:"quality,omitempty"`
	OutputFormat    string            `json:"outputFormat,omitempty"`
	Background      string            `json:"background,omitempty"`
	Count           int               `json:"count"`
	References      []ReferenceRecipe `json:"references"`
	CreatedAt       time.Time         `json:"createdAt"`
}

type Output struct {
	Kind string `json:"kind"`
	MIME string `json:"mime"`
	// 临时下载地址可能带签名；它与大体积数据不进入工程或普通 UI JSON。
	URL  string `json:"-"`
	Data []byte `json:"-"`
}

type Task struct {
	ID                  string   `json:"id"`
	ProviderID          string   `json:"providerId"`
	ModelID             string   `json:"modelId"`
	RemoteID            string   `json:"remoteId,omitempty"`
	Status              string   `json:"status"`
	SubmissionUncertain bool     `json:"submissionUncertain"`
	Outputs             []Output `json:"outputs"`
	Recipe              Recipe   `json:"recipe"`
}

type Provider interface {
	Capabilities() []Capability
	Validate(Request) error
	Submit(context.Context, Request) (Task, error)
	Get(context.Context, Task) (Task, error)
	Cancel(context.Context, Task) (Task, error)
}

type Config struct {
	ModelID    string
	BaseURL    string
	HTTPClient *http.Client    `json:"-"`
	Secrets    SecretSource    `json:"-"`
	References ReferenceSource `json:"-"`
	// 仅供显式 HTTP fixture 或本地开发代理使用；不得从普通项目文件恢复此选项。
	AllowLocalTestHTTP bool `json:"-"`
}

type Error struct {
	Code                string `json:"code"`
	Message             string `json:"message"`
	HTTPStatus          int    `json:"httpStatus,omitempty"`
	SubmissionUncertain bool   `json:"submissionUncertain"`
}

func (e *Error) Error() string { return e.Message }

func failure(code, message string) error { return &Error{Code: code, Message: message} }

type Registry struct {
	providers map[string]Provider
	order     []string
}

func NewRegistry(providers ...Provider) (*Registry, error) {
	registry := &Registry{providers: make(map[string]Provider)}
	for _, provider := range providers {
		if provider == nil || (reflect.ValueOf(provider).Kind() == reflect.Ptr && reflect.ValueOf(provider).IsNil()) {
			return nil, failure("provider", "生成适配器没有能力声明")
		}
		capabilities := provider.Capabilities()
		if len(capabilities) == 0 {
			return nil, failure("provider", "生成适配器没有能力声明")
		}
		id := capabilities[0].ProviderID
		if _, exists := registry.providers[id]; exists || id == "" {
			return nil, failure("provider", "生成适配器标识为空或重复")
		}
		for _, capability := range capabilities {
			if capability.ProviderID != id {
				return nil, failure("provider", "单个适配器的服务标识不一致")
			}
		}
		registry.providers[id] = provider
		registry.order = append(registry.order, id)
	}
	return registry, nil
}

func (r *Registry) Provider(id string) (Provider, error) {
	if p, exists := r.providers[id]; exists {
		return p, nil
	}
	return nil, failure("provider", fmt.Sprintf("生成服务未配置：%s", id))
}

func (r *Registry) Capabilities() []Capability {
	result := []Capability{}
	for _, id := range r.order {
		if p, exists := r.providers[id]; exists {
			result = append(result, p.Capabilities()...)
		}
	}
	return result
}

func (r *Registry) Validate(request Request) error {
	p, err := r.Provider(request.ProviderID)
	if err != nil {
		return err
	}
	return p.Validate(request)
}
