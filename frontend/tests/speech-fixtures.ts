import { vi } from 'vitest';
import type { Asset, Bootstrap, Project, Transcript } from '../src/types';
import type { SpeechAPI, SpeechCatalog, SpeechJob, SpeechSummary } from '../src/speech-types';
import type { ConnectionProfile } from '../src/generation-types';
import { defaultClip } from '../src/lib/editor';
import { fakeGenerationAPI } from './generation-fixtures';
export const speechCatalog: SpeechCatalog = {
  providerId: 'elevenlabs',
  asrModels: ['scribe_v2'],
  ttsModels: ['eleven_multilingual_v2'],
  timestampGranularities: ['word', 'character'],
  outputFormats: ['mp3_44100_128'],
  transcription: { supported: true, reason: '' },
  speechSynthesis: { supported: true, reason: '' },
  speechTiming: { supported: true, reason: '' },
  voiceListing: { supported: true, reason: '' },
  instantCloning: { supported: true, reason: '创建不代表身份验证' },
  diarization: { supported: true, reason: '' },
  characterTimestamps: { supported: true, reason: '' },
  remoteCancellation: { supported: false, reason: '不保证远端取消' },
  streaming: { supported: false, reason: '同步任务' },
  voiceVerification: { supported: false, reason: '未验证' },
  maxInputBytes: 67108864,
  maxTextCharacters: 10000,
  maxCloneFiles: 5,
  audioPreparation: { available: true, reason: '' },
  audioImport: { available: true, reason: '' },
};
export const speechConnections: ConnectionProfile[] = ['scribe_v2', 'eleven_multilingual_v2'].map(
  (modelId, index) => ({
    id: `speech-connection-${index}`,
    providerId: 'elevenlabs',
    name: index ? '配音连接' : '转写连接',
    modelId,
    configured: true,
    createdAt: '2026-10-25T00:00:00Z',
    updatedAt: '2026-10-25T00:00:00Z',
  }),
);
export const speechSource: Asset = {
  id: 'source-audio',
  name: '已登记口播.wav',
  path: '/fixture/source.wav',
  kind: 'audio',
  durationUs: 2000000,
  fpsNum: 0,
  fpsDen: 1,
  width: 0,
  height: 0,
  hasAudio: true,
  missing: false,
  fingerprint: 'sha256:fixture',
};
const settings = { width: 1920, height: 1080, fpsNum: 30, fpsDen: 1, sampleRate: 48000 };
export const speechSourceClip = {
  ...defaultClip('audio', 'audio-track', 0, settings, speechSource),
  id: 'source-clip',
};
export const sourceTranscript: Transcript = {
  id: 'transcript-1',
  assetId: speechSource.id,
  sourceFingerprint: speechSource.fingerprint!,
  sourceDurationUs: speechSource.durationUs,
  language: 'zh',
  rawWords: [
    { id: 'word-1', startUs: 100000, endUs: 800000, text: '原始识别', speakerId: 'speaker-1' },
  ],
  acceptedWords: [
    { id: 'word-1', startUs: 100000, endUs: 800000, text: '已接受文本', speakerId: 'speaker-1' },
  ],
  revision: 2,
  captionTrackId: 'caption-track',
};
export const speechCaption = {
  ...defaultClip('caption', 'caption-track', 3, settings),
  id: 'bound-caption',
  text: '人工字幕覆盖',
  captionSource: {
    transcriptId: sourceTranscript.id,
    sourceClipId: speechSourceClip.id,
    wordIds: ['word-1'],
    textOverride: true,
    styleOverride: true,
  },
};
export const speechProject: Project = {
  id: 'speech-project',
  name: '语音测试工程',
  schemaVersion: 1,
  revision: 3,
  settings,
  assets: [
    speechSource,
    {
      ...speechSource,
      id: 'video-source',
      name: '采访.mp4',
      path: '/fixture/video.mp4',
      kind: 'video',
    },
  ],
  tracks: [
    {
      id: 'audio-track',
      name: '音频 1',
      kind: 'audio',
      locked: false,
      muted: false,
      hidden: false,
    },
    {
      id: 'caption-track',
      name: '字幕 1',
      kind: 'caption',
      locked: false,
      muted: false,
      hidden: false,
    },
  ],
  clips: [speechSourceClip, speechCaption],
  transcripts: [sourceTranscript],
};
export const speechBootstrap: Bootstrap = {
  project: speechProject,
  projectPath: '',
  dirty: true,
  canUndo: true,
  canRedo: false,
  capabilities: {},
};
export function speechSummary(overrides: Partial<SpeechSummary> = {}): SpeechSummary {
  return {
    id: 'speech-job',
    connectionId: speechConnections[0].id,
    projectId: speechProject.id,
    projectRevision: speechProject.revision,
    providerId: 'elevenlabs',
    modelId: 'scribe_v2',
    mode: 'transcribe',
    status: 'succeeded',
    message: '源转写结果已保存',
    sourceAssetId: speechSource.id,
    sourceFingerprint: speechSource.fingerprint,
    sourceDurationUs: speechSource.durationUs,
    completedParts: 1,
    totalParts: 1,
    canResumeLocal: false,
    createdAt: '2026-10-25T00:00:00Z',
    updatedAt: '2026-10-25T00:00:00Z',
    warnings: [],
    audioImported: false,
    audioOnTimeline: false,
    timingRequested: false,
    timingAvailable: false,
    timingComplete: false,
    normalizedTimingAvailable: false,
    normalizedTimingComplete: false,
    ...overrides,
  };
}
export function speechJob(overrides: Partial<SpeechJob> = {}): SpeechJob {
  return {
    ...speechSummary(),
    input: {
      id: 'speech-job',
      connectionId: speechConnections[0].id,
      mode: 'transcribe',
      consent: true,
      transcribe: {
        assetId: speechSource.id,
        modelId: 'scribe_v2',
        granularity: 'word',
        diarize: false,
        tagAudioEvents: false,
      },
    },
    parts: [
      {
        id: 'part-1',
        requestId: 'part-request',
        fingerprint: 'sha256:part',
        offsetUs: 0,
        durationUs: 2000000,
        audioDurationUs: 2000000,
        status: 'succeeded',
        message: '已保存',
      },
    ],
    transcription: {
      id: 'speech-job',
      providerId: 'elevenlabs',
      modelId: 'scribe_v2',
      status: 'succeeded',
      submissionUncertain: false,
      assetId: speechSource.id,
      sourceSha256: speechSource.fingerprint!,
      sourceDurationUs: speechSource.durationUs,
      language: 'zh',
      text: '原始识别',
      words: [
        {
          text: '原始识别',
          startUs: 100000,
          endUs: 800000,
          timingComplete: true,
          type: 'word',
          speakerId: 'speaker-1',
        },
      ],
      granularity: 'word',
      timingComplete: true,
      warnings: [],
    },
    ...overrides,
  };
}
export function fakeSpeechAPI() {
  const connections = fakeGenerationAPI();
  return {
    ...connections,
    getConnections: vi.fn<SpeechAPI['getConnections']>().mockResolvedValue(speechConnections),
    getSpeechCatalog: vi.fn<SpeechAPI['getSpeechCatalog']>().mockResolvedValue(speechCatalog),
    listVoices: vi.fn<SpeechAPI['listVoices']>().mockResolvedValue({
      voices: [{ id: 'voice-1', name: '测试音色', verificationState: 'unverified' }],
      hasMore: false,
    }),
    startSpeechJob: vi
      .fn<SpeechAPI['startSpeechJob']>()
      .mockRejectedValue(new Error('测试禁止真实语音请求')),
    listSpeechJobs: vi.fn<SpeechAPI['listSpeechJobs']>().mockResolvedValue([]),
    getSpeechJob: vi.fn<SpeechAPI['getSpeechJob']>().mockResolvedValue(speechJob()),
    cancelSpeechJob: vi
      .fn<SpeechAPI['cancelSpeechJob']>()
      .mockResolvedValue(speechSummary({ status: 'cancelling' })),
    resumeSpeechLocal: vi.fn<SpeechAPI['resumeSpeechLocal']>().mockResolvedValue(speechSummary()),
    acknowledgeSpeechUnknown: vi
      .fn<SpeechAPI['acknowledgeSpeechUnknown']>()
      .mockResolvedValue(speechSummary({ status: 'unknown', acknowledged: true })),
    acceptSpeechTranscript: vi
      .fn<SpeechAPI['acceptSpeechTranscript']>()
      .mockResolvedValue(speechBootstrap),
    correctSourceTranscript: vi
      .fn<SpeechAPI['correctSourceTranscript']>()
      .mockResolvedValue(speechBootstrap),
    importSpeechAudio: vi.fn<SpeechAPI['importSpeechAudio']>().mockResolvedValue(speechBootstrap),
    detachCaption: vi.fn<SpeechAPI['detachCaption']>().mockResolvedValue(speechBootstrap),
    previewCaptionLayout: vi
      .fn<SpeechAPI['previewCaptionLayout']>()
      .mockRejectedValue(new Error('测试尚未指定分句预览')),
    applyCaptionLayout: vi.fn<SpeechAPI['applyCaptionLayout']>().mockResolvedValue(speechBootstrap),
  };
}
