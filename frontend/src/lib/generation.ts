import type { Asset, Project } from '../types';
import type {
  ConnectionProfile,
  GenerationCapability,
  GenerationJob,
  GenerationReference,
  GenerationRequest,
} from '../generation-types';

export const providerNames: Record<string, string> = {
  openai: 'OpenAI',
  google: 'Google',
  minimax: 'MiniMax',
  seedance: 'Seedance · 火山方舟北京区',
  elevenlabs: 'ElevenLabs',
};
export const modeNames: Record<string, string> = {
  'image.generate': '文字生成图片',
  'image.edit': '参考图片修改',
  'video.text': '文字生成视频',
  'video.image': '首尾帧生成视频',
  'video.reference': '参考图片生成视频',
};
export const generationStatus: Record<GenerationJob['status'], string> = {
  queued: '排队中',
  submitting: '正在提交',
  running: '生成中',
  waiting: '等待查询',
  importing: '正在保存结果',
  succeeded: '结果已保存',
  failed: '失败',
  unknown: '提交结果未知',
  cancelled: '已停止本地等待',
};
export const jobCanArchive = (job: GenerationJob) =>
  !job.canResume &&
  (['succeeded', 'failed', 'cancelled'].includes(job.status) ||
    (job.status === 'unknown' && job.acknowledged === true));
const referenceRoleNames: Record<string, string> = {
  first_frame: '首帧',
  last_frame: '尾帧',
  reference_image: '参考图',
};
export const referenceRoleName = (role: string) => referenceRoleNames[role] || role;

export interface GenerationDraft {
  mode: string;
  prompt: string;
  size: string;
  aspectRatio: string;
  quality: string;
  outputFormat: string;
  count: string;
  durationSeconds: string;
  references: GenerationReference[];
  generateAudio?: boolean;
  parentRecipeId?: string;
  background?: string;
  referenceFingerprints?: Record<string, string>;
  preserveDefaults?: boolean;
}

export const eligibleReferenceImages = (assets: Asset[]) =>
  assets.filter(
    (asset) =>
      asset.kind === 'image' && !asset.missing && /\.(png|jpe?g)$/i.test(asset.path || asset.name),
  );
export function generationConnections(
  profiles: ConnectionProfile[],
  catalog: GenerationCapability[],
) {
  const providers = new Set(catalog.map((capability) => capability.providerId));
  return profiles.filter((profile) => providers.has(profile.providerId));
}
export const findCapability = (
  profile: ConnectionProfile | undefined,
  catalog: GenerationCapability[],
) =>
  profile
    ? catalog.find(
        (capability) =>
          capability.providerId === profile.providerId && capability.modelId === profile.modelId,
      )
    : undefined;
export const canGenerate = (capability: GenerationCapability | undefined) =>
  Boolean(capability && !capability.needsModelConfiguration && capability.modes.length);

function gcd(a: number, b: number): number {
  return b ? gcd(b, a % b) : a;
}
export function sizeAspect(size: string): string | undefined {
  const match = /^(\d+)x(\d+)$/.exec(size);
  if (!match) return undefined;
  const width = Number(match[1]);
  const height = Number(match[2]);
  const divisor = gcd(width, height);
  return divisor ? `${width / divisor}:${height / divisor}` : undefined;
}

export function allowedAspects(capability: GenerationCapability, mode: string): string[] {
  const values = capability.aspectRatios || [];
  if (capability.modeAspectRatios?.[mode])
    return values.filter((value) => capability.modeAspectRatios![mode].includes(value));
  if (capability.providerId !== 'minimax') return values;
  if (mode === 'video.image') return values.filter((value) => value === 'adaptive');
  if (mode === 'video.text' || capability.modelId === 'MiniMax-H3-Lite')
    return values.filter((value) => value !== 'adaptive');
  return values;
}

