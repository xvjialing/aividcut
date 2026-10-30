package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"

	"aividcut/internal/application"
	"aividcut/internal/connections"
	"aividcut/internal/editor"
	"aividcut/internal/generation"
	"aividcut/internal/generationjobs"
	"aividcut/internal/inspectordrafts"
	"aividcut/internal/jobs"
	"aividcut/internal/localagent"
	"aividcut/internal/mcpserver"
	"aividcut/internal/media"
	"aividcut/internal/mediatools"
	"aividcut/internal/ocr"
	"aividcut/internal/projectcollect"
	"aividcut/internal/projectrecovery"
	"aividcut/internal/projectversions"
	"aividcut/internal/relinkbatch"
	"aividcut/internal/resourceprofile"
	"aividcut/internal/snapshotstorage"
	"aividcut/internal/speech"
	"aividcut/internal/speechjobs"
	"aividcut/internal/workflows"

	wails "github.com/wailsapp/wails/v3/pkg/application"
)

type App struct {
	ctx              context.Context
	desktop          *wails.App
	window           *wails.WebviewWindow
	service          *application.Service
	closing          atomic.Bool
	ready            atomic.Bool
	settingsMenuItem *wails.MenuItem
}

func NewApp() (*App, error) {
	dir := os.Getenv("AIVIDCUT_DATA_DIR")
	var cacheDir string
	if dir == "" {
		config, err := os.UserConfigDir()
		if err != nil {
			return nil, err
		}
		dir = filepath.Join(config, "AIVidCut")
		cache, err := os.UserCacheDir()
		if err != nil {
			return nil, err
		}
		cacheDir = filepath.Join(cache, "AIVidCut", "previews")
	} else {
		cacheDir = filepath.Join(dir, "previews")
	}
	service, err := application.NewWithMediaSettings(cacheDir, filepath.Join(dir, "media-tools"))
	if err != nil {
		return nil, err
	}
	if err := service.ConfigureGeneration(application.GenerationOptions{Directory: dir}); err != nil {
		service.Close()
		return nil, err
	}
	if err := service.ConfigureAgents(application.AgentOptions{Directory: filepath.Join(dir, "agents")}); err != nil {
		service.Close()
		return nil, err
	}
	if err := service.ConfigureWorkflows(filepath.Join(dir, "workflows")); err != nil {
		service.Close()
		return nil, err
	}
	if err := service.ConfigurePlayback(application.PlaybackOptions{Config: service.ConfiguredPlayback()}); err != nil {
		service.Close()
		return nil, err
	}
	if err := service.ConfigureProjectVersions(filepath.Join(dir, "versions")); err != nil {
		service.Close()
		return nil, err
	}
	if err := service.ConfigureRecovery(application.RecoveryOptions{Directory: filepath.Join(dir, "recovery")}); err != nil {
		service.Close()
		return nil, err
	}
	if err := service.ConfigureInspectorDrafts(filepath.Join(dir, "inspector-drafts")); err != nil {
		service.Close()
		return nil, err
	}
	return &App{ctx: context.Background(), service: service}, nil
}

func (a *App) ServiceStartup(ctx context.Context, _ wails.ServiceOptions) error {
	a.ctx = ctx
	return nil
}

func (a *App) ServiceShutdown() error { a.service.Close(); return nil }

func (a *App) requestClose() bool {
	if a.closing.Load() {
		return false
	}
	if !a.ready.Load() && !a.service.GetBootstrap().Dirty {
		return false
	}
	a.desktop.Event.Emit("project:close-request")
	return true
}

func (a *App) FrontendReady() {
	a.ready.Store(true)
	if a.settingsMenuItem != nil {
		a.settingsMenuItem.SetEnabled(true)
	}
}

func (a *App) ConfirmClose(action string) error {
	switch action {
	case "cancel":
		return nil
	case "save":
		b, err := a.SaveProject()
		if err != nil {
			return err
		}
		if b.Dirty || b.ProjectPath == "" {
			return nil
		}
	case "discard":
		_ = a.service.DiscardCurrentRecovery()
	default:
		return errors.New("无效的关闭操作")
	}
	a.closing.Store(true)
	a.desktop.Quit()
	return nil
}

