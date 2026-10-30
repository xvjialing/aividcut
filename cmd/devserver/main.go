// 开发服务复用真实 Go 应用层，用于浏览器联调与自动化；不作为桌面发行入口。
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"aividcut/internal/application"
	"aividcut/internal/connections"
	"aividcut/internal/editor"
	"aividcut/internal/generationjobs"
	"aividcut/internal/localagent"
	"aividcut/internal/media"
	"aividcut/internal/mediatools"
	"aividcut/internal/speech"
	"aividcut/internal/speechjobs"
	"aividcut/internal/workflows"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:43110", "仅允许回环地址")
	data := flag.String("data", "work/dev-data", "开发持久资料及预览目录")
	static := flag.String("static", "", "可选的已构建前端目录")
	flag.Parse()
	host, _, err := net.SplitHostPort(*listen)
	if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		log.Fatal("开发服务仅允许监听回环地址")
	}
	service, err := application.NewWithMediaSettings(filepath.Join(*data, "previews"), filepath.Join(*data, "media-tools"))
	if err != nil {
		log.Fatal(err)
	}
	defer service.Close()
	if err := service.ConfigureGeneration(application.GenerationOptions{Directory: *data}); err != nil {
		log.Fatal(err)
	}
	if err := service.ConfigureAgents(application.AgentOptions{Directory: filepath.Join(*data, "agents")}); err != nil {
		log.Fatal(err)
	}
	if err := service.ConfigureWorkflows(filepath.Join(*data, "workflows")); err != nil {
		log.Fatal(err)
	}
	if err := service.ConfigureProjectVersions(filepath.Join(*data, "versions")); err != nil {
		log.Fatal(err)
	}
	if err := service.ConfigureRecovery(application.RecoveryOptions{Directory: filepath.Join(*data, "recovery")}); err != nil {
		log.Fatal(err)
	}
	if err := service.ConfigureInspectorDrafts(filepath.Join(*data, "inspector-drafts")); err != nil {
		log.Fatal(err)
	}
	if err := service.ConfigurePlayback(application.PlaybackOptions{Config: service.ConfiguredPlayback()}); err != nil {
		log.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store")
		requestHost, _, err := net.SplitHostPort(r.Host)
		if err != nil || (requestHost != "127.0.0.1" && requestHost != "localhost" && requestHost != "::1") {
			http.Error(w, "不接受外部主机请求", http.StatusForbidden)
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" && origin != "http://"+r.Host && origin != "http://localhost:5173" && origin != "http://127.0.0.1:5173" && origin != "http://localhost:5174" && origin != "http://127.0.0.1:5174" {
			http.Error(w, "不接受跨站请求", http.StatusForbidden)
			return
		}
		if r.Method != http.MethodPost || !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
			http.Error(w, "接口需要 JSON POST", http.StatusMethodNotAllowed)
			return
		}
		var payload struct {
			Args []json.RawMessage `json:"args"`
		}
		// 与工程文件上限一致，避免合法长转写的纠错在开发桥被2MiB截断。
		const requestLimit int64 = 64 << 20
		if r.ContentLength > requestLimit {
			writeResult(w, nil, errors.New("请求超过64MiB限制"))
			return
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, requestLimit))
		if err := decoder.Decode(&payload); err != nil {
			writeResult(w, nil, fmt.Errorf("请求格式错误：%w", err))
			return
		}
		var extra any
		if decoder.Decode(&extra) != io.EOF {
			writeResult(w, nil, errors.New("请求包含额外内容或超过限制"))
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
		defer cancel()
		result, err := dispatch(ctx, service, strings.TrimPrefix(r.URL.Path, "/api/"), payload.Args)
		writeResult(w, result, err)
	})
	if *static != "" {
		mux.Handle("/", http.FileServer(http.Dir(*static)))
	} else {
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprintln(w, "AIVidCut Go 开发服务；前端使用 Vite 或 -static frontend/dist")
		})
	}
	server := &http.Server{Addr: *listen, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-stop
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		server.Shutdown(ctx)
	}()
	log.Printf("AIVidCut 开发服务：http://%s", *listen)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

