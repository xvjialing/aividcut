import type {
  ClipboardStatus,
  ClipboardSelectionInput,
  ClipboardCutInput,
  ClipboardPasteInput,
  ClipboardResult,
} from '../clipboard-types';
import type { Bootstrap, EditRequest, ExportOptions, Job, PreviewFrame } from '../types';
import type { ProjectVersionsAPI } from '../project-version-types';
import { callDesktop, isDesktop } from './desktop';
import type { WaveformAPI, WaveformTile } from '../waveform-types';
import type { ThumbnailAPI, Thumbnail } from '../thumbnail-types';
import type {
  ConnectionProfile,
  GenerationAPI,
  GenerationCapability,
  GenerationJob,
  SaveConnectionInput,
  SubmitGenerationInput,
} from '../generation-types';
import type {
  AgentAPI,
  AgentInstallation,
  AgentJob,
  AgentLoginCommand,
  AgentPlanInput,
  AgentProvider,
  AgentWorkflow,
} from '../agent-types';
import type {
  SpeechAPI,
  SpeechCatalog,
  SpeechJob,
  SpeechSummary,
  SpeechTranscriptCorrection,
  SpeechVoiceListRequest,
  SpeechVoicePage,
  StartSpeechInput,
  CaptionLayoutPreview,
} from '../speech-types';
import type { MCPAPI, MCPAction, MCPConnection, MCPStatus } from '../mcp-types';
import type { AgentExecution, AgentExecutionAPI } from '../agent-execution-types';
import type { GenerationReplacementPreview, ReplacementAPI } from '../replacement-types';
import type {
  PlaybackAPI,
  PlaybackCapabilities,
  PlaybackStart,
  PlaybackStatus,
} from '../playback-types';
import type {
  WorkflowAPI,
  WorkflowDefinition,
  WorkflowPreset,
  WorkflowPreview,
} from '../workflow-types';

export { isDesktop } from './desktop';

export const generationPlacementAPI: import('../generation-placement-types').GenerationPlacementAPI =
  {
    preview: (input) => invoke('PreviewGenerationPlacement', input),
    apply: (id) => invoke('ApplyGenerationPlacement', id),
  };

export const generationReuseAPI: import('../generation-reuse-types').GenerationReuseAPI = {
  prepare: (input) => invoke('PrepareGenerationReuse', input),
};

export const frameCaptureAPI: import('../frame-capture-types').FrameCaptureAPI = {
  capture: (input) => invoke('CaptureFrameAsset', input),
  cancel: (input) => invoke('CancelFrameCapture', input),
};

export const rippleAPI: import('../ripple-types').RippleAPI = {
  preview: (input) => invoke('PreviewRippleDelete', input),
  apply: (id) => invoke('ApplyWorkflowPreview', id),
};

export const previewProxyAPI: import('../proxy-types').PreviewProxyAPI = {
  get: (input) => invoke('GetPreviewProxy', input),
  start: (input) => invoke('StartPreviewProxy', input),
  clear: (input) => invoke('ClearPreviewProxy', input),
  cancel: (id) => invoke('CancelJob', id),
};

export const transcriptCutAPI: import('../transcript-cut-types').TranscriptCutAPI = {
  locate: (input) => invoke('LocateTranscriptWords', input),
  preview: (input) => invoke('PreviewTranscriptCut', input),
  apply: (id) => invoke('ApplyWorkflowPreview', id),
};

export const contentBridge: import('../content-types').ContentAPI = {
  removeAnalysis: (input) => invoke('RemoveVisualAnalysis', input),
  search: (input) =>
    invoke<import('../content-types').SourceContentResult>('SearchSourceContent', input),
};

export const shotIndexAPI: import('../shot-index-types').ShotIndexAPI = {
  get: (input) => invoke('GetShotIndex', input),
  start: (input) => invoke('StartShotIndex', input),
  cancel: (id) => invoke('CancelJob', id),
};

export const sourceFrameAPI: import('../source-frame-types').SourceFrameAPI = {
  read: (input) =>
    invoke<Awaited<ReturnType<import('../source-frame-types').SourceFrameAPI['read']>>>(
      'GetSourceFrame',
      input,
    ),
  cancel: (id) => invoke('CancelThumbnailRequest', id),
};

