package application

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"aividcut/internal/editor"
	"aividcut/internal/localagent"
	"aividcut/internal/media"
	"aividcut/internal/projectstore"
)

type fixtureAgent struct {
	inspections atomic.Int32
	plans       atomic.Int32
	plan        func(context.Context, localagent.PlanInput) (localagent.PlanResult, error)
	inspect     func(context.Context, localagent.Provider) localagent.Installation
}

func (r *fixtureAgent) Inspect(ctx context.Context, p localagent.Provider) localagent.Installation {
	r.inspections.Add(1)
	if r.inspect != nil {
		return r.inspect(ctx, p)
	}
	return localagent.Installation{Provider: p, Installed: true, Version: "fixture", AuthState: "signed_in", Isolation: localagent.Isolation{Supported: true, Status: "ready"}}
}

func TestAgentCloseCancelsMetadataInspectionBeforeReleasingState(t *testing.T) {
	started := make(chan struct{})
	finished := make(chan error, 1)
	runner := &fixtureAgent{inspect: func(ctx context.Context, p localagent.Provider) localagent.Installation {
		close(started)
		<-ctx.Done()
		return localagent.Installation{Provider: p}
	}}
	s := newAgentService(t, t.TempDir(), runner)
	go func() { _, err := s.GetAgentStatus(context.Background()); finished <- err }()
	<-started
	s.Close()
	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) {
			t.Fatal("关闭未取消CLI状态检测", err)
		}
	case <-time.After(time.Second):
		t.Fatal("关闭后状态检测仍运行")
	}
}
func (r *fixtureAgent) LoginInstructions(context.Context, localagent.Provider) (localagent.LoginCommand, error) {
	return localagent.LoginCommand{Executable: "/fixture/cli", Args: []string{"login"}, Environment: map[string]string{"CODEX_HOME": "/fixture/private-profile"}, Notice: "仅展示，不执行"}, nil
}
func (r *fixtureAgent) Plan(ctx context.Context, input localagent.PlanInput, notify func(localagent.Event)) (localagent.PlanResult, error) {
	r.plans.Add(1)
	if notify != nil {
		notify(localagent.Event{Stage: "fixture", Message: "正在校验测试计划"})
	}
	return r.plan(ctx, input)
}

