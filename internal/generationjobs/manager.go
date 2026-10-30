package generationjobs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"aividcut/internal/editor"
	"aividcut/internal/generation"
	"aividcut/internal/statefilelock"
)

type entry struct {
	record       record
	corrupt      bool
	active       bool
	cancel       context.CancelFunc
	persistedSHA string
}

type Manager struct {
	mu                    sync.Mutex
	entries               map[string]*entry
	journal, assets       string
	resolve               Resolver
	factory               Factory
	importer              *generation.ResultImporter
	pollInterval, timeout time.Duration
	ctx                   context.Context
	cancel                context.CancelFunc
	wg                    sync.WaitGroup
	sem                   chan struct{}
	lock                  *statefilelock.Lock
	closed                bool
	archives              map[string]archivedFile
	idOwners              map[string]string
	archiveDir            string
	done                  chan struct{}
}

func New(options Options) (*Manager, error) {
	if strings.TrimSpace(options.Directory) == "" || options.Resolve == nil {
		return nil, errors.New("生成任务需要独立目录和连接服务")
	}
	dir, err := filepath.Abs(options.Directory)
	if err != nil {
		return nil, err
	}
	lock, err := statefilelock.Acquire(filepath.Join(dir, ".lock"))
	if err != nil {
		return nil, err
	}
	ok := false
	defer func() {
		if !ok {
			lock.Close()
		}
	}()
	journal, assets := filepath.Join(dir, "jobs"), filepath.Join(dir, "assets")
	if err = os.MkdirAll(journal, 0700); err != nil {
		return nil, err
	}
	if err = os.MkdirAll(assets, 0700); err != nil {
		return nil, err
	}
	assets, err = filepath.EvalSymlinks(assets)
	if err != nil {
		return nil, err
	}
	importer := options.Importer
	if importer == nil {
		importer, err = generation.NewResultImporter(generation.ImportOptions{Directory: assets})
		if err != nil {
			return nil, err
		}
	}
	factory := options.Factory
	if factory == nil {
		factory = BuiltinProvider
	}
	poll, timeout := options.PollInterval, options.TaskTimeout
	if poll <= 0 {
		poll = 5 * time.Second
	}
	if timeout <= 0 {
		timeout = 30 * time.Minute
	}
	ctx, cancel := context.WithCancel(context.Background())
	m := &Manager{entries: map[string]*entry{}, journal: journal, assets: assets, resolve: options.Resolve, factory: factory, importer: importer, pollInterval: poll, timeout: timeout, ctx: ctx, cancel: cancel, sem: make(chan struct{}, 1), lock: lock, done: make(chan struct{})}
	m.archives, m.idOwners = map[string]archivedFile{}, map[string]string{}
	m.archiveDir = filepath.Join(dir, "archive")
	if err = os.MkdirAll(m.archiveDir, 0700); err != nil {
		cancel()
		return nil, err
	}
	if info, e := os.Lstat(m.archiveDir); e != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		cancel()
		return nil, errors.New("归档目录不是有效的本地普通目录")
	}
	if err = m.load(); err != nil {
		cancel()
		return nil, err
	}
	if err = m.loadArchiveIndex(); err != nil {
		cancel()
		return nil, err
	}
	ok = true
	return m, nil
}

