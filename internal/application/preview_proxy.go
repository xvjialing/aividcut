package application

import (
	"aividcut/internal/editor"
	"aividcut/internal/jobs"
	"aividcut/internal/media"
	"aividcut/internal/playback"
	"aividcut/internal/previewproxy"
	"aividcut/internal/statefilelock"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type PreviewProxyInput struct {
	ClearAll    bool   `json:"clearAll,omitempty"`
	ProjectID   string `json:"projectId"`
	SourceEpoch uint64 `json:"sourceEpoch"`
	AssetID     string `json:"assetId"`
	Fingerprint string `json:"fingerprint"`
	Profile     string `json:"profile"`
}
type PreviewProxyView struct {
	CacheVideos int                 `json:"cacheVideos"`
	CacheBytes  int64               `json:"cacheBytes"`
	Status      string              `json:"status"`
	Reason      string              `json:"reason,omitempty"`
	ProjectID   string              `json:"projectId"`
	SourceEpoch uint64              `json:"sourceEpoch"`
	AssetID     string              `json:"assetId"`
	Profile     string              `json:"profile"`
	Job         *jobs.Job           `json:"job,omitempty"`
	Proof       *previewproxy.Proof `json:"proof,omitempty"`
}
type proxyRun struct {
	jobID  string
	epoch  uint64
	engine *editor.Engine
}
type proxyFileProof struct {
	manifest        previewproxy.Manifest
	epoch           uint64
	source, derived os.FileInfo
}

func (s *Service) proxyContext(input PreviewProxyInput, maintenance ...bool) (editor.Asset, *editor.Engine, string, error) {
	if _, err := previewproxy.Edge(input.Profile); err != nil {
		return editor.Asset{}, nil, "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.engine.Snapshot()
	s.thumbMu.Lock()
	epoch := s.thumbEpoch
	s.thumbMu.Unlock()
	if p.ID != input.ProjectID || epoch != input.SourceEpoch {
		return editor.Asset{}, nil, "", errors.New("代理所属工程实例已变化")
	}
	at := assetIndex(p, input.AssetID)
	if at < 0 {
		return editor.Asset{}, nil, "", errors.New("代理源不存在")
	}
	asset := p.Assets[at]
	maintain := len(maintenance) > 0 && maintenance[0]
	if asset.Kind != "video" || (!maintain && asset.Missing) || asset.Fingerprint == "" || asset.Fingerprint != input.Fingerprint {
		return asset, nil, "", errors.New("代理需要当前已登记且来源未变化的视频")
	}
	if !maintain {
		for i, path := range []string{s.tools.FFmpegPath, s.tools.FFprobePath} {
			info, err := os.Stat(path)
			if err != nil || !sameFrameFile(info, s.proxyToolInfo[i]) {
				return asset, nil, "", errors.New("代理需要可用且身份未变化的FFmpeg/ffprobe，请核对工具")
			}
		}
	}
	return asset, s.engine, s.proxyKey(asset.Fingerprint, input.Profile), nil
}

// 以下缓存方法持proxyMu；只操作由内部SHA键命名的私有普通文件。
func (s *Service) proxyPathsLocked(key string) (string, string, error) {
	if s.proxyClosed || !previewproxy.ValidKey(key) {
		return "", "", errors.New("代理服务已关闭或缓存标识无效")
	}
	dir := filepath.Join(s.cacheDir, "video-proxies")
	dir, absoluteErr := filepath.Abs(dir)
	if absoluteErr != nil {
		return "", "", errors.New("代理缓存路径无效")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", "", err
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", "", errors.New("代理缓存目录不可用")
	}
	if s.proxyLock == nil {
		lock, err := statefilelock.Acquire(filepath.Join(dir, ".lock"))
		if err != nil {
			return "", "", errors.New("代理缓存正被其它客户端使用")
		}
		s.proxyLock = lock
	}
	return filepath.Join(dir, key+".mp4"), filepath.Join(dir, key+".json"), nil
}
func (s *Service) loadProxyLocked(key string) (*previewproxy.Manifest, error) {
	path, meta, err := s.proxyPathsLocked(key)
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(meta)
	if os.IsNotExist(err) {
		if _, err = os.Lstat(path); err == nil {
			return nil, errors.New("存在未完成的代理，请先清理该档代理")
		}
		return nil, nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Size() > 64<<10 {
		return nil, errors.New("代理清单损坏，请先清理")
	}
	file, err := os.Open(meta)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var m previewproxy.Manifest
	decoder := json.NewDecoder(io.LimitReader(file, (64<<10)+1))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&m) != nil || m.Key != key || m.Validate() != nil {
		return nil, errors.New("代理清单不匹配，请先清理")
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return nil, errors.New("代理清单包含额外数据")
	}
	info, err = os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() != m.Proof.Bytes {
		return nil, errors.New("代理文件缺失或大小已变化，请先清理")
	}
	return &m, nil
}
func (s *Service) proxyProofReadyLocked(key string, asset editor.Asset, epoch uint64, manifest *previewproxy.Manifest) bool {
	p, ok := s.proxyProofs[key]
	if !ok || p.epoch != epoch || manifest == nil || p.manifest != *manifest {
		return false
	}
	path, _, err := s.proxyPathsLocked(key)
	if err != nil {
		return false
	}
	source, a := os.Stat(asset.Path)
	derived, b := os.Lstat(path)
	return a == nil && b == nil && derived.Mode().IsRegular() && sameFrameFile(p.source, source) && sameFrameFile(p.derived, derived)
}
func (s *Service) GetPreviewProxy(ctx context.Context, input PreviewProxyInput) (PreviewProxyView, error) {
	if err := ctx.Err(); err != nil {
		return PreviewProxyView{}, err
	}
	asset, engine, key, err := s.proxyContext(input, true)
	if err != nil {
		return PreviewProxyView{}, err
	}
	view := PreviewProxyView{Status: "missing", ProjectID: input.ProjectID, SourceEpoch: input.SourceEpoch, AssetID: asset.ID, Profile: input.Profile}
	s.proxyMu.Lock()
	defer s.proxyMu.Unlock()
	view.CacheVideos, view.CacheBytes = s.proxyStatsLocked()
	if run := s.proxyRuns[key]; run != nil && run.engine == engine && run.epoch == input.SourceEpoch {
		for _, job := range s.jobs.List() {
			if job.ID == run.jobID && (job.Status == "queued" || job.Status == "running" || job.Status == "failed" || job.Status == "cancelled") {
				view.Job = &job
				view.Status = job.Status
				view.Reason = job.Message
				return view, nil
			}
		}
	}
	value, err := s.loadProxyLocked(key)
	if err != nil {
		view.Status = "invalid"
		view.Reason = err.Error()
		return view, nil
	}
	if value == nil {
		if len(s.relatedProxyKeysLocked(asset.Fingerprint, input.Profile)) > 0 {
			view.Status = "invalid"
			view.Reason = "已有其它工具版本或无效代理，请清理该档后重新生成"
			return view, nil
		}
		view.Reason = "尚无代理，可在后台生成；不改工程和原片"
		return view, nil
	}
	if value.SourceFingerprint != asset.Fingerprint || value.Profile != input.Profile {
		view.Status = "invalid"
		view.Reason = "代理来源不匹配，请先清理"
		return view, nil
	}
	view.Status = "cached"
	view.Reason = "已有代理，需核对当前来源后使用"
	if !asset.Missing && s.proxyToolsUnchanged() && s.proxyProofReadyLocked(key, asset, input.SourceEpoch, value) {
		view.Status = "ready"
		view.Reason = "代理已核对；实时预览重新准备后可使用，声音和导出仍原片"
		proof := value.Proof
		view.Proof = &proof
	}
	return view, nil
}
func (s *Service) proxyScope(input PreviewProxyInput, asset editor.Asset, engine *editor.Engine) error {
	current, active, _, err := s.proxyContext(input)
	if err != nil {
		return err
	}
	if active != engine || current.Path != asset.Path {
		return errors.New("代理源或工程已切换")
	}
	return nil
}
func (s *Service) StartPreviewProxy(ctx context.Context, input PreviewProxyInput) (PreviewProxyView, error) {
	if err := ctx.Err(); err != nil {
		return PreviewProxyView{}, err
	}
	asset, engine, key, err := s.proxyContext(input)
	if err != nil {
		return PreviewProxyView{}, err
	}
	view, err := s.GetPreviewProxy(ctx, input)
	if err != nil {
		return view, err
	}
	if view.Status == "ready" || view.Status == "queued" || view.Status == "running" {
		return view, nil
	}
	if view.Status == "invalid" {
		return view, errors.New("请先清理该档无效代理，再重新生成")
	}
	s.proxyMu.Lock()
	defer s.proxyMu.Unlock()
	if s.proxyRuns == nil {
		s.proxyRuns = map[string]*proxyRun{}
	}
	if old := s.proxyRuns[key]; old != nil {
		for _, job := range s.jobs.List() {
			if job.ID == old.jobID && (job.Status == "queued" || job.Status == "running") {
				view.Status = job.Status
				view.Job = &job
				return view, nil
			}
		}
	}
	if len(s.proxyRuns) >= 64 {
		for oldKey, old := range s.proxyRuns {
			active := false
			for _, job := range s.jobs.List() {
				if job.ID == old.jobID && (job.Status == "queued" || job.Status == "running") {
					active = true
				}
			}
			if !active {
				delete(s.proxyRuns, oldKey)
				break
			}
		}
	}
	path, meta, err := s.proxyPathsLocked(key)
	if err != nil {
		return view, err
	}
	run := &proxyRun{epoch: input.SourceEpoch, engine: engine}
	s.proxyRuns[key] = run
	job, err := s.jobs.Start("preview-proxy", "等待生成/核对画面代理", func(child context.Context, report func(float64, string)) (string, error) {
		if err := s.proxyScope(input, asset, engine); err != nil {
			return "", err
		}
		report(-1, "正在核对原文件来源")
		source, err := s.verifyFrameSource(child, asset, input.SourceEpoch)
		if err != nil {
			return "", err
		}
		s.proxyMu.Lock()
		m, err := s.loadProxyLocked(key)
		s.proxyMu.Unlock()
		if err != nil {
			return "", err
		}
		temporary := ""
		if m == nil {
			temporary = filepath.Join(filepath.Dir(path), key+"."+editor.NewID("part")+".part.mp4")
			defer os.Remove(temporary)
			proof, err := s.tools.BuildPreviewProxy(child, asset.Path, temporary, input.Profile, func(message string) { report(-1, message) })
			if err != nil {
				return "", err
			}
			m = &previewproxy.Manifest{Version: previewproxy.Version, Key: key, SourceFingerprint: asset.Fingerprint, Profile: input.Profile, Proof: proof}
		} else {
			origin, originErr := s.tools.PreviewProxySourceOrigin(child, asset.Path)
			if originErr != nil || origin != m.Proof.OriginUs {
				return "", errors.New("代理原格式起点不匹配，请清理后重新生成")
			}
			report(-1, "正在核对已缓存代理指纹")
			if err = verifySourceFingerprint(child, path, m.Proof.ProxyFingerprint); err != nil {
				return "", errors.New("代理指纹不匹配，请先清理后重新生成")
			}
		}
		if err = child.Err(); err != nil {
			return "", err
		}
		if err = checkFrameProof(asset.Path, source); err != nil {
			return "", err
		}
		if err = s.proxyScope(input, asset, engine); err != nil {
			return "", err
		}
		s.proxyMu.Lock()
		defer s.proxyMu.Unlock()
		if s.proxyClosed || s.proxyRuns[key] != run {
			return "", errors.New("代理任务所属实例已撤销")
		}
		if temporary != "" {
			if err = s.proxyBudgetLocked(m.Proof.Bytes, temporary); err != nil {
				return "", err
			}
			if err = media.PublishDerivedFile(child, temporary, path); err != nil {
				return "", err
			}
			encoded, _ := json.Marshal(m)
			temp, err := os.CreateTemp(filepath.Dir(meta), ".manifest-*")
			if err != nil {
				os.Remove(path)
				return "", err
			}
			name := temp.Name()
			defer os.Remove(name)
			_, writeErr := temp.Write(encoded)
			syncErr := temp.Sync()
			closeErr := temp.Close()
			if err = errors.Join(writeErr, syncErr, closeErr); err == nil {
				err = os.Rename(name, meta)
			}
			if err != nil {
				os.Remove(path)
				return "", err
			}
		}
		derived, err := os.Lstat(path)
		if err != nil || !derived.Mode().IsRegular() {
			return "", errors.New("代理文件已变化")
		}
		if s.proxyProofs == nil {
			s.proxyProofs = map[string]proxyFileProof{}
		}
		s.proxyProofs[key] = proxyFileProof{manifest: *m, epoch: input.SourceEpoch, source: source, derived: derived}
		return "", nil
	})
	if err != nil {
		delete(s.proxyRuns, key)
		return view, err
	}
	run.jobID = job.ID
	view.Status = job.Status
	view.Job = &job
	return view, nil
}
func (s *Service) proxyBudgetLocked(add int64, exclude string) error {
	entries, err := os.ReadDir(filepath.Join(s.cacheDir, "video-proxies"))
	if err != nil {
		return err
	}
	count := 0
	var total int64
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".mp4") && entry.Name() != filepath.Base(exclude) {
			info, err := entry.Info()
			if err != nil || !info.Mode().IsRegular() {
				continue
			}
			total += info.Size()
			if previewproxy.ValidKey(strings.TrimSuffix(entry.Name(), ".mp4")) {
				count++
			}
		}
	}
	if count >= previewproxy.MaxEntries || total+add > previewproxy.MaxCacheBytes {
		return errors.New("代理缓存已达32份或8GiB预算，请退出实时预览后清理")
	}
	return nil
}
func (s *Service) ClearPreviewProxy(ctx context.Context, input PreviewProxyInput) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	asset, engine, key, err := s.proxyContext(input, true)
	if err != nil {
		return err
	}
	s.playbackMu.Lock()
	state := s.playbackState
	s.playbackMu.Unlock()
	if state != nil {
		select {
		case <-state.transition:
			defer func() { state.transition <- struct{}{} }()
		case <-ctx.Done():
			return ctx.Err()
		}
		s.playbackMu.Lock()
		current := state.current
		s.playbackMu.Unlock()
		if current != nil {
			select {
			case <-current.native.Done():
			default:
				return errors.New("请先回到工作区预览，再清理代理")
			}
		}
		if state.retiring != nil {
			select {
			case <-state.retiring:
			default:
				return errors.New("此前媒体进程仍在释放，请稍后清理")
			}
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.thumbMu.Lock()
	epoch := s.thumbEpoch
	s.thumbMu.Unlock()
	if s.engine != engine || epoch != input.SourceEpoch {
		return errors.New("清理所属工程实例已切换")
	}
	currentProject := s.engine.Snapshot()
	at := assetIndex(currentProject, input.AssetID)
	if at < 0 || currentProject.Assets[at].Fingerprint != asset.Fingerprint {
		return errors.New("清理所属素材已变化")
	}
	s.proxyMu.Lock()
	defer s.proxyMu.Unlock()
	if run := s.proxyRuns[key]; run != nil {
		for _, job := range s.jobs.List() {
			if run.jobID == job.ID && (job.Status == "queued" || job.Status == "running") {
				return errors.New("代理任务仍在运行，请先取消并等待结束")
			}
		}
	}
	path, _, err := s.proxyPathsLocked(key)
	if err != nil {
		return err
	}
	keys := s.relatedProxyKeysLocked(asset.Fingerprint, input.Profile)
	keys[key] = true
	if input.ClearAll {
		for _, job := range s.jobs.List() {
			if (job.Type == "preview-proxy" || job.Type == "export") && (job.Status == "queued" || job.Status == "running") {
				return errors.New("仍有代理或导出任务在运行，请先结束任务再清理全部代理")
			}
		}
		entries, e := os.ReadDir(filepath.Dir(path))
		if e != nil {
			return e
		}
		for _, entry := range entries {
			if candidate, ok := ownedProxyKey(entry.Name()); ok {
				keys[candidate] = true
			}
		}
	}
	targets := []string{}
	for candidate := range keys {
		video, manifest, e := s.proxyPathsLocked(candidate)
		if e != nil {
			return e
		}
		targets = append(targets, manifest, video)
	}
	sourceInfoByPath := map[string]os.FileInfo{}
	for _, source := range currentProject.Assets {
		sourceInfoByPath[source.Path], _ = os.Stat(source.Path)
	}
	for _, target := range targets {
		info, _ := os.Stat(target)
		for _, source := range currentProject.Assets {
			sourceInfo := sourceInfoByPath[source.Path]
			if filepath.Clean(source.Path) == filepath.Clean(target) || info != nil && sourceInfo != nil && os.SameFile(info, sourceInfo) {
				return errors.New("当前工程已把该代理文件登记为素材，请先整理引用，未清理")
			}
		}
	}
	for _, target := range targets {
		info, e := os.Lstat(target)
		if os.IsNotExist(e) {
			continue
		}
		if e != nil || !info.Mode().IsRegular() {
			return errors.New("代理缓存不是普通文件，未清理")
		}
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	for _, entry := range entries {
		name := entry.Name()
		for candidate := range keys {
			if strings.HasPrefix(name, candidate+".part-") && strings.HasSuffix(name, ".part.mp4") {
				if info, e := entry.Info(); e == nil && info.Mode().IsRegular() {
					targets = append(targets, filepath.Join(filepath.Dir(path), name))
				}
			}
		}
	}
	for _, target := range targets {
		if err = os.Remove(target); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	for candidate := range keys {
		delete(s.proxyProofs, candidate)
		delete(s.proxyRuns, candidate)
	}
	return nil
}
func (s *Service) cancelPreviewProxies() {
	s.proxyMu.Lock()
	ids := []string{}
	for _, run := range s.proxyRuns {
		ids = append(ids, run.jobID)
	}
	s.proxyRuns = map[string]*proxyRun{}
	s.proxyProofs = map[string]proxyFileProof{}
	s.proxyMu.Unlock()
	for _, id := range ids {
		if id != "" {
			s.jobs.Cancel(id)
		}
	}
}
func (s *Service) playbackVisualSources(p editor.Project, profile string, epoch uint64) (map[string]playback.VisualSource, int) {
	out := map[string]playback.VisualSource{}
	missing := 0
	used := map[string]bool{}
	for _, clip := range p.Clips {
		if clip.Kind == "video" {
			used[clip.AssetID] = true
		}
	}
	if !s.proxyToolsUnchanged() {
		return out, len(used)
	}
	s.proxyMu.Lock()
	defer s.proxyMu.Unlock()
	for _, asset := range p.Assets {
		if !used[asset.ID] {
			continue
		}
		key := s.proxyKey(asset.Fingerprint, profile)
		known, ok := s.proxyProofs[key]
		if !ok || known.epoch != epoch {
			missing++
			continue
		}
		m, err := s.loadProxyLocked(key)
		if err == nil && m != nil && s.proxyProofReadyLocked(key, asset, epoch, m) {
			path, _, _ := s.proxyPathsLocked(key)
			out[asset.ID] = playback.VisualSource{Path: path, Fingerprint: m.Proof.ProxyFingerprint, SourceFingerprint: asset.Fingerprint, OriginUs: m.Proof.OriginUs}
		} else {
			missing++
		}
	}
	return out, missing
}

// 清理按已登记源/档位查找本应用清单，不依赖编码工具或当前源文件仍可用。
func (s *Service) relatedProxyKeysLocked(fingerprint, profile string) map[string]bool {
	out := map[string]bool{}
	dir := filepath.Join(s.cacheDir, "video-proxies")
	entries, _ := os.ReadDir(dir)
	for _, entry := range entries {
		key := strings.TrimSuffix(entry.Name(), ".json")
		if !strings.HasSuffix(entry.Name(), ".json") || !previewproxy.ValidKey(key) {
			continue
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() || info.Size() > 64<<10 {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			continue
		}
		var m previewproxy.Manifest
		if json.Unmarshal(data, &m) == nil && m.Key == key && m.SourceFingerprint == fingerprint && m.Profile == profile {
			out[key] = true
		}
	}
	return out
}

func (s *Service) proxyKey(fingerprint, profile string) string {
	toolIdentity := []any{}
	for _, info := range s.proxyToolInfo {
		if info == nil {
			toolIdentity = append(toolIdentity, nil)
		} else {
			toolIdentity = append(toolIdentity, info.Size(), info.ModTime().UnixNano())
		}
	}
	raw, _ := json.Marshal([]any{previewproxy.Version, fingerprint, profile, s.mediaReport.FFmpegVersion, toolIdentity})
	return fmt.Sprintf("%x", sha256.Sum256(raw))
}

func (s *Service) proxyToolsUnchanged() bool {
	for i, path := range []string{s.tools.FFmpegPath, s.tools.FFprobePath} {
		info, err := os.Stat(path)
		if err != nil || !sameFrameFile(info, s.proxyToolInfo[i]) {
			return false
		}
	}
	return true
}

func ownedProxyKey(name string) (string, bool) {
	for _, suffix := range []string{".mp4", ".json"} {
		if strings.HasSuffix(name, suffix) {
			key := strings.TrimSuffix(name, suffix)
			if previewproxy.ValidKey(key) {
				return key, true
			}
		}
	}
	if len(name) > 70 && previewproxy.ValidKey(name[:64]) && strings.HasPrefix(name[64:], ".part-") && strings.HasSuffix(name, ".part.mp4") {
		return name[:64], true
	}
	return "", false
}
func (s *Service) proxyStatsLocked() (int, int64) {
	entries, _ := os.ReadDir(filepath.Join(s.cacheDir, "video-proxies"))
	videos := 0
	var total int64
	for _, entry := range entries {
		if _, ok := ownedProxyKey(entry.Name()); !ok {
			continue
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		total += info.Size()
		if strings.HasSuffix(entry.Name(), ".mp4") && previewproxy.ValidKey(strings.TrimSuffix(entry.Name(), ".mp4")) {
			videos++
		}
	}
	return videos, total
}
