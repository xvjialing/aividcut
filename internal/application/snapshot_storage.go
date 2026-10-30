package application

import (
	"aividcut/internal/editor"
	"aividcut/internal/snapshotstorage"
	"errors"
	"math"
	"time"
)

type SnapshotStorageInput struct {
	ProjectScope
	Kind   string `json:"kind"`
	View   string `json:"view"`
	Offset int    `json:"offset"`
	Limit  int    `json:"limit"`
}
type SnapshotStorageChange struct {
	ProjectScope
	Token            string `json:"token"`
	Action           string `json:"action"`
	ConfirmPermanent bool   `json:"confirmPermanent"`
}
type SnapshotStorageResult struct {
	Kind           string `json:"kind"`
	ID             string `json:"id"`
	Action         string `json:"action"`
	AlreadyApplied bool   `json:"alreadyApplied"`
}
type storageToken struct {
	scope     ProjectScope
	kind      string
	item      snapshotstorage.Item
	created   time.Time
	completed string
}

func (s *Service) ListSnapshotStorage(input SnapshotStorageInput) (snapshotstorage.Page, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkProjectScopeLocked(input.ProjectScope); err != nil {
		return snapshotstorage.Page{}, err
	}
	if !snapshotstorage.ValidView(input.View) || input.Offset < 0 || input.Limit < 1 || input.Limit > 50 {
		return snapshotstorage.Page{}, errors.New("资料位置或分页参数无效")
	}
	var rows []snapshotstorage.Item
	var err error
	switch input.Kind {
	case "version":
		if s.versions == nil || s.versionsClosed {
			return snapshotstorage.Page{}, errors.New("版本服务未配置")
		}
		rows, err = s.versions.StorageItems(input.ProjectID, input.View)
	case "recovery":
		if s.recovery == nil || s.recovery.closing {
			return snapshotstorage.Page{}, errors.New("恢复服务未配置或正在关闭")
		}
		rows, err = s.recovery.store.StorageItems(input.View)
	default:
		return snapshotstorage.Page{}, errors.New("资料类型无效")
	}
	if err != nil {
		return snapshotstorage.Page{}, err
	}
	var totalBytes int64
	for _, row := range rows {
		if row.Bytes < 0 || row.Bytes > math.MaxInt64-totalBytes {
			return snapshotstorage.Page{}, errors.New("资料大小超过支持范围")
		}
		totalBytes += row.Bytes
	}
	start := min(input.Offset, len(rows))
	end := min(start+input.Limit, len(rows))
	items := []snapshotstorage.Item{}
	if s.storageTokens == nil {
		s.storageTokens = map[string]*storageToken{}
	}
	project := s.engine.Snapshot()
	protectedPaths := []string{s.projectPath}
	for _, asset := range project.Assets {
		protectedPaths = append(protectedPaths, asset.Path)
	}
	for _, row := range rows[start:end] {
		if snapshotstorage.Conflicts(row.Proof, protectedPaths) {
			row.Locked = true
			row.Reason = "该资料正作为当前工程文件或素材使用，不能移动或清理"
		}
		if input.Kind == "recovery" && row.ID == s.recovery.activeID {
			row.Locked = true
			row.Reason = "当前工程正在使用的自动保护点，正常保存后自动清理"
		}
		token := editor.NewID("storage")
		s.storageTokens[token] = &storageToken{scope: input.ProjectScope, kind: input.Kind, item: row, created: time.Now()}
		s.storageTokenOrder = append(s.storageTokenOrder, token)
		row.Token = token
		row.Proof = snapshotstorage.Proof{}
		items = append(items, row)
	}
	for len(s.storageTokenOrder) > 512 {
		id := s.storageTokenOrder[0]
		s.storageTokenOrder = s.storageTokenOrder[1:]
		delete(s.storageTokens, id)
	}
	return snapshotstorage.Page{Items: items, Total: len(rows), TotalBytes: totalBytes, Offset: input.Offset, Limit: input.Limit}, nil
}

func (s *Service) ChangeSnapshotStorage(input SnapshotStorageChange) (SnapshotStorageResult, error) {
	// 与周期捕获互斥，旧写入或退役删除不能影响刚还原的记录。
	s.mu.Lock()
	token := s.storageTokens[input.Token]
	r := s.recovery
	s.mu.Unlock()
	if token == nil {
		return SnapshotStorageResult{}, errors.New("资料身份已过期，请刷新列表")
	}
	if token.kind == "recovery" {
		if r == nil {
			return SnapshotStorageResult{}, errors.New("恢复服务未配置")
		}
		r.ops.Lock()
		defer r.ops.Unlock()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkProjectScopeLocked(input.ProjectScope); err != nil {
		return SnapshotStorageResult{}, err
	}
	if token.scope != input.ProjectScope || time.Since(token.created) > 10*time.Minute {
		return SnapshotStorageResult{}, errors.New("资料范围或身份已过期，请刷新核对")
	}
	if input.Action != "trash" && input.Action != "restore" && input.Action != "purge" {
		return SnapshotStorageResult{}, errors.New("资料操作无效")
	}
	if input.Action == "purge" && !input.ConfirmPermanent {
		return SnapshotStorageResult{}, errors.New("永久删除必须在客户端核对不可恢复后果")
	}
	result := SnapshotStorageResult{Kind: token.kind, ID: token.item.ID, Action: input.Action}
	if token.completed != "" {
		if token.completed != input.Action {
			return SnapshotStorageResult{}, errors.New("旧资料身份不能改作其它操作")
		}
		result.AlreadyApplied = true
		return result, nil
	}
	if token.item.Locked {
		return SnapshotStorageResult{}, errors.New(token.item.Reason)
	}
	project := s.engine.Snapshot()
	paths := []string{s.projectPath}
	for _, asset := range project.Assets {
		paths = append(paths, asset.Path)
	}
	var err error
	if token.kind == "version" {
		if s.versions == nil || s.versionsClosed {
			return SnapshotStorageResult{}, errors.New("版本服务已关闭")
		}
		err = s.versions.ChangeStorage(input.ProjectID, token.item.Proof, input.Action, paths)
	} else {
		if s.recovery != r || r.closing {
			return SnapshotStorageResult{}, errors.New("恢复服务已变化或关闭")
		}
		if token.item.ID == r.activeID {
			return SnapshotStorageResult{}, errors.New("当前自动保护点不能清理，请先正常保存工程")
		}
		err = r.store.ChangeStorage(token.item.Proof, input.Action, paths)
	}
	if err != nil {
		return SnapshotStorageResult{}, err
	}
	token.completed = input.Action
	return result, nil
}
