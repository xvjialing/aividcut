package application

import (
	"errors"
	"math"
	"sync"
	"time"

	"aividcut/internal/editor"
	"aividcut/internal/projectrecovery"
	"aividcut/internal/snapshotstorage"
)

type RecoveryOptions struct {
	Directory string
	Interval  time.Duration
}
type RecoveryStatus struct {
	Scope             ProjectScope `json:"scope"`
	Enabled           bool         `json:"enabled"`
	IntervalSeconds   float64      `json:"intervalSeconds"`
	InputDrafts       int          `json:"inputDrafts"`
	Pending           int          `json:"pending"`
	StartupPending    int          `json:"startupPending"`
	Unreadable        int          `json:"unreadable"`
	ProtectedAt       *time.Time   `json:"protectedAt,omitempty"`
	ProtectedRevision int64        `json:"protectedRevision"`
	Error             string       `json:"error,omitempty"`
}
type RecoveryListInput struct {
	Offset int `json:"offset"`
	Limit  int `json:"limit"`
}
type RestoreRecoveryInput struct {
	ProjectScope
	ID     string `json:"id"`
	SHA256 string `json:"sha256"`
}
type recoveryKey struct {
	engine          *editor.Engine
	revision        int64
	savedHash, path string
}
type recoveryState struct {
	ops                                sync.Mutex
	store                              recoveryStorage
	interval                           time.Duration
	stop                               chan struct{}
	done                               chan struct{}
	closing                            bool
	activeID, inflightID, originalPath string
	checked, suppressed                recoveryKey
	protectedAt                        *time.Time
	protectedRevision                  int64
	lastError                          string
}

type recoveryStorage interface {
	Write(string, editor.Project, string, bool) (projectrecovery.Summary, error)
	Retire(string) error
	ForgetRetired(string)
	List(string, int, int) (projectrecovery.Page, error)
	Load(string, string) (editor.Project, projectrecovery.Summary, []string, error)
	Close() error
	StorageItems(string) ([]snapshotstorage.Item, error)
	ChangeStorage(snapshotstorage.Proof, string, []string) error
}

func (s *Service) ConfigureRecovery(options RecoveryOptions) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.recoveryClosed {
		return errors.New("自动恢复服务已关闭")
	}
	if s.recovery != nil {
		return errors.New("自动恢复目录已经配置")
	}
	interval := options.Interval
	if interval == 0 {
		interval = 5 * time.Second
	}
	if interval < 10*time.Millisecond {
		return errors.New("自动恢复间隔过短")
	}
	store, err := projectrecovery.Open(options.Directory)
	if err != nil {
		return err
	}
	r := &recoveryState{store: store, interval: interval, stop: make(chan struct{}), done: make(chan struct{})}
	s.recovery = r
	go func() {
		defer close(r.done)
		defer store.Close()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				_ = s.checkpointRecovery(r, false)
			case <-r.stop:
				_ = s.checkpointRecovery(r, true)
				return
			}
		}
	}()
	return nil
}

func (s *Service) recoveryKeyLocked() recoveryKey {
	_, rev := s.engine.Identity()
	return recoveryKey{engine: s.engine, revision: rev, savedHash: s.savedHash, path: s.projectPath}
}