func argument[T any](args []json.RawMessage, i int) (T, error) {
	var value T
	if i >= len(args) {
		return value, errors.New("缺少请求参数")
	}
	err := json.Unmarshal(args[i], &value)
	return value, err
}

func dispatch(ctx context.Context, s *application.Service, method string, args []json.RawMessage) (any, error) {
	switch method {
	case "GetClipboardStatus":
		return s.GetClipboardStatus(), nil
	case "CopyTimelineSelection":
		input, err := argument[application.ClipboardSelectionInput](args, 0)
		if err != nil {
			return nil, err
		}
		return s.CopyTimelineSelection(input)
	case "CutTimelineSelection":
		input, err := argument[application.ClipboardCutInput](args, 0)
		if err != nil {
			return nil, err
		}
		return s.CutTimelineSelection(input)
	case "PasteTimelineSelection":
		input, err := argument[application.ClipboardPasteInput](args, 0)
		if err != nil {
			return nil, err
		}
		return s.PasteTimelineSelection(input)
	case "GetResourceStatus":
		return s.GetResourceStatus(ctx)
	case "GetMediaTools":
		return s.GetMediaTools()
	case "InspectMediaTools":
		input, err := argument[mediatools.Config](args, 0)
		if err != nil {
			return nil, err
		}
		return s.InspectMediaTools(ctx, input)
	case "SaveMediaTools":
		id, err := argument[string](args, 0)
		if err != nil {
			return nil, err
		}
		return s.SaveMediaTools(id)
	case "GetResourceState":
		return s.GetResourceState(ctx)
	case "StartResourceProfile":
		input, err := argument[application.StartResourceProfileInput](args, 0)
		if err != nil {
			return nil, err
		}
		return s.StartResourceProfile(input)
	case "StopResourceProfile":
		id, err := argument[string](args, 0)
		if err != nil {
			return nil, err
		}
		return s.StopResourceProfile(id)
	case "ExportResourceProfile":
		input, err := argument[application.ExportResourceProfileInput](args, 0)
		if err != nil {
			return nil, err
		}
		return s.ExportResourceProfile(ctx, input)
	case "GetBootstrap":
		return s.GetBootstrap(), nil
	case "GetPlaybackCapabilities":
		return s.GetPlaybackCapabilities(ctx)
	case "AddAudioToTimeline":
		input, err := argument[application.AddAudioInput](args, 0)
		if err != nil {
			return nil, err
		}
		return s.AddAudioToTimeline(ctx, input)
	case "GetPlaybackStatus":
		return s.GetPlaybackStatus(), nil
	case "StartPlayback":
		input, err := argument[application.PlaybackStartInput](args, 0)
		if err != nil {
			return nil, err
		}
		return s.StartPlayback(ctx, input)
	case "ControlPlayback":
		input, err := argument[application.PlaybackControlInput](args, 0)
		if err != nil {
			return nil, err
		}
		return s.ControlPlayback(ctx, input)
	case "StopPlayback":
		id, err := argument[string](args, 0)
		if err != nil {
			return nil, err
		}
		return s.StopPlayback(id)
	case "GetWorkflowCatalog":
		return s.GetWorkflowCatalog(), nil
	case "GetWorkflowPresets":
		return s.GetWorkflowPresets()
	case "SaveWorkflowPreset":
		input, err := argument[workflows.PresetInput](args, 0)
		if err != nil {
			return nil, err
		}
		return s.SaveWorkflowPreset(ctx, input)
	case "DeleteWorkflowPreset":
		id, err := argument[string](args, 0)
		if err != nil {
			return nil, err
		}
		version, err := argument[int64](args, 1)
		if err != nil {
			return nil, err
		}
		return nil, s.DeleteWorkflowPreset(ctx, id, version)
	case "PreviewWorkflow":
		input, err := argument[workflows.Input](args, 0)
		if err != nil {
			return nil, err
		}
		return s.PreviewWorkflow(ctx, input)
	case "ApplyWorkflowPreview":
		id, err := argument[string](args, 0)
		if err != nil {
			return nil, err
		}
		return s.ApplyWorkflowPreview(ctx, id)
	case "GetMCPStatus":
		return s.GetMCPStatus(), nil
	case "StartMCP":
		input, err := argument[application.MCPStartInput](args, 0)
		if err != nil {
			return nil, err
		}
		return s.StartMCP(input)
	case "StopMCP":
		return s.StopMCP()
	case "GetMCPConnection":
		return s.GetMCPConnection()
	case "ListMCPRequests":
		return s.ListMCPRequests(), nil
	case "RejectMCPRequest":
		id, err := argument[string](args, 0)
		if err != nil {
			return nil, err
		}
		return s.RejectMCPRequest(id)
	case "ApproveMCPRequest":
		id, err := argument[string](args, 0)
		if err != nil {
			return nil, err
		}
		confirmation, err := argument[application.MCPActionConfirmation](args, 1)
		if err != nil {
			return nil, err
		}
		if request, readErr := s.GetMCPRequest(id); readErr == nil && request.Kind == "export" && confirmation.ExportPath == "" {
			return nil, errors.New("浏览器开发模式请先选择导出路径")
		}
		return s.ApproveMCPRequest(ctx, id, confirmation)
	case "GetSpeechCatalog":
		return s.GetSpeechCatalog()
	case "ListVoices":
		id, err := argument[string](args, 0)
		if err != nil {
			return nil, err
		}
		request, err := argument[speech.VoiceListRequest](args, 1)
		if err != nil {
			return nil, err
		}
		return s.ListVoices(ctx, id, request)
	case "StartSpeechJob":
		input, err := argument[speechjobs.Input](args, 0)
		if err != nil {
			return nil, err
		}
		return s.StartSpeechJob(ctx, input)
	case "ListSpeechJobs":
		return s.ListSpeechJobs()
	case "GetSpeechJob":
		id, err := argument[string](args, 0)
		if err != nil {
			return nil, err
		}
		return s.GetSpeechJob(id)
	case "CancelSpeechJob":
		id, err := argument[string](args, 0)
		if err != nil {
			return nil, err
		}
		return s.CancelSpeechJob(id)
	case "ResumeSpeechLocal":
		id, err := argument[string](args, 0)
		if err != nil {
			return nil, err
		}
		return s.ResumeSpeechLocal(ctx, id)
	case "AcknowledgeSpeechUnknown":
		id, err := argument[string](args, 0)
		if err != nil {
			return nil, err
		}
		return s.AcknowledgeSpeechUnknown(id)
	case "AcceptSpeechTranscript":
		id, err := argument[string](args, 0)
		if err != nil {
			return nil, err
		}
		options, err := argument[application.AcceptTranscriptOptions](args, 1)
		if err != nil {
			return nil, err
		}
		return s.AcceptSpeechTranscript(ctx, id, options)
	case "CorrectSourceTranscript":
		input, err := argument[application.CorrectTranscriptInput](args, 0)
		if err != nil {
			return nil, err
		}
		return s.CorrectSourceTranscript(ctx, input)
	case "PreviewCaptionLayout":
		input, err := argument[application.CaptionLayoutInput](args, 0)
		if err != nil {
			return nil, err
		}
		return s.PreviewCaptionLayout(ctx, input)
	case "ApplyCaptionLayout":
		id, err := argument[string](args, 0)
		if err != nil {
			return nil, err
		}
		return s.ApplyCaptionLayout(ctx, id)
	case "ImportSpeechAudio":
		id, err := argument[string](args, 0)
		if err != nil {
			return nil, err
		}
		options, err := argument[application.ImportSpeechAudioOptions](args, 1)
		if err != nil {
			return nil, err
		}
		return s.ImportSpeechAudio(ctx, id, options)
	case "DetachCaption":
		input, err := argument[application.DetachCaptionInput](args, 0)
		if err != nil {
			return nil, err
		}
		return s.DetachCaption(ctx, input)
	case "GetAgentStatus":
		return s.GetAgentStatus(ctx)
	case "GetAgentExecutionStatus":
		return s.GetAgentExecutionStatus(ctx)
	case "StartAgentExecution":
		input, err := argument[application.StartAgentExecutionInput](args, 0)
		if err != nil {
			return nil, err
		}
		return s.StartAgentExecution(ctx, input)
	case "ListAgentExecutions":
		return s.ListAgentExecutions()
	case "GetAgentExecution":
		id, err := argument[string](args, 0)
		if err != nil {
			return nil, err
		}
		return s.GetAgentExecution(id)
	case "CancelAgentExecution":
		id, err := argument[string](args, 0)
		if err != nil {
			return nil, err
		}
		return s.CancelAgentExecution(id)
	case "GetAgentLoginInstructions":
		provider, err := argument[localagent.Provider](args, 0)
		if err != nil {
			return nil, err
		}
		return s.GetAgentLoginInstructions(ctx, provider)
	case "GetAgentWorkflows":
		return s.GetAgentWorkflows(), nil
	case "StartAgentPlan":
		input, err := argument[application.StartAgentPlanInput](args, 0)
		if err != nil {
			return nil, err
		}
		return s.StartAgentPlan(ctx, input)
	case "ListAgentPlans":
		return s.ListAgentPlans()
	case "CancelAgentPlan":
		id, err := argument[string](args, 0)
		if err != nil {
			return nil, err
		}
		return s.CancelAgentPlan(id)
	case "ApplyAgentPlan":
		id, err := argument[string](args, 0)
		if err != nil {
			return nil, err
		}
		return s.ApplyAgentPlan(id)
	case "GetConnections":
		return s.GetConnections()
	case "SaveConnection":
		input, err := argument[connections.SaveInput](args, 0)
		if err != nil {
			return nil, err
		}
		return s.SaveConnection(ctx, input)
	case "DeleteConnection":
		id, err := argument[string](args, 0)
		if err != nil {
			return nil, err
		}
		return s.DeleteConnection(ctx, id)
	case "GetConnectionHealth":
		return s.GetConnectionHealth()
	case "CleanupCredentials":
		return s.CleanupCredentials(ctx)
	case "GetGenerationCatalog":
		return s.GetGenerationCatalog()
	case "SubmitGeneration":
		input, err := argument[generationjobs.SubmitInput](args, 0)
		if err != nil {
			return nil, err
		}
		return s.SubmitGeneration(ctx, input)
	case "ListGenerationJobs":
		return s.ListGenerationJobs()

	case "GetSourceOCRCapability":
		return s.GetSourceOCRCapability(), nil
	case "PrepareSourceOCR":
		input, err := argument[application.SourceOCRInput](args, 0)
		if err != nil {
			return nil, err
		}
		return s.PrepareSourceOCR(ctx, input)
	case "AcceptSourceOCR":
		input, err := argument[application.AcceptSourceOCRInput](args, 0)
		if err != nil {
			return nil, err
		}
		return s.AcceptSourceOCR(ctx, input)
	case "CancelSourceOCR":
		id, err := argument[string](args, 0)
		if err != nil {
			return nil, err
		}
		s.CancelSourceOCR(id)
		return nil, nil
	case "SyncGenerationJobs":
		input, err := argument[application.GenerationSyncInput](args, 0)
		if err != nil {
			return nil, err
		}
		return s.SyncGenerationJobs(ctx, input)
	case "ListArchivedGenerationJobs":
		input, err := argument[generationjobs.ArchivePageInput](args, 0)
		if err != nil {
			return nil, err
		}
		return s.ListArchivedGenerationJobs(ctx, input)
	case "ArchiveGenerationJob":
		id, err := argument[string](args, 0)
		if err != nil {
			return nil, err
		}
		return s.ArchiveGenerationJob(ctx, id)
	case "RestoreArchivedGenerationJob":
		id, err := argument[string](args, 0)
		if err != nil {
			return nil, err
		}
		return s.RestoreArchivedGenerationJob(ctx, id)
	case "StopGenerationWaiting":
		id, err := argument[string](args, 0)
		if err != nil {
			return nil, err
		}
		return s.StopGenerationWaiting(id)
	case "ResumeGeneration":
		id, err := argument[string](args, 0)
		if err != nil {
			return nil, err
		}
		return s.ResumeGeneration(ctx, id)
	case "ImportGenerationResult":
		id, err := argument[string](args, 0)
		if err != nil {
			return nil, err
		}
		return s.ImportGenerationResult(ctx, id)
	case "PreviewGenerationReplacement":
		input, err := argument[application.GenerationReplacementInput](args, 0)
		if err != nil {
			return nil, err
		}
		return s.PreviewGenerationReplacement(ctx, input)
	case "ApplyGenerationReplacement":
		id, err := argument[string](args, 0)
		if err != nil {
			return nil, err
		}
		return s.ApplyGenerationReplacement(ctx, id)
	case "ImportGenerationAssets":
		id, err := argument[string](args, 0)
		if err != nil {
			return nil, err
		}
		ids, err := argument[[]string](args, 1)
		if err != nil {
			return nil, err
		}
		return s.ImportGenerationAssets(ctx, id, ids)
	case "AcknowledgeGenerationUnknown":
		id, err := argument[string](args, 0)
		if err != nil {
			return nil, err
		}
		return s.AcknowledgeGenerationUnknown(id)
	case "NewProjectWithSettings":
		input, err := argument[application.NewProjectInput](args, 0)
		if err != nil {
			return nil, err
		}
		return s.NewProjectWithSettings(input)
	case "NewProject":
		name, err := argument[string](args, 0)
		if err != nil {
			return nil, err
		}
		return s.NewProject(name)
	case "ImportPaths":
		paths, err := argument[[]string](args, 0)
		if err != nil {
			return nil, err
		}
		return s.ImportPaths(ctx, paths)
	case "RelinkAssetPath":
		id, err := argument[string](args, 0)
		if err != nil {
			return nil, err
		}
		path, err := argument[string](args, 1)
		if err != nil {
			return nil, err
		}
		return s.RelinkAsset(ctx, id, path)
	case "OpenProjectPath":
		path, err := argument[string](args, 0)
		if err != nil {
			return nil, err
		}
		return s.OpenProjectPath(path)
	case "SaveProjectTo":
		path, err := argument[string](args, 0)
		if err != nil {
			return nil, err
		}
		return s.SaveProjectTo(path)
	case "SaveProject":
		return s.SaveProject()
	case "ListProjectVersions":
		input, err := argument[application.ListProjectVersionsInput](args, 0)
		if err != nil {
			return nil, err
		}
		return s.ListProjectVersions(input)
	case "ListSnapshotStorage":
		input, err := argument[application.SnapshotStorageInput](args, 0)
		if err != nil {
			return nil, err
		}
		return s.ListSnapshotStorage(input)
	case "ChangeSnapshotStorage":
		input, err := argument[application.SnapshotStorageChange](args, 0)
		if err != nil {
			return nil, err
		}
		return s.ChangeSnapshotStorage(input)
	case "GetRecoveryStatus":
		return s.GetRecoveryStatus(), nil
	case "ListInspectorDrafts":
		return s.ListInspectorDrafts()
	case "SaveInspectorDraft":
		input, err := argument[application.InspectorDraftInput](args, 0)
		if err != nil {
			return nil, err
		}
		return s.SaveInspectorDraft(input)
	case "DeleteInspectorDraft":
		input, err := argument[application.InspectorDraftRef](args, 0)
		if err != nil {
			return nil, err
		}
		return nil, s.DeleteInspectorDraft(input)
	case "PreviewInspectorDraft":
		input, err := argument[application.InspectorDraftReview](args, 0)
		if err != nil {
			return nil, err
		}
		return s.PreviewInspectorDraft(input)
	case "RestoreInspectorDraftBase":
		input, err := argument[application.InspectorDraftRef](args, 0)
		if err != nil {
			return nil, err
		}
		return s.RestoreInspectorDraftBase(input)
	case "ApplyInspectorDraft":
		id, err := argument[string](args, 0)
		if err != nil {
			return nil, err
		}
		return s.ApplyInspectorDraft(id)
	case "ListProjectRecoveries":
		input, err := argument[application.RecoveryListInput](args, 0)
		if err != nil {
			return nil, err
		}
		return s.ListProjectRecoveries(input)
	case "CheckpointRecovery":
		return s.CheckpointRecovery()
	case "RestoreProjectRecovery":
		input, err := argument[application.RestoreRecoveryInput](args, 0)
		if err != nil {
			return nil, err
		}
		return s.RestoreProjectRecovery(input)
	case "ChangeSequence":
		input, err := argument[application.SequenceChangeInput](args, 0)
		if err != nil {
			return nil, err
		}
		return s.ChangeSequence(input)
	case "InspectBatchRelink":
		input, err := argument[application.ProjectScope](args, 0)
		if err != nil {
			return nil, err
		}
		return s.InspectBatchRelink(ctx, input)
	case "PreviewBatchRelink":
		input, err := argument[application.BatchRelinkInput](args, 0)
		if err != nil {
			return nil, err
		}
		return s.PreviewBatchRelink(ctx, input)
	case "ApplyBatchRelink":
		input, err := argument[application.BatchRelinkRef](args, 0)
		if err != nil {
			return nil, err
		}
		return s.ApplyBatchRelink(ctx, input)
	case "CancelBatchRelink":
		input, err := argument[application.BatchRelinkRef](args, 0)
		if err != nil {
			return nil, err
		}
		return nil, s.CancelBatchRelink(input)
	case "ChooseBatchRelinkDirectory":
		return nil, errors.New("浏览器模式请直接输入当前文件夹完整路径")
	case "CollectProject":
		input, err := argument[application.CollectProjectInput](args, 0)
		if err != nil {
			return nil, err
		}
		return s.CollectProject(input)
	case "InspectProjectCollection":
		input, err := argument[application.ProjectScope](args, 0)
		if err != nil {
			return nil, err
		}
		return s.InspectProjectCollection(ctx, input)
	case "ChooseProjectCollectionDirectory":
		return nil, errors.New("浏览器模式请直接输入完整的上级文件夹路径")
	case "CreateProjectVersion":
		input, err := argument[application.CreateProjectVersionInput](args, 0)
		if err != nil {
			return nil, err
		}
		return s.CreateProjectVersion(input)
	case "RestoreProjectVersion":
		input, err := argument[application.RestoreProjectVersionInput](args, 0)
		if err != nil {
			return nil, err
		}
		return s.RestoreProjectVersion(input)
	case "ApplyScopedEdit":
		input, err := argument[application.ScopedEditInput](args, 0)
		if err != nil {
			return nil, err
		}
		return s.ApplyScopedEdit(input)
	case "ApplyEdit":
		request, err := argument[editor.EditRequest](args, 0)
		if err != nil {
			return nil, err
		}
		return s.ApplyEdit(request)
	case "Undo":
		return s.Undo()
	case "Redo":
		return s.Redo()
	case "ListJobs":
		return s.ListJobs(), nil
	case "CancelJob":
		id, err := argument[string](args, 0)
		if err != nil {
			return nil, err
		}
		return s.CancelJob(id)
	case "GetPreviewFrame":
		frame, err := argument[int64](args, 0)
		if err != nil {
			return nil, err
		}
		return s.GetPreviewFrame(ctx, frame)
	case "GetWaveformTile":
		request, err := argument[application.WaveformRequest](args, 0)
		if err != nil {
			return nil, err
		}
		return s.GetWaveformTile(ctx, request)
	case "AcceptMCPVisualAnalysis":
		input, err := argument[application.AcceptVisualAnalysisInput](args, 0)
		if err != nil {
			return nil, err
		}
		return s.AcceptMCPVisualAnalysis(ctx, input)
	case "RemoveVisualAnalysis":
		input, err := argument[application.RemoveVisualAnalysisInput](args, 0)
		if err != nil {
			return nil, err
		}
		return s.RemoveVisualAnalysis(ctx, input)
	case "SearchSourceContent":
		input, err := argument[application.SourceContentQuery](args, 0)
		if err != nil {
			return nil, err
		}
		return s.SearchSourceContent(ctx, input)
	case "PrepareGenerationReuse":
		input, err := argument[application.GenerationReuseInput](args, 0)
		if err != nil {
			return nil, err
		}
		return s.PrepareGenerationReuse(ctx, input)
	case "PreviewGenerationPlacement":
		input, err := argument[application.GenerationPlacementInput](args, 0)
		if err != nil {
			return nil, err
		}
		return s.PreviewGenerationPlacement(ctx, input)
	case "ApplyGenerationPlacement":
		id, err := argument[string](args, 0)
		if err != nil {
			return nil, err
		}
		return s.ApplyGenerationPlacement(ctx, id)
	case "GetPreviewProxy", "StartPreviewProxy", "ClearPreviewProxy":
		input, err := argument[application.PreviewProxyInput](args, 0)
		if err != nil {
			return nil, err
		}
		switch method {
		case "GetPreviewProxy":
			return s.GetPreviewProxy(ctx, input)
		case "StartPreviewProxy":
			return s.StartPreviewProxy(ctx, input)
		default:
			return nil, s.ClearPreviewProxy(ctx, input)
		}
	case "CancelFrameCapture":
		input, err := argument[application.FrameCaptureCancel](args, 0)
		if err != nil {
			return nil, err
		}
		return nil, s.CancelFrameCapture(input)
	case "CaptureFrameAsset":
		input, err := argument[application.FrameCaptureInput](args, 0)
		if err != nil {
			return nil, err
		}
		return s.CaptureFrameAsset(ctx, input)
	case "PreviewRippleDelete":
		input, err := argument[application.RippleInput](args, 0)
		if err != nil {
			return nil, err
		}
		return s.PreviewRippleDelete(ctx, input)
	case "LocateTranscriptWords":
		input, err := argument[application.TranscriptCutInput](args, 0)
		if err != nil {
			return nil, err
		}
		return s.LocateTranscriptWords(ctx, input)
	case "PreviewTranscriptCut":
		input, err := argument[application.TranscriptCutInput](args, 0)
		if err != nil {
			return nil, err
		}
		return s.PreviewTranscriptCut(ctx, input)
	case "GetShotIndex":
		input, err := argument[application.ShotIndexInput](args, 0)
		if err != nil {
			return nil, err
		}
		return s.GetShotIndex(ctx, input)
	case "StartShotIndex":
		input, err := argument[application.ShotIndexInput](args, 0)
		if err != nil {
			return nil, err
		}
		return s.StartShotIndex(ctx, input)
	case "GetSourceFrame":
		input, err := argument[application.SourceFrameRequest](args, 0)
		if err != nil {
			return nil, err
		}
		return s.GetSourceFrame(ctx, input)
	case "GetAssetThumbnail":
		request, err := argument[application.ThumbnailRequest](args, 0)
		if err != nil {
			return nil, err
		}
		return s.GetAssetThumbnail(ctx, request)
	case "CancelThumbnailRequest":
		id, err := argument[string](args, 0)
		if err != nil {
			return nil, err
		}
		s.CancelThumbnailRequest(id)
		return nil, nil
	case "CancelWaveformRequest":
		id, err := argument[string](args, 0)
		if err != nil {
			return nil, err
		}
		s.CancelWaveformRequest(id)
		return nil, nil
	case "ImportCaptions":
		path, err := argument[string](args, 0)
		if err != nil {
			return nil, err
		}
		trackID, err := argument[string](args, 1)
		if err != nil {
			return nil, err
		}
		return s.ImportCaptions(path, trackID)
	case "ExportCaptionsTo":
		path, err := argument[string](args, 0)
		if err != nil {
			return nil, err
		}
		trackID, err := argument[string](args, 1)
		if err != nil {
			return nil, err
		}
		return s.ExportCaptionsTo(path, trackID)
	case "ExportVideo":
		options, err := argument[media.ExportOptions](args, 0)
		if err != nil {
			return nil, err
		}
		return s.ExportVideo(options)
	case "OpenProject", "SaveProjectAs", "ImportMedia", "ImportLUT", "ImportFont", "RelinkAsset", "ImportSubtitle", "ExportSubtitles":
		return nil, errors.New("浏览器开发模式请使用路径对话框；系统文件选择器在 Wails 桌面端可用")
	default:
		return nil, errors.New("不支持的接口")
	}
}

func writeResult(w http.ResponseWriter, result any, err error) {
	if err != nil {
		json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
		return
	}
	json.NewEncoder(w).Encode(map[string]any{"result": result})
}
