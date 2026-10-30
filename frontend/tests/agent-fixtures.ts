import { vi } from 'vitest';
import type {
  AgentAPI,
  AgentInstallation,
  AgentJob,
  AgentLoginCommand,
  AgentWorkflow,
} from '../src/agent-types';
import type { Bootstrap, Project } from '../src/types';
import { defaultClip } from '../src/lib/editor';

const settings = { width: 1920, height: 1080, fpsNum: 30, fpsDen: 1, sampleRate: 48000 };
export const agentClip = {
  ...defaultClip('text', 'text-track', 0, settings),
  id: 'clip-selected',
  name: '开场标题',
  text: '开场',
};
export const agentProject: Project = {
  id: 'agent-project',
  name: 'Agent 测试工程',
  schemaVersion: 1,
  revision: 7,
  settings,
  assets: [],
  tracks: [
    { id: 'text-track', name: '文字 1', kind: 'text', locked: false, muted: false, hidden: false },
  ],
  clips: [agentClip],
};
export const readyAgent: AgentInstallation = {
  provider: 'codex',
  path: '/fixture/codex',
  installed: true,
  version: '0.164.0',
  authState: 'signed_in',
  authReason: '测试作用域已登录',
  profileDirectory: '/fixture/private/codex',
  isolation: {
    supported: true,
    status: 'ready',
    reason: 'fixture 隔离检查通过',
    checks: [{ id: 'tools', status: 'pass', reason: '工具面已核对' }],
    missingFlags: [],
    evidence: ['fixture 检查证据'],
    limitations: ['测试不代表真实 CLI 账号验收'],
  },
};
export const agentLogin: AgentLoginCommand = {
  executable: '/fixture/bin/codex',
  args: ['login'],
  environment: { CODEX_HOME: '/fixture/private/codex' },
  notice: '测试只展示登录方式，不执行。',
};
export const agentWorkflows: AgentWorkflow[] = [
  {
    id: 'manual-plan',
    name: '剪辑计划',
    description: '根据明确要求返回编辑计划。',
    available: true,
    reason: '',
  },
  {
    id: 'talking-head',
    name: '口播粗剪',
    description: '根据已接受转写分析粗剪。',
    available: false,
    reason: '需要源转写和停顿证据，不可猜测区间。',
  },
];
export function agentJob(overrides: Partial<AgentJob> = {}): AgentJob {
  return {
    id: 'agent-job',
    provider: 'codex',
    projectId: agentProject.id,
    projectRevision: agentProject.revision,
    status: 'succeeded',
    message: '候选计划已通过副本校验',
    createdAt: '2026-10-25T00:00:00Z',
    updatedAt: '2026-10-25T00:00:00Z',
    applied: false,
    result: {
      provider: 'codex',
      request: {
        baseRevision: agentProject.revision,
        idempotencyKey: 'plan-key',
        label: '移动标题',
        commands: [{ type: 'update_clip', clip: { ...agentClip, startFrame: 90 } }],
      },
      coreValidated: true,
      warnings: ['仅在工程副本校验，尚未应用'],
    },
    ...overrides,
  };
}
export const agentApplied: Bootstrap = {
  project: { ...agentProject, revision: 8, clips: [{ ...agentClip, startFrame: 90 }] },
  dirty: true,
  canUndo: true,
  canRedo: false,
  projectPath: '',
  capabilities: {},
};
export function fakeAgentAPI() {
  return {
    getAgentStatus: vi.fn<AgentAPI['getAgentStatus']>().mockResolvedValue([readyAgent]),
    getAgentLoginInstructions: vi
      .fn<AgentAPI['getAgentLoginInstructions']>()
      .mockResolvedValue(agentLogin),
    getAgentWorkflows: vi.fn<AgentAPI['getAgentWorkflows']>().mockResolvedValue(agentWorkflows),
    startAgentPlan: vi
      .fn<AgentAPI['startAgentPlan']>()
      .mockRejectedValue(new Error('测试禁止真实 CLI 推理')),
    listAgentPlans: vi.fn<AgentAPI['listAgentPlans']>().mockResolvedValue([]),
    cancelAgentPlan: vi
      .fn<AgentAPI['cancelAgentPlan']>()
      .mockResolvedValue(agentJob({ status: 'cancelled', result: undefined })),
    applyAgentPlan: vi.fn<AgentAPI['applyAgentPlan']>().mockResolvedValue(agentApplied),
  };
}
