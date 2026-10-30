package generation

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

const fixtureSecret = "fixture-only-key-do-not-log"

func pngData(t *testing.T, width, height int) []byte {
	t.Helper()
	picture := image.NewRGBA(image.Rect(0, 0, width, height))
	picture.Set(0, 0, color.RGBA{R: 30, G: 100, B: 200, A: 255})
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, picture); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func fixtureConfig(server *httptest.Server) Config {
	return Config{BaseURL: server.URL, HTTPClient: server.Client(), AllowLocalTestHTTP: true, Secrets: SecretFunc(func(context.Context, string) (string, error) { return fixtureSecret, nil })}
}

func fixtureReferences(t *testing.T, data []byte) *FileReferences {
	t.Helper()
	path := filepath.Join(t.TempDir(), "参考图.png")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	source, err := NewFileReferences([]RegisteredFile{{ID: "reference", Path: path}})
	if err != nil {
		t.Fatal(err)
	}
	return source
}

func requestBody(t *testing.T, r *http.Request) map[string]any {
	t.Helper()
	var value map[string]any
	if err := json.NewDecoder(r.Body).Decode(&value); err != nil {
		t.Error(err)
	}
	return value
}

func TestOpenAITextImageUsesDeclaredJSONAndImportsRecipeWithoutSecrets(t *testing.T) {
	imageData := pngData(t, 4, 4)
	var posts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posts.Add(1)
		if r.Method != "POST" || r.URL.Path != "/images/generations" || r.Header.Get("Authorization") != "Bearer "+fixtureSecret {
			t.Errorf("错误的生成请求路径或鉴权")
		}
		body := requestBody(t, r)
		if body["model"] != "gpt-image-2.5-sunburst" || body["n"] != float64(2) || body["size"] != "1536x864" || body["quality"] != "medium" {
			t.Errorf("请求参数未正确转换：%v", body)
		}
		if _, exists := body["response_format"]; exists {
			t.Error("GPT Image 不应使用已不支持的 response_format")
		}
		json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{{"b64_json": base64.StdEncoding.EncodeToString(imageData)}, {"b64_json": base64.StdEncoding.EncodeToString(imageData)}}})
	}))
	defer server.Close()
	provider, err := NewOpenAI(fixtureConfig(server))
	if err != nil {
		t.Fatal(err)
	}
	task, err := provider.Submit(context.Background(), Request{ID: "image-test", Mode: ImageGenerate, Prompt: "一个用于讲解的蓝色图案", AspectRatio: "16:9", Count: 2, Quality: "medium"})
	if err != nil || task.Status != Succeeded || len(task.Outputs) != 2 || task.Recipe.Size != "1536x864" {
		t.Fatalf("同步图像未返回真实结果：%+v %v", task, err)
	}
	serialized, _ := json.Marshal(task)
	if bytes.Contains(serialized, []byte(fixtureSecret)) || bytes.Contains(serialized, []byte(base64.StdEncoding.EncodeToString(imageData))) {
		t.Fatal("普通任务JSON泄露凭据或大体积图像")
	}
	importer, err := NewResultImporter(ImportOptions{Directory: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	assets, err := importer.Import(context.Background(), task)
	if err != nil || len(assets) != 2 {
		t.Fatalf("结果导入失败：%v", err)
	}
	again, err := importer.Import(context.Background(), task)
	if err != nil || !reflect.DeepEqual(assets, again) || posts.Load() != 1 {
		t.Fatalf("重读生成结果发生重复请求：%v", err)
	}
	contents, err := os.ReadFile(filepath.Join(filepath.Dir(assets[0].Path), "recipe.json"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(contents, []byte(fixtureSecret)) || bytes.Contains(contents, []byte(server.URL)) || !bytes.Contains(contents, []byte(task.Recipe.ModelID)) {
		t.Fatal("配方泄露地址/凭据或丢失具体模型")
	}
	if assets[0].RecipeID != task.Recipe.ID || assets[0].SHA256 == "" {
		t.Fatal("导入素材没有可追溯配方和指纹")
	}
}

func TestOpenAIEditUsesRegisteredImageBytesNotLoopbackURL(t *testing.T) {
	imageData := pngData(t, 256, 256)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/images/edits" {
			t.Error("图片编辑没有使用 edits 接口")
		}
		body := requestBody(t, r)
		images, ok := body["images"].([]any)
		if !ok || len(images) != 1 {
			t.Error("缺少JSON编辑图像输入")
			return
		}
		value := images[0].(map[string]any)["image_url"].(string)
		if !strings.HasPrefix(value, "data:image/png;base64,") || strings.Contains(value, "127.0.0.1") {
			t.Error("将本地素材 URL 当成云端参考")
		}
		decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(value, "data:image/png;base64,"))
		if err != nil || !bytes.Equal(decoded, imageData) {
			t.Error("参考图字节发生变化")
		}
		json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{{"b64_json": base64.StdEncoding.EncodeToString(imageData)}}})
	}))
	defer server.Close()
	config := fixtureConfig(server)
	config.References = fixtureReferences(t, imageData)
	config.ModelID = "gpt-image-2.5-flare"
	provider, _ := NewOpenAI(config)
	task, err := provider.Submit(context.Background(), Request{Mode: ImageEdit, Prompt: "保留主体，调整背景", References: []Reference{{AssetID: "reference", Role: "reference_image"}}})
	if err != nil || task.Status != Succeeded || len(task.Recipe.References) != 1 || task.Recipe.References[0].SHA256 == "" {
		t.Fatalf("编辑或参考配方失败：%v", err)
	}
}

