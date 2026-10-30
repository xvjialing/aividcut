package application

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"aividcut/internal/connections"
	"aividcut/internal/generation"
	"aividcut/internal/generationjobs"
	"aividcut/internal/media"
)

// 所有生成接线测试仅访问此内存凭据和本地假 Provider。
type appSecretStore struct {
	mu        sync.Mutex
	values    map[string][]byte
	reads     int
	deleteErr error
}

func (store *appSecretStore) Set(ctx context.Context, id string, secret []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	store.values[id] = append([]byte(nil), secret...)
	return nil
}
func (store *appSecretStore) Get(ctx context.Context, id string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	store.reads++
	secret, ok := store.values[id]
	if !ok {
		return nil, connections.ErrSecretNotFound
	}
	return append([]byte(nil), secret...), nil
}
func (store *appSecretStore) Delete(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.deleteErr != nil {
		return store.deleteErr
	}
	if _, ok := store.values[id]; !ok {
		return connections.ErrSecretNotFound
	}
	delete(store.values, id)
	return nil
}
func (store *appSecretStore) readCount() int {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.reads
}

type appProviderControl struct {
	posts, gets, cancels atomic.Int32
	mu                   sync.Mutex
	seenSecrets          []string
	submit               func(context.Context, generation.Request) (generation.Task, error)
	get                  func(context.Context, generation.Task) (generation.Task, error)
}
type appFixtureProvider struct {
	control   *appProviderControl
	secrets   generation.SecretSource
	validator generation.Provider
}