export const thumbnailAPI: ThumbnailAPI = {
  read: (request) => invoke<Thumbnail>('GetAssetThumbnail', request),
  cancel: (id) => invoke<unknown>('CancelThumbnailRequest', id),
};

export const waveformAPI: WaveformAPI = {
  read: (request) => invoke<WaveformTile>('GetWaveformTile', request),
  cancel: (id) => invoke<unknown>('CancelWaveformRequest', id),
};

function errorMessage(error: unknown): string {
  if (error instanceof Error) return error.message;
  if (typeof error === 'string') return error;
  if (error && typeof error === 'object' && 'message' in error) return String(error.message);
  return '操作未完成，请重试。';
}

// 生产环境只接受 Wails 桥；HTTP 仅供开发时联调同一 Go 服务。
export async function invoke<T>(method: string, ...args: unknown[]): Promise<T> {
  try {
    if (isDesktop()) {
      try {
        return (await callDesktop(method, args)) as T;
      } catch (error) {
        if (error instanceof ReferenceError)
          throw new Error('当前桌面接口尚未就绪，请重启更新后的 AIVidCut 再试。');
        throw error;
      }
    }
    if (!import.meta.env.DEV) throw new Error('桌面连接尚未就绪，请重新打开 AIVidCut。');
    const response = await fetch(`${import.meta.env.VITE_API_BASE || ''}/api/${method}`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ args }),
    });
    let payload: { result?: T; error?: unknown };
    try {
      payload = await response.json();
    } catch {
      throw new Error('无法连接本地剪辑服务。请启动 Go 开发服务后重试。');
    }
    if (!response.ok || payload.error)
      throw new Error(errorMessage(payload.error || `本地服务返回 ${response.status}`));
    if (!('result' in payload)) throw new Error('本地服务未返回有效结果。');
    return payload.result as T;
  } catch (error) {
    throw new Error(errorMessage(error));
  }
}

export const bridge = {
  clipboardStatus: () => invoke<ClipboardStatus>('GetClipboardStatus'),
  copySelection: (input: ClipboardSelectionInput) =>
    invoke<ClipboardResult>('CopyTimelineSelection', input),
  cutSelection: (input: ClipboardCutInput) =>
    invoke<ClipboardResult>('CutTimelineSelection', input),
  pasteSelection: (input: ClipboardPasteInput) =>
    invoke<ClipboardResult>('PasteTimelineSelection', input),

  bootstrap: () => invoke<Bootstrap>('GetBootstrap'),
  newProject: (name: string) => invoke<Bootstrap>('NewProject', name),
  newProjectWithSettings: (input: {
    name: string;
    settings: import('../types').Settings;
    projectId: string;
    projectRevision: number;
    sourceEpoch: number;
  }) => invoke<Bootstrap>('NewProjectWithSettings', input),
  openProject: () => invoke<Bootstrap>('OpenProject'),
  openProjectPath: (path: string) => invoke<Bootstrap>('OpenProjectPath', path),
  saveProject: () => invoke<Bootstrap>('SaveProject'),
  saveProjectAs: () => invoke<Bootstrap>('SaveProjectAs'),
  saveProjectTo: (path: string) => invoke<Bootstrap>('SaveProjectTo', path),
  importMedia: () => invoke<Bootstrap>('ImportMedia'),
  importPaths: (paths: string[]) => invoke<Bootstrap>('ImportPaths', paths),
  importLUT: () => invoke<Bootstrap>('ImportLUT'),
  importFont: () => invoke<Bootstrap>('ImportFont'),
  apply: (request: EditRequest) => invoke<Bootstrap>('ApplyEdit', request),
  applyScoped: (scope: import('../types').ProjectScope, request: EditRequest) =>
    invoke<Bootstrap>('ApplyScopedEdit', { scope, request }),
  addAudioToTimeline: (input: {
    projectId: string;
    projectRevision: number;
    assetId: string;
    clipId: string;
    startFrame: number;
    trackId?: string;
  }) => invoke<Bootstrap>('AddAudioToTimeline', input),
  undo: () => invoke<Bootstrap>('Undo'),
  redo: () => invoke<Bootstrap>('Redo'),
  exportVideo: (options: ExportOptions) => invoke<Job>('ExportVideo', options),
  jobs: () => invoke<Job[]>('ListJobs'),
  cancelJob: (id: string) => invoke<Job>('CancelJob', id),
  previewFrame: (frame: number) => invoke<PreviewFrame>('GetPreviewFrame', frame),
  confirmClose: (action: 'save' | 'discard' | 'cancel') => invoke<unknown>('ConfirmClose', action),
  frontendReady: () => invoke<unknown>('FrontendReady'),
  importCaptions: (path: string, trackID = '') =>
    invoke<Bootstrap>('ImportCaptions', path, trackID),
  exportCaptionsTo: (path: string, trackID = '') =>
    invoke<string>('ExportCaptionsTo', path, trackID),
  importSubtitle: () => invoke<Bootstrap>('ImportSubtitle'),
  exportSubtitles: () => invoke<string>('ExportSubtitles'),
  relinkAsset: (assetID: string) => invoke<Bootstrap>('RelinkAsset', assetID),
  relinkAssetPath: (assetID: string, path: string) =>
    invoke<Bootstrap>('RelinkAssetPath', assetID, path),
};

