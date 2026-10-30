package application

import (
	"aividcut/internal/editor"
	"aividcut/internal/jobs"
	"aividcut/internal/shotindex"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type ShotIndexInput struct {
	ProjectID     string `json:"projectId"`
	SourceEpoch   uint64 `json:"sourceEpoch"`
	AssetID       string `json:"assetId"`
	Fingerprint   string `json:"fingerprint"`
	Threshold     int    `json:"threshold"`
	MinDurationUs int64  `json:"minDurationUs"`
	Force         bool   `json:"force,omitempty"`
}
type ShotIndexView struct {
	Status      string           `json:"status"`
	Reason      string           `json:"reason,omitempty"`
	ProjectID   string           `json:"projectId"`
	SourceEpoch uint64           `json:"sourceEpoch"`
	AssetID     string           `json:"assetId"`
	Key         string           `json:"key,omitempty"`
	Job         *jobs.Job        `json:"job,omitempty"`
	Index       *shotindex.Index `json:"index,omitempty"`
	Cached      bool             `json:"cached"`
}
type shotRun struct {
	jobID  string
	engine *editor.Engine
	epoch  uint64
}

func (s *Service) shotContext(input ShotIndexInput, scope *mcpProjectScope) (editor.Asset, *editor.Engine, string, shotindex.Config, error) {
	c, err := shotindex.Normalize(shotindex.Config{Threshold: input.Threshold, MinDurationUs: input.MinDurationUs})
	if err != nil {
		return editor.Asset{}, nil, "", c, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if scope != nil {
		if err := scope.checkLocked(s); err != nil {
			return editor.Asset{}, nil, "", c, err
		}
	}
	project := s.engine.Snapshot()
	s.thumbMu.Lock()
	epoch := s.thumbEpoch
	s.thumbMu.Unlock()
	if project.ID != input.ProjectID || epoch != input.SourceEpoch {
		return editor.Asset{}, nil, "", c, errors.New("镜头索引所属工程实例已切换")
	}
	at := assetIndex(project, input.AssetID)
	if at < 0 {
		return editor.Asset{}, nil, "", c, errors.New("镜头索引素材不存在")
	}
	asset := project.Assets[at]
	if asset.Kind != "video" || asset.Missing || asset.DurationUs <= 0 || asset.Fingerprint == "" || asset.Fingerprint != input.Fingerprint {
		return editor.Asset{}, nil, "", c, errors.New("镜头检测需要当前已登记且来源未变化的视频")
	}
	if !s.mediaReport.FFmpeg.Available || s.mediaReport.FFmpegVersion == "" {
		return editor.Asset{}, nil, "", c, errors.New("镜头检测需要可用的FFmpeg")
	}
	tool, err := os.Stat(s.tools.FFmpegPath)
	if err != nil {
		return editor.Asset{}, nil, "", c, errors.New("FFmpeg已不可读取，请重新核对工具")
	}
	if !sameFrameFile(tool, s.shotToolInfo) {
		return editor.Asset{}, nil, "", c, errors.New("FFmpeg文件已变化，请重新打开客户端核对工具")
	}
	raw, _ := json.Marshal([]any{shotindex.Version, asset.Fingerprint, asset.DurationUs, c, s.mediaReport.FFmpegVersion, tool.Size(), tool.ModTime().UnixNano()})
	return asset, s.engine, fmt.Sprintf("%x", sha256.Sum256(raw)), c, nil
}
func (s *Service) shotCachePath(key string) (string, error) {
	if len(key) != 64 || strings.ContainsAny(key, "/\\") {
		return "", errors.New("镜头缓存标识无效")
	}
	dir := filepath.Join(s.cacheDir, "shot-index")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", errors.New("镜头缓存目录不可用")
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("镜头缓存目录不是本地普通目录")
	}
	return filepath.Join(dir, key+".json"), nil
}
func (s *Service) loadShotIndex(key string) (*shotindex.Index, error) {
	path, err := s.shotCachePath(key)
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Size() > 1<<20 {
		return nil, errors.New("镜头缓存无效，可重新检测")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, errors.New("镜头缓存暂时不可读取")
	}
	defer f.Close()
	var value shotindex.Index
	d := json.NewDecoder(io.LimitReader(f, (1<<20)+1))
	d.DisallowUnknownFields()
	if d.Decode(&value) != nil {
		return nil, errors.New("镜头缓存损坏，可重新检测")
	}
	var extra any
	if d.Decode(&extra) != io.EOF || value.Key != key || value.Validate() != nil {
		return nil, errors.New("镜头缓存与当前方法不一致，可重新检测")
	}
	return &value, nil
}
func (s *Service) saveShotIndex(value shotindex.Index) error {
	path, err := s.shotCachePath(value.Key)
	if err != nil {
		return err
	}
	data, err := json.Marshal(value)
	if err != nil || len(data) > 1<<20 {
		return errors.New("镜头缓存超过预算")
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".shot-*")
	if err != nil {
		return errors.New("镜头缓存无法创建")
	}
	defer os.Remove(file.Name())
	_, err = file.Write(data)
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(file.Name(), path)
	}
	if err != nil {
		return errors.New("镜头缓存保存失败，请恢复磁盘后重试")
	}
	// 派生缓存最多32份；淘汰不修改工程或分析记录。
	files, _ := os.ReadDir(filepath.Dir(path))
	type candidate struct {
		path string
		time time.Time
	}
	values := []candidate{}
	for _, entry := range files {
		if entry.Type().IsRegular() && strings.HasSuffix(entry.Name(), ".json") {
			if info, e := entry.Info(); e == nil {
				values = append(values, candidate{filepath.Join(filepath.Dir(path), entry.Name()), info.ModTime()})
			}
		}
	}
	for len(values) > 32 {
		at := 0
		for i := range values {
			if values[i].time.Before(values[at].time) {
				at = i
			}
		}
		os.Remove(values[at].path)
		values = append(values[:at], values[at+1:]...)
	}
	return nil
}
func (s *Service) frameProofKnown(asset editor.Asset, epoch uint64) bool {
	info, err := os.Stat(asset.Path)
	if err != nil || !s.frameProofMu.TryLock() {
		return false
	}
	defer s.frameProofMu.Unlock()
	key := fmt.Sprintf("%d\x00%s\x00%s", epoch, asset.Path, asset.Fingerprint)
	for _, proof := range s.frameProofs {
		if proof.key == key && sameFrameFile(proof.info, info) {
			return true
		}
	}
	return false
}
func sameFrameFile(a, b os.FileInfo) bool {
	return a != nil && b != nil && os.SameFile(a, b) && a.Size() == b.Size() && a.ModTime().Equal(b.ModTime())
}
func (s *Service) GetShotIndex(ctx context.Context, input ShotIndexInput) (ShotIndexView, error) {
	return s.getShotIndex(ctx, input, nil)
}
func (s *Service) getShotIndex(ctx context.Context, input ShotIndexInput, scope *mcpProjectScope) (ShotIndexView, error) {
	if err := ctx.Err(); err != nil {
		return ShotIndexView{}, err
	}
	asset, engine, key, config, err := s.shotContext(input, scope)
	if err != nil {
		return ShotIndexView{}, err
	}
	view := ShotIndexView{Status: "missing", ProjectID: input.ProjectID, SourceEpoch: input.SourceEpoch, AssetID: asset.ID, Key: key}
	s.shotMu.Lock()
	closed := s.shotClosed
	run := s.shotRuns[key]
	s.shotMu.Unlock()
	if closed {
		return ShotIndexView{}, errors.New("镜头索引服务已关闭")
	}
	if run != nil && run.engine == engine && run.epoch == input.SourceEpoch {
		for _, job := range s.jobs.List() {
			if job.ID == run.jobID {
				if job.Status == "queued" || job.Status == "running" {
					view.Status = "running"
					view.Job = &job
					return view, nil
				}
				if job.Status == "failed" || job.Status == "cancelled" {
					view.Status = job.Status
					view.Job = &job
					view.Reason = job.Message
					return view, nil
				}
			}
		}
	}
	value, err := s.loadShotIndex(key)
	if err != nil {
		view.Status = "invalid"
		view.Reason = err.Error()
		return view, nil
	}
	if value == nil {
		view.Reason = "尚无本地镜头索引，明确检测后可供回看和Agent查询"
		return view, nil
	}
	if value.SourceFingerprint != asset.Fingerprint || value.SourceDurationUs != asset.DurationUs || value.Config != config || value.DetectorVersion != s.mediaReport.FFmpegVersion {
		view.Status = "invalid"
		view.Reason = "镜头缓存与当前来源或配置不一致，可重新检测"
		return view, nil
	}
	view.Cached = true
	if !s.frameProofKnown(asset, input.SourceEpoch) {
		view.Status = "cached"
		view.Reason = "已有缓存，需明确核对当前源指纹后复用"
		return view, nil
	}
	view.Status = "ready"
	view.Index = value
	return view, nil
}
func (s *Service) StartShotIndex(ctx context.Context, input ShotIndexInput) (ShotIndexView, error) {
	if err := ctx.Err(); err != nil {
		return ShotIndexView{}, err
	}
	asset, engine, key, c, err := s.shotContext(input, nil)
	if err != nil {
		return ShotIndexView{}, err
	}
	existing, err := s.GetShotIndex(ctx, input)
	if err != nil {
		return ShotIndexView{}, err
	}
	if existing.Status == "running" || existing.Status == "ready" && !input.Force {
		return existing, nil
	}
	s.shotMu.Lock()
	defer s.shotMu.Unlock()
	if s.shotClosed {
		return ShotIndexView{}, errors.New("镜头索引服务已关闭")
	}
	if s.shotRuns == nil {
		s.shotRuns = map[string]*shotRun{}
	}
	if old := s.shotRuns[key]; old != nil {
		for _, job := range s.jobs.List() {
			if job.ID == old.jobID && (job.Status == "queued" || job.Status == "running") {
				existing.Status = "running"
				existing.Job = &job
				return existing, nil
			}
		}
	}
	if len(s.shotRuns) >= 64 {
		for oldKey, old := range s.shotRuns {
			active := false
			for _, job := range s.jobs.List() {
				if job.ID == old.jobID && (job.Status == "queued" || job.Status == "running") {
					active = true
				}
			}
			if !active {
				delete(s.shotRuns, oldKey)
				break
			}
		}
	}
	run := &shotRun{engine: engine, epoch: input.SourceEpoch}
	s.shotRuns[key] = run
	job, err := s.jobs.Start("shot-index", "等待本地镜头检测", func(child context.Context, report func(float64, string)) (string, error) {

		if err := s.shotScope(engine, input, asset); err != nil {
			return "", err
		}
		report(-1, "正在核对源文件指纹，不调用模型")
		proof, err := s.verifyFrameSource(child, asset, input.SourceEpoch)
		if err != nil {
			return "", err
		}
		var value *shotindex.Index
		if !input.Force {
			value, _ = s.loadShotIndex(key)
			if value != nil && (value.SourceFingerprint != asset.Fingerprint || value.SourceDurationUs != asset.DurationUs || value.Config != c || value.DetectorVersion != s.mediaReport.FFmpegVersion) {
				value = nil
			}
		}
		if value == nil {
			scan, err := s.tools.DetectScenes(child, asset.Path, c.Threshold, func(position int64) {
				report(-1, fmt.Sprintf("本地视频已扫描到 %.2f秒", float64(position)/1e6))
			})
			if err != nil {
				return "", err
			}
			index, err := shotindex.Build(key, asset.Fingerprint, s.mediaReport.FFmpegVersion, asset.DurationUs, scan.StartUs, scan.EndUs, scan.Frames, scan.Cuts, c)
			if err != nil {
				return "", err
			}
			value = &index
		}
		if err := child.Err(); err != nil {
			return "", err
		}
		if err := checkFrameProof(asset.Path, proof); err != nil {
			return "", err
		}
		if err := s.shotScope(engine, input, asset); err != nil {
			return "", err
		}
		if err = s.saveShotIndex(*value); err != nil {
			return "", err
		}
		report(1, fmt.Sprintf("已保存%d段候选镜头；仍需回看，不是语义判断", len(value.Shots)))
		return "", nil
	})
	if err != nil {
		delete(s.shotRuns, key)
		return ShotIndexView{}, err
	}
	run.jobID = job.ID
	existing.Status = "running"
	existing.Job = &job
	existing.Index = nil
	return existing, nil
}
func (s *Service) shotScope(engine *editor.Engine, input ShotIndexInput, asset editor.Asset) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, _ := s.engine.Identity()
	s.thumbMu.Lock()
	epoch := s.thumbEpoch
	s.thumbMu.Unlock()
	if s.engine != engine || id != input.ProjectID || epoch != input.SourceEpoch {
		return errors.New("镜头检测所属工程已切换")
	}
	current := s.engine.Snapshot()
	at := assetIndex(current, asset.ID)
	if at < 0 || current.Assets[at].Path != asset.Path || current.Assets[at].Fingerprint != asset.Fingerprint || current.Assets[at].Missing {
		return errors.New("镜头检测来源已变化")
	}
	return nil
}
func (s *Service) cancelShotIndexes() {
	s.shotMu.Lock()
	ids := []string{}
	for _, run := range s.shotRuns {
		if run.jobID != "" {
			ids = append(ids, run.jobID)
		}
	}
	s.shotRuns = map[string]*shotRun{}
	s.shotMu.Unlock()
	for _, id := range ids {
		s.jobs.Cancel(id)
	}
}
func (s *Service) closeShotIndexes() {
	s.shotMu.Lock()
	s.shotClosed = true
	s.shotMu.Unlock()
	s.cancelShotIndexes()
}
