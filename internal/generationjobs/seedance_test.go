package generationjobs

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"aividcut/internal/editor"
	"aividcut/internal/generation"
)

// 使用真实适配器与持久任务，仅连接回环服务；视频为工程内可完整解码的固定夹具。
func TestSeedanceRestartResumesOriginalIDAndPersistsVideoRecipe(t *testing.T) {
	var posts, gets, downloads atomic.Int32
	var finish atomic.Bool
	video, err := os.ReadFile("testdata/seedance-result.mp4")
	if err != nil {
		t.Fatal(err)
	}
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "POST" && r.URL.Path == "/api/v3/contents/generations/tasks":
			posts.Add(1)
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["generate_audio"] != false || r.Header.Get("Authorization") != "Bearer fixture-secret" {
				t.Error("声音选择或惰性凭据未传递")
			}
			fmt.Fprint(w, `{"id":"seedance-remote-original"}`)
		case r.Method == "GET" && r.URL.Path == "/api/v3/contents/generations/tasks/seedance-remote-original":
			gets.Add(1)
			status := "running"
			if finish.Load() {
				status = "succeeded"
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "seedance-remote-original", "model": generation.Seedance25Model, "status": status, "output_format": "mp4", "content": map[string]string{"video_url": server.URL + "/result.mp4?signature=fixture-private"}})
		case r.Method == "GET" && r.URL.Path == "/result.mp4":
			downloads.Add(1)
			if r.Header.Get("Authorization") != "" {
				t.Error("下载泄露供应商凭据")
			}
			w.Header().Set("Content-Type", "video/mp4")
			_, _ = w.Write(video)
		default:
			t.Errorf("意外请求 %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	dir := t.TempDir()
	importer, err := generation.NewResultImporter(generation.ImportOptions{Directory: filepath.Join(dir, "assets"), HTTPClient: server.Client(), AllowLocalTestHTTP: true})
	if err != nil {
		t.Fatal(err)
	}
	options := Options{Directory: dir, Importer: importer, PollInterval: time.Millisecond, TaskTimeout: time.Second, Resolve: func(context.Context, string) (Binding, error) {
		return Binding{ProviderID: generation.Seedance, ModelID: generation.Seedance25Model, Secrets: generation.SecretFunc(func(context.Context, string) (string, error) { return "fixture-secret", nil })}, nil
	}, Factory: func(_ string, model string, secrets generation.SecretSource, refs generation.ReferenceSource) (generation.Provider, error) {
		return generation.NewSeedance(generation.Config{ModelID: model, BaseURL: server.URL, HTTPClient: server.Client(), AllowLocalTestHTTP: true, Secrets: secrets, References: refs})
	}}
	m, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	audio := false
	input := SubmitInput{ConnectionID: "fixture", Consent: false, Request: generation.Request{ID: "seedance-resume", ProviderID: generation.Seedance, ModelID: generation.Seedance25Model, Mode: generation.VideoText, Prompt: "蓝色画面", Size: "720p", DurationSeconds: 5, GenerateAudio: &audio}}
	project := editor.NewProject("Seedance恢复")
	if _, err = m.Submit(context.Background(), project, input); err == nil || posts.Load() != 0 {
		t.Fatal("未经确认提交")
	}
	input.Consent = true
	if _, err = m.Submit(context.Background(), project, input); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for gets.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if gets.Load() == 0 {
		t.Fatal("未进入原任务查询")
	}
	m.Close()
	audio = true // 调用方修改原指针不应改变已保存请求。
	restored, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	job, err := restored.Get(input.Request.ID)
	if err != nil || job.RemoteID != "seedance-remote-original" || !job.CanResume || job.Request.GenerateAudio == nil || *job.Request.GenerateAudio {
		t.Fatalf("恢复身份或参数丢失: %+v %v", job, err)
	}
	finish.Store(true)
	if _, err = restored.Resume(context.Background(), input.Request.ID); err != nil {
		t.Fatal(err)
	}
	job = awaitIdle(t, restored, input.Request.ID, Succeeded)
	if posts.Load() != 1 || downloads.Load() != 1 || len(job.Assets) != 1 || job.Assets[0].MIME != "video/mp4" || job.Imported {
		t.Fatalf("恢复重复提交或自动上轨: %+v", job)
	}
	saved, err := os.ReadFile(job.Assets[0].Path)
	if err != nil || !bytes.Equal(saved, video) {
		t.Fatal("结果未完整保存")
	}
	manifest, err := os.ReadFile(filepath.Join(filepath.Dir(job.Assets[0].Path), "recipe.json"))
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Recipe generation.Recipe `json:"recipe"`
	}
	if json.Unmarshal(manifest, &result) != nil || result.Recipe.GenerateAudio == nil || *result.Recipe.GenerateAudio || result.Recipe.Size != "720p" {
		t.Fatal("配方未持久化实际声音参数")
	}
	journal, err := os.ReadFile(filepath.Join(dir, "jobs", job.ID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, data := range [][]byte{journal, manifest} {
		if bytes.Contains(data, []byte("fixture-secret")) || bytes.Contains(data, []byte("signature=")) {
			t.Fatal("日志或配方泄露凭据或临时地址")
		}
	}
	// 同一ID仅改变声音也应冲突，不能绕过已核对请求再次POST。
	if _, err = restored.Submit(context.Background(), project, input); err == nil || posts.Load() != 1 {
		t.Fatal("声音变化未参与去重")
	}
}

func TestCatalogueDeclaresSeedanceProtocolWithoutClaimingAccount(t *testing.T) {
	catalog, err := Catalogue()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, capability := range catalog {
		if capability.ProviderID != generation.Seedance {
			continue
		}
		found = true
		if capability.ModelID != generation.Seedance25Model || capability.NeedsModelConfiguration || capability.AccountVerified || !capability.SupportsAudioGeneration || capability.DefaultSize != "720p" || len(capability.Modes) != 3 {
			t.Fatalf("目录与适配不一致: %+v", capability)
		}
	}
	if !found {
		t.Fatal("缺少用户指定模型家族")
	}
}