export const projectVersionsAPI: ProjectVersionsAPI = {
  list: (input) => invoke('ListProjectVersions', input),
  create: (input) => invoke('CreateProjectVersion', input),
  restore: (input) => invoke('RestoreProjectVersion', input),
};

export const projectCollectAPI: import('../project-collect-types').ProjectCollectAPI = {
  inspect: (scope) => invoke('InspectProjectCollection', scope),
  chooseDirectory: () => invoke('ChooseProjectCollectionDirectory'),
  start: (input) => invoke('CollectProject', input),
};

export const batchRelinkAPI: import('../batch-relink-types').BatchRelinkAPI = {
  inspect: (input) => invoke('InspectBatchRelink', input),
  preview: (input) => invoke('PreviewBatchRelink', input),
  apply: (input) => invoke('ApplyBatchRelink', input),
  cancel: (input) => invoke('CancelBatchRelink', input),
  chooseDirectory: () => invoke('ChooseBatchRelinkDirectory'),
};

export const sequenceAPI: import('../sequence-types').SequenceAPI = {
  change: (input) => invoke('ChangeSequence', input),
};

export const recoveryAPI: import('../recovery-types').RecoveryAPI = {
  status: () => invoke('GetRecoveryStatus'),
  list: (input) => invoke('ListProjectRecoveries', input),
  checkpoint: () => invoke('CheckpointRecovery'),
  restore: (input) => invoke('RestoreProjectRecovery', input),
};

export const snapshotStorageAPI: import('../snapshot-storage-types').SnapshotStorageAPI = {
  list: (input) => invoke('ListSnapshotStorage', input),
  change: (input) => invoke('ChangeSnapshotStorage', input),
};

export const generationBridge: GenerationAPI = {
  getConnections: () => invoke<ConnectionProfile[]>('GetConnections'),
  saveConnection: (input: SaveConnectionInput) =>
    invoke<ConnectionProfile>('SaveConnection', input),
  deleteConnection: (id: string) => invoke<{ warning?: string }>('DeleteConnection', id),
  getConnectionHealth: () =>
    invoke<{ pendingCleanup: number; storage: string }>('GetConnectionHealth'),
  cleanupCredentials: () => invoke<{ pendingCleanup: number }>('CleanupCredentials'),
  getGenerationCatalog: () => invoke<GenerationCapability[]>('GetGenerationCatalog'),
  submitGeneration: (input: SubmitGenerationInput) =>
    invoke<GenerationJob>('SubmitGeneration', input),
  listGenerationJobs: () => invoke<GenerationJob[]>('ListGenerationJobs'),
  syncGenerationJobs: (input) =>
    invoke<import('../generation-types').GenerationSync>('SyncGenerationJobs', input),
  listArchivedGenerationJobs: (input) =>
    invoke<import('../generation-types').GenerationArchivePage>(
      'ListArchivedGenerationJobs',
      input,
    ),
  archiveGenerationJob: (id) =>
    invoke<import('../generation-types').GenerationArchiveRecord>('ArchiveGenerationJob', id),
  restoreArchivedGenerationJob: (id) => invoke<GenerationJob>('RestoreArchivedGenerationJob', id),
  stopGenerationWaiting: (id: string) => invoke<GenerationJob>('StopGenerationWaiting', id),
  resumeGeneration: (id: string) => invoke<GenerationJob>('ResumeGeneration', id),
  importGenerationResult: (id: string) => invoke<Bootstrap>('ImportGenerationResult', id),
  importGenerationAssets: (id: string, assetIds: string[]) =>
    invoke<Bootstrap>('ImportGenerationAssets', id, assetIds),
  acknowledgeGenerationUnknown: (id: string) =>
    invoke<GenerationJob>('AcknowledgeGenerationUnknown', id),
};

