package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"aividcut/internal/jobs"
	"aividcut/internal/localagent"
	"aividcut/internal/statefilelock"
)

type AgentRunner interface {
	Inspect(context.Context, localagent.Provider) localagent.Installation
	LoginInstructions(context.Context, localagent.Provider) (localagent.LoginCommand, error)
	Plan(context.Context, localagent.PlanInput, func(localagent.Event)) (localagent.PlanResult, error)
}
type AgentOptions struct {
	Directory string
	Runner    AgentRunner
}
type StartAgentPlanInput struct {
	ID          string              `json:"id"`
	Provider    localagent.Provider `json:"provider"`
	Instruction string              `json:"instruction"`
	Workflow    string              `json:"workflow"`
	Selection   []string            `json:"selection"`
	Consent     bool                `json:"consent"`
}
type AgentJob struct {
	ID              string                 `json:"id"`
	Provider        localagent.Provider    `json:"provider"`
	ProjectID       string                 `json:"projectId"`
	ProjectRevision int64                  `json:"projectRevision"`
	Status          string                 `json:"status"`
	Message         string                 `json:"message"`
	CreatedAt       time.Time              `json:"createdAt"`
	UpdatedAt       time.Time              `json:"updatedAt"`
	Result          *localagent.PlanResult `json:"result,omitempty"`
	Applied         bool                   `json:"applied"`
}
type agentRecord struct {
	Job         AgentJob `json:"job"`
	Signature   string   `json:"signature"`
	ProjectHash string   `json:"projectHash"`
	Selection   []string `json:"selection,omitempty"`
	queueID     string
}
type agentState struct {
	runner        AgentRunner
	queue         *jobs.Manager
	entries       map[string]*agentRecord
	executions    map[string]*agentExecutionRecord
	path, workDir string
	lock          *statefilelock.Lock
	ctx           context.Context
	cancel        context.CancelFunc
	metadataWG    sync.WaitGroup
}

var agentIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

