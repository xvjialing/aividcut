package generation

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func completedTask(output Output) Task {
	return Task{ID: "download-task", ProviderID: OpenAI, ModelID: "fixture-model", Status: Succeeded, Outputs: []Output{output}, Recipe: Recipe{ID: "download-task", ProviderID: OpenAI, ModelID: "fixture-model", Mode: ImageGenerate, Prompt: "测试素材", Count: 1, References: []ReferenceRecipe{}, CreatedAt: time.Unix(1000, 0).UTC()}}
}

func TestDownloadFailureRetriesSameOutputWithoutAuthOrRegeneration(t *testing.T) {
	data := pngData(t, 6, 6)
	var downloads atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.Header.Get("Authorization") != "" || r.Header.Get("x-goog-api-key") != "" || r.Header.Get("Cookie") != "" {
			t.Error("下载泄露生成鉴权或重新提交生成")
		}
		if downloads.Add(1) == 1 {
			w.WriteHeader(503)
			io.WriteString(w, fixtureSecret)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		w.Write(data)
	}))
	defer server.Close()
	task := completedTask(Output{Kind: "image", MIME: "image/png", URL: server.URL + "/generated.png?signature=temporary-result-token"})
	directory := t.TempDir()
	client := server.Client()
	jar, _ := cookiejar.New(nil)
	endpoint, _ := url.Parse(server.URL)
	jar.SetCookies(endpoint, []*http.Cookie{{Name: "private-session", Value: "fixture-cookie"}})
	client.Jar = jar
	importer, err := NewResultImporter(ImportOptions{Directory: directory, HTTPClient: client, AllowLocalTestHTTP: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := importer.Import(context.Background(), task); err == nil || strings.Contains(err.Error(), fixtureSecret) {
		t.Fatal("下载失败被隐藏或泄露响应内容")
	}
	if _, err := os.Stat(filepath.Join(directory, task.ID)); !os.IsNotExist(err) {
		t.Fatal("失败下载发布了部分结果")
	}
	entries, _ := os.ReadDir(directory)
	if len(entries) != 0 {
		t.Fatal("失败下载遗留临时结果")
	}
	assets, err := importer.Import(context.Background(), task)
	if err != nil || len(assets) != 1 || downloads.Load() != 2 {
		t.Fatalf("没有按原结果重试下载：%v", err)
	}
	_, err = importer.Import(context.Background(), task)
	if err != nil || downloads.Load() != 2 {
		t.Fatal("已导入结果发生重复下载")
	}
	recipe, _ := os.ReadFile(filepath.Join(directory, task.ID, "recipe.json"))
	if strings.Contains(string(recipe), "temporary-result-token") || strings.Contains(string(recipe), server.URL) {
		t.Fatal("临时签名URL进入配方文件")
	}
}

func TestDefaultImporterRejectsPrivateAndCredentialURLsBeforeDownload(t *testing.T) {
	importer, _ := NewResultImporter(ImportOptions{Directory: t.TempDir()})
	for _, raw := range []string{"http://127.0.0.1/private", "https://127.0.0.1/private", "https://[::1]/private", "https://10.0.0.1/private", "https://169.254.169.254/latest/meta-data", "https://100.64.0.1/private", "https://localhost/private", "file:///etc/passwd", "https://user:secret@cdn.example.com/a.png"} {
		t.Run(raw, func(t *testing.T) {
			task := completedTask(Output{Kind: "image", MIME: "image/png", URL: raw})
			if _, err := importer.Import(context.Background(), task); err == nil {
				t.Fatal("不安全下载地址被接受")
			}
		})
	}
	for _, address := range []string{"127.0.0.1", "::1", "::ffff:127.0.0.1", "192.168.1.1", "169.254.1.1", "100.64.1.1", "198.18.0.1", "203.0.113.1", "2001:db8::1"} {
		if publicIP(net.ParseIP(address)) {
			t.Errorf("保留地址被识别为公网：%s", address)
		}
	}
	if !publicIP(net.ParseIP("1.1.1.1")) || !publicIP(net.ParseIP("2606:4700:4700::1111")) {
		t.Fatal("有效公网地址错误分类")
	}
}

func TestDownloaderRejectsRedirectToPrivateAddress(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Location", "http://169.254.169.254/latest/meta-data")
		w.WriteHeader(302)
	}))
	defer server.Close()
	importer, _ := NewResultImporter(ImportOptions{Directory: t.TempDir(), HTTPClient: server.Client(), AllowLocalTestHTTP: true})
	if _, err := importer.Import(context.Background(), completedTask(Output{Kind: "image", MIME: "image/png", URL: server.URL})); err == nil || calls.Load() != 1 {
		t.Fatal("下载重定向越过私网限制")
	}
}