export const agentBridge: AgentAPI = {
  getAgentStatus: () => invoke<AgentInstallation[]>('GetAgentStatus'),
  getAgentLoginInstructions: (provider: AgentProvider) =>
    invoke<AgentLoginCommand>('GetAgentLoginInstructions', provider),
  getAgentWorkflows: () => invoke<AgentWorkflow[]>('GetAgentWorkflows'),
  startAgentPlan: (input: AgentPlanInput) => invoke<AgentJob>('StartAgentPlan', input),
  listAgentPlans: () => invoke<AgentJob[]>('ListAgentPlans'),
  cancelAgentPlan: (id: string) => invoke<AgentJob>('CancelAgentPlan', id),
  applyAgentPlan: (id: string) => invoke<Bootstrap>('ApplyAgentPlan', id),
};

export const speechBridge: SpeechAPI = {
  getConnections: generationBridge.getConnections,
  saveConnection: generationBridge.saveConnection,
  deleteConnection: generationBridge.deleteConnection,
  getConnectionHealth: generationBridge.getConnectionHealth,
  cleanupCredentials: generationBridge.cleanupCredentials,
  getSpeechCatalog: () => invoke<SpeechCatalog>('GetSpeechCatalog'),
  listVoices: (connectionId: string, request: SpeechVoiceListRequest) =>
    invoke<SpeechVoicePage>('ListVoices', connectionId, request),
  startSpeechJob: (input: StartSpeechInput) => invoke<SpeechSummary>('StartSpeechJob', input),
  listSpeechJobs: () => invoke<SpeechSummary[]>('ListSpeechJobs'),
  getSpeechJob: (id: string) => invoke<SpeechJob>('GetSpeechJob', id),
  cancelSpeechJob: (id: string) => invoke<SpeechSummary>('CancelSpeechJob', id),
  resumeSpeechLocal: (id: string) => invoke<SpeechSummary>('ResumeSpeechLocal', id),
  acknowledgeSpeechUnknown: (id: string) => invoke<SpeechSummary>('AcknowledgeSpeechUnknown', id),
  acceptSpeechTranscript: (id, options) => invoke<Bootstrap>('AcceptSpeechTranscript', id, options),
  correctSourceTranscript: (input: SpeechTranscriptCorrection) =>
    invoke<Bootstrap>('CorrectSourceTranscript', input),
  importSpeechAudio: (id, options) => invoke<Bootstrap>('ImportSpeechAudio', id, options),
  detachCaption: (input) => invoke<Bootstrap>('DetachCaption', input),
  previewCaptionLayout: (input) => invoke<CaptionLayoutPreview>('PreviewCaptionLayout', input),
  applyCaptionLayout: (id) => invoke<Bootstrap>('ApplyCaptionLayout', id),
};
export const mcpBridge: MCPAPI = {
  acceptVisualAnalysis: (input) => invoke('AcceptMCPVisualAnalysis', input),
  getMCPStatus: () => invoke<MCPStatus>('GetMCPStatus'),
  startMCP: (input) => invoke<MCPStatus>('StartMCP', input),
  stopMCP: () => invoke<MCPStatus>('StopMCP'),
  getMCPConnection: () => invoke<MCPConnection>('GetMCPConnection'),
  listMCPRequests: () => invoke<MCPAction[]>('ListMCPRequests'),
  rejectMCPRequest: (id) => invoke<MCPAction>('RejectMCPRequest', id),
  approveMCPRequest: (id, confirmation) => invoke<MCPAction>('ApproveMCPRequest', id, confirmation),
};
export const workflowBridge: WorkflowAPI = {
  getWorkflowCatalog: () => invoke<WorkflowDefinition[]>('GetWorkflowCatalog'),
  getWorkflowPresets: () => invoke<WorkflowPreset[]>('GetWorkflowPresets'),
  saveWorkflowPreset: (input) => invoke<WorkflowPreset>('SaveWorkflowPreset', input),
  deleteWorkflowPreset: (id, baseVersion) =>
    invoke<unknown>('DeleteWorkflowPreset', id, baseVersion),
  previewWorkflow: (input) => invoke<WorkflowPreview>('PreviewWorkflow', input),
  applyWorkflowPreview: (id) => invoke<Bootstrap>('ApplyWorkflowPreview', id),
};
export const executionBridge: AgentExecutionAPI = {
  getAgentExecutionStatus: () => invoke<AgentInstallation[]>('GetAgentExecutionStatus'),
  startAgentExecution: (input) => invoke<AgentExecution>('StartAgentExecution', input),
  listAgentExecutions: () => invoke<AgentExecution[]>('ListAgentExecutions'),
  getAgentExecution: (id) => invoke<AgentExecution>('GetAgentExecution', id),
  cancelAgentExecution: (id) => invoke<AgentExecution>('CancelAgentExecution', id),
};
export const replacementBridge: ReplacementAPI = {
  previewGenerationReplacement: (input) =>
    invoke<GenerationReplacementPreview>('PreviewGenerationReplacement', input),
  applyGenerationReplacement: (id) => invoke<Bootstrap>('ApplyGenerationReplacement', id),
};
export const playbackBridge: PlaybackAPI = {
  getPlaybackCapabilities: () => invoke<PlaybackCapabilities>('GetPlaybackCapabilities'),
  startPlayback: (input) => invoke<PlaybackStart>('StartPlayback', input),
  controlPlayback: (input) => invoke<PlaybackStatus>('ControlPlayback', input),
  getPlaybackStatus: () => invoke<PlaybackStatus>('GetPlaybackStatus'),
  stopPlayback: (id) => invoke<PlaybackStatus>('StopPlayback', id),
};