// 编辑锁只用于捕获快照与发布状态，周期文件I/O在锁外且单写入，不阻塞普通编辑。
func (s *Service) checkpointRecovery(r *recoveryState, closing bool) error {
	r.ops.Lock()
	defer r.ops.Unlock()
	s.mu.Lock()
	if s.recovery != r || (r.closing && !closing) {
		s.mu.Unlock()
		return errors.New("自动恢复服务正在关闭")
	}
	key := s.recoveryKeyLocked()
	if key == r.suppressed || (key == r.checked && r.lastError == "") {
		s.mu.Unlock()
		return nil
	}
	p := s.engine.Snapshot()
	if projectContentHash(p) == s.savedHash {
		err := s.retireRecoveryLocked()
		r.checked = key
		s.mu.Unlock()
		return err
	}
	if r.activeID == "" {
		r.activeID = editor.NewID("recovery")
	}
	id := r.activeID
	r.inflightID = id
	path := s.projectPath
	if path == "" {
		path = r.originalPath
	}
	s.mu.Unlock()
	v, err := r.store.Write(id, p, path, false)
	s.mu.Lock()
	defer s.mu.Unlock()
	r.inflightID = ""
	current := s.recoveryKeyLocked()
	if r.activeID != id || current.engine != key.engine || current.savedHash != key.savedHash || current.path != key.path {
		_ = r.store.Retire(id)
		r.store.ForgetRetired(id)
		return nil
	}
	if err != nil {
		if !errors.Is(err, projectrecovery.ErrRetired) {
			r.lastError = "恢复副本写入失败，请及时手动保存并检查磁盘或恢复资料容量。"
		}
		return err
	}
	r.checked = key
	r.lastError = ""
	at := v.UpdatedAt
	r.protectedAt = &at
	r.protectedRevision = v.Revision
	return nil
}

// 调用方持编辑锁；退役与存储串行，旧快照不能在保存或切换后复活。
func (s *Service) retireRecoveryLocked() error {
	r := s.recovery
	if r == nil {
		return nil
	}
	id := r.activeID
	r.activeID = ""
	r.originalPath = ""
	r.checked = recoveryKey{}
	r.protectedAt = nil
	r.protectedRevision = 0
	r.lastError = ""
	if id == "" {
		return nil
	}
	err := r.store.Retire(id)
	if r.inflightID != id {
		r.store.ForgetRetired(id)
	}
	if err != nil {
		r.lastError = "工程操作已完成，但旧恢复副本清理失败，请检查恢复中心。"
	}
	return err
}

func (s *Service) DiscardCurrentRecovery() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.recovery == nil {
		return nil
	}
	err := s.retireRecoveryLocked()
	s.recovery.suppressed = s.recoveryKeyLocked()
	return err
}

func (s *Service) GetRecoveryStatus() RecoveryStatus {
	s.mu.Lock()
	r := s.recovery
	if r == nil || s.recoveryClosed {
		s.mu.Unlock()
		return RecoveryStatus{}
	}
	id := r.activeID
	status := RecoveryStatus{Scope: s.projectScopeLocked(), Enabled: true, IntervalSeconds: r.interval.Seconds(), ProtectedAt: r.protectedAt, ProtectedRevision: r.protectedRevision, Error: r.lastError}
	if s.drafts != nil && !s.draftsClosed {
		count, err := s.drafts.store.Count()
		if err != nil {
			status.Error = "无法统计属性输入草稿，请在恢复中心核对。"
		} else {
			status.InputDrafts = count
		}
		if s.drafts.lastError != "" {
			status.Error = s.drafts.lastError
		}
	}
	if status.ProtectedAt != nil {
		at := *status.ProtectedAt
		status.ProtectedAt = &at
	}
	s.mu.Unlock()
	page, err := r.store.List(id, 0, 50)
	if err != nil {
		status.Error = "无法读取恢复资料，请及时手动保存并检查恢复目录。"
	} else {
		status.Pending = page.Total
		status.Unreadable = page.Unreadable
		for _, record := range page.Records {
			if !record.BeforeRestore {
				status.StartupPending++
			}
		}
	}
	return status
}

func (s *Service) ListProjectRecoveries(input RecoveryListInput) (projectrecovery.Page, error) {
	s.mu.Lock()
	r := s.recovery
	if r == nil || r.closing {
		s.mu.Unlock()
		return projectrecovery.Page{}, errors.New("自动恢复服务未配置或正在关闭")
	}
	id := r.activeID
	s.mu.Unlock()
	return r.store.List(id, input.Offset, input.Limit)
}
func (s *Service) CheckpointRecovery() (RecoveryStatus, error) {
	s.mu.Lock()
	r := s.recovery
	s.mu.Unlock()
	if r == nil {
		return RecoveryStatus{}, errors.New("自动恢复服务未配置")
	}
	err := s.checkpointRecovery(r, false)
	return s.GetRecoveryStatus(), err
}