func (s *Service) ConfigureAgents(options AgentOptions) error {
	s.agentMu.Lock()
	defer s.agentMu.Unlock()
	if s.agentsClosed || s.agents != nil {
		return errors.New("本地 Agent 服务已配置或正在关闭")
	}
	if strings.TrimSpace(options.Directory) == "" {
		return errors.New("本地 Agent 需要独立数据目录")
	}
	directory, err := filepath.Abs(options.Directory)
	if err != nil {
		return err
	}
	lock, err := statefilelock.Acquire(filepath.Join(directory, ".lock"))
	if err != nil {
		return err
	}
	workDir, err := os.MkdirTemp("", "aividcut-agent-work-")
	if err != nil {
		lock.Close()
		return err
	}
	ok := false
	defer func() {
		if !ok {
			os.RemoveAll(workDir)
			lock.Close()
		}
	}()
	runner := options.Runner
	if runner == nil {
		runner, err = localagent.NewPlanner(localagent.Config{StateDir: filepath.Join(directory, "profiles"), WorkDir: workDir, Timeout: 2 * time.Minute})
		if err != nil {
			return err
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	state := &agentState{runner: runner, entries: map[string]*agentRecord{}, executions: map[string]*agentExecutionRecord{}, path: filepath.Join(directory, "plans.json"), workDir: workDir, lock: lock, ctx: ctx, cancel: cancel}
	state.queue = jobs.NewWithObserver(func(job jobs.Job) {
		s.agentMu.Lock()
		defer s.agentMu.Unlock()
		for _, record := range state.entries {
			if record.queueID != job.ID {
				continue
			}
			if record.Job.Status == "succeeded" || record.Job.Status == "failed" || record.Job.Status == "cancelled" {
				return
			}
			if job.Status == "failed" || job.Status == "cancelled" {
				record.Job.Status = job.Status
				record.Job.Message = "本地会话已结束，没有应用计划"
				record.Job.UpdatedAt = time.Now().UTC()
				_ = state.save()
			}
			return
		}
		state.finishExecutionLocked(job)
	})
	if err = state.load(); err != nil {
		cancel()
		state.queue.Close()
		return err
	}
	if err = state.loadExecutions(); err != nil {
		cancel()
		state.queue.Close()
		return err
	}
	s.agents = state
	ok = true
	return nil
}

func (state *agentState) load() error {
	info, err := os.Lstat(state.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Size() > 16<<20 {
		return errors.New("本地计划资料不是有效普通文件或超过16MiB")
	}
	f, err := os.Open(state.path)
	if err != nil {
		return err
	}
	defer f.Close()
	var saved struct {
		Version int           `json:"version"`
		Plans   []agentRecord `json:"plans"`
	}
	d := json.NewDecoder(io.LimitReader(f, (16<<20)+1))
	d.DisallowUnknownFields()
	if err = d.Decode(&saved); err != nil {
		return errors.New("本地计划资料损坏，不能将已有请求当作新会话")
	}
	var extra any
	if d.Decode(&extra) != io.EOF || saved.Version != 1 || len(saved.Plans) > 1000 {
		return errors.New("本地计划资料版本或数量无效")
	}
	for i := range saved.Plans {
		r := &saved.Plans[i]
		j := &r.Job
		if !agentIDPattern.MatchString(j.ID) || state.entries[j.ID] != nil || !knownAgent(j.Provider) || j.ProjectID == "" || j.ProjectRevision < 0 || len(r.Signature) != 64 || len(r.ProjectHash) != 64 || j.CreatedAt.IsZero() {
			return errors.New("本地计划记录身份或参数无效")
		}
		switch j.Status {
		case "queued", "running", "cancelling":
			j.Status, j.Message = "failed", "上次会话已中断，未自动重新调用 CLI；请核对其使用记录"
		case "succeeded":
			if j.Result == nil || j.Result.Provider != j.Provider || j.Result.Request.BaseRevision != j.ProjectRevision {
				return errors.New("本地计划结果与工程身份不一致")
			}
		case "failed", "cancelled":
		default:
			return errors.New("本地计划状态无效")
		}
		state.entries[j.ID] = r
	}
	return nil
}

func (state *agentState) save() error {
	saved := struct {
		Version int           `json:"version"`
		Plans   []agentRecord `json:"plans"`
	}{Version: 1, Plans: []agentRecord{}}
	for _, r := range state.entries {
		saved.Plans = append(saved.Plans, *r)
	}
	sort.Slice(saved.Plans, func(i, j int) bool { return saved.Plans[i].Job.ID < saved.Plans[j].Job.ID })
	data, err := json.Marshal(saved)
	if err != nil || len(data) > 16<<20 {
		return errors.New("本地计划记录超过保存限制")
	}
	f, err := os.CreateTemp(filepath.Dir(state.path), ".agent-plans-*")
	if err != nil {
		return errors.New("无法保存本地计划状态")
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Rename(f.Name(), state.path); err != nil {
		return errors.New("无法提交本地计划状态")
	}
	return nil
}

func knownAgent(provider localagent.Provider) bool {
	return provider == localagent.Codex || provider == localagent.Claude
}
func (s *Service) agentLocked() (*agentState, error) {
	if s.agentsClosed || s.agents == nil {
		return nil, errors.New("本地 Agent 服务尚未配置或已关闭")
	}
	return s.agents, nil
}
func cloneAgentJob(job AgentJob) AgentJob {
	data, _ := json.Marshal(job)
	var result AgentJob
	_ = json.Unmarshal(data, &result)
	return result
}

func (s *Service) GetAgentStatus(ctx context.Context) ([]localagent.Installation, error) {
	s.agentMu.Lock()
	state, err := s.agentLocked()
	if err == nil {
		state.metadataWG.Add(1)
	}
	s.agentMu.Unlock()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	defer state.metadataWG.Done()
	stop := context.AfterFunc(state.ctx, cancel)
	defer stop()
	result := []localagent.Installation{}
	for _, provider := range []localagent.Provider{localagent.Codex, localagent.Claude} {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		result = append(result, state.runner.Inspect(ctx, provider))
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return result, nil
}
func (s *Service) GetAgentLoginInstructions(ctx context.Context, provider localagent.Provider) (localagent.LoginCommand, error) {
	if err := ctx.Err(); err != nil {
		return localagent.LoginCommand{}, err
	}
	if !knownAgent(provider) {
		return localagent.LoginCommand{}, errors.New("未知本地 CLI")
	}
	s.agentMu.Lock()
	state, err := s.agentLocked()
	if err == nil {
		state.metadataWG.Add(1)
	}
	s.agentMu.Unlock()
	if err != nil {
		return localagent.LoginCommand{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	defer state.metadataWG.Done()
	stop := context.AfterFunc(state.ctx, cancel)
	defer stop()
	result, err := state.runner.LoginInstructions(ctx, provider)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return localagent.LoginCommand{}, ctxErr
	}
	return result, err
}
func (s *Service) GetAgentWorkflows() []localagent.Workflow {
	s.mu.Lock()
	defer s.mu.Unlock()
	return localagent.Workflows(s.engine.Snapshot())
}

func agentSignature(input StartAgentPlanInput, projectID string) string {
	selection := append([]string{}, input.Selection...)
	sort.Strings(selection)
	data, _ := json.Marshal(struct {
		Provider                         localagent.Provider
		ProjectID, Instruction, Workflow string
		Selection                        []string
	}{input.Provider, projectID, input.Instruction, input.Workflow, selection})
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}
func (s *Service) StartAgentPlan(ctx context.Context, input StartAgentPlanInput) (AgentJob, error) {
	if err := ctx.Err(); err != nil {
		return AgentJob{}, err
	}
	input.Instruction = strings.TrimSpace(input.Instruction)
	input.Selection = append([]string(nil), input.Selection...)
	sort.Strings(input.Selection)
	if !agentIDPattern.MatchString(input.ID) || !knownAgent(input.Provider) || input.Instruction == "" || len(input.Instruction) > 32<<10 {
		return AgentJob{}, errors.New("请提供明确的 CLI、稳定请求标识和有效剪辑要求")
	}
	s.agentMu.Lock()
	defer s.agentMu.Unlock()
	state, err := s.agentLocked()
	if err != nil {
		return AgentJob{}, err
	}
	s.mu.Lock()
	project := s.engine.Snapshot()
	s.mu.Unlock()
	sig := agentSignature(input, project.ID)
	if previous := state.entries[input.ID]; previous != nil {
		if previous.Signature != sig {
			return AgentJob{}, errors.New("该请求标识已用于不同本地计划")
		}
		s.refreshAgentLocked(state, previous)
		return cloneAgentJob(previous.Job), nil
	}
	if !input.Consent {
		return AgentJob{}, errors.New("请先明确调用所选 CLI 生成计划；它可能使用账户额度")
	}
	if len(state.entries) >= 1000 {
		return AgentJob{}, errors.New("本地计划历史达到当前上限，需归档后继续")
	}
	if input.Workflow != "" {
		found := false
		for _, workflow := range localagent.Workflows(project) {
			if workflow.ID == input.Workflow {
				found = true
				if !workflow.Available {
					return AgentJob{}, errors.New(workflow.Reason)
				}
			}
		}
		if !found {
			return AgentJob{}, errors.New("未知本地工作流")
		}
	}
	seen := map[string]bool{}
	for _, id := range input.Selection {
		if seen[id] {
			return AgentJob{}, errors.New("选区片段标识重复")
		}
		seen[id] = true
		found := false
		for _, clip := range project.Clips {
			if clip.ID == id {
				found = true
				break
			}
		}
		if !found {
			return AgentJob{}, errors.New("选区包含不存在的片段")
		}
	}
	now := time.Now().UTC()
	record := &agentRecord{Job: AgentJob{ID: input.ID, Provider: input.Provider, ProjectID: project.ID, ProjectRevision: project.Revision, Status: "queued", Message: "本地计划已登记，等待调用 CLI", CreatedAt: now, UpdatedAt: now}, Signature: sig, ProjectHash: projectHash(project), Selection: input.Selection}
	state.entries[input.ID] = record
	if err = state.save(); err != nil {
		delete(state.entries, input.ID)
		return AgentJob{}, err
	}
	planInput := localagent.PlanInput{Provider: input.Provider, Project: project, Instruction: input.Instruction, Workflow: input.Workflow, Selection: input.Selection}
	job, err := state.queue.Start("agent.plan", "等待本地 CLI 生成计划", func(runCtx context.Context, progress func(float64, string)) (string, error) {
		s.agentMu.Lock()
		if record.Job.Status == "cancelling" || runCtx.Err() != nil {
			s.agentMu.Unlock()
			return "", context.Canceled
		}
		record.Job.Status, record.Job.Message = "running", "正在调用本地 CLI 生成候选计划"
		record.Job.UpdatedAt = time.Now().UTC()
		if err := state.save(); err != nil {
			record.Job.Status, record.Job.Message = "failed", "提交状态无法保存，未调用 CLI"
			s.agentMu.Unlock()
			return "", err
		}
		s.agentMu.Unlock()
		result, err := state.runner.Plan(runCtx, planInput, func(event localagent.Event) {
			s.agentMu.Lock()
			defer s.agentMu.Unlock()
			if record.Job.Status != "cancelling" && len(event.Message) <= 512 {
				record.Job.Message = event.Message
				record.Job.UpdatedAt = time.Now().UTC()
			}
		})
		if err == nil {
			if result.Provider != input.Provider {
				err = errors.New("CLI 计划的服务归属不一致")
			} else if validationErr := localagent.ValidatePlanForInput(planInput, result.Request); validationErr != nil {
				err = validationErr
			}
		}
		s.agentMu.Lock()
		defer s.agentMu.Unlock()
		record.Job.UpdatedAt = time.Now().UTC()
		if err != nil || runCtx.Err() != nil {
			if runCtx.Err() != nil || errors.Is(err, context.Canceled) {
				record.Job.Status, record.Job.Message = "cancelled", "本地等待已停止，没有自动应用计划"
			} else {
				record.Job.Status, record.Job.Message = "failed", safeAgentError(err)
			}
			_ = state.save()
			if err == nil {
				err = runCtx.Err()
			}
			return "", err
		}
		result.CoreValidated = true
		record.Job.Result = &result
		record.Job.Status, record.Job.Message = "succeeded", "候选计划已校验，等待单独应用到工程"
		if err := state.save(); err != nil {
			record.Job.Message = "计划已校验，结果暂未保存；重开后不会自动重运行"
		}
		return "", nil
	})
	if err != nil {
		record.Job.Status, record.Job.Message = "failed", "未能进入本地队列，未调用 CLI"
		_ = state.save()
		return AgentJob{}, err
	}
	record.queueID = job.ID
	return cloneAgentJob(record.Job), nil
}

func safeAgentError(err error) string {
	var known *localagent.Error
	if errors.As(err, &known) {
		return known.Message
	}
	return "本地计划未完成，请检查 CLI 可用性；未自动修改工程或重新调用"
}
func (s *Service) refreshAgentLocked(state *agentState, record *agentRecord) {
	if record.queueID == "" || record.Job.Status == "succeeded" || record.Job.Status == "failed" || record.Job.Status == "cancelled" {
		return
	}
	for _, job := range state.queue.List() {
		if job.ID != record.queueID {
			continue
		}
		if job.Status == "cancelled" || job.Status == "failed" {
			record.Job.Status = job.Status
			record.Job.Message = "本地会话已结束，没有应用计划"
			record.Job.UpdatedAt = time.Now().UTC()
			_ = state.save()
		}
		return
	}
}
func (s *Service) ListAgentPlans() ([]AgentJob, error) {
	s.agentMu.Lock()
	defer s.agentMu.Unlock()
	state, err := s.agentLocked()
	if err != nil {
		return nil, err
	}
	result := []AgentJob{}
	for _, record := range state.entries {
		s.refreshAgentLocked(state, record)
		result = append(result, cloneAgentJob(record.Job))
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CreatedAt.After(result[j].CreatedAt) })
	return result, nil
}
func (s *Service) CancelAgentPlan(id string) (AgentJob, error) {
	s.agentMu.Lock()
	defer s.agentMu.Unlock()
	state, err := s.agentLocked()
	if err != nil {
		return AgentJob{}, err
	}
	record := state.entries[id]
	if record == nil {
		return AgentJob{}, errors.New("找不到本地计划")
	}
	s.refreshAgentLocked(state, record)
	if record.Job.Status == "queued" || record.Job.Status == "running" || record.Job.Status == "cancelling" {
		if _, err = state.queue.Cancel(record.queueID); err != nil {
			return AgentJob{}, err
		}
		record.Job.Status, record.Job.Message = "cancelling", "正在停止本地等待，不会自动应用结果"
		record.Job.UpdatedAt = time.Now().UTC()
		_ = state.save()
	}
	return cloneAgentJob(record.Job), nil
}
func (s *Service) ApplyAgentPlan(id string) (Bootstrap, error) {
	s.agentMu.Lock()
	defer s.agentMu.Unlock()
	state, err := s.agentLocked()
	if err != nil {
		return Bootstrap{}, err
	}
	record := state.entries[id]
	if record == nil || record.Job.Status != "succeeded" || record.Job.Result == nil {
		return Bootstrap{}, errors.New("只有已校验的成功计划可以应用")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	project := s.engine.Snapshot()
	if project.ID != record.Job.ProjectID {
		return Bootstrap{}, errors.New("请切回计划对应的原工程")
	}
	if record.Job.Applied {
		return s.bootstrapLocked(), nil
	}
	if project.Revision != record.Job.ProjectRevision || projectHash(project) != record.ProjectHash {
		return Bootstrap{}, errors.New("工程已变化，请基于当前版本重新生成计划")
	}
	request := record.Job.Result.Request
	if err := localagent.ValidatePlanForInput(localagent.PlanInput{Project: project, Selection: record.Selection}, request); err != nil {
		return Bootstrap{}, err
	}
	if _, err = s.engine.Apply(request); err != nil {
		return Bootstrap{}, err
	}
	record.Job.Applied = true
	record.Job.Message = "计划已作为一次编辑应用，可撤销"
	record.Job.UpdatedAt = time.Now().UTC()
	if err := state.save(); err != nil {
		record.Job.Message = "计划已应用，历史标记暂未保存；不会在本会话重复应用"
	}
	return s.bootstrapLocked(), nil
}
func (s *Service) closeAgents() {
	s.agentMu.Lock()
	s.agentsClosed = true
	state := s.agents
	s.agents = nil
	s.agentMu.Unlock()
	if state == nil {
		return
	}
	state.cancel()
	state.queue.Close()
	cleanup := func() { os.RemoveAll(state.workDir); state.lock.Close() }
	done := make(chan struct{})
	go func() { <-state.queue.Done(); state.metadataWG.Wait(); cleanup(); close(done) }()
	select {
	case <-state.queue.Done():
		select {
		case <-done:
		case <-time.After(3 * time.Second):
		}
	default:
		// 队列已用尽关闭等待预算，之后由后台收尾，不再串行等待第二次。
	}
}
