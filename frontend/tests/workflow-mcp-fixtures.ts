import { vi } from 'vitest';
import type {
  WorkflowAPI,
  WorkflowDefinition,
  WorkflowPreset,
  WorkflowPreview,
} from '../src/workflow-types';
import type { MCPAction, MCPAPI, MCPStatus } from '../src/mcp-types';
import { agentApplied, agentJob, agentProject } from './agent-fixtures';
import { configuredConnection } from './generation-fixtures';
export const workflowDefinition: WorkflowDefinition = {
  id: 'evidence-edit',
  name: '证据剪辑',
  description: '仅使用已接受源词证据。',
  version: '1.0',
  builtin: true,
  available: true,
  parameters: [
    {
      id: 'paddingFrames',
      name: '保留边距 · 帧',
      type: 'integer',
      required: true,
      min: 0,
      max: 30,
      step: 1,
      description: '保留帧数',
    },
    {
      id: 'gain',
      name: '音量系数',
      type: 'number',
      required: true,
      min: 0,
      max: 2,
      step: 0.1,
      description: '',
    },
    { id: 'label', name: '片段标记', type: 'string', required: true, description: '' },
    {
      id: 'style',
      name: '剪辑方式',
      type: 'select',
      required: true,
      description: '',
      options: [
        { value: 'gentle', name: '温和' },
        { value: 'tight', name: '紧凑' },
      ],
    },
    { id: 'preserve', name: '保留文字', type: 'boolean', required: true, description: '' },
    { id: 'asset', name: '现有素材', type: 'asset', required: false, description: '' },
    { id: 'track', name: '目标轨道', type: 'track', required: false, description: '' },
  ],
  defaults: { paddingFrames: 3, gain: 1, label: '候选', style: 'gentle', preserve: false },
};
export const unavailableWorkflow: WorkflowDefinition = {
  ...workflowDefinition,
  id: 'needs-transcript',
  name: '口播证据粗剪',
  available: false,
  reason: '需要已接受源转写；间隙不能当作静音证据。',
};
export const workflowPreset: WorkflowPreset = {
  id: 'preset-1',
  name: '我的紧凑参数',
  workflowId: workflowDefinition.id,
  workflowVersion: '1.0',
  version: 2,
  parameters: { ...workflowDefinition.defaults, paddingFrames: 8 },
  createdAt: '2026-10-25T00:00:00Z',
  updatedAt: '2026-10-25T00:00:00Z',
};
export const workflowPreview: WorkflowPreview = {
  id: 'preview-1',
  applied: false,
  workflowId: workflowDefinition.id,
  workflowVersion: workflowDefinition.version,
  projectId: agentProject.id,
  projectRevision: agentProject.revision,
  request: agentJob().result!.request,
  summary: '将标题移动到90帧',
  effects: [
    {
      kind: 'move',
      clipId: agentProject.clips[0].id,
      startFrame: 0,
      endFrame: 90,
      description: '移动已选标题',
    },
  ],
  evidence: [
    {
      kind: 'accepted_words',
      transcriptId: 'accepted-source',
      sourceStartUs: 100,
      sourceEndUs: 800,
      startFrame: 0,
      endFrame: 3,
      description: '已接受源词证据',
    },
  ],
  warnings: [],
  affectedClipIds: [agentProject.clips[0].id],
  savedFrames: 15,
  createdAt: '2026-10-25T00:00:00Z',
};
export function fakeWorkflowAPI() {
  return {
    getWorkflowCatalog: vi
      .fn<WorkflowAPI['getWorkflowCatalog']>()
      .mockResolvedValue([workflowDefinition, unavailableWorkflow]),
    getWorkflowPresets: vi
      .fn<WorkflowAPI['getWorkflowPresets']>()
      .mockResolvedValue([workflowPreset]),
    saveWorkflowPreset: vi
      .fn<WorkflowAPI['saveWorkflowPreset']>()
      .mockResolvedValue({ ...workflowPreset, version: 3 }),
    deleteWorkflowPreset: vi.fn<WorkflowAPI['deleteWorkflowPreset']>().mockResolvedValue(null),
    previewWorkflow: vi.fn<WorkflowAPI['previewWorkflow']>().mockResolvedValue(workflowPreview),
    applyWorkflowPreview: vi
      .fn<WorkflowAPI['applyWorkflowPreview']>()
      .mockResolvedValue(agentApplied),
  };
}
export const stoppedMCP: MCPStatus = {
  running: false,
  projectRevision: agentProject.revision,
  allowEdits: false,
  scopeValid: false,
  audit: [],
};
export const runningMCP: MCPStatus = {
  running: true,
  projectId: agentProject.id,
  projectRevision: agentProject.revision,
  allowEdits: false,
  scopeValid: true,
  address: 'http://127.0.0.1:40123',
  audit: [],
};
export function mcpAction(overrides: Partial<MCPAction> = {}): MCPAction {
  return {
    id: 'prepared-action',
    projectId: agentProject.id,
    projectRevision: agentProject.revision,
    kind: 'generation',
    status: 'pending',
    canApprove: true,
    message: '等待用户核对，没有收费调用',
    connectionId: configuredConnection.id,
    generation: {
      id: 'gen-request',
      providerId: 'openai',
      modelId: configuredConnection.modelId,
      mode: 'image.generate',
      prompt: '补充城市远景',
      count: 1,
    },
    createdAt: '2026-10-25T00:00:00Z',
    updatedAt: '2026-10-25T00:00:00Z',
    ...overrides,
  };
}
export function fakeMCPAPI() {
  return {
    getMCPStatus: vi.fn<MCPAPI['getMCPStatus']>().mockResolvedValue(stoppedMCP),
    startMCP: vi.fn<MCPAPI['startMCP']>().mockResolvedValue(runningMCP),
    stopMCP: vi.fn<MCPAPI['stopMCP']>().mockResolvedValue(stoppedMCP),
    getMCPConnection: vi.fn<MCPAPI['getMCPConnection']>().mockResolvedValue({
      url: 'http://127.0.0.1:40123/mcp',
      transport: 'streamable-http',
      httpHeaders: { Authorization: 'Bearer fixture-only-test-token' },
    }),
    listMCPRequests: vi.fn<MCPAPI['listMCPRequests']>().mockResolvedValue([]),
    rejectMCPRequest: vi
      .fn<MCPAPI['rejectMCPRequest']>()
      .mockResolvedValue(mcpAction({ status: 'rejected' })),
    approveMCPRequest: vi
      .fn<MCPAPI['approveMCPRequest']>()
      .mockResolvedValue(mcpAction({ status: 'started', jobId: 'real-service-job' })),
  };
}