func (a *App) GetBootstrap() application.Bootstrap           { return a.service.GetBootstrap() }
func (a *App) GetRecoveryStatus() application.RecoveryStatus { return a.service.GetRecoveryStatus() }
func (a *App) ListProjectRecoveries(input application.RecoveryListInput) (projectrecovery.Page, error) {
	return a.service.ListProjectRecoveries(input)
}
func (a *App) CheckpointRecovery() (application.RecoveryStatus, error) {
	return a.service.CheckpointRecovery()
}
func (a *App) RestoreProjectRecovery(input application.RestoreRecoveryInput) (application.Bootstrap, error) {
	return a.service.RestoreProjectRecovery(input)
}
func (a *App) ListSnapshotStorage(input application.SnapshotStorageInput) (snapshotstorage.Page, error) {
	return a.service.ListSnapshotStorage(input)
}
func (a *App) ChangeSnapshotStorage(input application.SnapshotStorageChange) (application.SnapshotStorageResult, error) {
	return a.service.ChangeSnapshotStorage(input)
}
func (a *App) ChooseProjectCollectionDirectory() (string, error) {
	return a.openFile(openDialogOptions{Title: "选择工程副本的上级文件夹", DefaultFilename: a.service.GetBootstrap().ProjectPath, Directories: true})
}
func (a *App) InspectProjectCollection(input application.ProjectScope) (projectcollect.Summary, error) {
	return a.service.InspectProjectCollection(a.ctx, input)
}
func (a *App) CollectProject(input application.CollectProjectInput) (jobs.Job, error) {
	return a.service.CollectProject(input)
}
func (a *App) ListProjectVersions(input application.ListProjectVersionsInput) (projectversions.Page, error) {
	return a.service.ListProjectVersions(input)
}
func (a *App) CreateProjectVersion(input application.CreateProjectVersionInput) (projectversions.Summary, error) {
	return a.service.CreateProjectVersion(input)
}
func (a *App) RestoreProjectVersion(input application.RestoreProjectVersionInput) (application.RestoreProjectVersionResult, error) {
	return a.service.RestoreProjectVersion(input)
}
func (a *App) GetClipboardStatus() application.ClipboardStatus { return a.service.GetClipboardStatus() }
func (a *App) CopyTimelineSelection(input application.ClipboardSelectionInput) (application.ClipboardResult, error) {
	return a.service.CopyTimelineSelection(input)
}
func (a *App) CutTimelineSelection(input application.ClipboardCutInput) (application.ClipboardResult, error) {
	return a.service.CutTimelineSelection(input)
}
func (a *App) PasteTimelineSelection(input application.ClipboardPasteInput) (application.ClipboardResult, error) {
	return a.service.PasteTimelineSelection(input)
}

func (a *App) NewProject(name string) (application.Bootstrap, error) {
	return a.service.NewProject(name)
}
func (a *App) NewProjectWithSettings(input application.NewProjectInput) (application.Bootstrap, error) {
	return a.service.NewProjectWithSettings(input)
}

func (a *App) ApplyEdit(request editor.EditRequest) (application.Bootstrap, error) {
	return a.service.ApplyEdit(request)
}

func (a *App) ApplyScopedEdit(input application.ScopedEditInput) (application.Bootstrap, error) {
	return a.service.ApplyScopedEdit(input)
}

func (a *App) Undo() (application.Bootstrap, error) { return a.service.Undo() }

func (a *App) Redo() (application.Bootstrap, error) { return a.service.Redo() }

func (a *App) ImportMedia() (application.Bootstrap, error) {
	paths, err := a.openFiles(openDialogOptions{Title: "导入素材", Filters: []wails.FileFilter{{DisplayName: "视频、音频与图片", Pattern: "*.mp4;*.mov;*.mkv;*.webm;*.avi;*.mp3;*.wav;*.m4a;*.aac;*.flac;*.png;*.jpg;*.jpeg;*.webp;*.gif"}}})
	if err != nil {
		return application.Bootstrap{}, err
	}
	return a.service.ImportPaths(a.ctx, paths)
}