func signature(connectionID, projectID string, request generation.Request, estimates ...*CostEstimate) string {
	var estimate *CostEstimate
	if len(estimates) > 0 {
		estimate = estimates[0]
	}
	data, _ := json.Marshal(struct {
		ConnectionID string
		ProjectID    string
		Request      generation.Request
		Estimate     *CostEstimate `json:",omitempty"`
	}{connectionID, projectID, request, estimate})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func cloneJob(job Job) Job {
	data, _ := json.Marshal(job)
	var result Job
	_ = json.Unmarshal(data, &result)
	if result.Assets == nil {
		result.Assets = []generation.ImportedAsset{}
	}
	return result
}

func (m *Manager) duplicateLocked(id, sig string) (Job, bool, error) {
	if owner := m.idOwners[strings.ToLower(id)]; owner != "" && owner != id {
		return Job{}, true, errors.New("请求标识与已有记录存在大小写冲突，请使用不同标识")
	}
	if _, ok := m.archives[id]; ok {
		return Job{}, true, errors.New("该请求已归档，请先还原原记录；不会重新提交生成")
	}
	e, ok := m.entries[id]
	if !ok {
		return Job{}, false, nil
	}
	if e.corrupt {
		return Job{}, true, errors.New("该请求已有损坏记录，请核对服务记录，不能作为新生成提交")
	}
	if e.record.Signature != sig {
		return Job{}, true, errors.New("请求标识已用于不同内容，请勿重复使用")
	}
	return cloneJob(e.record.Job), true, nil
}

func referencesFor(project editor.Project, request generation.Request) (generation.ReferenceSource, error) {
	files := []generation.RegisteredFile{}
	seen := map[string]bool{}
	for _, reference := range request.References {
		if seen[reference.AssetID] {
			continue
		}
		var source *editor.Asset
		for i := range project.Assets {
			if project.Assets[i].ID == reference.AssetID {
				source = &project.Assets[i]
				break
			}
		}
		if source == nil || source.Kind != "image" || source.Missing {
			return nil, errors.New("参考图片必须来自当前工程中可读取的已导入素材")
		}
		files = append(files, generation.RegisteredFile{ID: source.ID, Path: source.Path, ExpectedSHA256: strings.TrimPrefix(source.Fingerprint, "sha256:")})
		seen[reference.AssetID] = true
	}
	return generation.NewFileReferences(files)
}

func (m *Manager) Submit(ctx context.Context, project editor.Project, input SubmitInput) (Job, error) {
	if err := ctx.Err(); err != nil {
		return Job{}, err
	}
	request := input.Request
	if request.GenerateAudio != nil {
		value := *request.GenerateAudio
		request.GenerateAudio = &value
	}
	request.References = append([]generation.Reference(nil), request.References...)
	if request.Count == 0 {
		request.Count = 1
	}
	if !safeID.MatchString(request.ID) || input.ConnectionID == "" || project.ID == "" || request.ProviderID == "" || request.ModelID == "" {
		return Job{}, errors.New("生成请求需要稳定标识、工程和明确的连接模型")
	}
	estimate, err := calculateEstimate(request, input.Estimate)
	if err != nil {
		return Job{}, err
	}
	sig := signature(input.ConnectionID, project.ID, request, estimate)
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return Job{}, errors.New("生成任务服务已关闭")
	}
	if job, ok, err := m.duplicateLocked(request.ID, sig); ok {
		m.mu.Unlock()
		return job, err
	}
	m.mu.Unlock()
	if !input.Consent {
		return Job{}, errors.New("请先核对模型、参考素材和服务商计费后明确生成")
	}
	refs, err := referencesFor(project, request)
	if err != nil {
		return Job{}, err
	}
	binding, err := m.resolve(ctx, input.ConnectionID)
	if err != nil {
		return Job{}, err
	}
	release := binding.Release
	if release == nil {
		release = func() {}
	}
	keep := false
	defer func() {
		if !keep {
			release()
		}
	}()
	if binding.ProviderID != request.ProviderID || binding.ModelID != request.ModelID {
		return Job{}, errors.New("连接与生成请求的服务商或模型不一致")
	}
	provider, err := m.factory(binding.ProviderID, binding.ModelID, binding.Secrets, refs)
	if err != nil {
		return Job{}, err
	}
	if err = provider.Validate(request); err != nil {
		return Job{}, err
	}
	if err = ctx.Err(); err != nil {
		return Job{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return Job{}, errors.New("生成任务服务已关闭")
	}
	if job, ok, err := m.duplicateLocked(request.ID, sig); ok {
		return job, err
	}
	active := 0
	for _, e := range m.entries {
		if e.active {
			active++
		}
	}
	if active >= 8 {
		return Job{}, errors.New("生成队列已达到当前上限，请先完成已有任务")
	}
	if len(m.entries) >= maxActiveRecords {
		return Job{}, errors.New("默认生成历史已满，请先归档已结束任务再提交")
	}
	now := time.Now().UTC()
	version := 1
	if estimate != nil {
		version = 2
	}
	e := &entry{record: record{Version: version, Signature: sig, Job: Job{ID: request.ID, ConnectionID: input.ConnectionID, ProjectID: project.ID, ProjectRevision: project.Revision, Request: request, Estimate: estimate, Status: Queued, Message: "已保存请求，等待本地提交", CreatedAt: now, UpdatedAt: now, Assets: []generation.ImportedAsset{}}}}
	if err = m.saveLocked(e); err != nil {
		return Job{}, err
	}
	m.entries[request.ID] = e
	m.idOwners[strings.ToLower(request.ID)] = request.ID
	keep = true
	m.startLocked(e, provider, release, false)
	return cloneJob(e.record.Job), nil
}

func (m *Manager) startLocked(e *entry, provider generation.Provider, release func(), resume bool) {
	ctx, cancel := context.WithTimeout(m.ctx, m.timeout)
	e.active, e.cancel = true, cancel
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		defer release()
		defer cancel()
		defer func() { m.mu.Lock(); e.active = false; e.cancel = nil; m.mu.Unlock() }()
		select {
		case m.sem <- struct{}{}:
			defer func() { <-m.sem }()
		case <-ctx.Done():
			m.interrupted(e)
			return
		}
		if ctx.Err() != nil {
			m.interrupted(e)
			return
		}
		m.run(ctx, e, provider, resume)
	}()
}