export function defaultGenerationDraft(
  capability: GenerationCapability | undefined,
  project: Project,
  prompt = '',
  requestedMode?: string,
): GenerationDraft {
  const mode = requestedMode || capability?.modes[0] || '';
  const aspects = capability ? allowedAspects(capability, mode) : [];
  const projectAspect = sizeAspect(`${project.settings.width}x${project.settings.height}`);
  const size =
    capability?.defaultSize && capability.sizes?.includes(capability.defaultSize)
      ? capability.defaultSize
      : capability?.mediaKind === 'video' && capability.sizes?.includes('768P')
        ? '768P'
        : capability?.mediaKind === 'image'
          ? capability.sizes?.find((value) => sizeAspect(value) === projectAspect) ||
            capability.sizes?.[0] ||
            ''
          : capability?.sizes?.[0] || '';
  return {
    mode,
    prompt,
    size,
    aspectRatio: aspects.includes(projectAspect || '') ? projectAspect! : aspects[0] || '',
    quality: capability?.qualities?.includes('auto') ? 'auto' : capability?.qualities?.[0] || '',
    outputFormat: capability?.outputFormats?.includes('png')
      ? 'png'
      : capability?.outputFormats?.[0] || '',
    count: '1',
    durationSeconds:
      capability?.mediaKind === 'video'
        ? String(
            Math.max(
              capability.minDurationSeconds || 1,
              Math.min(5, capability.maxDurationSeconds || 5),
            ),
          )
        : '',
    generateAudio: capability?.supportsAudioGeneration
      ? Boolean(capability.defaultGenerateAudio)
      : undefined,
    references: [],
  };
}

export function makeGenerationRequest(
  id: string,
  profile: ConnectionProfile,
  capability: GenerationCapability,
  draft: GenerationDraft,
): GenerationRequest {
  const request: GenerationRequest = {
    id,
    providerId: profile.providerId,
    modelId: profile.modelId,
    mode: draft.mode,
    prompt: draft.prompt.trim(),
    count: Number(draft.count),
  };
  if (capability.sizes?.length && draft.size) request.size = draft.size;
  const aspect =
    capability.mediaKind === 'image' && capability.sizes?.length
      ? sizeAspect(draft.size) || (draft.preserveDefaults ? draft.aspectRatio : undefined)
      : draft.aspectRatio;
  if (aspect && capability.aspectRatios?.includes(aspect)) request.aspectRatio = aspect;
  if (capability.qualities?.length && draft.quality) request.quality = draft.quality;
  if (capability.outputFormats?.length && draft.outputFormat)
    request.outputFormat = draft.outputFormat;
  if (capability.mediaKind === 'video') request.durationSeconds = Number(draft.durationSeconds);
  if (capability.supportsAudioGeneration) request.generateAudio = Boolean(draft.generateAudio);
  if (draft.references.length)
    request.references = draft.references.map((reference) => ({ ...reference }));
  if (draft.parentRecipeId) request.parentRecipeId = draft.parentRecipeId;
  if (draft.background) request.background = draft.background;
  return request;
}

