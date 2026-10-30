package generation

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestSeedanceModesBodyAndImmutableRecipe(t *testing.T) {
	for _, mode := range []string{VideoText, VideoImage, VideoReference} {
		t.Run(mode, func(t *testing.T) {
			var posts atomic.Int32
			audio := false
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				posts.Add(1)
				if r.Method != "POST" || r.URL.Path != seedanceTasksPath || r.Header.Get("Authorization") != "Bearer "+fixtureSecret {
					t.Error("提交路径或鉴权错误")
				}
				body := requestBody(t, r)
				if body["model"] != Seedance25Model || body["duration"] != float64(5) || body["resolution"] != "720p" || body["generate_audio"] != false || body["output_format"] != "mp4" {
					t.Errorf("默认参数或声音选择不一致: %v", body)
				}
				content := body["content"].([]any)
				if content[0].(map[string]any)["text"] != "城市镜头" {
					t.Error("描述未传递")
				}
				if mode == VideoText {
					if len(content) != 1 || body["ratio"] != "16:9" {
						t.Error("文生视频夹带参考")
					}
				} else {
					image := content[1].(map[string]any)
					role := "reference_image"
					if mode == VideoImage {
						role = "first_frame"
						if body["ratio"] != "adaptive" {
							t.Error("首帧模式未自动画幅")
						}
					}
					if image["role"] != role || image["type"] != "image_url" || !strings.HasPrefix(image["image_url"].(map[string]any)["url"].(string), "data:image/png;base64,") {
						t.Error("参考用途或数据错误")
					}
					if mode == VideoReference && body["omni_reference_task_type"] != "reference" {
						t.Error("未固定参考任务，可能误判为编辑")
					}
				}
				fmt.Fprint(w, `{"id":"cgt-fixture"}`)
			}))
			defer server.Close()
			cfg := fixtureConfig(server)
			cfg.References = fixtureReferences(t, pngData(t, 300, 300))
			p, err := NewSeedance(cfg)
			if err != nil {
				t.Fatal(err)
			}
			request := Request{ID: "seedance-test", Mode: mode, Prompt: "城市镜头", GenerateAudio: &audio}
			if mode != VideoText {
				role := "reference_image"
				if mode == VideoImage {
					role = "first_frame"
				}
				request.References = []Reference{{AssetID: "reference", Role: role}}
			}
			task, err := p.Submit(context.Background(), request)
			if err != nil || task.Status != Queued || task.RemoteID != "cgt-fixture" || posts.Load() != 1 {
				t.Fatalf("未正确提交: %+v %v", task, err)
			}
			audio = true
			if task.Recipe.GenerateAudio == nil || *task.Recipe.GenerateAudio {
				t.Fatal("配方被调用方修改")
			}
			raw, _ := json.Marshal(task)
			if strings.Contains(string(raw), fixtureSecret) || strings.Contains(string(raw), "data:image/") {
				t.Fatal("任务泄露密钥或大体积参考")
			}
		})
	}
}

func TestSeedanceValidationAndImagePreflight(t *testing.T) {
	p, _ := NewSeedance(Config{})
	invalid := []Request{
		{Mode: VideoImage, References: []Reference{{AssetID: "reference", Role: "last_frame"}}},
		{Mode: VideoImage, AspectRatio: "16:9", References: []Reference{{AssetID: "reference", Role: "first_frame"}}},
		{Mode: VideoText, References: []Reference{{AssetID: "reference", Role: "reference_image"}}},
		{Mode: VideoReference, References: []Reference{{AssetID: "reference", Role: "first_frame"}}},
		{Mode: VideoText, DurationSeconds: 31}, {Mode: VideoText, DurationSeconds: 3}, {Mode: VideoText, DurationSeconds: -1},
		{Mode: VideoText, OutputFormat: "mov"}, {Mode: VideoText, Count: 2}, {Mode: VideoText, Size: "4k"},
	}
	for _, r := range invalid {
		r.Prompt = "镜头"
		if p.Validate(r) == nil {
			t.Fatalf("非法请求被接受: %+v", r)
		}
	}
	for _, seconds := range []int{4, 30} {
		if err := p.Validate(Request{Mode: VideoText, Prompt: "镜头", DurationSeconds: seconds, AspectRatio: "adaptive"}); err != nil {
			t.Fatal(err)
		}
	}
	many := Request{Mode: VideoReference, Prompt: "镜头"}
	for i := 0; i < 31; i++ {
		many.References = append(many.References, Reference{AssetID: "reference", Role: "reference_image"})
	}
	if p.Validate(many) == nil {
		t.Fatal("超过30参考仍接受")
	}
	for _, size := range [][2]int{{299, 300}, {300, 299}, {300, 751}} {
		cfg := Config{References: fixtureReferences(t, pngData(t, size[0], size[1]))}
		p, _ := NewSeedance(cfg)
		_, err := p.Submit(context.Background(), Request{Mode: VideoImage, Prompt: "镜头", References: []Reference{{AssetID: "reference", Role: "first_frame"}}})
		if e, ok := err.(*Error); !ok || e.Code != "reference_image" {
			t.Fatalf("图片未在读取密钥和联网前被拒绝: %v", err)
		}
	}
	for _, newProvider := range []func(Config) (Provider, error){func(c Config) (Provider, error) { return NewOpenAI(c) }, func(c Config) (Provider, error) { return NewGemini(c) }, func(c Config) (Provider, error) { return NewMiniMax(c) }} {
		other, _ := newProvider(Config{})
		mode := other.Capabilities()[0].Modes[0]
		audio := false
		if other.Validate(Request{Mode: mode, Prompt: "镜头", GenerateAudio: &audio}) == nil {
			t.Fatal("未声明声音能力的模型静默忽略声音字段")
		}
	}
}

