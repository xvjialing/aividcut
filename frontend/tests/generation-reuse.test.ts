import { expect, it } from 'vitest';
import {
  draftFromGenerationRequest,
  makeGenerationRequest,
  requestSignature,
  generationContentSignature,
  validateGenerationDraft,
} from '../src/lib/generation';
import {
  configuredConnection,
  openAICapability,
  h3Capability,
  seedanceCapability,
  generationProject,
} from './generation-fixtures';
import type { GenerationCapability, GenerationRequest } from '../src/generation-types';

it('四类能力参数往返保留声音false、背景、角色与父配方', () => {
  const google: GenerationCapability = {
    ...openAICapability,
    providerId: 'google',
    modelId: 'gemini-2.5-flash-image',
    sizes: undefined,
    qualities: undefined,
    outputFormats: undefined,
    maxCount: 1,
  };
  const cases: Array<[GenerationCapability, Partial<GenerationRequest>]> = [
    [
      openAICapability,
      {
        mode: 'image.edit',
        size: '1536x864',
        aspectRatio: '16:9',
        quality: 'high',
        outputFormat: 'png',
        background: 'transparent',
        count: 2,
        references: [{ assetId: 'png', role: 'reference_image' }],
      },
    ],
    [
      google,
      {
        mode: 'image.edit',
        aspectRatio: '16:9',
        count: 1,
        references: [{ assetId: 'png', role: 'reference_image' }],
      },
    ],
    [
      h3Capability,
      {
        mode: 'video.image',
        size: '768P',
        aspectRatio: 'adaptive',
        durationSeconds: 8,
        count: 1,
        references: [
          { assetId: 'png', role: 'first_frame' },
          { assetId: 'jpeg', role: 'last_frame' },
        ],
      },
    ],
    [
      seedanceCapability,
      {
        mode: 'video.image',
        size: '720p',
        aspectRatio: 'adaptive',
        durationSeconds: 6,
        outputFormat: 'mp4',
        generateAudio: false,
        count: 1,
        references: [{ assetId: 'png', role: 'first_frame' }],
      },
    ],
  ];
  for (const [capability, fields] of cases) {
    const request: GenerationRequest = {
      id: 'old',
      providerId: capability.providerId,
      modelId: capability.modelId,
      prompt: '保留镜头描述',
      mode: 'image.generate',
      parentRecipeId: 'parent',
      ...fields,
    };
    const draft = draftFromGenerationRequest(request, {}),
      profile = {
        ...configuredConnection,
        providerId: request.providerId,
        modelId: request.modelId,
      };
    const next = makeGenerationRequest('new', profile, capability, draft);
    expect(next).toEqual({ ...request, id: 'new' });
    draft.references.push({ assetId: 'new-reference', role: 'reference_image' });
    expect(request.references?.length).toBe(fields.references?.length);
  }
});
it('稀疏旧请求保留服务商默认，默认语义不偷偷改成当前面板推荐值', () => {
  const request: GenerationRequest = {
    id: 'old',
    providerId: 'openai',
    modelId: configuredConnection.modelId,
    mode: 'image.generate',
    prompt: '沿用',
    count: 1,
    parentRecipeId: 'parent',
  };
  const draft = draftFromGenerationRequest(request, {});
  expect(
    validateGenerationDraft(
      configuredConnection,
      openAICapability,
      draft,
      generationProject.assets,
    ),
  ).toEqual({});
  expect(makeGenerationRequest('new', configuredConnection, openAICapability, draft)).toEqual({
    ...request,
    id: 'new',
  });
});
it('参数签名不受JSON字段顺序影响，父配方不绕过相同内容的未知任务保护', () => {
  const a: GenerationRequest = {
    id: 'a',
    providerId: 'openai',
    modelId: 'model',
    mode: 'image.edit',
    prompt: '原文',
    count: 1,
    references: [{ assetId: 'png', role: 'reference_image' }],
  };
  const b = JSON.parse(
    '{"prompt":"原文","mode":"image.edit","modelId":"model","providerId":"openai","references":[{"role":"reference_image","assetId":"png"}],"id":"b","count":1}',
  ) as GenerationRequest;
  expect(requestSignature('connection', a)).toBe(requestSignature('connection', b));
  expect(generationContentSignature('connection', a)).toBe(
    generationContentSignature('connection', { ...b, parentRecipeId: 'different-parent' }),
  );
  expect(requestSignature('connection', a)).not.toBe(
    requestSignature('connection', { ...b, parentRecipeId: 'different-parent' }),
  );
});
it('原配方指纹变化阻止旧参考提交，显式重新选择后使用当前证明', () => {
  const r: GenerationRequest = {
    id: 'old',
    providerId: 'openai',
    modelId: configuredConnection.modelId,
    mode: 'image.edit',
    prompt: '原文',
    size: '1024x1024',
    quality: 'high',
    outputFormat: 'png',
    count: 1,
    references: [{ assetId: 'png', role: 'reference_image' }],
  };
  const assets = generationProject.assets.map((a) =>
    a.id === 'png' ? { ...a, fingerprint: 'sha256:new' } : a,
  );
  const draft = draftFromGenerationRequest(r, { png: 'sha256:old' });
  expect(
    validateGenerationDraft(configuredConnection, openAICapability, draft, assets).references,
  ).toContain('原参考图片已改变');
  draft.referenceFingerprints = { png: 'sha256:new' };
  expect(validateGenerationDraft(configuredConnection, openAICapability, draft, assets)).toEqual(
    {},
  );
});
