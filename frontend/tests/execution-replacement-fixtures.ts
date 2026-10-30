import { vi } from 'vitest';
import type { AgentExecution, AgentExecutionAPI } from '../src/agent-execution-types';
import type { GenerationReplacementPreview, ReplacementAPI } from '../src/replacement-types';
import type { Asset, Bootstrap, Project } from '../src/types';
import type { GenerationOutputAsset } from '../src/generation-types';
import { defaultClip } from '../src/lib/editor';
import { readyAgent } from './agent-fixtures';
import { generationJob, generationProject } from './generation-fixtures';
export const executionInstallation = {
  ...readyAgent,
  isolation: {
    ...readyAgent.isolation,
    status: 'execution_ready',
    reason: '第一方MCP执行诊断通过',
  },
};
export const originalAsset: Asset = {
  ...generationProject.assets[2],
  id: 'original-source',
  name: '原口播.mp4',
  durationUs: 10000000,
  hasAudio: true,
  url: 'http://127.0.0.1:43111/media/original-fixture',
};
export const originalClip = {
  ...defaultClip('video', 'video-track', 90, generationProject.settings, originalAsset),
  id: 'original-shot',
  sourceInUs: 1200000,
  durationFrames: 60,
  speedNum: 2,
  speedDen: 1,
  transform: { x: 12, y: -5, scale: 0.8, rotation: 0 },
};
export const replacementProject: Project = {
  ...generationProject,
  id: 'replace-project',
  revision: 7,
  assets: [originalAsset],
  tracks: [
    {
      id: 'video-track',
      name: '视频 1',
      kind: 'video',
      locked: false,
      muted: false,
      hidden: false,
    },
  ],
  clips: [originalClip],
};
export const candidateOutput: GenerationOutputAsset = {
  id: 'candidate-1',
  kind: 'video',
  name: '候选画面.mp4',
  path: '/saved/candidate.mp4',
  url: 'http://127.0.0.1:43111/media/candidate-fixture',
  mime: 'video/mp4',
  sha256: 'fixture-candidate-hash',
  bytes: 1024,
  recipeId: 'recipe',
};
export const candidateAsset: Asset = {
  ...originalAsset,
  id: candidateOutput.id,
  name: candidateOutput.name,
  path: candidateOutput.path,
  url: candidateOutput.url,
  durationUs: 8000000,
  hasAudio: true,
};
export const candidateJob = generationJob({
  projectId: replacementProject.id,
  projectRevision: 7,
  status: 'succeeded',
  assets: [candidateOutput],
});
export const replacementPreview: GenerationReplacementPreview = {
  id: 'replacement-preview',
  projectId: replacementProject.id,
  projectRevision: 7,
  jobId: candidateJob.id,
  candidateId: candidateOutput.id,
  clipId: originalClip.id,
  createdAt: '2026-10-25T00:00:00Z',
  applied: false,
  summary: '保留时间线范围并替换画面，原声保留',
  effects: [
    { kind: 'replace', clipId: originalClip.id, description: '画面替换，片段ID和时序保持' },
    { kind: 'audio_carrier', description: '真实原声使用同源载体保留，字幕人工覆盖保留' },
  ],
  warnings: ['浏览器对照不代表最终LUT合成'],
  affectedClipIds: [originalClip.id],
  originalClip,
  originalAsset,
  candidateAsset,
  replacementClip: { ...originalClip, assetId: candidateAsset.id, sourceInUs: 0 },
  audioMode: 'keep_original',
  lutMode: 'keep',
  speedMode: 'preserve',
};
export const replacedBootstrap: Bootstrap = {
  project: {
    ...replacementProject,
    revision: 8,
    assets: [originalAsset, candidateAsset],
    clips: [replacementPreview.replacementClip],
  },
  projectPath: '',
  dirty: true,
  canUndo: true,
  canRedo: false,
  capabilities: {},
};
export function fakeReplacementAPI() {
  return {
    previewGenerationReplacement: vi
      .fn<ReplacementAPI['previewGenerationReplacement']>()
      .mockResolvedValue(replacementPreview),
    applyGenerationReplacement: vi
      .fn<ReplacementAPI['applyGenerationReplacement']>()
      .mockResolvedValue(replacedBootstrap),
  };
}
export function executionJob(overrides: Partial<AgentExecution> = {}): AgentExecution {
  return {
    id: 'execution-1',
    provider: 'codex',
    projectId: replacementProject.id,
    projectRevision: 7,
    endRevision: 7,
    allowEdits: true,
    selection: [originalClip.id],
    workflow: 'manual-plan',
    status: 'running',
    stage: 'tools',
    message: '通过第一方工具执行中',
    createdAt: '2026-10-25T00:00:00Z',
    updatedAt: '2026-10-25T00:00:00Z',
    warnings: [],
    ...overrides,
  };
}
export function fakeExecutionAPI() {
  return {
    getAgentExecutionStatus: vi
      .fn<AgentExecutionAPI['getAgentExecutionStatus']>()
      .mockResolvedValue([executionInstallation]),
    startAgentExecution: vi
      .fn<AgentExecutionAPI['startAgentExecution']>()
      .mockResolvedValue(executionJob()),
    listAgentExecutions: vi.fn<AgentExecutionAPI['listAgentExecutions']>().mockResolvedValue([]),
    getAgentExecution: vi.fn<AgentExecutionAPI['getAgentExecution']>().mockResolvedValue(
      executionJob({
        endRevision: 8,
        audit: [
          {
            id: 'audit-1',
            tool: 'aividcut_apply_edit',
            success: true,
            revision: 8,
            createdAt: '2026-10-25T00:00:01Z',
          },
        ],
      }),
    ),
    cancelAgentExecution: vi.fn<AgentExecutionAPI['cancelAgentExecution']>().mockResolvedValue(
      executionJob({
        status: 'cancelled',
        endRevision: 8,
        message: '后续执行已取消，已完成编辑保留',
      }),
    ),
  };
}
