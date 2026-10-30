import { vi } from 'vitest';
import type {
  ConnectionProfile,
  GenerationAPI,
  GenerationCapability,
  GenerationJob,
} from '../src/generation-types';
import type { Bootstrap, Project } from '../src/types';

export const generationProject: Project = {
  id: 'project-generation',
  schemaVersion: 1,
  revision: 0,
  name: '生成测试工程',
  settings: { width: 1920, height: 1080, fpsNum: 30, fpsDen: 1, sampleRate: 48000 },
  tracks: [],
  clips: [],
  assets: [
    {
      id: 'png',
      name: '参考图.png',
      kind: 'image',
      path: '/registered/ref.png',
      width: 1024,
      height: 1024,
      fpsNum: 0,
      fpsDen: 1,
      durationUs: 0,
      hasAudio: false,
      missing: false,
    },
    {
      id: 'jpeg',
      name: '产品.jpg',
      kind: 'image',
      path: '/registered/product.jpg',
      width: 1024,
      height: 1024,
      fpsNum: 0,
      fpsDen: 1,
      durationUs: 0,
      hasAudio: false,
      missing: false,
    },
    {
      id: 'video',
      name: '镜头.mp4',
      kind: 'video',
      path: '/registered/clip.mp4',
      width: 1920,
      height: 1080,
      fpsNum: 30,
      fpsDen: 1,
      durationUs: 1000000,
      hasAudio: false,
      missing: false,
    },
  ],
};
export const configuredConnection: ConnectionProfile = {
  id: 'connection-test',
  providerId: 'openai',
  name: '我的图片连接',
  modelId: 'gpt-image-2.5-sunburst',
  configured: true,
  createdAt: '2026-10-25T00:00:00Z',
  updatedAt: '2026-10-25T00:00:00Z',
};
export const openAICapability: GenerationCapability = {
  providerId: 'openai',
  family: 'GPT Image',
  modelId: configuredConnection.modelId,
  mediaKind: 'image',
  modes: ['image.generate', 'image.edit'],
  sizes: ['1024x1024', '1536x1024', '1536x864'],
  aspectRatios: ['1:1', '3:2', '16:9'],
  qualities: ['auto', 'high'],
  outputFormats: ['png', 'jpeg'],
  maxReferences: 16,
  maxCount: 10,
  async: false,
  supportsCancel: false,
  needsModelConfiguration: false,
  accountVerified: false,
  note: '账号未验证',
  sourceUrl: 'https://example.invalid/docs',
};
export const h3Capability: GenerationCapability = {
  providerId: 'minimax',
  family: 'MiniMax H3',
  modelId: 'MiniMax-H3',
  mediaKind: 'video',
  modes: ['video.text', 'video.image', 'video.reference'],
  sizes: ['768P', '2K'],
  aspectRatios: ['16:9', '9:16', 'adaptive'],
  maxReferences: 9,
  maxCount: 1,
  minDurationSeconds: 4,
  maxDurationSeconds: 15,
  async: true,
  supportsCancel: false,
  needsModelConfiguration: false,
  accountVerified: false,
  note: '账号未验证',
  sourceUrl: 'https://example.invalid/docs',
};
export const seedanceCapability: GenerationCapability = {
  providerId: 'seedance',
  family: 'Seedance 2.5 · 火山方舟北京区',
  modelId: 'doubao-seedance-2-5-260628',
  mediaKind: 'video',
  modes: ['video.text', 'video.image', 'video.reference'],
  sizes: ['480p', '720p', '1080p'],
  defaultSize: '720p',
  aspectRatios: ['16:9', '9:16', 'adaptive'],
  modeAspectRatios: { 'video.image': ['adaptive'] },
  requiresFirstFrame: true,
  supportsAudioGeneration: true,
  defaultGenerateAudio: true,
  outputFormats: ['mp4'],
  maxReferences: 30,
  maxCount: 1,
  minDurationSeconds: 4,
  maxDurationSeconds: 30,
  async: true,
  supportsCancel: false,
  needsModelConfiguration: false,
  accountVerified: false,
  note: '火山方舟北京区标准 API，账户未验证',
  sourceUrl:
    'https://console.volcengine.com/ark/region:cn-beijing/docs/create-video-generation-task-api',
};
export const generationBootstrap: Bootstrap = {
  project: generationProject,
  projectPath: '',
  dirty: false,
  canUndo: false,
  canRedo: false,
  capabilities: {},
};
export function generationJob(overrides: Partial<GenerationJob> = {}): GenerationJob {
  return {
    id: 'generation-job',
    connectionId: configuredConnection.id,
    projectId: generationProject.id,
    projectRevision: 0,
    request: {
      id: 'request-test',
      providerId: 'openai',
      modelId: configuredConnection.modelId,
      mode: 'image.generate',
      prompt: '城市夜景',
      count: 1,
      size: '1536x864',
      aspectRatio: '16:9',
      quality: 'auto',
      outputFormat: 'png',
    },
    status: 'queued',
    message: '等待处理',
    createdAt: '2026-10-25T00:00:00Z',
    updatedAt: '2026-10-25T00:00:00Z',
    canResume: false,
    assets: [],
    imported: false,
    ...overrides,
  };
}
export function fakeGenerationAPI() {
  const api = {
    getConnections: vi
      .fn<GenerationAPI['getConnections']>()
      .mockResolvedValue([configuredConnection]),
    saveConnection: vi
      .fn<GenerationAPI['saveConnection']>()
      .mockResolvedValue(configuredConnection),
    deleteConnection: vi.fn<GenerationAPI['deleteConnection']>().mockResolvedValue({}),
    getConnectionHealth: vi
      .fn<GenerationAPI['getConnectionHealth']>()
      .mockResolvedValue({ pendingCleanup: 0, storage: 'mock' }),
    cleanupCredentials: vi
      .fn<GenerationAPI['cleanupCredentials']>()
      .mockResolvedValue({ pendingCleanup: 0 }),
    getGenerationCatalog: vi
      .fn<GenerationAPI['getGenerationCatalog']>()
      .mockResolvedValue([openAICapability, h3Capability, seedanceCapability]),
    submitGeneration: vi
      .fn<GenerationAPI['submitGeneration']>()
      .mockRejectedValue(new Error('测试不允许真实生成')),
    listGenerationJobs: vi.fn<GenerationAPI['listGenerationJobs']>().mockResolvedValue([]),
    listArchivedGenerationJobs: vi
      .fn<GenerationAPI['listArchivedGenerationJobs']>()
      .mockResolvedValue({ items: [], total: 0, active: 0, offset: 0, limit: 20 }),
    archiveGenerationJob: vi
      .fn<GenerationAPI['archiveGenerationJob']>()
      .mockRejectedValue(new Error('未配置归档测试记录')),
    restoreArchivedGenerationJob: vi
      .fn<GenerationAPI['restoreArchivedGenerationJob']>()
      .mockRejectedValue(new Error('未配置归档测试记录')),
    stopGenerationWaiting: vi
      .fn<GenerationAPI['stopGenerationWaiting']>()
      .mockResolvedValue(generationJob({ status: 'cancelled' })),
    resumeGeneration: vi.fn<GenerationAPI['resumeGeneration']>().mockResolvedValue(generationJob()),
    importGenerationResult: vi
      .fn<GenerationAPI['importGenerationResult']>()
      .mockResolvedValue(generationBootstrap),
    importGenerationAssets: vi
      .fn<GenerationAPI['importGenerationAssets']>()
      .mockResolvedValue(generationBootstrap),
    acknowledgeGenerationUnknown: vi
      .fn<GenerationAPI['acknowledgeGenerationUnknown']>()
      .mockResolvedValue(generationJob({ status: 'unknown', acknowledged: true })),
  };
  return Object.assign(api, {
    syncGenerationJobs: vi
      .fn<GenerationAPI['syncGenerationJobs']>()
      .mockImplementation(async () => ({
        fingerprint: 'a'.repeat(64),
        changed: true,
        jobs: (await api.listGenerationJobs()) || [],
      })),
  });
}