func (a *App) ImportFont() (application.Bootstrap, error) {
	path, err := a.openFile(openDialogOptions{Title: "导入工程字体", Filters: []wails.FileFilter{{DisplayName: "单字体TTF/OTF", Pattern: "*.ttf;*.otf"}}})
	if err != nil {
		return application.Bootstrap{}, err
	}
	if path == "" {
		return a.GetBootstrap(), nil
	}
	return a.service.ImportPaths(a.ctx, []string{path})
}

func (a *App) ImportLUT() (application.Bootstrap, error) {
	path, err := a.openFile(openDialogOptions{Title: "导入 3D LUT", Filters: []wails.FileFilter{{DisplayName: "3D Cube LUT", Pattern: "*.cube"}}})
	if err != nil {
		return application.Bootstrap{}, err
	}
	if path == "" {
		return a.GetBootstrap(), nil
	}
	return a.service.ImportPaths(a.ctx, []string{path})
}

func (a *App) ImportPaths(paths []string) (application.Bootstrap, error) {
	return a.service.ImportPaths(a.ctx, paths)
}

func (a *App) RelinkAsset(assetID string) (application.Bootstrap, error) {
	project := a.service.GetBootstrap().Project
	var source *editor.Asset
	for i := range project.Assets {
		if project.Assets[i].ID == assetID {
			source = &project.Assets[i]
			break
		}
	}
	if source == nil {
		return application.Bootstrap{}, errors.New("找不到需要重定位的素材")
	}
	filter := wails.FileFilter{DisplayName: "媒体素材", Pattern: "*.mp4;*.mov;*.mkv;*.webm;*.avi;*.mp3;*.wav;*.m4a;*.aac;*.flac;*.png;*.jpg;*.jpeg;*.webp"}
	if source.Kind == "font" {
		filter = wails.FileFilter{DisplayName: "单字体TTF/OTF", Pattern: "*.ttf;*.otf"}
	} else if source.Kind == "lut" {
		filter = wails.FileFilter{DisplayName: "3D Cube LUT", Pattern: "*.cube"}
	}
	path, err := a.openFile(openDialogOptions{Title: "重新定位素材", DefaultFilename: source.Name, Filters: []wails.FileFilter{filter}})
	if err != nil {
		return application.Bootstrap{}, err
	}
	if path == "" {
		return a.GetBootstrap(), nil
	}
	return a.service.RelinkAsset(a.ctx, assetID, path)
}

func (a *App) RelinkAssetPath(assetID, path string) (application.Bootstrap, error) {
	return a.service.RelinkAsset(a.ctx, assetID, path)
}

func (a *App) ChangeSequence(input application.SequenceChangeInput) (application.Bootstrap, error) {
	return a.service.ChangeSequence(input)
}

func (a *App) InspectBatchRelink(input application.ProjectScope) (relinkbatch.Inventory, error) {
	return a.service.InspectBatchRelink(a.ctx, input)
}
func (a *App) PreviewBatchRelink(input application.BatchRelinkInput) (application.BatchRelinkPreview, error) {
	return a.service.PreviewBatchRelink(a.ctx, input)
}
func (a *App) ApplyBatchRelink(input application.BatchRelinkRef) (application.Bootstrap, error) {
	return a.service.ApplyBatchRelink(a.ctx, input)
}
func (a *App) CancelBatchRelink(input application.BatchRelinkRef) error {
	return a.service.CancelBatchRelink(input)
}
func (a *App) ChooseBatchRelinkDirectory() (string, error) {
	return a.openFile(openDialogOptions{Title: "选择素材当前所在的文件夹", Directories: true})
}

func (a *App) SaveProject() (application.Bootstrap, error) {
	if a.GetBootstrap().ProjectPath == "" {
		return a.SaveProjectAs()
	}
	return a.service.SaveProject()
}

func (a *App) SaveProjectAs() (application.Bootstrap, error) {
	path, err := a.saveFile(saveDialogOptions{Title: "保存工程", DefaultFilename: safeFilename(a.GetBootstrap().Project.Name) + ".aividcut", Filters: []wails.FileFilter{{DisplayName: "AIVidCut 工程", Pattern: "*.aividcut"}}, CanCreateDirectories: true})
	if err != nil {
		return application.Bootstrap{}, err
	}
	if path == "" {
		return a.GetBootstrap(), nil
	}
	return a.service.SaveProjectTo(path)
}

func (a *App) SaveProjectTo(path string) (application.Bootstrap, error) {
	return a.service.SaveProjectTo(path)
}