func (provider *appFixtureProvider) Capabilities() []generation.Capability {
	return provider.validator.Capabilities()
}
func (provider *appFixtureProvider) Validate(request generation.Request) error {
	return provider.validator.Validate(request)
}
func (provider *appFixtureProvider) recordSecret(ctx context.Context, id string) error {
	secret, err := provider.secrets.Secret(ctx, id)
	if err != nil {
		return err
	}
	provider.control.mu.Lock()
	defer provider.control.mu.Unlock()
	provider.control.seenSecrets = append(provider.control.seenSecrets, secret)
	return nil
}
func (provider *appFixtureProvider) Submit(ctx context.Context, request generation.Request) (generation.Task, error) {
	provider.control.posts.Add(1)
	if err := provider.recordSecret(ctx, request.ProviderID); err != nil {
		return generation.Task{}, err
	}
	if provider.control.submit != nil {
		return provider.control.submit(ctx, request)
	}
	task := appTask(request, generation.Succeeded, "")
	for range request.Count {
		task.Outputs = append(task.Outputs, appImageOutput())
	}
	return task, nil
}
func (provider *appFixtureProvider) Get(ctx context.Context, task generation.Task) (generation.Task, error) {
	provider.control.gets.Add(1)
	if err := provider.recordSecret(ctx, task.ProviderID); err != nil {
		return task, err
	}
	if provider.control.get != nil {
		return provider.control.get(ctx, task)
	}
	task.Status, task.SubmissionUncertain = generation.Succeeded, false
	task.Outputs = []generation.Output{appImageOutput()}
	return task, nil
}
func (provider *appFixtureProvider) Cancel(context.Context, generation.Task) (generation.Task, error) {
	provider.control.cancels.Add(1)
	return generation.Task{}, errors.New("测试不能调用远端取消")
}
func (control *appProviderControl) factory(provider, model string, secret generation.SecretSource, refs generation.ReferenceSource) (generation.Provider, error) {
	validator, err := generationjobs.BuiltinProvider(provider, model, nil, refs)
	if err != nil {
		return nil, err
	}
	return &appFixtureProvider{control: control, secrets: secret, validator: validator}, nil
}
func appTask(request generation.Request, status, remote string) generation.Task {
	return generation.Task{ID: request.ID, ProviderID: request.ProviderID, ModelID: request.ModelID, RemoteID: remote, Status: status, Recipe: generation.Recipe{ID: request.ID, ProviderID: request.ProviderID, ModelID: request.ModelID, Mode: request.Mode, Count: request.Count, Prompt: request.Prompt, CreatedAt: time.Now().UTC(), References: []generation.ReferenceRecipe{}}}
}
func appImageOutput() generation.Output {
	var data bytes.Buffer
	_ = png.Encode(&data, image.NewRGBA(image.Rect(0, 0, 32, 18)))
	return generation.Output{Kind: "image", MIME: "image/png", Data: data.Bytes()}
}
func configuredGenerationService(t *testing.T, directory string, store *appSecretStore, control *appProviderControl) *Service {
	t.Helper()
	service, err := New(media.Toolchain{}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(service.Close)
	if err := service.ConfigureGeneration(GenerationOptions{Directory: directory, SecretStore: store, StorageName: "fixture-memory", Factory: control.factory}); err != nil {
		t.Fatal(err)
	}
	return service
}
func appSaveConnection(t *testing.T, service *Service, id, key string) ConnectionProfile {
	t.Helper()
	profile, err := service.SaveConnection(context.Background(), connections.SaveInput{ID: id, ProviderID: generation.OpenAI, ModelID: "gpt-image-2.5-sunburst", Name: "生成测试", APIKey: key})
	if err != nil {
		t.Fatal(err)
	}
	return profile
}
func appSubmit(id, connectionID string, count int) generationjobs.SubmitInput {
	return generationjobs.SubmitInput{ConnectionID: connectionID, Consent: true, Request: generation.Request{ID: id, ProviderID: generation.OpenAI, ModelID: "gpt-image-2.5-sunburst", Mode: generation.ImageGenerate, Prompt: "仅本地测试图片", Count: count}}
}
func appAwaitJob(t *testing.T, service *Service, id, status string) GenerationJob {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		jobs, err := service.ListGenerationJobs()
		if err != nil {
			t.Fatal(err)
		}
		for _, job := range jobs {
			if job.ID == id && job.Status == status {
				return job
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("任务没有达到预期状态 %s", status)
	return GenerationJob{}
}
func appEventually(t *testing.T, operation func() error) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	var err error
	for time.Now().Before(deadline) {
		if err = operation(); err == nil {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal(err)
}

func TestGenerationConfigurationLockLazyReadAndFailureCleanup(t *testing.T) {
	directory := t.TempDir()
	store := &appSecretStore{values: map[string][]byte{}}
	control := &appProviderControl{}
	service := configuredGenerationService(t, directory, store, control)
	if _, err := service.GetConnections(); err != nil {
		t.Fatal(err)
	}
	if health, err := service.GetConnectionHealth(); err != nil || health.Storage != "fixture-memory" || health.PendingCleanup != 0 {
		t.Fatal("连接健康状态错误", err)
	}
	if store.readCount() != 0 {
		t.Fatal("配置或列表不应读取凭据")
	}
	other, err := New(media.Toolchain{}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	options := GenerationOptions{Directory: directory, SecretStore: store, Factory: control.factory}
	if err := other.ConfigureGeneration(options); err == nil {
		t.Fatal("同一根目录允许第二个写入实例")
	}
	service.Close()
	if err := other.ConfigureGeneration(options); err != nil {
		t.Fatal("关闭后根目录锁未释放", err)
	}
	brokenDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(brokenDir, "connections.json"), []byte(`{"broken":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	broken, err := New(media.Toolchain{}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer broken.Close()
	options.Directory = brokenDir
	if err := broken.ConfigureGeneration(options); err == nil {
		t.Fatal("无效连接文件被接受")
	}
	if err := os.Remove(filepath.Join(brokenDir, "connections.json")); err != nil {
		t.Fatal(err)
	}
	if err := broken.ConfigureGeneration(options); err != nil {
		t.Fatal("初始化失败遗留根目录锁", err)
	}
}

func TestApplicationConnectionWarningsAndModelValidation(t *testing.T) {
	directory := t.TempDir()
	store := &appSecretStore{values: map[string][]byte{}}
	service := configuredGenerationService(t, directory, store, &appProviderControl{})
	profile := appSaveConnection(t, service, "", "fixture-old-key")
	store.deleteErr = errors.New("fixture-secret-error")
	updated := appSaveConnection(t, service, profile.ID, "fixture-new-key")
	if updated.Warning == "" || strings.Contains(updated.Warning, "fixture-secret-error") || !updated.Configured {
		t.Fatal("已提交清理警告未转换为成功 Profile.warning")
	}
	health, err := service.GetConnectionHealth()
	if err != nil || health.PendingCleanup != 1 {
		t.Fatal("清理待办缺失", err)
	}
	store.deleteErr = nil
	if health, err = service.CleanupCredentials(context.Background()); err != nil || health.PendingCleanup != 0 {
		t.Fatal("显式清理失败", err)
	}
	for _, input := range []connections.SaveInput{
		{ProviderID: "unknown", ModelID: "gpt-image-2.5-sunburst", Name: "无效", APIKey: "unused"},
		{ProviderID: generation.Google, ModelID: "gpt-image-2.5-sunburst", Name: "模型归属不符", APIKey: "unused"},
		{ProviderID: generation.OpenAI, Name: "空模型", APIKey: "unused"},
	} {
		if _, err := service.SaveConnection(context.Background(), input); err == nil {
			t.Fatal("未验证服务与模型组合")
		}
	}
	encoded, _ := json.Marshal(updated)
	if strings.Contains(string(encoded), "fixture-new-key") || strings.Contains(string(encoded), "secretHandle") {
		t.Fatal("连接响应泄露凭据")
	}
	stored, err := os.ReadFile(filepath.Join(directory, "connections.json"))
	if err != nil || bytes.Contains(stored, []byte("fixture-new-key")) || bytes.Contains(stored, []byte("fixture-old-key")) {
		t.Fatal("连接文件出现明文 Key")
	}
	store.deleteErr = errors.New("fixture delete denied")
	deleted, err := service.DeleteConnection(context.Background(), profile.ID)
	if err != nil || deleted.Warning == "" {
		t.Fatal("已提交删除未转 warning", err)
	}
	profiles, err := service.GetConnections()
	if err != nil || len(profiles) != 0 {
		t.Fatal("已提交删除仍显示连接", err)
	}
}

func TestGeneratedCandidatesImportUndoRedoSaveAndOriginalProject(t *testing.T) {
	store, control := &appSecretStore{values: map[string][]byte{}}, &appProviderControl{}
	service := configuredGenerationService(t, t.TempDir(), store, control)
	connection := appSaveConnection(t, service, "", "fixture-only-key")
	input := appSubmit("candidate-selection", connection.ID, 2)
	if _, err := service.SubmitGeneration(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	job := appAwaitJob(t, service, input.Request.ID, generationjobs.Succeeded)
	if len(job.Assets) != 2 || job.Imported || len(job.ImportedAssetIDs) != 0 {
		t.Fatal("生成候选状态不正确")
	}
	for _, asset := range job.Assets {
		if !strings.HasPrefix(asset.URL, "http://127.0.0.1:") {
			t.Fatal("没有返回本地签名 URL")
		}
		response, err := http.Get(asset.URL)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, response.Body)
		response.Body.Close()
		if response.StatusCode != 200 {
			t.Fatal("本地生成候选无法读取")
		}
	}
	encoded, err := json.Marshal(job)
	if err != nil || strings.Count(string(encoded), `"assets":`) != 1 || strings.Contains(string(encoded), "fixture-only-key") {
		t.Fatal("任务视图编码泄露或 assets 字段冲突", err)
	}
	if _, err := service.ImportGenerationAssets(context.Background(), job.ID, nil); err == nil {
		t.Fatal("空选择未拒绝")
	}
	if _, err := service.ImportGenerationAssets(context.Background(), job.ID, []string{"not-this-task"}); err == nil {
		t.Fatal("错误候选未拒绝")
	}
	partial, err := service.ImportGenerationAssets(context.Background(), job.ID, []string{job.Assets[0].ID})
	if err != nil || len(partial.Project.Assets) != 1 || partial.Project.Assets[0].ID != job.Assets[0].ID || partial.Project.Assets[0].Width != 32 || partial.Project.Assets[0].Height != 18 {
		t.Fatal("选中候选未按真实元数据及稳定 ID 导入", err)
	}
	view := appAwaitJob(t, service, job.ID, generationjobs.Succeeded)
	if view.Imported || len(view.ImportedAssetIDs) != 1 {
		t.Fatal("部分导入被误报为全部")
	}
	if _, err := service.Undo(); err != nil {
		t.Fatal(err)
	}
	view = appAwaitJob(t, service, job.ID, generationjobs.Succeeded)
	if view.Imported || len(view.ImportedAssetIDs) != 0 {
		t.Fatal("Undo 后沿用了任务持久导入标记")
	}
	all, err := service.ImportGenerationResult(context.Background(), job.ID)
	if err != nil || len(all.Project.Assets) != 2 || !all.CanUndo {
		t.Fatal("整批导入失败", err)
	}
	if undone, err := service.Undo(); err != nil || len(undone.Project.Assets) != 0 {
		t.Fatal("生成结果未作为整批撤销", err)
	}
	if _, err := service.Redo(); err != nil {
		t.Fatal(err)
	}
	beforeRevision := service.GetBootstrap().Project.Revision
	if repeated, err := service.ImportGenerationResult(context.Background(), job.ID); err != nil || repeated.Project.Revision != beforeRevision {
		t.Fatal("重复导入更改了工程或丢失幂等性", err)
	}
	projectPath := filepath.Join(t.TempDir(), "generated.aividcut")
	if _, err := service.SaveProjectTo(projectPath); err != nil {
		t.Fatal(err)
	}
	if _, err := service.NewProject("其他工程"); err != nil {
		t.Fatal(err)
	}
	view = appAwaitJob(t, service, job.ID, generationjobs.Succeeded)
	if view.Imported || len(view.ImportedAssetIDs) != 0 {
		t.Fatal("其他工程被误报已导入")
	}
	if _, err := service.ImportGenerationResult(context.Background(), job.ID); err == nil {
		t.Fatal("允许导入不同目标工程")
	}
	if _, err := service.OpenProjectPath(projectPath); err != nil {
		t.Fatal(err)
	}
	view = appAwaitJob(t, service, job.ID, generationjobs.Succeeded)
	if !view.Imported || len(view.ImportedAssetIDs) != 2 || view.Assets[0].URL == "" {
		t.Fatal("重开原工程未重算真实导入状态及会话 URL")
	}
	input.Consent = false
	if _, err := service.SubmitGeneration(context.Background(), input); err != nil || control.posts.Load() != 1 {
		t.Fatal("重复提交触发重新生成", err)
	}
}

func TestGenerationImportRejectsHashMismatchAtomicallyAndSymlinkPreview(t *testing.T) {
	store, control := &appSecretStore{values: map[string][]byte{}}, &appProviderControl{}
	service := configuredGenerationService(t, t.TempDir(), store, control)
	connection := appSaveConnection(t, service, "", "fixture-only-key")
	if _, err := service.SubmitGeneration(context.Background(), appSubmit("tampered-result", connection.ID, 2)); err != nil {
		t.Fatal(err)
	}
	job := appAwaitJob(t, service, "tampered-result", generationjobs.Succeeded)
	data, err := os.ReadFile(job.Assets[1].Path)
	if err != nil {
		t.Fatal(err)
	}
	data[len(data)-1] ^= 1
	if err := os.WriteFile(job.Assets[1].Path, data, 0600); err != nil {
		t.Fatal(err)
	}
	before := service.GetBootstrap().Project
	if _, err := service.ImportGenerationResult(context.Background(), job.ID); err == nil || !strings.Contains(err.Error(), "SHA-256") {
		t.Fatal("未检查真实文件 SHA-256", err)
	}
	if after := service.GetBootstrap().Project; len(after.Assets) != 0 || after.Revision != before.Revision {
		t.Fatal("第二个候选失败仍留下部分导入")
	}
	outside := fixtureImage(t)
	if err := os.Remove(job.Assets[0].Path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, job.Assets[0].Path); err != nil {
		t.Fatal(err)
	}
	view := appAwaitJob(t, service, job.ID, generationjobs.Succeeded)
	if view.Assets[0].URL != "" {
		t.Fatal("任务目录中的符号链接被发布为预览 URL")
	}
	response, err := http.Get(job.Assets[0].URL)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatal("之前发出的候选 URL 未撤销，仍能读取替换后的链接文件")
	}
	if _, err := service.ImportGenerationAssets(context.Background(), job.ID, []string{job.Assets[0].ID}); err == nil {
		t.Fatal("符号链接结果被导入")
	}
}

func TestWaitingTaskCanRotateSameModelKeyAndResumeAfterRestart(t *testing.T) {
	directory := t.TempDir()
	store, control := &appSecretStore{values: map[string][]byte{}}, &appProviderControl{}
	control.submit = func(_ context.Context, request generation.Request) (generation.Task, error) {
		return appTask(request, generation.Running, "same-remote-task"), &generation.Error{Code: "fixture_disconnect", Message: "本地提交返回中断", SubmissionUncertain: true}
	}
	service := configuredGenerationService(t, directory, store, control)
	connection := appSaveConnection(t, service, "", "fixture-first-key")
	projectPath := filepath.Join(t.TempDir(), "original.aividcut")
	if _, err := service.SaveProjectTo(projectPath); err != nil {
		t.Fatal(err)
	}
	input := appSubmit("recover-existing-task", connection.ID, 1)
	if _, err := service.SubmitGeneration(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	job := appAwaitJob(t, service, input.Request.ID, generationjobs.Waiting)
	if !job.CanResume || job.RemoteID != "same-remote-task" {
		t.Fatal("已知远端任务不可恢复")
	}
	if _, err := service.DeleteConnection(context.Background(), connection.ID); err == nil {
		t.Fatal("等待中任务允许删除原连接")
	}
	if _, err := service.SaveConnection(context.Background(), connections.SaveInput{ID: connection.ID, ProviderID: generation.OpenAI, ModelID: "gpt-image-2.5-flare", Name: "错误改模型"}); err == nil {
		t.Fatal("等待中任务允许改变模型")
	}
	appEventually(t, func() error {
		_, err := service.SaveConnection(context.Background(), connections.SaveInput{ID: connection.ID, ProviderID: generation.OpenAI, ModelID: connection.ModelID, Name: "轮换令牌", APIKey: "fixture-renewed-key"})
		return err
	})
	service.Close()
	readsBefore := store.readCount()
	restarted := configuredGenerationService(t, directory, store, control)
	if _, err := restarted.GetConnections(); err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.GetConnectionHealth(); err != nil {
		t.Fatal(err)
	}
	job = appAwaitJob(t, restarted, input.Request.ID, generationjobs.Waiting)
	if store.readCount() != readsBefore || control.posts.Load() != 1 {
		t.Fatal("重启自动读取了钥匙串或重新生成")
	}
	if _, err := restarted.OpenProjectPath(projectPath); err != nil {
		t.Fatal(err)
	}
	appEventually(t, func() error { _, err := restarted.ResumeGeneration(context.Background(), job.ID); return err })
	job = appAwaitJob(t, restarted, job.ID, generationjobs.Succeeded)
	if control.posts.Load() != 1 || control.gets.Load() != 1 {
		t.Fatal("恢复未严格查询原任务")
	}
	control.mu.Lock()
	seen := append([]string{}, control.seenSecrets...)
	control.mu.Unlock()
	if len(seen) != 2 || seen[0] != "fixture-first-key" || seen[1] != "fixture-renewed-key" {
		t.Fatal("恢复没有使用新令牌查询同一服务")
	}
	if imported, err := restarted.ImportGenerationResult(context.Background(), job.ID); err != nil || len(imported.Project.Assets) != 1 {
		t.Fatal("恢复结果无法加入原目标工程", err)
	}
}

func TestStopWaitingUnknownRequiresAcknowledgmentAndNeverCancelsRemote(t *testing.T) {
	store, control := &appSecretStore{values: map[string][]byte{}}, &appProviderControl{}
	started := make(chan struct{})
	control.submit = func(ctx context.Context, request generation.Request) (generation.Task, error) {
		close(started)
		<-ctx.Done()
		return appTask(request, generation.Unknown, ""), ctx.Err()
	}
	service := configuredGenerationService(t, t.TempDir(), store, control)
	connection := appSaveConnection(t, service, "", "fixture-key")
	input := appSubmit("unknown-submission", connection.ID, 1)
	if _, err := service.SubmitGeneration(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("本地假提交未开始")
	}
	if _, err := service.SaveConnection(context.Background(), connections.SaveInput{ID: connection.ID, ProviderID: generation.OpenAI, ModelID: connection.ModelID, Name: "运行中换 Key", APIKey: "replacement"}); err == nil {
		t.Fatal("active Lease 未阻止运行中换 Key")
	}
	if _, err := service.StopGenerationWaiting(input.Request.ID); err != nil {
		t.Fatal(err)
	}
	job := appAwaitJob(t, service, input.Request.ID, generationjobs.Unknown)
	if _, err := service.ResumeGeneration(context.Background(), job.ID); err == nil {
		t.Fatal("未知同步提交不应允许自动恢复")
	}
	if _, err := service.DeleteConnection(context.Background(), connection.ID); err == nil {
		t.Fatal("未知任务未核对前允许删连接")
	}
	appEventually(t, func() error { _, err := service.AcknowledgeGenerationUnknown(job.ID); return err })
	if _, err := service.DeleteConnection(context.Background(), connection.ID); err != nil {
		t.Fatal("核对后不能删除连接", err)
	}
	if control.posts.Load() != 1 || control.cancels.Load() != 0 {
		t.Fatal("停止等待触发了远端取消或重复生成")
	}
}

func TestSlowWorkerKeepsRootLockUntilGenerationDone(t *testing.T) {
	directory := t.TempDir()
	store, control := &appSecretStore{values: map[string][]byte{}}, &appProviderControl{}
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	control.submit = func(ctx context.Context, request generation.Request) (generation.Task, error) {
		close(started)
		// 特意模拟未及时响应 context 的第三方 Provider。
		<-release
		return appTask(request, generation.Unknown, ""), ctx.Err()
	}
	service := configuredGenerationService(t, directory, store, control)
	connection := appSaveConnection(t, service, "", "fixture-only-key")
	if _, err := service.SubmitGeneration(context.Background(), appSubmit("slow-shutdown", connection.ID, 1)); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("慢任务没有开始")
	}
	service.Close()
	other, err := New(media.Toolchain{}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	options := GenerationOptions{Directory: directory, SecretStore: store, Factory: control.factory}
	if err := other.ConfigureGeneration(options); err == nil {
		t.Fatal("极慢 worker 尚未结束时根锁被提前释放")
	}
	once.Do(func() { close(release) })
	appEventually(t, func() error { return other.ConfigureGeneration(options) })
	if control.posts.Load() != 1 {
		t.Fatal("接管目录触发了重新生成")
	}
}
