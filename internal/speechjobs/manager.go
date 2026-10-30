package speechjobs

import (
	"aividcut/internal/editor"
	"aividcut/internal/generation"
	"aividcut/internal/jobs"
	"aividcut/internal/speech"
	"aividcut/internal/statefilelock"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

type speechAudio struct {
	Result speech.AudioResult
	Timing *speech.TimedAudioResult
}

type entry struct {
	record          record
	identity        Summary
	active, corrupt bool
	queueID         string
	release         func()
	partResults     map[int]speech.Transcription
	audio           *speechAudio
	voice           *speech.CloneResult
}

type Manager struct {
	mu               sync.Mutex
	entries          map[string]*entry
	options          Options
	journal, outputs string
	queue            *jobs.Manager
	lock             *statefilelock.Lock
	closed           bool
	done             chan struct{}
	writeFile        func(string, []byte) error
	removePrepared   func(string) error
}

func New(options Options) (*Manager, error) {
	if strings.TrimSpace(options.Directory) == "" || options.Resolve == nil {
		return nil, errors.New("语音任务需要独立持久目录和连接解析器")
	}
	directory, err := filepath.Abs(options.Directory)
	if err != nil {
		return nil, errors.New("语音任务目录无效")
	}
	lock, err := statefilelock.Acquire(filepath.Join(directory, ".lock"))
	if err != nil {
		return nil, err
	}
	ok := false
	defer func() {
		if !ok {
			_ = lock.Close()
		}
	}()
	journal, outputs := filepath.Join(directory, "jobs"), filepath.Join(directory, "results")
	if err := os.MkdirAll(journal, 0700); err != nil {
		return nil, errors.New("无法创建语音任务状态目录")
	}
	if err := os.MkdirAll(outputs, 0700); err != nil {
		return nil, errors.New("无法创建语音结果目录")
	}
	outputs, err = filepath.EvalSymlinks(outputs)
	if err != nil {
		return nil, errors.New("无法解析语音结果目录")
	}
	if options.Factory == nil {
		options.Factory = func(secrets generation.SecretSource, files speech.MediaSource) (speech.Provider, error) {
			return speech.NewElevenLabs(speech.Config{Secrets: secrets, Files: files})
		}
	}
	m := &Manager{entries: map[string]*entry{}, options: options, journal: journal, outputs: outputs, lock: lock, done: make(chan struct{}), writeFile: atomicWrite, removePrepared: os.RemoveAll}
	if err := m.load(); err != nil {
		return nil, err
	}
	m.queue = jobs.NewWithObserver(m.observe)
	ok = true
	return m, nil
}

func normalizeInput(project editor.Project, input Input) (Input, []editor.Asset, error) {
	if input.Synthesize != nil && !utf8.ValidString(input.Synthesize.Text) || input.Clone != nil && (!utf8.ValidString(input.Clone.Name) || !utf8.ValidString(input.Clone.Description) || !utf8.ValidString(input.Clone.Consent.Subject)) {
		return Input{}, nil, errors.New("语音文字与声明必须为有效UTF-8，不能替换无效字符后提交")
	}
	input = cloneJSON(input)
	if !safeID.MatchString(input.ID) || input.ConnectionID == "" || project.ID == "" {
		return Input{}, nil, errors.New("语音任务需要稳定请求标识、明确连接和原目标工程")
	}
	count := 0
	if input.Transcribe != nil {
		count++
	}
	if input.Synthesize != nil {
		count++
	}
	if input.Clone != nil {
		count++
	}
	if count != 1 {
		return Input{}, nil, errors.New("语音任务必须且只能包含对应模式的一个请求")
	}
	var sourceIDs []string
	switch input.Mode {
	case Transcribe:
		if input.Transcribe == nil {
			return Input{}, nil, errors.New("转写模式需要转写请求")
		}
		r := input.Transcribe
		if r.ID != "" && r.ID != input.ID {
			return Input{}, nil, errors.New("语音操作标识与任务标识不一致")
		}
		r.ID = input.ID
		if r.ModelID == "" {
			r.ModelID = speech.ScribeV2
		}
		if r.Granularity == "" {
			r.Granularity = "word"
		}
		sourceIDs = []string{r.AssetID}
	case Synthesize:
		if input.Synthesize == nil {
			return Input{}, nil, errors.New("配音模式需要配音请求")
		}
		r := input.Synthesize
		if r.ID != "" && r.ID != input.ID {
			return Input{}, nil, errors.New("语音操作标识与任务标识不一致")
		}
		r.ID = input.ID
		if r.ModelID == "" {
			r.ModelID = speech.MultilingualV2
		}
		if r.OutputFormat == "" {
			r.OutputFormat = "mp3_44100_128"
		}
		if !operationID.MatchString(r.VoiceID) || strings.TrimSpace(r.Text) == "" || len([]rune(r.Text)) > 10000 {
			return Input{}, nil, errors.New("配音需要有效音色和非空且不超过10000字符的文本")
		}
	case Clone:
		if input.Clone == nil {
			return Input{}, nil, errors.New("克隆模式需要音色请求")
		}
		r := input.Clone
		if r.ID != "" && r.ID != input.ID {
			return Input{}, nil, errors.New("语音操作标识与任务标识不一致")
		}
		r.ID = input.ID
		if strings.TrimSpace(r.Name) == "" || len(r.Name) > 500 || !r.Consent.Confirmed || strings.TrimSpace(r.Consent.Subject) == "" || r.Consent.Authority != "self" && r.Consent.Authority != "authorized" || len(r.AssetIDs) < 1 || len(r.AssetIDs) > 5 {
			return Input{}, nil, errors.New("自定义音色需要名称、1至5份音频和明确的主体/本人或授权声明")
		}
		sourceIDs = r.AssetIDs
	default:
		return Input{}, nil, errors.New("不支持的语音任务模式")
	}
	refs, seen := []editor.Asset{}, map[string]bool{}
	for _, id := range sourceIDs {
		if id == "" || seen[id] {
			return Input{}, nil, errors.New("语音素材标识为空或重复")
		}
		seen[id] = true
		found := false
		for _, asset := range project.Assets {
			if asset.ID != id {
				continue
			}
			found = true
			if asset.Missing || asset.DurationUs <= 0 || hashValue(asset.Fingerprint) == "" || !filepath.IsAbs(asset.Path) || asset.Kind != "audio" && asset.Kind != "video" || asset.Kind == "video" && !asset.HasAudio || input.Mode == Clone && asset.Kind != "audio" {
				return Input{}, nil, errors.New("语音输入必须是当前工程已核对指纹、有效时长且包含声音的本地素材")
			}
			asset.URL, asset.ThumbnailURL = "", ""
			refs = append(refs, asset)
			break
		}
		if !found {
			return Input{}, nil, errors.New("语音输入不属于当前工程")
		}
	}
	return input, refs, nil
}

func checkBinding(input Input, binding Binding, provider speech.Provider) error {
	if provider == nil {
		return errors.New("语音服务没有返回适配器")
	}
	cap := provider.Capabilities()
	if cap.ProviderID == "" || cap.ProviderID != binding.ProviderID {
		return errors.New("语音连接与适配器服务商不一致")
	}
	contains := func(values []string, value string) bool {
		for _, candidate := range values {
			if candidate == value {
				return true
			}
		}
		return false
	}
	switch input.Mode {
	case Transcribe:
		if !cap.Transcription.Supported || binding.ModelID != input.Transcribe.ModelID || !contains(cap.ASRModels, binding.ModelID) || !contains(cap.TimestampGranularities, input.Transcribe.Granularity) {
			return errors.New("转写请求与连接模型或时间戳能力不一致")
		}
	case Synthesize:
		if !cap.SpeechSynthesis.Supported || binding.ModelID != input.Synthesize.ModelID || !contains(cap.TTSModels, binding.ModelID) || input.Synthesize.OutputFormat != "mp3_44100_128" || !contains(cap.OutputFormats, input.Synthesize.OutputFormat) {
			return errors.New("配音请求与连接模型或输出格式不一致")
		}
		if input.Synthesize.WithTiming {
			if _, ok := provider.(speech.TimedTTSProvider); !ok || !cap.SpeechTiming.Supported {
				return errors.New("此适配器不支持显式带时间配音；未回退为普通配音，未提交请求")
			}
		}
	case Clone:
		if !cap.InstantCloning.Supported || !contains(cap.TTSModels, binding.ModelID) {
			return errors.New("自定义音色需要支持该能力的配音模型连接")
		}
	}
	return nil
}

func (m *Manager) duplicateLocked(id, sig string) (Job, bool, error) {
	e, found := m.entries[id]
	if !found {
		return Job{}, false, nil
	}
	if e.corrupt || e.record.Signature != sig {
		return Job{}, true, errors.New("请求标识已有不同内容或损坏记录，不会作为新语音生成再次提交")
	}
	job, err := m.viewLocked(e)
	return job, true, err
}

func (m *Manager) Submit(ctx context.Context, project editor.Project, input Input) (Job, error) {
	if err := ctx.Err(); err != nil {
		return Job{}, err
	}
	input, refs, err := normalizeInput(project, input)
	if err != nil {
		return Job{}, err
	}
	r := record{Version: 1, Input: input, Sources: refs, Summary: Summary{ID: input.ID, ConnectionID: input.ConnectionID, ProjectID: project.ID}}
	r.Signature = recordSignature(r)
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return Job{}, errors.New("语音任务服务已关闭")
	}
	if job, found, err := m.duplicateLocked(input.ID, r.Signature); found {
		m.mu.Unlock()
		return job, err
	}
	m.mu.Unlock()
	if !input.Consent {
		return Job{}, errors.New("请先核对语音服务商、模型、原素材和计费后明确提交")
	}
	if input.Mode == Transcribe && m.options.Prepare == nil {
		return Job{}, errors.New("尚未配置本地音轨准备，不能直接提交未验证的长视频")
	}
	if input.Mode == Synthesize && m.options.Probe == nil && m.options.ProbeAudio == nil {
		return Job{}, errors.New("尚未配置音频探测，不能将未核查时长的配音标为完成")
	}
	binding, err := m.options.Resolve(ctx, input.ConnectionID)
	if err != nil {
		return Job{}, errors.New("无法取得语音连接，请检查配置和系统凭据库")
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
	provider, err := m.options.Factory(binding.Secrets, nil)
	if err != nil {
		return Job{}, errors.New("无法创建语音适配器")
	}
	if err := checkBinding(input, binding, provider); err != nil {
		return Job{}, err
	}
	if err := ctx.Err(); err != nil {
		return Job{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return Job{}, errors.New("语音任务服务已关闭")
	}
	if job, found, err := m.duplicateLocked(input.ID, r.Signature); found {
		return job, err
	}
	if len(m.entries) >= 2000 {
		return Job{}, errors.New("语音历史达到当前上限，请先归档任务数据")
	}
	now := time.Now().UTC()
	r.Summary = Summary{ID: input.ID, ConnectionID: input.ConnectionID, ProjectID: project.ID, ProjectRevision: project.Revision, ProviderID: binding.ProviderID, ModelID: binding.ModelID, Mode: input.Mode, Status: Queued, Message: "请求已保存，等待本地准备", CreatedAt: now, UpdatedAt: now, Warnings: []string{}}
	if input.Mode == Synthesize && input.Synthesize.WithTiming {
		r.Summary.TimingRequested, r.Summary.TimingReason = true, "尚未收到带时间配音响应"
	}
	if input.Mode == Transcribe {
		r.Summary.SourceAssetID, r.Summary.SourceFingerprint, r.Summary.SourceDurationUs = refs[0].ID, refs[0].Fingerprint, refs[0].DurationUs
	}
	e := &entry{record: r, identity: r.Summary, active: true, release: release, partResults: map[int]speech.Transcription{}}
	if err := m.saveLocked(e); err != nil {
		return Job{}, err
	}
	m.entries[input.ID] = e
	queued, err := m.queue.Start("speech:"+input.ID, "准备语音任务", func(workCtx context.Context, _ func(float64, string)) (string, error) {
		return "", m.run(workCtx, e, binding)
	})
	if err != nil {
		e.active = false
		e.release = nil
		m.stateLocked(e, Failed, "本地队列未受理，尚未提交语音请求", false)
		return Job{}, errors.New("本地语音队列已满或正在关闭，未提交收费请求")
	}
	e.queueID = queued.ID
	keep = true
	return m.viewLocked(e)
}

func (m *Manager) saveLocked(e *entry) error {
	return m.writeJSON(filepath.Join(m.journal, e.record.Summary.ID+".json"), e.record, 2<<20)
}
func (m *Manager) stateLocked(e *entry, status, message string, resume bool) error {
	e.record.Summary.Status, e.record.Summary.Message, e.record.Summary.CanResumeLocal = status, message, resume
	e.record.Summary.UpdatedAt = time.Now().UTC()
	return m.saveLocked(e)
}

func (m *Manager) observe(job jobs.Job) {
	id := strings.TrimPrefix(job.Type, "speech:")
	m.mu.Lock()
	e := m.entries[id]
	if e == nil {
		m.mu.Unlock()
		return
	}
	e.active, e.queueID = false, ""
	if e.record.Summary.Status != Succeeded && e.record.Summary.Status != Failed && e.record.Summary.Status != Unknown && e.record.Summary.Status != Cancelled && e.record.Summary.Status != Saving {
		if e.record.Intent {
			_ = m.stateLocked(e, Unknown, "语音提交结果未知，请核对原请求；应用不会自动重发", false)
		} else if job.Status == "cancelled" {
			_ = m.stateLocked(e, Cancelled, "已取消本地等待，尚未发起收费请求", false)
		} else {
			_ = m.stateLocked(e, Failed, "语音准备或本地工作未完成，尚未发起收费请求", false)
		}
	}
	release := e.release
	e.release = nil
	m.mu.Unlock()
	if release != nil {
		release()
	}
}

func (m *Manager) Get(id string) (Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e := m.entries[id]
	if e == nil {
		return Job{}, errors.New("找不到语音任务")
	}
	return m.viewLocked(e)
}
func (m *Manager) List() []Summary {
	m.mu.Lock()
	defer m.mu.Unlock()
	values := []Summary{}
	for _, e := range m.entries {
		values = append(values, cloneJSON(e.record.Summary))
	}
	sort.Slice(values, func(i, j int) bool {
		if values[i].CreatedAt.Equal(values[j].CreatedAt) {
			return values[i].ID > values[j].ID
		}
		return values[i].CreatedAt.After(values[j].CreatedAt)
	})
	return values
}

func (m *Manager) Cancel(id string) (Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e := m.entries[id]
	if e == nil {
		return Job{}, errors.New("找不到语音任务")
	}
	if e.active && e.record.Summary.Status != Succeeded && e.record.Summary.Status != Failed && e.record.Summary.Status != Unknown && e.record.Summary.Status != Cancelled {
		if e.record.Summary.CanResumeLocal {
			_, _ = m.queue.Cancel(e.queueID)
			return m.viewLocked(e)
		}
		_ = m.stateLocked(e, Cancelling, "正在停止本地等待；不代表服务商取消了远端处理", false)
		_, _ = m.queue.Cancel(e.queueID)
	}
	return m.viewLocked(e)
}

func (m *Manager) ResumeLocal(ctx context.Context, id string) (Job, error) {
	if err := ctx.Err(); err != nil {
		return Job{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	e := m.entries[id]
	if e == nil || e.corrupt || e.active || m.closed || !e.record.Summary.CanResumeLocal {
		return Job{}, errors.New("当前语音任务没有可恢复的本地结果，不能重新提交收费请求")
	}
	if err := m.stateLocked(e, Saving, "正在保存已有结果，不会重新调用语音生成", false); err != nil {
		e.record.Summary.CanResumeLocal = true
		return Job{}, err
	}
	e.active = true
	queued, err := m.queue.Start("speech:"+id, "恢复本地语音结果", func(workCtx context.Context, _ func(float64, string)) (string, error) {
		return "", m.finishLocal(workCtx, e)
	})
	if err != nil {
		e.active = false
		e.record.Summary.CanResumeLocal = true
		return Job{}, errors.New("本地恢复队列暂不可用")
	}
	e.queueID = queued.ID
	return m.viewLocked(e)
}

func (m *Manager) AcknowledgeUnknown(id string) (Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e := m.entries[id]
	if e == nil || e.corrupt || e.active || e.record.Summary.Status != Unknown {
		return Job{}, errors.New("当前语音任务不能标为已核对")
	}
	e.record.Summary.Acknowledged = true
	if err := m.stateLocked(e, Unknown, "已记录用户核对；不会重发或声称远端已取消", false); err != nil {
		return Job{}, err
	}
	return m.viewLocked(e)
}

func (m *Manager) Close() {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.closed = true
	m.mu.Unlock()
	m.queue.Close()
	finish := func() { <-m.queue.Done(); _ = m.lock.Close(); close(m.done) }
	select {
	case <-m.queue.Done():
		finish()
	default:
		go finish()
	}
}
func (m *Manager) Done() <-chan struct{} { return m.done }

func (m *Manager) output(id, name string) string { return filepath.Join(m.outputs, id, name) }
func (m *Manager) partPath(id string, index int) string {
	return m.output(id, fmt.Sprintf("part-%04d.json", index+1))
}