func (m *Manager) setLocked(e *entry, status, message string, resume bool) error {
	e.record.Job.Status, e.record.Job.Message, e.record.Job.CanResume = status, message, resume
	e.record.Job.UpdatedAt = time.Now().UTC()
	if err := m.saveLocked(e); err != nil {
		m.persistenceFailureLocked(e)
		return err
	}
	return nil
}

// 保存失败不能把已知远端任务永久留在不可恢复的运行状态。
func (m *Manager) persistenceFailureLocked(e *entry) {
	j := &e.record.Job
	if j.Status == Succeeded && len(j.Assets) > 0 {
		j.Message = "本地结果已保存，任务状态暂未写入；请保留应用数据"
		return
	}
	if validTask(*j, e.record.Task) && (e.record.Task.RemoteID != "" || e.record.Task.Status == generation.Succeeded) {
		j.Status, j.CanResume = Waiting, true
		j.Message = "任务状态暂未写入，恢复存储后可继续原任务；不会重新提交"
	} else if j.Status == Submitting {
		j.Status, j.CanResume = Failed, false
		j.Message = "提交状态无法保存，本次未发送生成请求"
	} else {
		j.Status, j.CanResume = Unknown, false
		j.Message = "无法保存任务状态，请保留本地数据并核对服务记录；不会自动重新提交"
	}
}

func (m *Manager) interrupted(e *entry) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if e.record.Job.Status == Succeeded || e.record.Job.Status == Failed {
		return
	}
	if e.record.Task.RemoteID != "" || e.record.Task.Status == generation.Succeeded {
		_ = m.setLocked(e, Waiting, "已停止本地等待，远端任务未被取消；可继续查询或取回结果", true)
	} else if e.record.Job.Status == Queued {
		_ = m.setLocked(e, Cancelled, "本地排队已取消，尚未提交", false)
	} else {
		_ = m.setLocked(e, Unknown, "提交结果未知，请核对服务记录；不会自动重新生成", false)
	}
}

func validTask(job Job, task generation.Task) bool {
	return task.ID == job.ID && task.ProviderID == job.Request.ProviderID && task.ModelID == job.Request.ModelID && task.Recipe.ID == job.ID && task.Recipe.ProviderID == task.ProviderID && task.Recipe.ModelID == task.ModelID
}