func newAgentService(t *testing.T, directory string, runner AgentRunner) *Service {
	t.Helper()
	s, err := New(media.Toolchain{}, filepath.Join(t.TempDir(), "previews"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	if err := s.ConfigureAgents(AgentOptions{Directory: directory, Runner: runner}); err != nil {
		t.Fatal(err)
	}
	return s
}
func renameAgentResult(input localagent.PlanInput, name string) localagent.PlanResult {
	return localagent.PlanResult{Provider: input.Provider, Request: editor.EditRequest{BaseRevision: input.Project.Revision, IdempotencyKey: editor.NewID("fixture-agent"), Label: "修改工程名", Commands: []editor.Command{{Type: "update_project", Name: name}}}, Warnings: []string{}}
}
func agentInput(id string) StartAgentPlanInput {
	return StartAgentPlanInput{ID: id, Provider: localagent.Codex, Instruction: "将工程名改为测试剪辑", Workflow: "manual-plan", Selection: []string{}, Consent: true}
}
func awaitAgent(t *testing.T, s *Service, id, status string) AgentJob {
	t.Helper()
	until := time.Now().Add(3 * time.Second)
	for time.Now().Before(until) {
		jobs, err := s.ListAgentPlans()
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
	jobs, _ := s.ListAgentPlans()
	t.Fatalf("任务未到达 %s: %+v", status, jobs)
	return AgentJob{}
}

func TestAgentLazyConfigurationStatusAndExclusiveDirectory(t *testing.T) {
	directory := t.TempDir()
	runner := &fixtureAgent{}
	s := newAgentService(t, directory, runner)
	if runner.inspections.Load() != 0 || runner.plans.Load() != 0 {
		t.Fatal("启动时自动调用了 CLI")
	}
	status, err := s.GetAgentStatus(context.Background())
	if err != nil || len(status) != 2 || runner.inspections.Load() != 2 {
		t.Fatal("检测未覆盖两种 CLI", err)
	}
	login, err := s.GetAgentLoginInstructions(context.Background(), localagent.Codex)
	if err != nil || len(login.Args) != 1 || runner.plans.Load() != 0 {
		t.Fatal("登录说明不应发起推理", err)
	}
	other, err := New(media.Toolchain{}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if err := other.ConfigureAgents(AgentOptions{Directory: directory, Runner: runner}); err == nil {
		t.Fatal("两个实例共同写入本地计划")
	}
	s.Close()
	if err := other.ConfigureAgents(AgentOptions{Directory: directory, Runner: runner}); err != nil {
		t.Fatal("关闭后数据锁未释放", err)
	}
}

func TestAgentPlanSeparateApplyUndoAndIdempotency(t *testing.T) {
	runner := &fixtureAgent{plan: func(_ context.Context, input localagent.PlanInput) (localagent.PlanResult, error) {
		return renameAgentResult(input, "测试剪辑"), nil
	}}
	s := newAgentService(t, t.TempDir(), runner)
	before := s.GetBootstrap()
	input := agentInput("apply-plan")
	input.Consent = false
	if _, err := s.StartAgentPlan(context.Background(), input); err == nil {
		t.Fatal("没有明确调用就启动 CLI")
	}
	input.Consent = true
	if _, err := s.StartAgentPlan(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	job := awaitAgent(t, s, input.ID, "succeeded")
	if job.Result == nil || !job.Result.CoreValidated || job.Applied {
		t.Fatal("计划未校验或提前应用")
	}
	if s.GetBootstrap().Project.Name != before.Project.Name {
		t.Fatal("生成计划自动修改工程")
	}
	input.Consent = false
	if _, err := s.StartAgentPlan(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	if runner.plans.Load() != 1 {
		t.Fatal("重复请求再次推理")
	}
	changed := input
	changed.Instruction = "别的内容"
	if _, err := s.StartAgentPlan(context.Background(), changed); err == nil {
		t.Fatal("复用ID接受不同内容")
	}
	after, err := s.ApplyAgentPlan(input.ID)
	if err != nil || after.Project.Name != "测试剪辑" || !after.Dirty {
		t.Fatal("应用计划失败", err)
	}
	again, err := s.ApplyAgentPlan(input.ID)
	if err != nil || again.Project.Revision != after.Project.Revision {
		t.Fatal("重复应用增加编辑", err)
	}
	undone, err := s.Undo()
	if err != nil || undone.Project.Name != before.Project.Name {
		t.Fatal("计划不能整组撤销", err)
	}
	again, err = s.ApplyAgentPlan(input.ID)
	if err != nil || again.Project.Name != before.Project.Name {
		t.Fatal("撤销后同ID被自动重放", err)
	}
}

func TestAgentPersistentPlanCanApplyAfterProjectReopen(t *testing.T) {
	directory := t.TempDir()
	runner := &fixtureAgent{plan: func(_ context.Context, input localagent.PlanInput) (localagent.PlanResult, error) {
		return renameAgentResult(input, "恢复计划"), nil
	}}
	s := newAgentService(t, directory, runner)
	path := filepath.Join(t.TempDir(), "source.aividcut")
	if _, err := s.SaveProjectTo(path); err != nil {
		t.Fatal(err)
	}
	input := agentInput("persistent")
	if _, err := s.StartAgentPlan(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	awaitAgent(t, s, input.ID, "succeeded")
	s.Close()
	restored := newAgentService(t, directory, runner)
	if _, err := restored.OpenProjectPath(path); err != nil {
		t.Fatal(err)
	}
	if _, err := restored.StartAgentPlan(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	if runner.plans.Load() != 1 {
		t.Fatal("重开后重复调用 CLI")
	}
	result, err := restored.ApplyAgentPlan(input.ID)
	if err != nil || result.Project.Name != "恢复计划" {
		t.Fatal("保存计划无法应用到原工程", err)
	}
}

func TestAgentRejectsStaleRevisionAndSameIdentityChangedContents(t *testing.T) {
	runner := &fixtureAgent{plan: func(_ context.Context, input localagent.PlanInput) (localagent.PlanResult, error) {
		return renameAgentResult(input, "不能覆盖人工修改"), nil
	}}
	s := newAgentService(t, t.TempDir(), runner)
	input := agentInput("stale")
	if _, err := s.StartAgentPlan(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	awaitAgent(t, s, input.ID, "succeeded")
	before := s.GetBootstrap().Project
	if _, err := s.ApplyEdit(editor.EditRequest{BaseRevision: before.Revision, IdempotencyKey: "manual", Commands: []editor.Command{{Type: "update_project", Name: "人工修改"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApplyAgentPlan(input.ID); err == nil {
		t.Fatal("旧计划覆盖新版本")
	}
	altered := editor.CloneProject(before)
	altered.Name = "同ID同revision的外部修改"
	path := filepath.Join(t.TempDir(), "changed.aividcut")
	if err := projectstore.Save(path, altered); err != nil {
		t.Fatal(err)
	}
	if _, err := s.OpenProjectPath(path); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApplyAgentPlan(input.ID); err == nil {
		t.Fatal("仅核对 ID/revision 而忽略内容变化")
	}
	if _, err := s.NewProject("其他工程"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApplyAgentPlan(input.ID); err == nil {
		t.Fatal("应用到不同工程")
	}
}

func TestAgentRevalidatesRunnerSelectionAndProtectsProject(t *testing.T) {
	runner := &fixtureAgent{plan: func(_ context.Context, input localagent.PlanInput) (localagent.PlanResult, error) {
		return renameAgentResult(input, "越过选区改名"), nil
	}}
	s := newAgentService(t, t.TempDir(), runner)
	clip := editor.DefaultClip("selected", "track-text-1", "text")
	clip.Text = "标题"
	p := s.GetBootstrap().Project
	if _, err := s.ApplyEdit(editor.EditRequest{BaseRevision: p.Revision, IdempotencyKey: "add-title", Commands: []editor.Command{{Type: "add_clip", Clip: &clip}}}); err != nil {
		t.Fatal(err)
	}
	input := agentInput("outside-selection")
	input.Selection = []string{clip.ID}
	if _, err := s.StartAgentPlan(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	job := awaitAgent(t, s, input.ID, "failed")
	if job.Result != nil || job.Applied {
		t.Fatal("越权计划保留为可用")
	}
	if _, err := s.ApplyAgentPlan(input.ID); err == nil {
		t.Fatal("越权计划可应用")
	}
	if s.GetBootstrap().Project.Name == "越过选区改名" {
		t.Fatal("校验时改了真实工程")
	}
}

func TestAgentQueuedCancelAndInFlightCancelNeverApplies(t *testing.T) {
	started := make(chan struct{})
	runner := &fixtureAgent{plan: func(ctx context.Context, _ localagent.PlanInput) (localagent.PlanResult, error) {
		close(started)
		<-ctx.Done()
		return localagent.PlanResult{}, ctx.Err()
	}}
	s := newAgentService(t, t.TempDir(), runner)
	if _, err := s.StartAgentPlan(context.Background(), agentInput("first")); err != nil {
		t.Fatal(err)
	}
	<-started
	if _, err := s.StartAgentPlan(context.Background(), agentInput("second")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CancelAgentPlan("second"); err != nil {
		t.Fatal(err)
	}
	awaitAgent(t, s, "second", "cancelled")
	if _, err := s.CancelAgentPlan("first"); err != nil {
		t.Fatal(err)
	}
	awaitAgent(t, s, "first", "cancelled")
	if runner.plans.Load() != 1 {
		t.Fatal("取消的排队任务仍调用CLI")
	}
	if s.GetBootstrap().CanUndo {
		t.Fatal("取消产生真实编辑")
	}
}

func TestAgentStorageFailureBeforeCLIAndPublicErrorRedaction(t *testing.T) {
	runner := &fixtureAgent{plan: func(context.Context, localagent.PlanInput) (localagent.PlanResult, error) {
		return localagent.PlanResult{}, errors.New("fixture-secret-must-not-show")
	}}
	s := newAgentService(t, t.TempDir(), runner)
	if err := os.Mkdir(s.agents.path, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := s.StartAgentPlan(context.Background(), agentInput("persist-failure")); err == nil {
		t.Fatal("未保存请求仍调用CLI")
	}
	if runner.plans.Load() != 0 {
		t.Fatal("持久化失败后仍调用CLI")
	}
	if err := os.Remove(s.agents.path); err != nil {
		t.Fatal(err)
	}
	if _, err := s.StartAgentPlan(context.Background(), agentInput("safe-error")); err != nil {
		t.Fatal(err)
	}
	job := awaitAgent(t, s, "safe-error", "failed")
	if strings.Contains(job.Message, "fixture-secret") {
		t.Fatal("原始CLI错误泄漏")
	}
}

func TestAgentInterruptedRecordIsNeverAutomaticallyRerun(t *testing.T) {
	directory := t.TempDir()
	runner := &fixtureAgent{}
	s := newAgentService(t, directory, runner)
	input := agentInput("interrupted")
	p := s.GetBootstrap().Project
	path := filepath.Join(t.TempDir(), "project.aividcut")
	if _, err := s.SaveProjectTo(path); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	s.agentMu.Lock()
	s.agents.entries[input.ID] = &agentRecord{Job: AgentJob{ID: input.ID, Provider: input.Provider, ProjectID: p.ID, ProjectRevision: p.Revision, Status: "running", CreatedAt: now, UpdatedAt: now}, Signature: agentSignature(input, p.ID), ProjectHash: projectHash(p)}
	err := s.agents.save()
	s.agentMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	restored := newAgentService(t, directory, runner)
	if _, err := restored.OpenProjectPath(path); err != nil {
		t.Fatal(err)
	}
	job, err := restored.StartAgentPlan(context.Background(), input)
	if err != nil || job.Status != "failed" || runner.plans.Load() != 0 {
		t.Fatal("中断记录被当作新请求", err)
	}
}

func TestAgentSlowWorkerKeepsPrivateDirectoryAndLock(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var released atomic.Bool
	t.Cleanup(func() {
		if released.CompareAndSwap(false, true) {
			close(release)
		}
	})
	runner := &fixtureAgent{plan: func(ctx context.Context, _ localagent.PlanInput) (localagent.PlanResult, error) {
		close(started)
		<-release
		return localagent.PlanResult{}, ctx.Err()
	}}
	directory := t.TempDir()
	s := newAgentService(t, directory, runner)
	workDir := s.agents.workDir
	if _, err := s.StartAgentPlan(context.Background(), agentInput("slow-close")); err != nil {
		t.Fatal(err)
	}
	<-started
	s.Close()
	if _, err := os.Stat(workDir); err != nil {
		t.Fatal("worker结束前删除临时目录", err)
	}
	other, err := New(media.Toolchain{}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if err := other.ConfigureAgents(AgentOptions{Directory: directory, Runner: runner}); err == nil {
		t.Fatal("慢worker结束前释放了资料锁")
	}
	if released.CompareAndSwap(false, true) {
		close(release)
	}
	until := time.Now().Add(2 * time.Second)
	for time.Now().Before(until) {
		if err := other.ConfigureAgents(AgentOptions{Directory: directory, Runner: runner}); err == nil {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("worker结束后未释放资料锁")
}