func TestGeminiTextAndReferencePayloadAndFinalImageSelection(t *testing.T) {
	imageData := pngData(t, 5, 5)
	for _, mode := range []string{ImageGenerate, ImageEdit} {
		t.Run(mode, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/models/gemini-2.5-flash-image:generateContent" || r.Header.Get("x-goog-api-key") != fixtureSecret || r.URL.Query().Get("key") != "" {
					t.Error("Gemini 接口或鉴权方式不正确")
				}
				body := requestBody(t, r)
				config := body["generationConfig"].(map[string]any)
				modalities := config["responseModalities"].([]any)
				if !reflect.DeepEqual(modalities, []any{"TEXT", "IMAGE"}) {
					t.Error("没有请求图像输出")
				}
				parts := body["contents"].([]any)[0].(map[string]any)["parts"].([]any)
				if mode == ImageEdit {
					if len(parts) != 2 || parts[1].(map[string]any)["inlineData"] == nil {
						t.Error("编辑没有携带本地图字节")
					}
				} else if len(parts) != 1 {
					t.Error("文字生成携带了意外参考")
				}
				inline := map[string]string{"mimeType": "image/png", "data": base64.StdEncoding.EncodeToString(imageData)}
				json.NewEncoder(w).Encode(map[string]any{"candidates": []any{map[string]any{"content": map[string]any{"parts": []any{map[string]any{"thought": true, "inlineData": inline}, map[string]string{"text": "最终图像"}, map[string]any{"inlineData": inline}}}}}})
			}))
			defer server.Close()
			config := fixtureConfig(server)
			config.References = fixtureReferences(t, imageData)
			provider, _ := NewGemini(config)
			request := Request{Mode: mode, Prompt: "一张清晰的说明图", AspectRatio: "16:9"}
			if mode == ImageEdit {
				request.References = []Reference{{AssetID: "reference"}}
			}
			task, err := provider.Submit(context.Background(), request)
			if err != nil || task.Status != Succeeded || len(task.Outputs) != 1 {
				t.Fatalf("Gemini 最终图像处理错误：%+v %v", task, err)
			}
		})
	}
}

func TestMiniMaxSubmitPollAndUnsupportedCancelPreserveTask(t *testing.T) {
	imageData := pngData(t, 256, 256)
	var posts, queries atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+fixtureSecret {
			t.Error("H3 鉴权缺失")
		}
		if r.Method == http.MethodPost {
			posts.Add(1)
			if r.URL.Path != "/v2/video_generation" {
				t.Error("H3 创建路径不正确")
			}
			body := requestBody(t, r)
			if body["ratio"] != "adaptive" || body["duration"] != float64(5) || body["resolution"] != "768P" {
				t.Errorf("H3 参数未按模式转换：%v", body)
			}
			content := body["content"].([]any)
			if len(content) != 3 || content[1].(map[string]any)["role"] != "first_frame" || content[2].(map[string]any)["role"] != "last_frame" {
				t.Error("首尾帧用途错误")
			}
			io.WriteString(w, `{"task_id":"h3-fixture-task"}`)
			return
		}
		if r.Method != http.MethodGet || r.URL.Path != "/v2/query/video_generation/h3-fixture-task" {
			t.Error("H3 查询重新生成或使用错误地址")
		}
		if queries.Add(1) == 1 {
			io.WriteString(w, `{"task":{"id":"h3-fixture-task","model":"MiniMax-H3","status":"running"}}`)
		} else {
			io.WriteString(w, `{"task":{"id":"h3-fixture-task","model":"MiniMax-H3","status":"succeeded","content":{"url":"https://cdn.example.com/result.mp4"}}}`)
		}
	}))
	defer server.Close()
	config := fixtureConfig(server)
	config.References = fixtureReferences(t, imageData)
	provider, _ := NewMiniMax(config)
	task, err := provider.Submit(context.Background(), Request{ID: "h3-test", Mode: VideoImage, Prompt: "保持主体缓慢推进", References: []Reference{{AssetID: "reference", Role: "first_frame"}, {AssetID: "reference", Role: "last_frame"}}})
	if err != nil || task.Status != Queued || task.RemoteID != "h3-fixture-task" {
		t.Fatalf("没有获得真实远端任务ID：%+v %v", task, err)
	}
	task, err = provider.Get(context.Background(), task)
	if err != nil || task.Status != Running {
		t.Fatalf("查询未保持运行状态：%v", err)
	}
	task, err = provider.Get(context.Background(), task)
	if err != nil || task.Status != Succeeded || len(task.Outputs) != 1 {
		t.Fatalf("查询完成结果缺失：%v", err)
	}
	unchanged, err := provider.Cancel(context.Background(), task)
	if err == nil || unchanged.Status != Succeeded || posts.Load() != 1 || queries.Load() != 2 {
		t.Fatal("不支持的取消伪造了成功或重新提交")
	}
}

