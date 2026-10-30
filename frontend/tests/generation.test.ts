import { describe, expect, it } from 'vitest';
import {
  allowedAspects,
  canGenerate,
  requestSignature,
  defaultGenerationDraft,
  eligibleReferenceImages,
  jobCanResume,
  makeGenerationRequest,
  validateGenerationDraft,
} from '../src/lib/generation';
import {
  configuredConnection,
  generationJob,
  generationProject,
  h3Capability,
  openAICapability,
  seedanceCapability,
} from './generation-fixtures';

describe('生成能力与参考用途', () => {
  it('默认数量1并优先继承工程画幅，不向未声明模型注入其他参数', () => {
    const draft = defaultGenerationDraft(openAICapability, generationProject, '城市');
    expect(draft.count).toBe('1');
    expect(draft.size).toBe('1536x864');
    const request = makeGenerationRequest('request', configuredConnection, openAICapability, draft);
    expect(request.aspectRatio).toBe('16:9');
    expect(request).not.toHaveProperty('durationSeconds');
    expect(request).not.toHaveProperty('background');
    expect(
      validateGenerationDraft(
        configuredConnection,
        openAICapability,
        draft,
        generationProject.assets,
      ),
    ).toEqual({});
  });
  it('H3首尾帧与参考模式互斥，不能默默忽略用途错误', () => {
    const profile = {
      ...configuredConnection,
      providerId: 'minimax',
      modelId: h3Capability.modelId,
    };
    const draft = defaultGenerationDraft(
      h3Capability,
      generationProject,
      '缓慢推近',
      'video.image',
    );
    expect(draft.aspectRatio).toBe('adaptive');
    draft.references = [{ assetId: 'png', role: 'first_frame' }];
    expect(validateGenerationDraft(profile, h3Capability, draft, generationProject.assets)).toEqual(
      {},
    );
    draft.references.push({ assetId: 'jpeg', role: 'reference_image' });
    expect(
      validateGenerationDraft(profile, h3Capability, draft, generationProject.assets).references,
    ).toContain('不能混入');
    draft.mode = 'video.reference';
    draft.references = [{ assetId: 'jpeg', role: 'reference_image' }];
    draft.aspectRatio = '16:9';
    expect(validateGenerationDraft(profile, h3Capability, draft, generationProject.assets)).toEqual(
      {},
    );
  });
  it('按模型声明校验时长/数量、模式画幅与参考素材类型', () => {
    const profile = {
      ...configuredConnection,
      providerId: 'minimax',
      modelId: h3Capability.modelId,
    };
    const draft = defaultGenerationDraft(h3Capability, generationProject, '镜头');
    draft.durationSeconds = '3';
    draft.count = '2';
    draft.aspectRatio = 'adaptive';
    const errors = validateGenerationDraft(profile, h3Capability, draft, generationProject.assets);
    expect(errors.durationSeconds).toBeTruthy();
    expect(errors.count).toBeTruthy();
    expect(errors.aspectRatio).toBeTruthy();
    expect(
      allowedAspects({ ...h3Capability, modelId: 'MiniMax-H3-Lite' }, 'video.reference'),
    ).not.toContain('adaptive');
    expect(eligibleReferenceImages(generationProject.assets).map((asset) => asset.id)).toEqual([
      'png',
      'jpeg',
    ]);
  });
  it('Seedance 使用声明默认值、首帧限制并把声音选择计入核对签名', () => {
    const profile = {
      ...configuredConnection,
      providerId: 'seedance',
      modelId: seedanceCapability.modelId,
    };
    const draft = defaultGenerationDraft(seedanceCapability, generationProject, '镜头');
    expect(draft.size).toBe('720p');
    expect(draft.durationSeconds).toBe('5');
    expect(draft.generateAudio).toBe(true);
    expect(
      validateGenerationDraft(profile, seedanceCapability, draft, generationProject.assets),
    ).toEqual({});
    const voiced = makeGenerationRequest('id', profile, seedanceCapability, draft);
    const silent = makeGenerationRequest('id', profile, seedanceCapability, {
      ...draft,
      generateAudio: false,
    });
    expect(silent.generateAudio).toBe(false);
    expect(requestSignature(profile.id, voiced)).not.toBe(requestSignature(profile.id, silent));
    const frame = defaultGenerationDraft(
      seedanceCapability,
      generationProject,
      '镜头',
      'video.image',
    );
    expect(allowedAspects(seedanceCapability, frame.mode)).toEqual(['adaptive']);
    frame.references = [{ assetId: 'png', role: 'last_frame' }];
    expect(
      validateGenerationDraft(profile, seedanceCapability, frame, generationProject.assets)
        .references,
    ).toContain('必须选择一张首帧');
    frame.references.unshift({ assetId: 'jpeg', role: 'first_frame' });
    expect(
      validateGenerationDraft(profile, seedanceCapability, frame, generationProject.assets),
    ).toEqual({});
    const unrelated = makeGenerationRequest('id', configuredConnection, openAICapability, {
      ...defaultGenerationDraft(openAICapability, generationProject, '图'),
      generateAudio: false,
    });
    expect(unrelated).not.toHaveProperty('generateAudio');
  });
  it('目录可提交和账号已验证分开，未知模型仍不可用', () => {
    expect(canGenerate(seedanceCapability)).toBe(true);
    expect(seedanceCapability.accountVerified).toBe(false);
    expect(canGenerate(undefined)).toBe(false);
    expect(canGenerate({ ...seedanceCapability, needsModelConfiguration: true })).toBe(false);
    expect(
      jobCanResume(generationJob({ status: 'unknown', canResume: true, remoteId: undefined })),
    ).toBe(false);
    expect(
      jobCanResume(generationJob({ status: 'waiting', canResume: true, remoteId: 'existing-id' })),
    ).toBe(true);
  });
});