func (m *Manager) run(ctx context.Context, e *entry, provider generation.Provider, resume bool) {
	m.mu.Lock()
	request, task := e.record.Job.Request, e.record.Task
	if !resume {
		if err := m.setLocked(e, Submitting, "正在向生成服务提交，请勿重复生成", false); err != nil {
			m.mu.Unlock()
			return
		}
	}
	m.mu.Unlock()
	if resume && task.RemoteID != "" {
		// 重新取得原远端任务的结果地址，避免重复下载已经过期的签名 URL。
		task.Status = generation.Running
	}
	if !resume {
		result, err := provider.Submit(ctx, request)
		m.mu.Lock()
		valid := validTask(e.record.Job, result)
		if valid {
			e.record.Task = result
			e.record.Job.RemoteID = result.RemoteID
			task = result
		}
		if err != nil {
			var known *generation.Error
			if valid && result.RemoteID != "" {
				_ = m.setLocked(e, Waiting, "提交已有远端标识，可继续查询原任务", true)
			} else if valid && result.Status == generation.Failed && errors.As(err, &known) && !known.SubmissionUncertain {
				_ = m.setLocked(e, Failed, "生成服务拒绝了本次请求，请检查权限、余额或参数", false)
			} else if errors.As(err, &known) && !known.SubmissionUncertain && ctx.Err() == nil {
				_ = m.setLocked(e, Failed, "请求未完成，请检查参考素材、连接或参数；未自动重交", false)
			} else {
				_ = m.setLocked(e, Unknown, "提交结果未知，请核对服务记录；不会自动重新生成", false)
			}
			m.mu.Unlock()
			return
		}
		if !valid {
			_ = m.setLocked(e, Unknown, "服务结果与原请求身份不一致，请核对服务记录", false)
			m.mu.Unlock()
			return
		}
		if err = m.saveLocked(e); err != nil {
			m.persistenceFailureLocked(e)
			m.mu.Unlock()
			return
		}
		m.mu.Unlock()
	}
	for {
		if task.Status == generation.Succeeded && !task.SubmissionUncertain {
			m.importResult(ctx, e, task)
			return
		}
		if task.Status == generation.Failed || task.Status == generation.Cancelled {
			m.mu.Lock()
			_ = m.setLocked(e, Failed, "生成服务报告任务未成功", false)
			m.mu.Unlock()
			return
		}
		if task.RemoteID == "" {
			m.mu.Lock()
			_ = m.setLocked(e, Unknown, "服务没有返回可恢复的任务标识，请核对服务记录", false)
			m.mu.Unlock()
			return
		}
		if ctx.Err() != nil {
			m.interrupted(e)
			return
		}
		m.mu.Lock()
		if err := m.setLocked(e, Running, "生成服务处理中，可停止本地等待", false); err != nil {
			m.mu.Unlock()
			return
		}
		m.mu.Unlock()
		if !resume {
			timer := time.NewTimer(m.pollInterval)
			select {
			case <-ctx.Done():
				timer.Stop()
				m.interrupted(e)
				return
			case <-timer.C:
			}
		}
		resume = false
		result, err := provider.Get(ctx, task)
		m.mu.Lock()
		if err != nil {
			_ = m.setLocked(e, Waiting, "查询中断，可继续查询同一远端任务；未重新生成", true)
			m.mu.Unlock()
			return
		}
		if !validTask(e.record.Job, result) || result.RemoteID != task.RemoteID {
			_ = m.setLocked(e, Waiting, "查询返回的身份不一致，保留原任务供核对", true)
			m.mu.Unlock()
			return
		}
		task = result
		e.record.Task = result
		e.record.Job.RemoteID = result.RemoteID
		if err = m.saveLocked(e); err != nil {
			m.persistenceFailureLocked(e)
			m.mu.Unlock()
			return
		}
		m.mu.Unlock()
	}
}

func (m *Manager) importResult(ctx context.Context, e *entry, task generation.Task) {
	m.mu.Lock()
	if err := m.setLocked(e, Importing, "生成已完成，正在将结果保存到本机", false); err != nil {
		m.mu.Unlock()
		return
	}
	m.mu.Unlock()
	assets, err := m.importer.Import(ctx, task)
	m.mu.Lock()
	defer m.mu.Unlock()
	if err != nil {
		if len(task.Outputs) > 0 && (len(task.Outputs[0].Data) > 0 || task.Outputs[0].URL != "") || task.RemoteID != "" {
			_ = m.setLocked(e, Waiting, "生成结果尚未落盘，可继续取回已有结果；不会重新生成", true)
		} else {
			_ = m.setLocked(e, Unknown, "同步生成的返回内容未保存，请核对服务记录；不会重新生成", false)
		}
		return
	}
	e.record.Job.Assets = assets
	// 导入已原子提交，之后才到达的取消不能将成功伪报为取消。
	_ = m.setLocked(e, Succeeded, "生成结果已保存在本机，可选择加入工程素材库", false)
	e.record.Task.Outputs = nil
}

func (m *Manager) Get(id string) (Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.entries[id]
	if !ok {
		if _, archived := m.archives[id]; archived {
			return Job{}, errors.New("生成任务已归档，请先还原记录")
		}
		return Job{}, errors.New("找不到生成任务")
	}
	return cloneJob(e.record.Job), nil
}