func TestSeedancePollingStatesIdentityAndNoResubmit(t *testing.T) {
	cases := []struct {
		status, model, id, url, format, want string
		bad                                  bool
	}{
		{"queued", Seedance25Model, "remote", "", "", Queued, false},
		{"running", Seedance25Model, "remote", "", "", Running, false},
		{"succeeded", Seedance25Model, "remote", "https://cdn.example.com/video.mp4?signature=private", "mp4", Succeeded, false},
		{"expired", Seedance25Model, "remote", "", "", Failed, false},
		{"failed", Seedance25Model, "remote", "", "", Failed, false},
		{"cancelled", Seedance25Model, "remote", "", "", Cancelled, false},
		{"mystery", Seedance25Model, "remote", "", "", "", true},
		{"succeeded", Seedance25Model, "remote", "", "mp4", "", true},
		{"succeeded", Seedance25Model, "remote", "http://127.0.0.1/private", "mp4", "", true},
		{"succeeded", Seedance25Model, "remote", "https://cdn.example.com/a.mov", "mov", "", true},
		{"queued", "different", "remote", "", "", "", true},
		{"queued", Seedance25Model, "different", "", "", "", true},
	}
	for _, c := range cases {
		t.Run(c.status+c.id+c.format+c.model+c.url, func(t *testing.T) {
			var gets atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gets.Add(1)
				if r.Method != "GET" || r.URL.Path != seedanceTasksPath+"/remote" {
					t.Error("恢复查询变成重交或错误路径")
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"id": c.id, "model": c.model, "status": c.status, "output_format": c.format, "content": map[string]string{"video_url": c.url}})
			}))
			defer server.Close()
			cfg := fixtureConfig(server)
			// 测试服务可为HTTP，结果URL仍按正式下载策略验证。
			p, _ := NewSeedance(cfg)
			p.localTest = false
			original := Task{ID: "local", ProviderID: Seedance, ModelID: Seedance25Model, RemoteID: "remote", Status: Running}
			task, err := p.Get(context.Background(), original)
			if (err != nil) != c.bad || (!c.bad && task.Status != c.want) || gets.Load() != 1 {
				t.Fatalf("状态处理错误: %+v %v", task, err)
			}
			if c.bad && task.Status != original.Status {
				t.Fatal("无效响应改变原状态")
			}
			if c.want == Succeeded {
				if len(task.Outputs) != 1 || task.Outputs[0].MIME != "video/mp4" {
					t.Fatal("结果未映射")
				}
				raw, _ := json.Marshal(task)
				if strings.Contains(string(raw), "signature") {
					t.Fatal("签名地址进入普通任务JSON")
				}
			}
			if _, err := p.Cancel(context.Background(), task); err == nil {
				t.Fatal("冒充支持远端取消")
			}
		})
	}
}

func TestSeedanceUncertainSubmitNeverRetries(t *testing.T) {
	for _, response := range []string{`{}`, `{"id":"../unsafe"}`, `not-json`} {
		t.Run(response, func(t *testing.T) {
			var posts atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { posts.Add(1); fmt.Fprint(w, response) }))
			defer server.Close()
			p, _ := NewSeedance(fixtureConfig(server))
			task, err := p.Submit(context.Background(), Request{Mode: VideoText, Prompt: "镜头"})
			if err == nil || task.Status != Unknown || !task.SubmissionUncertain || posts.Load() != 1 {
				t.Fatal("不确定提交被重交或冒充失败")
			}
			if _, err = p.Get(context.Background(), task); err == nil || posts.Load() != 1 {
				t.Fatal("无ID恢复重新POST")
			}
		})
	}
}