func TestImporterRejectsChangedRecipesFilesAndDirectoryEscape(t *testing.T) {
	data := pngData(t, 4, 4)
	directory := t.TempDir()
	importer, _ := NewResultImporter(ImportOptions{Directory: directory})
	task := completedTask(Output{Kind: "image", MIME: "image/png", Data: data})
	assets, err := importer.Import(context.Background(), task)
	if err != nil {
		t.Fatal(err)
	}
	changed := task
	changed.Recipe.Prompt = "不同生成意图"
	if _, err := importer.Import(context.Background(), changed); err == nil {
		t.Fatal("同任务ID覆盖了不同配方")
	}
	os.WriteFile(assets[0].Path, pngData(t, 5, 5), 0600)
	if _, err := importer.Import(context.Background(), task); err == nil {
		t.Fatal("已导入素材变化未被发现")
	}
	escaping := task
	escaping.ID = "../escape"
	escaping.Recipe.ID = escaping.ID
	if _, err := importer.Import(context.Background(), escaping); err == nil {
		t.Fatal("任务ID可逃出输出目录")
	}
	unknown := task
	unknown.ID = "unknown-task"
	unknown.Recipe.ID = unknown.ID
	unknown.Status = Unknown
	unknown.SubmissionUncertain = true
	if _, err := importer.Import(context.Background(), unknown); err == nil {
		t.Fatal("未知任务被伪报成功导入")
	}
}

func TestImporterValidatesActualContentAndPublishesAllOrNothing(t *testing.T) {
	data := pngData(t, 4, 4)
	directory := t.TempDir()
	importer, _ := NewResultImporter(ImportOptions{Directory: directory})
	task := completedTask(Output{Kind: "image", MIME: "image/png", Data: data})
	task.Outputs = append(task.Outputs, Output{Kind: "image", MIME: "image/jpeg", Data: []byte("not jpeg")})
	if _, err := importer.Import(context.Background(), task); err == nil {
		t.Fatal("伪造图像被接受")
	}
	entries, _ := os.ReadDir(directory)
	if len(entries) != 0 {
		t.Fatal("一组结果失败后仍发布了部分图像")
	}
	tiny, _ := NewResultImporter(ImportOptions{Directory: t.TempDir(), MaxFileBytes: 4})
	if _, err := tiny.Import(context.Background(), completedTask(Output{Kind: "image", MIME: "image/png", Data: data})); err == nil {
		t.Fatal("结果大小限制未执行")
	}
}

func TestExistingManifestCannotPointOutsideResultDirectory(t *testing.T) {
	directory := t.TempDir()
	importer, _ := NewResultImporter(ImportOptions{Directory: directory})
	task := completedTask(Output{Kind: "image", MIME: "image/png", Data: pngData(t, 4, 4)})
	_, err := importer.Import(context.Background(), task)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(directory, task.ID, "recipe.json")
	raw, _ := os.ReadFile(manifestPath)
	var manifest importManifest
	json.Unmarshal(raw, &manifest)
	outside := filepath.Join(t.TempDir(), "outside.png")
	os.WriteFile(outside, pngData(t, 4, 4), 0600)
	manifest.Assets[0].Path = outside
	raw, _ = json.Marshal(manifest)
	os.WriteFile(manifestPath, raw, 0600)
	if _, err := importer.Import(context.Background(), task); err == nil {
		t.Fatal("配方可引用结果目录外文件")
	}
}

func TestProviderRedirectDoesNotForwardCredentialsOrRetry(t *testing.T) {
	var forwarded atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { forwarded.Add(1) }))
	defer other.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", other.URL)
		w.WriteHeader(307)
	}))
	defer server.Close()
	provider, _ := NewOpenAI(fixtureConfig(server))
	task, err := provider.Submit(context.Background(), Request{Mode: ImageGenerate, Prompt: "图"})
	if err == nil || !task.SubmissionUncertain || forwarded.Load() != 0 {
		t.Fatal("生成请求向重定向地址泄露鉴权，或未标记未知提交")
	}
}
