package application

import (
	"aividcut/internal/editor"
	"aividcut/internal/relinkbatch"
	"context"
	"errors"
	"regexp"
	"time"
)

type BatchRelinkInput struct {
	ProjectScope
	ID      string `json:"id"`
	OldRoot string `json:"oldRoot"`
	NewRoot string `json:"newRoot"`
}
type BatchRelinkRef struct {
	ProjectScope
	ID string `json:"id"`
}
type BatchRelinkPreview struct {
	ID string `json:"id"`
	relinkbatch.Summary
}
type batchRelinkRun struct {
	scope                    ProjectScope
	created                  time.Time
	cancel                   context.CancelFunc
	cancelled, busy, applied bool
	plan                     *relinkbatch.Plan
}

var batchRelinkID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,80}$`)

const batchRelinkMaxRecords = 512
const batchRelinkMaxPlans = 32

func (s *Service) InspectBatchRelink(ctx context.Context, input ProjectScope) (relinkbatch.Inventory, error) {
	s.mu.Lock()
	if err := s.checkProjectScopeLocked(input); err != nil {
		s.mu.Unlock()
		return relinkbatch.Inventory{}, err
	}
	p := s.engine.Snapshot()
	s.mu.Unlock()
	return relinkbatch.Inspect(ctx, p)
}
func (s *Service) pruneBatchRelinksLocked() {
	if s.batchRelinks == nil {
		s.batchRelinks = map[string]*batchRelinkRun{}
	}
	for id, run := range s.batchRelinks {
		if !run.busy && time.Since(run.created) > 10*time.Minute {
			delete(s.batchRelinks, id)
		}
	}
}
func (s *Service) PreviewBatchRelink(ctx context.Context, input BatchRelinkInput) (BatchRelinkPreview, error) {
	if !batchRelinkID.MatchString(input.ID) {
		return BatchRelinkPreview{}, errors.New("核对请求标识无效")
	}
	s.mu.Lock()
	s.pruneBatchRelinksLocked()
	if s.batchRelinksClosed {
		s.mu.Unlock()
		return BatchRelinkPreview{}, errors.New("素材修复服务已关闭")
	}
	if err := s.checkProjectScopeLocked(input.ProjectScope); err != nil {
		s.mu.Unlock()
		return BatchRelinkPreview{}, err
	}
	if s.batchRelinks[input.ID] != nil || len(s.batchRelinks) >= batchRelinkMaxRecords {
		s.mu.Unlock()
		return BatchRelinkPreview{}, errors.New("核对请求已结束或短期请求过多，请稍后重新核对")
	}
	plans := 0
	for _, run := range s.batchRelinks {
		if run.busy {
			s.mu.Unlock()
			return BatchRelinkPreview{}, errors.New("已有素材核对正在读取，请先取消或等待完成")
		}
		if run.plan != nil && !run.cancelled && !run.applied {
			plans++
		}
	}
	if plans >= batchRelinkMaxPlans {
		s.mu.Unlock()
		return BatchRelinkPreview{}, errors.New("待处理核对过多，请先关闭不用的核对窗口")
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	run := &batchRelinkRun{scope: input.ProjectScope, created: time.Now(), cancel: cancel, busy: true}
	s.batchRelinks[input.ID] = run
	p := s.engine.Snapshot()
	s.mu.Unlock()
	plan, err := relinkbatch.Prepare(ctx, p, input.OldRoot, input.NewRoot)
	s.mu.Lock()
	defer s.mu.Unlock()
	defer cancel()
	run.busy = false
	run.cancel = nil
	if err != nil {
		run.cancelled = true
		return BatchRelinkPreview{}, err
	}
	if ctx.Err() != nil || run.cancelled || s.batchRelinksClosed {
		return BatchRelinkPreview{}, errors.New("素材核对已取消")
	}
	if err := s.checkProjectScopeLocked(input.ProjectScope); err != nil {
		return BatchRelinkPreview{}, err
	}
	run.plan = plan
	return BatchRelinkPreview{ID: input.ID, Summary: plan.Summary}, nil
}

// CancelBatchRelink 也保留短期撤销凭据，阻止晚到的同ID读取在取消后重新启动。
func (s *Service) CancelBatchRelink(input BatchRelinkRef) error {
	if !batchRelinkID.MatchString(input.ID) {
		return errors.New("核对请求标识无效")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneBatchRelinksLocked()
	if run := s.batchRelinks[input.ID]; run != nil {
		if run.scope != input.ProjectScope {
			return errors.New("核对请求归属不一致")
		}
		run.cancelled = true
		run.plan = nil
		if run.cancel != nil {
			run.cancel()
		}
	} else if len(s.batchRelinks) < batchRelinkMaxRecords {
		s.batchRelinks[input.ID] = &batchRelinkRun{scope: input.ProjectScope, created: time.Now(), cancelled: true}
	}
	return nil
}
func (s *Service) ApplyBatchRelink(ctx context.Context, input BatchRelinkRef) (Bootstrap, error) {
	s.mu.Lock()
	run := s.batchRelinks[input.ID]
	if run == nil || run.scope != input.ProjectScope || run.cancelled || run.applied || run.plan == nil || run.busy || s.batchRelinksClosed || time.Since(run.created) > 10*time.Minute {
		s.mu.Unlock()
		return Bootstrap{}, errors.New("此核对已失效或已处理，请重新核对素材")
	}
	if err := s.checkProjectScopeLocked(input.ProjectScope); err != nil {
		s.mu.Unlock()
		return Bootstrap{}, err
	}
	if run.plan.Summary.Matched == 0 {
		s.mu.Unlock()
		return Bootstrap{}, errors.New("没有内容一致的候选文件，未修改工程")
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	run.cancel = cancel
	run.busy = true
	plan := run.plan
	s.mu.Unlock()
	err := plan.Verify(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()
	defer cancel()
	defer func() { run.busy = false; run.cancel = nil }()
	if err != nil {
		return Bootstrap{}, err
	}
	if ctx.Err() != nil || run.cancelled || s.batchRelinksClosed {
		return Bootstrap{}, errors.New("素材修复已取消，未修改工程")
	}
	if err := s.checkProjectScopeLocked(input.ProjectScope); err != nil {
		return Bootstrap{}, err
	}
	if err := plan.CheckFiles(); err != nil {
		return Bootstrap{}, err
	}
	commands, err := plan.Commands(s.engine.Snapshot())
	if err != nil {
		return Bootstrap{}, err
	}
	if _, err = s.engine.Apply(editor.EditRequest{BaseRevision: input.ProjectRevision, IdempotencyKey: "batch-relink-" + input.ID, Label: "批量修复素材链接", Commands: commands}); err != nil {
		return Bootstrap{}, err
	}
	run.applied = true
	run.plan = nil
	return s.bootstrapLocked(), nil
}