func (a *App) OpenProject() (application.Bootstrap, error) {
	// macOS 可能尚未识别原子保存后的自定义文件类型；最终仍由工程解析器核验格式和大小。
	path, err := a.openFile(openDialogOptions{Title: "打开工程", Filters: []wails.FileFilter{{DisplayName: "AIVidCut 工程", Pattern: "*.aividcut;*.json"}}, AllowsOtherFileTypes: true})
	if err != nil {
		return application.Bootstrap{}, err
	}
	if path == "" {
		return a.GetBootstrap(), nil
	}
	return a.service.OpenProjectPath(path)
}

func (a *App) OpenProjectPath(path string) (application.Bootstrap, error) {
	return a.service.OpenProjectPath(path)
}

func (a *App) ExportVideo(options media.ExportOptions) (jobs.Job, error) {
	if options.Path == "" {
		path, err := a.saveFile(saveDialogOptions{Title: "导出视频", DefaultFilename: a.sequenceExportFilename(), Filters: []wails.FileFilter{{DisplayName: "MP4 视频", Pattern: "*.mp4"}}, CanCreateDirectories: true})
		if err != nil {
			return jobs.Job{}, err
		}
		if path == "" {
			return jobs.Job{}, errors.New("已取消导出")
		}
		options.Path, options.Overwrite = path, true
	}
	return a.service.ExportVideo(options)
}

func (a *App) sequenceExportFilename() string {
	b := a.GetBootstrap()
	name := b.Project.Name
	if len(b.Sequences) > 1 {
		name += " - " + editor.CurrentSequenceName(b.Project)
	}
	return safeFilename(name) + ".mp4"
}

func (a *App) GetPreviewFrame(frame int64) (application.PreviewFrame, error) {
	return a.service.GetPreviewFrame(a.ctx, frame)
}

func (a *App) GetWaveformTile(request application.WaveformRequest) (media.WaveformTile, error) {
	return a.service.GetWaveformTile(a.ctx, request)
}
func (a *App) CancelWaveformRequest(id string) { a.service.CancelWaveformRequest(id) }

func (a *App) AcceptMCPVisualAnalysis(input application.AcceptVisualAnalysisInput) (application.Bootstrap, error) {
	return a.service.AcceptMCPVisualAnalysis(a.ctx, input)
}
func (a *App) RemoveVisualAnalysis(input application.RemoveVisualAnalysisInput) (application.Bootstrap, error) {
	return a.service.RemoveVisualAnalysis(a.ctx, input)
}

func (a *App) SearchSourceContent(input application.SourceContentQuery) (application.SourceContentResult, error) {
	return a.service.SearchSourceContent(a.ctx, input)
}

func (a *App) LocateTranscriptWords(input application.TranscriptCutInput) (application.TranscriptWordSelection, error) {
	return a.service.LocateTranscriptWords(a.ctx, input)
}
func (a *App) PreviewTranscriptCut(input application.TranscriptCutInput) (application.WorkflowPreview, error) {
	return a.service.PreviewTranscriptCut(a.ctx, input)
}

func (a *App) GetShotIndex(input application.ShotIndexInput) (application.ShotIndexView, error) {
	return a.service.GetShotIndex(a.ctx, input)
}
func (a *App) StartShotIndex(input application.ShotIndexInput) (application.ShotIndexView, error) {
	return a.service.StartShotIndex(a.ctx, input)
}

func (a *App) GetSourceFrame(input application.SourceFrameRequest) (application.SourceFrame, error) {
	return a.service.GetSourceFrame(a.ctx, input)
}

func (a *App) GetAssetThumbnail(request application.ThumbnailRequest) (application.AssetThumbnail, error) {
	return a.service.GetAssetThumbnail(a.ctx, request)
}
func (a *App) CancelThumbnailRequest(id string) { a.service.CancelThumbnailRequest(id) }

func (a *App) ListJobs() []jobs.Job { return a.service.ListJobs() }

func (a *App) CancelJob(id string) (jobs.Job, error) { return a.service.CancelJob(id) }

