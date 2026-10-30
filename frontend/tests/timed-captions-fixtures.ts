import type { Asset, CaptionLayout, Transcript } from '../src/types';
import type { CaptionLayoutPreview, SpeechJob } from '../src/speech-types';
import {
  sourceTranscript,
  speechBootstrap,
  speechCaption,
  speechConnections,
  speechJob,
  speechProject,
  speechSource,
  speechSummary,
} from './speech-fixtures';
export const timedAudio: Asset = {
  ...speechSource,
  id: 'timed-audio',
  name: '带时间配音.mp3',
  path: '/fixture/timed.mp3',
  durationUs: 2000000,
  url: 'http://127.0.0.1:43111/media/timed-fixture',
};
export const timedSummary = speechSummary({
  id: 'timed-job',
  mode: 'synthesize',
  modelId: 'eleven_multilingual_v2',
  connectionId: speechConnections[1].id,
  audio: timedAudio,
  sourceAssetId: undefined,
  timingRequested: true,
  timingAvailable: true,
  timingComplete: true,
  normalizedTimingAvailable: true,
  normalizedTimingComplete: true,
});
export const timedJob: SpeechJob = speechJob({
  ...timedSummary,
  transcription: undefined,
  input: {
    id: timedSummary.id,
    mode: 'synthesize',
    connectionId: speechConnections[1].id,
    consent: true,
    synthesize: {
      voiceId: 'voice-1',
      modelId: 'eleven_multilingual_v2',
      outputFormat: 'mp3_44100_128',
      text: '12',
      withTiming: true,
    },
  },
  timing: {
    id: timedSummary.id,
    providerId: 'elevenlabs',
    modelId: 'eleven_multilingual_v2',
    status: 'succeeded',
    submissionUncertain: false,
    voiceId: 'voice-1',
    outputFormat: 'mp3_44100_128',
    mime: 'audio/mpeg',
    alignment: { characters: ['1', '2'], startTimesSeconds: [0, 0.1], endTimesSeconds: [0.1, 0.2] },
    normalizedAlignment: {
      characters: ['十', '二'],
      startTimesSeconds: [0, 0.1],
      endTimesSeconds: [0.1, 0.2],
    },
    timingComplete: true,
    warnings: [],
  },
  timingChoices: [
    { source: 'original', available: true, reason: '', text: '12', wordCount: 2 },
    { source: 'normalized', available: true, reason: '', text: '十二', wordCount: 2 },
  ],
});
export const readableLayout: CaptionLayout = {
  mode: 'readable',
  maxCharsPerLine: 18,
  maxLines: 2,
  maxDurationUs: 4000000,
  pauseThresholdUs: 700000,
};
export const captionLayoutPreview: CaptionLayoutPreview = {
  id: 'layout-preview',
  projectId: speechProject.id,
  projectRevision: speechProject.revision,
  transcriptId: sourceTranscript.id,
  transcriptRevision: sourceTranscript.revision,
  layout: readableLayout,
  summary: '阅读分句预览已生成，人工修改保持独立',
  beforeCount: 10,
  afterCount: 1,
  protectedCount: 1,
  warnings: ['人工修改的字幕不与其他组悄悄合并'],
  captions: [{ ...speechCaption, text: '手改字幕\n原样保留', startFrame: 3, durationFrames: 21 }],
  truncated: false,
  createdAt: '2026-10-25T00:00:00Z',
  applied: false,
};
export const layoutApplied = {
  ...speechBootstrap,
  project: {
    ...speechProject,
    revision: speechProject.revision + 1,
    transcripts: [
      {
        ...sourceTranscript,
        revision: sourceTranscript.revision + 1,
        captionLayout: readableLayout,
      },
    ],
  },
};
export const ttsTranscript: Transcript = {
  ...sourceTranscript,
  id: 'timed-job-transcript',
  assetId: timedAudio.id,
  origin: 'tts',
  timingSource: 'normalized',
  captionLayout: readableLayout,
};