// 成功配方只读深拷贝，不返回临时下载地址或提交凭据。
func (m *Manager) CompletedRecipe(id string) (generation.Recipe, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.entries[id]
	if !ok || e.corrupt || e.record.Job.Status != Succeeded || e.record.Job.CanResume || !validTask(e.record.Job, e.record.Task) || e.record.Task.Recipe.ID != id {
		return generation.Recipe{}, errors.New("来源任务没有可复用的成功配方")
	}
	data, err := json.Marshal(e.record.Task.Recipe)
	var result generation.Recipe
	if err == nil {
		err = json.Unmarshal(data, &result)
	}
	return result, err
}
func (m *Manager) List() []Job {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]Job, 0, len(m.entries))
	for _, e := range m.entries {
		result = append(result, cloneJob(e.record.Job))
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].CreatedAt.Equal(result[j].CreatedAt) {
			return result[i].ID > result[j].ID
		}
		return result[i].CreatedAt.After(result[j].CreatedAt)
	})
	return result
}
func (m *Manager) StopWaiting(id string) (Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.entries[id]
	if !ok {
		return Job{}, errors.New("找不到生成任务")
	}
	if e.cancel != nil {
		e.cancel()
	}
	return cloneJob(e.record.Job), nil
}
func (m *Manager) Resume(ctx context.Context, id string) (Job, error) {
	m.mu.Lock()
	e, ok := m.entries[id]
	if !ok {
		m.mu.Unlock()
		return Job{}, errors.New("找不到生成任务")
	}
	if m.closed || e.active || e.corrupt || !e.record.Job.CanResume {
		m.mu.Unlock()
		return Job{}, errors.New("当前任务不能恢复或仍在结束本地等待")
	}
	job, task := e.record.Job, e.record.Task
	m.mu.Unlock()
	if task.Status == generation.Succeeded && !task.SubmissionUncertain {
		assets, found, err := m.importer.RecoverLocal(ctx, task)
		if err != nil {
			return Job{}, err
		}
		if found {
			m.mu.Lock()
			defer m.mu.Unlock()
			if m.closed || e.active || !e.record.Job.CanResume {
				return Job{}, errors.New("任务状态已变化，请刷新后再继续")
			}
			e.record.Job.Assets = assets
			if err := m.setLocked(e, Succeeded, "已有本地结果已恢复，可选择加入工程素材库", false); err != nil {
				return cloneJob(e.record.Job), err
			}
			return cloneJob(e.record.Job), nil
		}
	}
	var provider generation.Provider
	release := func() {}
	if task.Status != generation.Succeeded || task.RemoteID != "" {
		binding, err := m.resolve(ctx, job.ConnectionID)
		if err != nil {
			return Job{}, err
		}
		if binding.Release != nil {
			release = binding.Release
		}
		if binding.ProviderID != job.Request.ProviderID || binding.ModelID != job.Request.ModelID {
			release()
			return Job{}, errors.New("原连接模型已变化，无法查询此任务")
		}
		provider, err = m.factory(binding.ProviderID, binding.ModelID, binding.Secrets, nil)
		if err != nil {
			release()
			return Job{}, err
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || e.active || !e.record.Job.CanResume || ctx.Err() != nil {
		release()
		return Job{}, errors.New("任务状态已变化，请刷新后再继续")
	}
	if task.RemoteID == "" && task.Status != generation.Succeeded {
		release()
		return Job{}, errors.New("任务缺少可恢复的远端标识")
	}
	if err := m.setLocked(e, Waiting, "正在继续原任务，不会重新提交生成", false); err != nil {
		release()
		return Job{}, err
	}
	m.startLocked(e, provider, release, true)
	return cloneJob(e.record.Job), nil
}
func (m *Manager) AcknowledgeUnknown(id string) (Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.entries[id]
	if !ok || e.corrupt || e.active || e.record.Job.Status != Unknown {
		return Job{}, errors.New("当前任务不能标记为已核对")
	}
	e.record.Job.Acknowledged = true
	if err := m.setLocked(e, Unknown, "已记录用户核对；应用不会重新提交或声称远端已取消", false); err != nil {
		return Job{}, err
	}
	return cloneJob(e.record.Job), nil
}
func (m *Manager) MarkImported(id string, ids []string) (Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.entries[id]
	if !ok || e.record.Job.Status != Succeeded {
		return Job{}, errors.New("只有成功落盘的任务能标记已导入")
	}
	allowed, selected := map[string]bool{}, map[string]bool{}
	for _, a := range e.record.Job.Assets {
		allowed[a.ID] = true
	}
	for _, id := range append(append([]string{}, e.record.Job.ImportedAssetIDs...), ids...) {
		if !allowed[id] {
			return Job{}, errors.New("导入候选不属于此任务")
		}
		selected[id] = true
	}
	e.record.Job.ImportedAssetIDs = nil
	for id := range selected {
		e.record.Job.ImportedAssetIDs = append(e.record.Job.ImportedAssetIDs, id)
	}
	sort.Strings(e.record.Job.ImportedAssetIDs)
	e.record.Job.Imported = len(selected) == len(allowed) && len(allowed) > 0
	if err := m.saveLocked(e); err != nil {
		return Job{}, err
	}
	return cloneJob(e.record.Job), nil
}
func (m *Manager) Close() {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.closed = true
	m.cancel()
	m.mu.Unlock()
	// 保持进程锁直到所有提交与文件发布结束，不能让第二实例接管仍在运行的请求。
	go func() { m.wg.Wait(); m.lock.Close(); close(m.done) }()
	select {
	case <-m.done:
	case <-time.After(3 * time.Second):
	}
}

// Done 仅在 Close 后所有工作及数据目录锁均已释放时关闭。
func (m *Manager) Done() <-chan struct{} { return m.done }