func (a *App) GetAgentStatus() ([]localagent.Installation, error) {
	return a.service.GetAgentStatus(a.ctx)
}
func (a *App) GetAgentLoginInstructions(provider localagent.Provider) (localagent.LoginCommand, error) {
	return a.service.GetAgentLoginInstructions(a.ctx, provider)
}
func (a *App) GetAgentWorkflows() []localagent.Workflow { return a.service.GetAgentWorkflows() }
func (a *App) StartAgentPlan(input application.StartAgentPlanInput) (application.AgentJob, error) {
	return a.service.StartAgentPlan(a.ctx, input)
}
func (a *App) ListAgentPlans() ([]application.AgentJob, error) { return a.service.ListAgentPlans() }
func (a *App) CancelAgentPlan(id string) (application.AgentJob, error) {
	return a.service.CancelAgentPlan(id)
}
func (a *App) ApplyAgentPlan(id string) (application.Bootstrap, error) {
	return a.service.ApplyAgentPlan(id)
}

func (a *App) GetAgentExecutionStatus() ([]localagent.Installation, error) {
	return a.service.GetAgentExecutionStatus(a.ctx)
}
func (a *App) StartAgentExecution(input application.StartAgentExecutionInput) (application.AgentExecution, error) {
	return a.service.StartAgentExecution(a.ctx, input)
}
func (a *App) ListAgentExecutions() ([]application.AgentExecution, error) {
	return a.service.ListAgentExecutions()
}
func (a *App) GetAgentExecution(id string) (application.AgentExecution, error) {
	return a.service.GetAgentExecution(id)
}
func (a *App) CancelAgentExecution(id string) (application.AgentExecution, error) {
	return a.service.CancelAgentExecution(id)
}

func (a *App) GetMCPStatus() application.MCPStatus { return a.service.GetMCPStatus() }

func (a *App) GetPlaybackCapabilities() (application.PlaybackCapabilities, error) {
	return a.service.GetPlaybackCapabilities(a.ctx)
}
func (a *App) StartPlayback(input application.PlaybackStartInput) (application.PlaybackStart, error) {
	return a.service.StartPlayback(a.ctx, input)
}
func (a *App) GetPlaybackStatus() application.PlaybackStatus { return a.service.GetPlaybackStatus() }
func (a *App) ControlPlayback(input application.PlaybackControlInput) (application.PlaybackStatus, error) {
	return a.service.ControlPlayback(a.ctx, input)
}
func (a *App) StopPlayback(id string) (application.PlaybackStatus, error) {
	return a.service.StopPlayback(id)
}
func (a *App) AddAudioToTimeline(input application.AddAudioInput) (application.Bootstrap, error) {
	return a.service.AddAudioToTimeline(a.ctx, input)
}

func (a *App) GetWorkflowCatalog() []workflows.Definition      { return a.service.GetWorkflowCatalog() }
func (a *App) GetWorkflowPresets() ([]workflows.Preset, error) { return a.service.GetWorkflowPresets() }
func (a *App) SaveWorkflowPreset(input workflows.PresetInput) (workflows.Preset, error) {
	return a.service.SaveWorkflowPreset(a.ctx, input)
}
func (a *App) DeleteWorkflowPreset(id string, version int64) error {
	return a.service.DeleteWorkflowPreset(a.ctx, id, version)
}
func (a *App) PreviewWorkflow(input workflows.Input) (application.WorkflowPreview, error) {
	return a.service.PreviewWorkflow(a.ctx, input)
}
func (a *App) ApplyWorkflowPreview(id string) (application.Bootstrap, error) {
	return a.service.ApplyWorkflowPreview(a.ctx, id)
}
func (a *App) PreviewCaptionLayout(input application.CaptionLayoutInput) (application.CaptionLayoutPreview, error) {
	return a.service.PreviewCaptionLayout(a.ctx, input)
}
func (a *App) ApplyCaptionLayout(id string) (application.Bootstrap, error) {
	return a.service.ApplyCaptionLayout(a.ctx, id)
}
func (a *App) StartMCP(input application.MCPStartInput) (application.MCPStatus, error) {
	return a.service.StartMCP(input)
}
func (a *App) StopMCP() (application.MCPStatus, error) { return a.service.StopMCP() }
func (a *App) GetMCPConnection() (mcpserver.ConnectionConfig, error) {
	return a.service.GetMCPConnection()
}
func (a *App) ListMCPRequests() []application.MCPAction { return a.service.ListMCPRequests() }
func (a *App) RejectMCPRequest(id string) (application.MCPAction, error) {
	return a.service.RejectMCPRequest(id)
}
func (a *App) ApproveMCPRequest(id string, confirmation application.MCPActionConfirmation) (application.MCPAction, error) {
	request, err := a.service.GetMCPRequest(id)
	if err != nil {
		return application.MCPAction{}, err
	}
	if request.Kind == "export" && request.Status == "pending" && confirmation.ExportPath == "" {
		path, err := a.saveFile(saveDialogOptions{Title: "确认Agent请求的视频导出位置", DefaultFilename: a.sequenceExportFilename(), Filters: []wails.FileFilter{{DisplayName: "MP4视频", Pattern: "*.mp4"}}, CanCreateDirectories: true})
		if err != nil {
			return application.MCPAction{}, err
		}
		if path == "" {
			return request, nil
		}
		confirmation.ExportPath, confirmation.Overwrite = path, true
	}
	return a.service.ApproveMCPRequest(a.ctx, id, confirmation)
}

