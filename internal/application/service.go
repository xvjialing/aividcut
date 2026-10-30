package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"aividcut/internal/editor"
	"aividcut/internal/exporter"
	"aividcut/internal/jobs"
	"aividcut/internal/localmedia"
	"aividcut/internal/mcpserver"
	"aividcut/internal/media"
	"aividcut/internal/ocr"
	"aividcut/internal/playback"
	"aividcut/internal/projectfonts"
	"aividcut/internal/projectstore"
	"aividcut/internal/projectversions"
	"aividcut/internal/resourceprofile"
	"aividcut/internal/statefilelock"
)

type Capability struct {
	Available bool   `json:"available"`
	Reason    string `json:"reason"`
}

type Bootstrap struct {
	Sequences    []editor.SequenceSummary `json:"sequences"`
	SourceEpoch  uint64                   `json:"sourceEpoch"`
	Project      editor.Project           `json:"project"`
	ProjectPath  string                   `json:"projectPath"`
	Dirty        bool                     `json:"dirty"`
	CanUndo      bool                     `json:"canUndo"`
	CanRedo      bool                     `json:"canRedo"`
	Capabilities map[string]Capability    `json:"capabilities"`
	Warnings     []string                 `json:"warnings,omitempty"`
}

type PreviewFrame struct {
	URL      string `json:"url"`
	Frame    int64  `json:"frame"`
	Revision int64  `json:"revision"`
}

type mediaURL struct {
	Path string
	URL  string
}

// Service 是 Wails 与开发 HTTP 桥共用的应用边界，不持有界面组件状态。
type Service struct {
	ocrMu                sync.Mutex
	ocrState             *sourceOCRState
	ocrRecognizer        ocr.Recognizer
	sequenceGeneration   uint64
	batchRelinks         map[string]*batchRelinkRun
	batchRelinksClosed   bool
	mediaStartupWarning  string
	mediaSettings        *mediaSettingsState
	resources            *resourceprofile.Recorder
	resourceExportMu     sync.Mutex
	drafts               *inspectorDraftState
	draftsClosed         bool
	storageTokens        map[string]*storageToken
	storageTokenOrder    []string
	recovery             *recoveryState
	recoveryClosed       bool
	versions             *projectversions.Store
	versionsClosed       bool
	captureMu            sync.Mutex
	captureActive        context.CancelFunc
	captureActiveID      string
	captureActiveProject string
	captureActiveEpoch   uint64
	proxyMu              sync.Mutex
	proxyLock            *statefilelock.Lock
	proxyRuns            map[string]*proxyRun
	proxyProofs          map[string]proxyFileProof
	proxyClosed          bool
	proxyToolInfo        [2]os.FileInfo
	shotMu               sync.Mutex
	shotRuns             map[string]*shotRun
	shotClosed           bool
	shotToolInfo         os.FileInfo
	frameProofMu         sync.Mutex
	frameProofs          []frameSourceProof
	contentClosed        bool
	contentMu            sync.Mutex
	contentCache         *sourceContentCache
	clipboard            *editor.TimelineClipboard
	clipboardVersion     uint64
	clipboardCuts        map[string][32]byte
	clipboardCutOrder    []string
	nativeExportConfig   atomic.Pointer[playback.Config]
	mu                   sync.Mutex
	previewMu            sync.Mutex
	exportMu             sync.Mutex
	engine               *editor.Engine
	projectPath          string
	savedHash            string
	tools                media.Toolchain
	mediaReport          media.CapabilityReport
	files                *localmedia.Server
	urls                 map[string]mediaURL
	jobs                 *jobs.Manager
	cacheDir             string
	cacheEpoch           string
	generationMu         sync.Mutex
	generation           *generationState
	generationClosed     bool
	agentMu              sync.Mutex
	agents               *agentState
	agentsClosed         bool
	mcpMu                sync.Mutex
	mcpServer            *mcpserver.Server
	mcpScope             *mcpProjectScope
	mcpAudit             []MCPAudit
	mcpClosed            bool
	mcpRequests          map[string]*mcpActionRecord
	workflowMu           sync.Mutex
	workflowState        *workflowState
	workflowsClosed      bool
	captionMu            sync.Mutex
	captionState         *captionLayoutState
	captionsClosed       bool
	playbackMu           sync.Mutex
	playbackState        *playbackState
	playbackClosed       bool
	playbackEvidence     atomic.Pointer[playbackEvidence]
	waveMu               sync.Mutex
	waveReader           *media.WaveformReader
	waveRequests         map[string]context.CancelFunc
	waveClosed           bool
	waveCancelled        map[string]time.Time
	thumbMu              sync.Mutex
	thumbReader          *media.ThumbnailReader
	frameReader          *media.ThumbnailReader
	thumbRequests        map[string]context.CancelFunc
	thumbCancelled       map[string]time.Time
	thumbClosed          bool
	thumbEpoch           uint64
}

