import { describe, expect, it } from 'vitest';
import {
  speechConnections as eligibleConnections,
  speechSources,
  speechUnavailable,
  validateSpeechRequest,
} from '../src/lib/speech';
import { speechCatalog, speechConnections, speechProject, speechSource } from './speech-fixtures';
describe('语音素材与付费前置条件', () => {
  it('转写仅允许登记音视频，克隆仅音频，缺失或静音视频不混入', () => {
    const assets = [
      ...speechProject.assets,
      { ...speechSource, id: 'missing', missing: true },
      { ...speechSource, id: 'silent-video', kind: 'video', hasAudio: false },
      { ...speechSource, id: 'image', kind: 'image' },
    ];
    expect(speechSources(assets).map((value) => value.id)).toEqual([
      'source-audio',
      'video-source',
    ]);
    expect(speechSources(assets, true).map((value) => value.id)).toEqual(['source-audio']);
  });
  it('ASR/TTS模型按当前任务过滤，媒体导入未就绪时拒绝配音', () => {
    expect(
      eligibleConnections(speechConnections, speechCatalog, 'synthesize').map(
        (value) => value.modelId,
      ),
    ).toEqual(['eleven_multilingual_v2']);
    expect(
      speechUnavailable(
        { ...speechCatalog, audioImport: { available: false, reason: '无法核对音频时长' } },
        'synthesize',
      ),
    ).toContain('无法核对音频时长');
  });
  it('克隆必须明确主体与授权声明，不能用视频样本代替', () => {
    const request = {
      connectionId: speechConnections[0].id,
      mode: 'clone' as const,
      clone: {
        name: '声音',
        assetIds: ['video-source'],
        consent: { confirmed: false, subject: '', authority: 'self' as const },
      },
    };
    expect(
      validateSpeechRequest(request, speechCatalog, speechConnections[0], speechProject.assets),
    ).toEqual(
      expect.objectContaining({ samples: expect.any(String), consent: expect.any(String) }),
    );
    request.clone = {
      ...request.clone,
      assetIds: [speechSource.id],
      consent: { confirmed: true, subject: '本人测试', authority: 'self' },
    };
    expect(
      validateSpeechRequest(request, speechCatalog, speechConnections[0], speechProject.assets),
    ).toEqual({});
  });
});