func TestProviderValidationRejectsUnsupportedAndMutuallyExclusiveParameters(t *testing.T) {
	openai, _ := NewOpenAI(Config{})
	google, _ := NewGemini(Config{})
	minimax, _ := NewMiniMax(Config{})
	cases := []struct {
		name     string
		provider Provider
		request  Request
	}{
		{"透明JPEG", openai, Request{Mode: ImageGenerate, Prompt: "图", Background: "transparent", OutputFormat: "jpeg"}},
		{"画幅冲突", openai, Request{Mode: ImageGenerate, Prompt: "图", Size: "1024x1024", AspectRatio: "16:9"}},
		{"编辑缺图", openai, Request{Mode: ImageEdit, Prompt: "图"}},
		{"Google未知质量控制", google, Request{Mode: ImageGenerate, Prompt: "图", Quality: "high"}},
		{"Google未知批量", google, Request{Mode: ImageGenerate, Prompt: "图", Count: 2}},
		{"H3互斥参考", minimax, Request{Mode: VideoImage, Prompt: "视频", References: []Reference{{AssetID: "a", Role: "first_frame"}, {AssetID: "b", Role: "reference_image"}}}},
		{"H3未验证视频参考", minimax, Request{Mode: VideoReference, Prompt: "视频", References: []Reference{{AssetID: "a", Role: "reference_video"}}}},
		{"H3首帧画幅被静默忽略", minimax, Request{Mode: VideoImage, Prompt: "视频", AspectRatio: "16:9", References: []Reference{{AssetID: "a", Role: "first_frame"}}}},
		{"H3文本adaptive", minimax, Request{Mode: VideoText, Prompt: "视频", AspectRatio: "adaptive"}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if err := test.provider.Validate(test.request); err == nil {
				t.Fatal("非法组合被静默接受")
			}
		})
	}
	highspeed, _ := NewMiniMax(Config{ModelID: "MiniMax-H3-Highspeed"})
	if err := highspeed.Validate(Request{Mode: VideoText, Prompt: "视频", Size: "2K"}); err == nil {
		t.Fatal("不同H3版本能力被抹平")
	}
	maxModel, _ := NewMiniMax(Config{ModelID: "MiniMax-H3-Max"})
	if err := maxModel.Validate(Request{Mode: VideoText, Prompt: "视频", DurationSeconds: 4}); err == nil {
		t.Fatal("H3 Max 接受了不支持的4秒")
	}
}

func TestUnknownSubmissionDoesNotRetryOrLeakProviderError(t *testing.T) {
	var count atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count.Add(1)
		w.WriteHeader(503)
		io.WriteString(w, `{"error":"`+fixtureSecret+` provider internal message"}`)
	}))
	defer server.Close()
	provider, _ := NewOpenAI(fixtureConfig(server))
	task, err := provider.Submit(context.Background(), Request{ID: "uncertain", Mode: ImageGenerate, Prompt: "图"})
	if err == nil || !task.SubmissionUncertain || task.Status != Unknown || strings.Contains(err.Error(), fixtureSecret) || count.Load() != 1 {
		t.Fatalf("未知提交被重试或错误泄漏：%v", err)
	}
	_, _ = provider.Get(context.Background(), task)
	if count.Load() != 1 {
		t.Fatal("Get 将未知同步任务重新提交")
	}
}