func New(tools media.Toolchain, cacheDir string) (*Service, error) {
	engine, err := editor.NewEngine(editor.NewProject("未命名工程"))
	if err != nil {
		return nil, err
	}
	files, err := localmedia.New()
	if err != nil {
		return nil, fmt.Errorf("无法启动本地素材服务：%w", err)
	}
	if err := os.MkdirAll(cacheDir, 0700); err != nil {
		files.Close()
		return nil, err
	}
	s := &Service{engine: engine, tools: tools, mediaReport: tools.CheckCapabilities(context.Background()), files: files, urls: map[string]mediaURL{}, jobs: jobs.New(), cacheDir: cacheDir, cacheEpoch: editor.NewID("cache")}
	s.shotToolInfo, _ = os.Stat(tools.FFmpegPath)
	s.proxyToolInfo[0], _ = os.Stat(tools.FFmpegPath)
	s.proxyToolInfo[1], _ = os.Stat(tools.FFprobePath)
	s.resources = resourceprofile.New(s.resourceContext)
	s.savedHash = projectContentHash(engine.Snapshot())
	return s, nil
}

func projectHash(p editor.Project) string {
	p.Revision = 0
	for i := range p.Assets {
		p.Assets[i].URL, p.Assets[i].ThumbnailURL = "", ""
	}
	data, _ := json.Marshal(p)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// 内容比较不含当前序列导航位置；任务计划仍使用含当前上下文的projectHash。
func projectContentHash(p editor.Project) string {
	if len(p.OtherSequences) == 0 && editor.CurrentSequenceID(p) == editor.MainSequenceID && editor.CurrentSequenceName(p) == "主时间线" {
		p.SequenceID, p.SequenceName = "", ""
		p.SequenceOrder = nil
		return projectHash(p)
	}
	for i := range p.Assets {
		p.Assets[i].URL, p.Assets[i].ThumbnailURL = "", ""
	}
	data, _ := json.Marshal(editor.ContentState(p))
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func (s *Service) Close() {
	s.closeSourceOCR()
	s.closeMediaSettings()
	if s.resources != nil {
		s.resources.Close()
	}
	s.mu.Lock()
	s.batchRelinksClosed = true
	for _, run := range s.batchRelinks {
		if run.cancel != nil {
			run.cancel()
		}
	}
	s.draftsClosed = true
	if s.drafts != nil {
		_ = s.drafts.store.Close()
	}
	s.mu.Unlock()
	s.closeProjectVersions()
	s.proxyMu.Lock()
	s.proxyClosed = true
	s.proxyMu.Unlock()
	s.cancelPreviewProxies()
	s.closeShotIndexes()
	s.contentMu.Lock()
	s.contentClosed = true
	s.contentCache = nil
	s.contentMu.Unlock()
	s.resetThumbnails(true)
	s.resetWaveforms(true)
	s.closePlayback()
	s.closeMCP()
	s.closeCaptionLayouts()
	s.closeWorkflows()
	s.closeAgents()
	s.closeGeneration()
	s.jobs.Close()
	s.closeRecovery()
	releaseProxyLock := func() {
		<-s.jobs.Done()
		s.proxyMu.Lock()
		if s.proxyLock != nil {
			_ = s.proxyLock.Close()
			s.proxyLock = nil
		}
		s.proxyMu.Unlock()
	}
	select {
	case <-s.jobs.Done():
		releaseProxyLock()
	default:
		go releaseProxyLock()
	}
	s.files.Close()
}

func (s *Service) bootstrapLocked() Bootstrap {
	p := s.engine.Snapshot()
	s.reconcileInspectorDraftsLocked(p)
	dirty := projectContentHash(p) != s.savedHash
	usedFonts := map[string]bool{}
	for _, c := range p.Clips {
		if c.TextStyle.FontAssetID != "" {
			usedFonts[c.TextStyle.FontAssetID] = true
		}
	}
	for i := range p.Assets {
		a := &p.Assets[i]
		if a.Kind == "font" && !usedFonts[a.ID] {
			info, err := os.Stat(a.Path)
			a.Missing = err != nil || !info.Mode().IsRegular()
			a.URL = ""
			s.files.Unregister(a.ID)
			delete(s.urls, a.ID)
			continue
		}
		if a.Kind == "lut" {
			continue
		}
		u, ok := s.urls[a.ID]
		if !ok || u.Path != a.Path {
			var err error
			var address string
			if a.Kind == "font" {
				address, err = s.files.RegisterFont(a.ID, a.Path, a.Fingerprint)
			} else {
				address, err = s.files.Register(a.ID, a.Path)
			}
			if err != nil {
				a.Missing = true
				s.files.Unregister(a.ID)
				delete(s.urls, a.ID)
				continue
			}
			u = mediaURL{Path: a.Path, URL: address}
			s.urls[a.ID] = u
		}
		a.URL = u.URL
	}
	convert := func(feature media.Feature) Capability {
		return Capability{Available: feature.Available, Reason: feature.Reason}
	}
	ffmpeg, ffprobe := convert(s.mediaReport.FFmpeg), convert(s.mediaReport.FFprobe)
	exportVideo, previewFrame := convert(s.mediaReport.ExportVideo), convert(s.mediaReport.PreviewFrame)
	if !ffmpeg.Available {
		exportVideo, previewFrame = ffmpeg, ffmpeg
	}
	for _, clip := range p.Clips {
		if (clip.Kind == "text" || clip.Kind == "caption") && !s.mediaReport.DrawText.Available {
			exportVideo, previewFrame = convert(s.mediaReport.DrawText), convert(s.mediaReport.DrawText)
		}
		if clip.LUT != nil && !s.mediaReport.LUT3D.Available {
			exportVideo, previewFrame = convert(s.mediaReport.LUT3D), convert(s.mediaReport.LUT3D)
		}
	}
	nativeExport := s.nativeExportCapability()
	if nativeExport.Available {
		exportVideo = nativeExport
	}
	nativePlayback := convert(s.mediaReport.NativePlayback)
	if evidence := s.playbackEvidence.Load(); evidence != nil && evidence.engine == s.engine && evidence.revision == p.Revision {
		nativePlayback = Capability{Available: true, Reason: "当前工程已在原生媒体进程实际拉帧；实验预览通过二进制Canvas/PCM显示，不代表GPU或双端实机完成"}
	}
	s.thumbMu.Lock()
	sourceEpoch := s.thumbEpoch
	s.thumbMu.Unlock()
	warnings := []string{}
	if s.mediaStartupWarning != "" {
		warnings = append(warnings, s.mediaStartupWarning)
	}
	if s.drafts != nil && s.drafts.lastError != "" {
		warnings = append(warnings, s.drafts.lastError)
	}
	sequences := editor.SequenceSummaries(p)
	p = editor.ActiveSequenceView(p)
	return Bootstrap{Sequences: sequences, Warnings: warnings, SourceEpoch: sourceEpoch, Project: p, ProjectPath: s.projectPath, Dirty: dirty, CanUndo: s.engine.CanUndo(), CanRedo: s.engine.CanRedo(), Capabilities: map[string]Capability{
		"ffmpeg": ffmpeg, "ffprobe": ffprobe, "exportVideo": exportVideo, "previewFrame": previewFrame,
		"drawText": convert(s.mediaReport.DrawText), "lut3d": convert(s.mediaReport.LUT3D),
		"nativePlayback":   nativePlayback,
		"nativeExport":     nativeExport,
		"audioMeasurement": audioMeasurementCapability(s.tools),
	}}
}

func (s *Service) GetBootstrap() Bootstrap {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.bootstrapLocked()
}

func (s *Service) NewProject(name string) (Bootstrap, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.newProjectLocked(editor.NewProject(name))
}
func (s *Service) newProjectLocked(project editor.Project) (Bootstrap, error) {
	engine, err := editor.NewEngine(project)
	if err != nil {
		return Bootstrap{}, err
	}
	_ = s.retireRecoveryLocked()
	s.engine, s.projectPath = engine, ""
	s.clipboardCuts, s.clipboardCutOrder = nil, nil
	s.resetWaveforms(false)
	s.resetThumbnails(false)
	s.files.Reset()
	s.urls = map[string]mediaURL{}
	s.savedHash = projectContentHash(engine.Snapshot())
	return s.bootstrapLocked(), nil
}

func (s *Service) ApplyEdit(req editor.EditRequest) (Bootstrap, error) {
	if err := validatePublicEdit(req); err != nil {
		return Bootstrap{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.engine.Apply(req); err != nil {
		return Bootstrap{}, err
	}
	return s.bootstrapLocked(), nil
}

func validatePublicEdit(req editor.EditRequest) error {
	for _, command := range req.Commands {
		if command.SequenceID != "" || command.SourceSequenceID != "" || command.Type == "create_sequence" || command.Type == "duplicate_sequence" || command.Type == "rename_sequence" || command.Type == "delete_sequence" {
			return errors.New("序列管理需核对当前工程范围并使用专用接口")
		}
		if command.VisualAnalysis != nil || command.VisualAnalysisID != "" || command.Type == "accept_visual_analysis" || command.Type == "delete_visual_analysis" {
			return errors.New("画面分析需通过待核对结果的专用接口接纳或移除")
		}
		if command.Type == "paste_clips" || command.ClipboardID != "" {
			return errors.New("粘贴需通过已保存工程剪贴板的专用接口")
		}
		if command.CaptionLayout != nil {
			return errors.New("字幕排版需先核对源词、人工覆盖和预览，再通过专用接口应用")
		}
		if command.AudioCarrier != nil || command.AudioMode != "" {
			return errors.New("原声载体和镜头声音策略需由已校验的替换预览提交")
		}
		if command.Type == "add_asset" || command.Type == "update_asset" {
			return errors.New("素材路径需通过导入或重定位操作校验，不能直接写入工程")
		}
		switch command.Type {
		case "replace_visual":
			return errors.New("镜头替换需先校验生成候选与原声/字幕，再应用已保存预览")
		case "accept_transcript", "correct_transcript", "generate_bound_captions", "detach_caption":
			return errors.New("源转写需通过已保存语音结果或明确的纠错/解绑接口校验")
		}
		if command.Transcript != nil || command.TranscriptID != "" || command.TranscriptRevision != 0 || len(command.Words) > 0 {
			return errors.New("普通剪辑命令不能携带未校验的源转写参数")
		}
	}
	return nil
}

func (s *Service) Undo() (Bootstrap, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	before, _ := s.engine.SequenceIdentity()
	if _, err := s.engine.Undo(); err != nil {
		return Bootstrap{}, err
	}
	s.sequenceContextChangedLocked(before)
	return s.bootstrapLocked(), nil
}

func (s *Service) Redo() (Bootstrap, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	before, _ := s.engine.SequenceIdentity()
	if _, err := s.engine.Redo(); err != nil {
		return Bootstrap{}, err
	}
	s.sequenceContextChangedLocked(before)
	return s.bootstrapLocked(), nil
}

func (s *Service) ImportPaths(ctx context.Context, paths []string) (Bootstrap, error) {
	if err := ctx.Err(); err != nil {
		return Bootstrap{}, err
	}
	if len(paths) == 0 {
		return s.GetBootstrap(), nil
	}
	if len(paths) > 64 {
		return Bootstrap{}, errors.New("一次最多导入 64 个文件")
	}
	s.mu.Lock()
	initial, tools, engine := s.engine.Snapshot(), s.tools, s.engine
	s.mu.Unlock()
	projectID := initial.ID
	knownPaths := map[string]bool{}
	for _, asset := range initial.Assets {
		knownPaths[filepath.Clean(asset.Path)] = true
	}
	budget, cancelBudget := newImportAudioBudget(ctx)
	defer cancelBudget()
	var imported []editor.Asset
	var warnings []string
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return Bootstrap{}, err
		}
		absolute, err := filepath.Abs(path)
		if err != nil {
			return Bootstrap{}, err
		}
		if knownPaths[filepath.Clean(absolute)] {
			continue
		}
		knownPaths[filepath.Clean(absolute)] = true
		var asset editor.Asset
		if strings.EqualFold(filepath.Ext(path), ".ttf") || strings.EqualFold(filepath.Ext(path), ".otf") {
			file, err := projectfonts.Read(absolute)
			if err != nil {
				return Bootstrap{}, err
			}
			info := file.Info
			asset = editor.Asset{ID: editor.NewID("font"), Name: filepath.Base(absolute), Kind: "font", Path: absolute, Fingerprint: "sha256:" + file.SHA256, Font: &info}
		} else if strings.EqualFold(filepath.Ext(path), ".cube") {
			before, statErr := os.Stat(absolute)
			if statErr != nil {
				return Bootstrap{}, statErr
			}
			if _, err := media.ValidateCube(absolute); err != nil {
				return Bootstrap{}, err
			}
			fingerprint, hashErr := fingerprintRelinkFile(ctx, absolute)
			if hashErr != nil {
				return Bootstrap{}, hashErr
			}
			after, statErr := os.Stat(absolute)
			if statErr != nil || !os.SameFile(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
				return Bootstrap{}, errors.New("导入期间LUT文件变化，请重新导入")
			}
			asset = editor.Asset{ID: editor.NewID("lut"), Name: filepath.Base(absolute), Kind: "lut", Path: absolute, Fingerprint: fingerprint}
		} else {
			asset, err = tools.Probe(ctx, absolute)
			if err != nil {
				return Bootstrap{}, fmt.Errorf("导入 %s：%w", filepath.Base(path), err)
			}
			if asset.ID == "" {
				asset.ID = editor.NewID("asset")
			}
			var warning string
			asset, warning, err = importedAudioEvidence(ctx, tools, asset, budget)
			if err != nil {
				return Bootstrap{}, fmt.Errorf("导入 %s：%w", filepath.Base(path), err)
			}
			if warning != "" {
				warnings = append(warnings, warning)
			}
		}
		imported = append(imported, asset)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.engine.Snapshot()
	if s.engine != engine || p.ID != projectID {
		return Bootstrap{}, errors.New("导入期间工程已切换，请在目标工程重新导入")
	}
	existing := map[string]bool{}
	for _, a := range p.Assets {
		existing[filepath.Clean(a.Path)] = true
	}
	commands := []editor.Command{}
	for i := range imported {
		a := imported[i]
		if existing[filepath.Clean(a.Path)] {
			continue
		}
		existing[filepath.Clean(a.Path)] = true
		commands = append(commands, editor.Command{Type: "add_asset", Asset: &a})
	}
	if len(commands) > 0 {
		if err := ctx.Err(); err != nil {
			return Bootstrap{}, err
		}
		if _, err := s.engine.Apply(editor.EditRequest{BaseRevision: p.Revision, IdempotencyKey: editor.NewID("import"), Label: "导入素材", Commands: commands}); err != nil {
			return Bootstrap{}, err
		}
	}
	result := s.bootstrapLocked()
	result.Warnings = warnings
	return result, nil
}

func (s *Service) SaveProjectTo(path string) (Bootstrap, error) {
	if strings.TrimSpace(path) == "" {
		return Bootstrap{}, errors.New("请选择工程保存位置")
	}
	if filepath.Ext(path) == "" {
		path += ".aividcut"
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return Bootstrap{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.engine.Snapshot()
	for _, asset := range p.Assets {
		if filepath.Clean(asset.Path) == filepath.Clean(absolute) {
			return Bootstrap{}, errors.New("工程文件不能覆盖原始素材")
		}
	}
	if err := projectstore.Save(absolute, p); err != nil {
		return Bootstrap{}, err
	}
	s.projectPath, s.savedHash = absolute, projectContentHash(p)
	_ = s.retireRecoveryLocked()
	return s.bootstrapLocked(), nil
}

func (s *Service) SaveProject() (Bootstrap, error) {
	s.mu.Lock()
	path := s.projectPath
	s.mu.Unlock()
	return s.SaveProjectTo(path)
}

func (s *Service) OpenProjectPath(path string) (Bootstrap, error) {
	p, warnings, err := projectstore.LoadWithWarnings(path)
	if err != nil {
		return Bootstrap{}, err
	}
	engine, err := editor.NewEngine(p)
	if err != nil {
		return Bootstrap{}, err
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return Bootstrap{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.retireRecoveryLocked()
	s.engine, s.projectPath, s.savedHash = engine, absolute, projectContentHash(p)
	s.clipboardCuts, s.clipboardCutOrder = nil, nil
	s.resetWaveforms(false)
	s.resetThumbnails(false)
	s.urls = map[string]mediaURL{}
	s.files.Reset()
	result := s.bootstrapLocked()
	result.Warnings = append(result.Warnings, warnings...)
	return result, nil
}

func (s *Service) ExportVideo(options media.ExportOptions) (jobs.Job, error) {
	return s.exportVideoForMCP(options, nil, 0)
}

func (s *Service) exportVideoForMCP(options media.ExportOptions, scope *mcpProjectScope, revision int64) (jobs.Job, error) {
	s.exportMu.Lock()
	defer s.exportMu.Unlock()
	if _, err := media.EncodingResourceArgs(options.VideoEncoder, options.ResourceMode); err != nil {
		return jobs.Job{}, err
	}
	if s.tools.FFmpegPath == "" {
		return jobs.Job{}, errors.New("需要配置 FFmpeg 后才能导出视频")
	}
	if strings.TrimSpace(options.Path) == "" {
		return jobs.Job{}, errors.New("请选择视频输出路径")
	}
	path, err := filepath.Abs(options.Path)
	if err != nil {
		return jobs.Job{}, err
	}
	options.Path = path
	if strings.ToLower(filepath.Ext(path)) != ".mp4" {
		return jobs.Job{}, errors.New("视频导出需要.mp4扩展名")
	}
	nativeConfig := s.nativeExportConfig.Load()
	renderer, err := resolveExportRenderer(options.Renderer, nativeConfig)
	if err != nil {
		return jobs.Job{}, err
	}
	s.mu.Lock()
	p := s.engine.Snapshot()
	projectPath := s.projectPath
	if scope != nil {
		if err := scope.checkRequestLocked(s); err != nil {
			s.mu.Unlock()
			return jobs.Job{}, err
		}
		if p.Revision != revision {
			s.mu.Unlock()
			return jobs.Job{}, errors.New("工程已变化，请重新准备导出请求")
		}
	}
	s.mu.Unlock()
	if len(p.Clips) == 0 {
		return jobs.Job{}, errors.New("时间线为空，添加片段后再导出")
	}
	if existing, err := os.Lstat(path); err == nil {
		if !existing.Mode().IsRegular() {
			return jobs.Job{}, errors.New("输出目标不能是目录、符号链接或特殊文件")
		}
		if !options.Overwrite {
			return jobs.Job{}, errors.New("输出文件已存在，请选择新文件名或勾选允许替换同名输出文件")
		}
	} else if !os.IsNotExist(err) {
		return jobs.Job{}, err
	}
	if outputMatchesSource(path, projectPath) {
		return jobs.Job{}, errors.New("视频输出不能覆盖工程文件")
	}
	for _, a := range p.Assets {
		if outputMatchesSource(path, a.Path) {
			return jobs.Job{}, errors.New("视频输出不能覆盖原始素材")
		}
	}
	for _, j := range s.jobs.List() {
		if j.Type == "export" && (j.Status == "running" || j.Status == "queued") {
			return jobs.Job{}, errors.New("已有视频导出任务，请完成或取消后再导出")
		}
	}
	return s.jobs.Start("export", "准备导出", func(ctx context.Context, report func(float64, string)) (string, error) {
		update := func(progress media.Progress) {
			report(progress.Fraction, progress.Stage)
		}
		var err error
		if renderer == "native" {
			err = exporter.Native(ctx, s.tools, *nativeConfig, p, options, update)
		} else {
			err = s.tools.Export(ctx, p, options, update)
		}
		return options.Path, err
	})
}

func outputMatchesSource(output, source string) bool {
	if source == "" {
		return false
	}
	absolute, err := filepath.Abs(source)
	if err == nil && output == absolute {
		return true
	}
	a, ae := os.Stat(output)
	b, be := os.Stat(source)
	return ae == nil && be == nil && os.SameFile(a, b)
}

func (s *Service) nativeExportCapability() Capability {
	config := s.nativeExportConfig.Load()
	if config == nil || config.WorkerPath == "" {
		return Capability{Reason: "尚未配置原生导出worker，自动模式使用FFmpeg合成"}
	}
	if !s.mediaReport.FFmpeg.Available {
		return Capability{Reason: "原生导出需要FFmpeg编码器"}
	}
	return Capability{Available: true, Reason: "原生工具已配置，导出将复用预览合成；当前工程语义、实际编码和输出提交在任务内验证"}
}

func (s *Service) GetPreviewFrame(ctx context.Context, frame int64) (PreviewFrame, error) {
	s.previewMu.Lock()
	defer s.previewMu.Unlock()
	if s.tools.FFmpegPath == "" {
		return PreviewFrame{}, errors.New("需要配置 FFmpeg 才能检查合成帧")
	}
	s.mu.Lock()
	p := s.engine.Snapshot()
	s.mu.Unlock()
	if frame < 0 {
		return PreviewFrame{}, errors.New("预览帧不能为负数")
	}
	key := sha256.Sum256([]byte(fmt.Sprintf("%s/%s/%d/%d", s.cacheEpoch, p.ID, p.Revision, frame)))
	id := "preview-" + hex.EncodeToString(key[:12])
	path := filepath.Join(s.cacheDir, id+".png")
	if _, err := os.Stat(path); err != nil {
		bounded, cancel := context.WithTimeout(ctx, 45*time.Second)
		defer cancel()
		if err := s.tools.PreviewFrame(bounded, p, frame, path); err != nil {
			return PreviewFrame{}, err
		}
	}
	u, err := s.files.Register(id, path)
	if err != nil {
		return PreviewFrame{}, err
	}
	s.prunePreviewCache(path)
	return PreviewFrame{URL: u, Frame: frame, Revision: p.Revision}, nil
}

// 低频合成检查最多保留八张磁盘帧，不让拖动检查无限增长缓存。
func (s *Service) prunePreviewCache(keep string) {
	entries, _ := os.ReadDir(s.cacheDir)
	type file struct {
		path     string
		modified time.Time
	}
	var cached []file
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), "preview-") || !strings.HasSuffix(entry.Name(), ".png") {
			continue
		}
		if info, err := entry.Info(); err == nil {
			cached = append(cached, file{filepath.Join(s.cacheDir, entry.Name()), info.ModTime()})
		}
	}
	sort.Slice(cached, func(i, j int) bool { return cached[i].modified.Before(cached[j].modified) })
	remaining := len(cached)
	for _, item := range cached {
		if remaining <= 8 {
			break
		}
		if item.path != keep {
			if os.Remove(item.path) == nil {
				remaining--
			}
		}
	}
}

func (s *Service) ListJobs() []jobs.Job { return s.jobs.List() }

func (s *Service) CancelJob(id string) (jobs.Job, error) { return s.jobs.Cancel(id) }

func resolveExportRenderer(renderer string, config *playback.Config) (string, error) {
	if renderer == "" || renderer == "auto" {
		renderer = "ffmpeg"
		if config != nil && config.WorkerPath != "" {
			renderer = "native"
		}
	}
	if renderer != "native" && renderer != "ffmpeg" {
		return "", errors.New("未支持的导出合成方式")
	}
	if renderer == "native" && (config == nil || config.WorkerPath == "") {
		return "", errors.New("原生导出需要先配置本项目媒体worker")
	}
	return renderer, nil
}