func (a *App) GetSpeechCatalog() (application.SpeechCatalog, error) {
	return a.service.GetSpeechCatalog()
}
func (a *App) ListVoices(connectionID string, request speech.VoiceListRequest) (speech.VoicePage, error) {
	return a.service.ListVoices(a.ctx, connectionID, request)
}
func (a *App) StartSpeechJob(input speechjobs.Input) (application.SpeechSummary, error) {
	return a.service.StartSpeechJob(a.ctx, input)
}
func (a *App) ListSpeechJobs() ([]application.SpeechSummary, error) {
	return a.service.ListSpeechJobs()
}
func (a *App) GetSpeechJob(id string) (application.SpeechJob, error) {
	return a.service.GetSpeechJob(id)
}
func (a *App) CancelSpeechJob(id string) (application.SpeechSummary, error) {
	return a.service.CancelSpeechJob(id)
}
func (a *App) ResumeSpeechLocal(id string) (application.SpeechSummary, error) {
	return a.service.ResumeSpeechLocal(a.ctx, id)
}
func (a *App) AcknowledgeSpeechUnknown(id string) (application.SpeechSummary, error) {
	return a.service.AcknowledgeSpeechUnknown(id)
}
func (a *App) AcceptSpeechTranscript(id string, options application.AcceptTranscriptOptions) (application.Bootstrap, error) {
	return a.service.AcceptSpeechTranscript(a.ctx, id, options)
}
func (a *App) CorrectSourceTranscript(input application.CorrectTranscriptInput) (application.Bootstrap, error) {
	return a.service.CorrectSourceTranscript(a.ctx, input)
}
func (a *App) ImportSpeechAudio(id string, options application.ImportSpeechAudioOptions) (application.Bootstrap, error) {
	return a.service.ImportSpeechAudio(a.ctx, id, options)
}
func (a *App) DetachCaption(input application.DetachCaptionInput) (application.Bootstrap, error) {
	return a.service.DetachCaption(a.ctx, input)
}

func (a *App) GetConnections() ([]application.ConnectionProfile, error) {
	return a.service.GetConnections()
}
func (a *App) SaveConnection(input connections.SaveInput) (application.ConnectionProfile, error) {
	return a.service.SaveConnection(a.ctx, input)
}
func (a *App) DeleteConnection(id string) (application.ConnectionMutation, error) {
	return a.service.DeleteConnection(a.ctx, id)
}
func (a *App) GetConnectionHealth() (application.ConnectionHealth, error) {
	return a.service.GetConnectionHealth()
}
func (a *App) CleanupCredentials() (application.ConnectionHealth, error) {
	return a.service.CleanupCredentials(a.ctx)
}
func (a *App) GetGenerationCatalog() ([]generation.Capability, error) {
	return a.service.GetGenerationCatalog()
}
func (a *App) SubmitGeneration(input generationjobs.SubmitInput) (application.GenerationJob, error) {
	return a.service.SubmitGeneration(a.ctx, input)
}
func (a *App) ListGenerationJobs() ([]application.GenerationJob, error) {
	return a.service.ListGenerationJobs()
}
func (a *App) SyncGenerationJobs(input application.GenerationSyncInput) (application.GenerationSync, error) {
	return a.service.SyncGenerationJobs(a.ctx, input)
}