func (s *Service) RestoreProjectRecovery(input RestoreRecoveryInput) (Bootstrap, error) {
	s.mu.Lock()
	r := s.recovery
	s.mu.Unlock()
	if r == nil {
		return Bootstrap{}, errors.New("自动恢复服务未配置")
	}
	r.ops.Lock()
	defer r.ops.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	if r.closing {
		return Bootstrap{}, errors.New("自动恢复服务正在关闭")
	}
	if err := s.checkProjectScopeLocked(input.ProjectScope); err != nil {
		return Bootstrap{}, err
	}
	if input.ID == r.activeID {
		return Bootstrap{}, errors.New("所选恢复点正在保护当前工程，无需再次恢复")
	}
	p, meta, warnings, err := r.store.Load(input.ID, input.SHA256)
	if err != nil {
		return Bootstrap{}, err
	}
	current := s.engine.Snapshot()
	revision := max(p.Revision, current.Revision)
	if revision == math.MaxInt64 {
		return Bootstrap{}, errors.New("工程修订已达到上限")
	}
	p.Revision = revision + 1
	engine, err := editor.NewEngine(p)
	if err != nil {
		return Bootstrap{}, err
	}
	if projectContentHash(current) != s.savedHash {
		id := r.activeID
		if id == "" {
			id = editor.NewID("recovery")
		}
		path := s.projectPath
		if path == "" {
			path = r.originalPath
		}
		backup, err := r.store.Write(id, current, path, true)
		if err != nil {
			return Bootstrap{}, errors.New("无法保留当前工程的恢复副本，已停止恢复：" + err.Error())
		}
		r.activeID = id
		r.checked = s.recoveryKeyLocked()
		r.suppressed = recoveryKey{}
		at := backup.UpdatedAt
		r.protectedAt = &at
		r.protectedRevision = backup.Revision
	} else {
		_ = s.retireRecoveryLocked()
	}
	updated, err := r.store.Write(meta.ID, p, meta.ProjectPath, false)
	// 当前没有待丢失的工作（或已成功备份）后，恢复点无法更新也不能阻止读回内存。
	// 原恢复文件保持可读，用户仍可另存到可写位置；后台继续显示异常并重试。
	updateFailed := err != nil
	if updateFailed {
		updated = meta
	}
	s.engine = engine
	s.projectPath = ""
	s.savedHash = ""
	s.clipboardCuts, s.clipboardCutOrder = nil, nil
	s.resetWaveforms(false)
	s.resetThumbnails(false)
	s.files.Reset()
	s.urls = map[string]mediaURL{}
	r.activeID = meta.ID
	r.originalPath = meta.ProjectPath
	r.checked = s.recoveryKeyLocked()
	r.suppressed = recoveryKey{}
	r.lastError = ""
	if updateFailed {
		r.lastError = "工程已恢复到内存，但自动恢复副本暂时无法更新，请尽快另选位置保存。"
	}
	at := updated.UpdatedAt
	r.protectedAt = &at
	r.protectedRevision = updated.Revision
	result := s.bootstrapLocked()
	result.Warnings = append(result.Warnings, warnings...)
	result.Warnings = append(result.Warnings, "已恢复为未保存工程；原文件未覆盖，保存时请选择位置，撤销历史从此处重新开始。")
	if updateFailed {
		result.Warnings = append(result.Warnings, r.lastError)
	}
	return result, nil
}

func (s *Service) closeRecovery() {
	s.mu.Lock()
	r := s.recovery
	s.recoveryClosed = true
	if r == nil || r.closing {
		s.mu.Unlock()
		return
	}
	r.closing = true
	close(r.stop)
	s.mu.Unlock()
	select {
	case <-r.done:
	case <-time.After(3 * time.Second):
	}
}