export function validateGenerationDraft(
  profile: ConnectionProfile | undefined,
  capability: GenerationCapability | undefined,
  draft: GenerationDraft,
  assets: Asset[],
): Record<string, string> {
  const errors: Record<string, string> = {};
  if (!profile?.configured) errors.connection = '请先保存已配置凭据的模型连接。';
  if (!canGenerate(capability))
    errors.connection = capability?.note || '当前连接模型没有可用的能力声明。';
  if (!capability || !profile) return errors;
  if (!capability.modes.includes(draft.mode)) errors.mode = '请选择此模型支持的任务。';
  if (!draft.prompt.trim()) errors.prompt = '请描述要生成的内容。';
  const count = Number(draft.count);
  if (
    !draft.count.trim() ||
    !Number.isInteger(count) ||
    count < 1 ||
    count > Math.max(1, capability.maxCount)
  )
    errors.count = `数量需要为 1–${Math.max(1, capability.maxCount)} 的整数。`;
  if (
    capability.sizes?.length &&
    !(draft.preserveDefaults && !draft.size) &&
    !capability.sizes.includes(draft.size)
  )
    errors.size = '请选择模型支持的尺寸。';
  if (
    capability.qualities?.length &&
    !(draft.preserveDefaults && !draft.quality) &&
    !capability.qualities.includes(draft.quality)
  )
    errors.quality = '请选择模型支持的质量。';
  if (
    capability.outputFormats?.length &&
    !(draft.preserveDefaults && !draft.outputFormat) &&
    !capability.outputFormats.includes(draft.outputFormat)
  )
    errors.outputFormat = '请选择模型支持的格式。';
  if (capability.mediaKind === 'video') {
    const duration = Number(draft.durationSeconds);
    if (
      !draft.durationSeconds.trim() ||
      !Number.isInteger(duration) ||
      duration < (capability.minDurationSeconds || 1) ||
      duration > (capability.maxDurationSeconds || 0)
    )
      errors.durationSeconds = '时长不在此模型声明的整数范围内。';
  }
  if (capability.mediaKind === 'video' || !capability.sizes?.length) {
    const aspects = allowedAspects(capability, draft.mode);
    if (
      aspects.length &&
      !(draft.preserveDefaults && !draft.aspectRatio) &&
      !aspects.includes(draft.aspectRatio)
    )
      errors.aspectRatio = '当前任务与画幅组合不受支持。';
  }
  const references = draft.references;
  const eligible = new Set(eligibleReferenceImages(assets).map((asset) => asset.id));
  if (references.some((reference) => !eligible.has(reference.assetId)))
    errors.references = '参考图必须是当前工程中可用的 PNG/JPEG 素材。';
  if (
    references.some(
      (ref) =>
        draft.referenceFingerprints?.[ref.assetId] &&
        draft.referenceFingerprints[ref.assetId] !==
          assets.find((asset) => asset.id === ref.assetId)?.fingerprint,
    )
  )
    errors.references = '原参考图片已改变或缺少来源证明，请移除后重新选择并核对。';
  if (draft.mode === 'image.generate' || draft.mode === 'video.text') {
    if (references.length) errors.references = '文字生成模式不接收参考图片。';
  } else if (draft.mode === 'video.image') {
    if (
      !references.length ||
      references.some((reference) => !['first_frame', 'last_frame'].includes(reference.role)) ||
      references.filter((reference) => reference.role === 'first_frame').length > 1 ||
      references.filter((reference) => reference.role === 'last_frame').length > 1
    )
      errors.references = '此模式需要首帧或尾帧，且不能混入其他参考用途。';
    if (
      capability.requiresFirstFrame &&
      !references.some((reference) => reference.role === 'first_frame')
    )
      errors.references = '此模型必须选择一张首帧，尾帧可选。';
  } else {
    if (
      !references.length ||
      references.length > capability.maxReferences ||
      references.some((reference) => reference.role !== 'reference_image')
    )
      errors.references = `此模式需要 1–${capability.maxReferences} 张参考图片。`;
  }
  return errors;
}

export function jobCanResume(job: GenerationJob): boolean {
  return job.canResume && !(job.status === 'unknown' && !job.remoteId);
}
export function requestSignature(connectionId: string, request: GenerationRequest): string {
  const { id: _id, ...payload } = request;
  return JSON.stringify({
    connectionId,
    request: canonical({ ...payload, count: payload.count ?? 1 }),
  });
}
function canonical(value: unknown): unknown {
  if (Array.isArray(value)) return value.map(canonical);
  if (value && typeof value === 'object')
    return Object.fromEntries(
      Object.entries(value)
        .filter(([, v]) => v !== undefined)
        .sort(([a], [b]) => a.localeCompare(b))
        .map(([key, v]) => [key, canonical(v)]),
    );
  return value;
}
export function generationContentSignature(connectionId: string, request: GenerationRequest) {
  const { parentRecipeId: _parent, ...payload } = request;
  return requestSignature(connectionId, payload);
}

export function draftFromGenerationRequest(
  request: GenerationRequest,
  proofs: Record<string, string>,
): GenerationDraft {
  return {
    mode: request.mode,
    prompt: request.prompt,
    size: request.size || '',
    aspectRatio: request.aspectRatio || '',
    quality: request.quality || '',
    outputFormat: request.outputFormat || '',
    count: String(request.count ?? 1),
    durationSeconds: request.durationSeconds ? String(request.durationSeconds) : '',
    generateAudio: request.generateAudio,
    background: request.background,
    parentRecipeId: request.parentRecipeId,
    references: (request.references || []).map((ref) => ({ ...ref })),
    referenceFingerprints: { ...proofs },
    preserveDefaults: true,
  };
}
export function safeConnectionError(cause: unknown, secret = ''): string {
  const message = cause instanceof Error ? cause.message : '连接操作未完成，请重试。';
  return [...new Set([secret, secret.trim()])]
    .filter(Boolean)
    .reduce((text, value) => text.split(value).join('（密钥已隐藏）'), message);
}