func (a *App) GetSourceOCRCapability() ocr.Capability { return a.service.GetSourceOCRCapability() }
func (a *App) PrepareSourceOCR(input application.SourceOCRInput) (application.SourceOCRDraft, error) {
	return a.service.PrepareSourceOCR(a.ctx, input)
}
func (a *App) AcceptSourceOCR(input application.AcceptSourceOCRInput) (application.Bootstrap, error) {
	return a.service.AcceptSourceOCR(a.ctx, input)
}
func (a *App) CancelSourceOCR(id string) { a.service.CancelSourceOCR(id) }
func (a *App) ListArchivedGenerationJobs(input generationjobs.ArchivePageInput) (generationjobs.ArchivePage, error) {
	return a.service.ListArchivedGenerationJobs(a.ctx, input)
}
func (a *App) ArchiveGenerationJob(id string) (generationjobs.ArchivedRecord, error) {
	return a.service.ArchiveGenerationJob(a.ctx, id)
}
func (a *App) RestoreArchivedGenerationJob(id string) (application.GenerationJob, error) {
	return a.service.RestoreArchivedGenerationJob(a.ctx, id)
}
func (a *App) StopGenerationWaiting(id string) (application.GenerationJob, error) {
	return a.service.StopGenerationWaiting(id)
}
func (a *App) ResumeGeneration(id string) (application.GenerationJob, error) {
	return a.service.ResumeGeneration(a.ctx, id)
}
func (a *App) ImportGenerationResult(id string) (application.Bootstrap, error) {
	return a.service.ImportGenerationResult(a.ctx, id)
}
func (a *App) ImportGenerationAssets(id string, ids []string) (application.Bootstrap, error) {
	return a.service.ImportGenerationAssets(a.ctx, id, ids)
}
func (a *App) PreviewGenerationReplacement(input application.GenerationReplacementInput) (application.GenerationReplacementPreview, error) {
	return a.service.PreviewGenerationReplacement(a.ctx, input)
}
func (a *App) ApplyGenerationReplacement(id string) (application.Bootstrap, error) {
	return a.service.ApplyGenerationReplacement(a.ctx, id)
}
func (a *App) AcknowledgeGenerationUnknown(id string) (application.GenerationJob, error) {
	return a.service.AcknowledgeGenerationUnknown(id)
}

func (a *App) ImportCaptions(path, trackID string) (application.Bootstrap, error) {
	return a.service.ImportCaptions(path, trackID)
}

func (a *App) ExportCaptionsTo(path, trackID string) (string, error) {
	return a.service.ExportCaptionsTo(path, trackID)
}

func (a *App) ImportSubtitle() (application.Bootstrap, error) {
	path, err := a.openFile(openDialogOptions{Title: "导入 SRT 字幕", Filters: []wails.FileFilter{{DisplayName: "SubRip 字幕", Pattern: "*.srt"}}})
	if err != nil {
		return application.Bootstrap{}, err
	}
	if path == "" {
		return a.GetBootstrap(), nil
	}
	return a.service.ImportCaptions(path, "")
}

func (a *App) ExportSubtitles() (string, error) {
	path, err := a.saveFile(saveDialogOptions{Title: "导出 SRT 字幕", DefaultFilename: safeFilename(a.GetBootstrap().Project.Name) + ".srt", Filters: []wails.FileFilter{{DisplayName: "SubRip 字幕", Pattern: "*.srt"}}, CanCreateDirectories: true})
	if err != nil || path == "" {
		return "", err
	}
	return a.service.ExportCaptionsTo(path, "")
}

func safeFilename(name string) string {
	name = strings.Map(func(r rune) rune {
		if strings.ContainsRune("/\\:*?\"<>|", r) || r < 32 {
			return '_'
		}
		return r
	}, strings.TrimSpace(name))
	if name == "" || name == "." || name == ".." {
		return "AIVidCut"
	}
	return name
}