export const inspectorDraftAPI: import('../inspector-draft-types').InspectorDraftAPI = {
  save: (input) => invoke('SaveInspectorDraft', input),
  list: () => invoke('ListInspectorDrafts'),
  remove: (input) => invoke('DeleteInspectorDraft', input),
  preview: (input) => invoke('PreviewInspectorDraft', input),
  apply: (id) => invoke('ApplyInspectorDraft', id),
  restoreBase: (input) => invoke('RestoreInspectorDraftBase', input),
};

export const resourceAPI: import('../resource-types').ResourceAPI = {
  state: () => invoke('GetResourceState'),
  status: () => invoke('GetResourceStatus'),
  start: (input) => invoke('StartResourceProfile', input),
  stop: (id) => invoke('StopResourceProfile', id),
  choosePath: () => invoke('ChooseResourceReportPath'),
  export: (input) => invoke('ExportResourceProfile', input),
};

export const mediaToolsAPI: import('../media-tools-types').MediaToolsAPI = {
  get: () => invoke('GetMediaTools'),
  inspect: (input) => invoke('InspectMediaTools', input),
  save: (id) => invoke('SaveMediaTools', id),
  choose: (kind) => invoke('ChooseMediaToolPath', kind),
};

export const sourceOCRBridge: import('../source-ocr-types').SourceOCRAPI = {
  capability: () => invoke('GetSourceOCRCapability'),
  prepare: (input) => invoke('PrepareSourceOCR', input),
  accept: (input) => invoke('AcceptSourceOCR', input),
  cancel: (id) => invoke('CancelSourceOCR', id),
};
