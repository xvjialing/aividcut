package application

import (
	"context"
	"errors"

	"aividcut/internal/contentindex"
	"aividcut/internal/editor"
)

type SourceContentQuery struct {
	SourceEpoch     uint64 `json:"sourceEpoch"`
	ProjectID       string `json:"projectId"`
	ProjectRevision int64  `json:"projectRevision"`
	AssetID         string `json:"assetId,omitempty"`
	Text            string `json:"text"`
	Origin          string `json:"origin,omitempty"`
	Offset          int    `json:"offset"`
	Limit           int    `json:"limit"`
}
type SourceContentResult struct {
	contentindex.Result
	SourceEpoch     uint64 `json:"sourceEpoch"`
	ProjectID       string `json:"projectId"`
	ProjectRevision int64  `json:"projectRevision"`
	AssetID         string `json:"assetId,omitempty"`
}
type sourceContentCache struct {
	engine   *editor.Engine
	revision int64
	assetID  string
	index    *contentindex.Index
}

func (s *Service) SearchSourceContent(ctx context.Context, input SourceContentQuery) (SourceContentResult, error) {
	return s.searchSourceContent(ctx, input, nil)
}
func (s *Service) searchSourceContent(ctx context.Context, input SourceContentQuery, scope *mcpProjectScope) (SourceContentResult, error) {
	query := contentindex.Query{Text: input.Text, Origin: input.Origin, Offset: input.Offset, Limit: input.Limit}
	if err := contentindex.Validate(query); err != nil {
		return SourceContentResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return SourceContentResult{}, err
	}
	// 单索引缓存；读取相同版本时不重复克隆整个工程。锁序固定contentMu→mu。
	s.contentMu.Lock()
	defer s.contentMu.Unlock()
	if s.contentClosed {
		return SourceContentResult{}, errors.New("素材检索服务已关闭")
	}
	s.mu.Lock()
	engine := s.engine
	check := func() error {
		if scope == nil {
			s.thumbMu.Lock()
			epoch := s.thumbEpoch
			s.thumbMu.Unlock()
			if input.SourceEpoch != epoch {
				return errors.New("检索所属工程实例已切换，请重新检索")
			}
		}
		if scope != nil {
			if err := scope.checkLocked(s); err != nil {
				return err
			}
		}
		id, revision := s.engine.Identity()
		if s.engine != engine || id != input.ProjectID || revision != input.ProjectRevision {
			return errors.New("工程或接受版本已变化，请重新检索")
		}
		return ctx.Err()
	}
	if err := check(); err != nil {
		s.mu.Unlock()
		return SourceContentResult{}, err
	}
	cache := s.contentCache
	var project editor.Project
	if cache == nil || cache.engine != engine || cache.revision != input.ProjectRevision || cache.assetID != input.AssetID {
		project = engine.Snapshot()
		cache = nil
	}
	s.mu.Unlock()
	if cache == nil {
		index, err := contentindex.Build(ctx, project, input.AssetID)
		if err != nil {
			return SourceContentResult{}, err
		}
		cache = &sourceContentCache{engine: engine, revision: input.ProjectRevision, assetID: input.AssetID, index: index}
	}
	result, err := cache.index.Search(ctx, query)
	if err != nil {
		return SourceContentResult{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := check(); err != nil {
		return SourceContentResult{}, err
	}
	s.contentCache = cache
	s.thumbMu.Lock()
	epoch := s.thumbEpoch
	s.thumbMu.Unlock()
	return SourceContentResult{SourceEpoch: epoch, Result: result, ProjectID: input.ProjectID, ProjectRevision: input.ProjectRevision, AssetID: input.AssetID}, nil
}