func (a *App) GetPreviewProxy(input application.PreviewProxyInput) (application.PreviewProxyView, error) {
	return a.service.GetPreviewProxy(a.ctx, input)
}
func (a *App) StartPreviewProxy(input application.PreviewProxyInput) (application.PreviewProxyView, error) {
	return a.service.StartPreviewProxy(a.ctx, input)
}
func (a *App) ClearPreviewProxy(input application.PreviewProxyInput) error {
	return a.service.ClearPreviewProxy(a.ctx, input)
}

func (a *App) PreviewRippleDelete(input application.RippleInput) (application.WorkflowPreview, error) {
	return a.service.PreviewRippleDelete(a.ctx, input)
}

func (a *App) CaptureFrameAsset(input application.FrameCaptureInput) (application.FrameCaptureResult, error) {
	return a.service.CaptureFrameAsset(a.ctx, input)
}

func (a *App) CancelFrameCapture(input application.FrameCaptureCancel) error {
	return a.service.CancelFrameCapture(input)
}

func (a *App) PrepareGenerationReuse(input application.GenerationReuseInput) (application.GenerationReusePlan, error) {
	return a.service.PrepareGenerationReuse(a.ctx, input)
}

func (a *App) PreviewGenerationPlacement(input application.GenerationPlacementInput) (application.GenerationPlacementPreview, error) {
	return a.service.PreviewGenerationPlacement(a.ctx, input)
}
func (a *App) ApplyGenerationPlacement(id string) (application.Bootstrap, error) {
	return a.service.ApplyGenerationPlacement(a.ctx, id)
}

func (a *App) SaveInspectorDraft(input application.InspectorDraftInput) (*inspectordrafts.Record, error) {
	return a.service.SaveInspectorDraft(input)
}
func (a *App) ListInspectorDrafts() (inspectordrafts.Page, error) {
	return a.service.ListInspectorDrafts()
}
func (a *App) DeleteInspectorDraft(input application.InspectorDraftRef) error {
	return a.service.DeleteInspectorDraft(input)
}
func (a *App) PreviewInspectorDraft(input application.InspectorDraftReview) (application.InspectorDraftPreview, error) {
	return a.service.PreviewInspectorDraft(input)
}
func (a *App) ApplyInspectorDraft(id string) (application.Bootstrap, error) {
	return a.service.ApplyInspectorDraft(id)
}
func (a *App) RestoreInspectorDraftBase(input application.InspectorDraftRef) (application.Bootstrap, error) {
	return a.service.RestoreInspectorDraftBase(input)
}

func (a *App) GetResourceState() (resourceprofile.State, error) {
	return a.service.GetResourceState(a.ctx)
}
func (a *App) StartResourceProfile(input application.StartResourceProfileInput) (resourceprofile.Summary, error) {
	return a.service.StartResourceProfile(input)
}
func (a *App) StopResourceProfile(id string) (resourceprofile.Summary, error) {
	return a.service.StopResourceProfile(id)
}
func (a *App) ExportResourceProfile(input application.ExportResourceProfileInput) (string, error) {
	return a.service.ExportResourceProfile(a.ctx, input)
}
func (a *App) ChooseResourceReportPath() (string, error) {
	return a.saveFile(saveDialogOptions{Title: "导出资源诊断报告", DefaultFilename: "aividcut-resources.json", Filters: []wails.FileFilter{{DisplayName: "JSON诊断报告", Pattern: "*.json"}}, CanCreateDirectories: true})
}

func (a *App) GetResourceStatus() (resourceprofile.State, error) {
	return a.service.GetResourceStatus(a.ctx)
}

func (a *App) GetMediaTools() (application.MediaToolsView, error) { return a.service.GetMediaTools() }
func (a *App) InspectMediaTools(input mediatools.Config) (application.MediaToolsInspection, error) {
	return a.service.InspectMediaTools(a.ctx, input)
}
func (a *App) SaveMediaTools(id string) (application.MediaToolsView, error) {
	return a.service.SaveMediaTools(id)
}
func (a *App) ChooseMediaToolPath(kind string) (string, error) {
	switch kind {
	case "ffmpeg", "ffprobe", "worker":
		return a.openFile(openDialogOptions{Title: "选择本机媒体程序", AllowsOtherFileTypes: true})
	case "repository", "profiles":
		return a.openFile(openDialogOptions{Title: "选择配套引擎目录", Directories: true})
	default:
		return "", errors.New("未知的媒体设置项")
	}
}