func TestTransportFailureAfterAcceptedRequestIsUncertain(t *testing.T) {
	var count atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count.Add(1)
		io.Copy(io.Discard, r.Body)
		connection, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		connection.Close()
	}))
	defer server.Close()
	provider, _ := NewGemini(fixtureConfig(server))
	task, err := provider.Submit(context.Background(), Request{Mode: ImageGenerate, Prompt: "图"})
	if err == nil || task.Status != Unknown || !task.SubmissionUncertain || count.Load() != 1 {
		t.Fatal("连接中断后伪报明确失败或自动重复生成")
	}
}

func TestRegistryKeepsFourFamiliesWithUnverifiedAccounts(t *testing.T) {
	one, _ := NewOpenAI(Config{})
	two, _ := NewGemini(Config{})
	three, _ := NewMiniMax(Config{})
	four, _ := NewSeedance(Config{})
	registry, err := NewRegistry(one, two, three, four)
	if err != nil || len(registry.Capabilities()) != 4 {
		t.Fatal("能力目录丢失命名模型家族")
	}
	if four.Capabilities()[0].NeedsModelConfiguration || four.Capabilities()[0].AccountVerified {
		t.Fatal("Seedance 协议能力与账户可用性混淆")
	}
	if _, err := four.Submit(context.Background(), Request{Mode: VideoText, Prompt: "视频"}); err == nil {
		t.Fatal("缺少凭据的 Seedance 伪造提交成功")
	}
	if _, err := NewSeedance(Config{ModelID: "unverified-model"}); err == nil {
		t.Fatal("未知 Seedance 型号继承参数能力")
	}
	if _, err := NewOpenAI(Config{ModelID: "gpt-image-1"}); err == nil {
		t.Fatal("已停用模型仍被默认接入")
	}
	if _, err := NewGemini(Config{ModelID: "unverified-nano-banana"}); err == nil {
		t.Fatal("未知模型沿用旧接口参数")
	}
	if _, err := NewOpenAI(Config{BaseURL: "https://user:secret@api.example.com"}); err == nil {
		t.Fatal("地址中允许嵌入凭据")
	}
	if _, err := NewOpenAI(Config{BaseURL: "http://127.0.0.1:1234"}); err == nil {
		t.Fatal("正式配置默认允许本地HTTP")
	}
}

type catalogExtension struct {
	Provider
	values []Capability
}

func (p catalogExtension) Capabilities() []Capability { return p.values }

func TestRegistrySupportsExtensionsAndRejectsBrokenProviders(t *testing.T) {
	one, _ := NewOpenAI(Config{})
	extension := catalogExtension{Provider: one, values: []Capability{{ProviderID: "future-provider", ModelID: "explicit-model", Family: "扩展模型", MediaKind: "image", Modes: []string{ImageGenerate}}}}
	registry, err := NewRegistry(one, extension)
	if err != nil || len(registry.Capabilities()) != 2 || registry.Capabilities()[1].ProviderID != "future-provider" {
		t.Fatal("目录将新适配器限制成固定四家")
	}
	var typedNil *OpenAIProvider
	if _, err := NewRegistry(typedNil); err == nil {
		t.Fatal("typed nil适配器未明确返回错误")
	}
	if _, err := NewRegistry(one, one); err == nil {
		t.Fatal("重复服务标识未被拒绝")
	}
	broken := extension
	broken.values = append(append([]Capability{}, extension.values...), Capability{ProviderID: "different-provider"})
	if _, err := NewRegistry(broken); err == nil {
		t.Fatal("同适配器返回多个服务身份未被拒绝")
	}
}

func TestRequestSizeCheckPrecedesCredentialReadAndSecretErrorsAreSanitized(t *testing.T) {
	var secretReads atomic.Int32
	provider, _ := NewGemini(Config{Secrets: SecretFunc(func(context.Context, string) (string, error) {
		secretReads.Add(1)
		return "", fmt.Errorf("vault error with %s", fixtureSecret)
	})})
	var response struct{}
	err := provider.request(context.Background(), http.MethodPost, "/not-sent", map[string]string{"blob": strings.Repeat("x", 20_000_000)}, &response)
	if value, ok := err.(*Error); !ok || value.Code != "reference_size" || secretReads.Load() != 0 {
		t.Fatal("超过内联限制后仍读取凭据或尝试提交")
	}
	err = provider.request(context.Background(), http.MethodPost, "/not-sent", map[string]string{"text": "small"}, &response)
	if err == nil || strings.Contains(err.Error(), fixtureSecret) || secretReads.Load() != 1 {
		t.Fatal("凭据来源错误泄露了内部信息")
	}
}
