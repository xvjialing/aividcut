import type { ConnectionAPI } from './generation-types';
import type { Asset, Bootstrap, CaptionLayout, Clip, SourceWord } from './types';

export interface SpeechCatalog extends SpeechCapabilities {
  audioPreparation: { available: boolean; reason: string };
  audioImport: { available: boolean; reason: string };
}
export type SpeechMode = 'transcribe' | 'synthesize' | 'clone';
export interface SpeechDraftRequest {
  connectionId: string;
  mode: SpeechMode;
  transcribe?: SpeechTranscribeRequest;
  synthesize?: SpeechSynthesizeRequest;
  clone?: SpeechCloneRequest;
}
export interface StartSpeechInput extends SpeechDraftRequest {
  id: string;
  consent: boolean;
}
export type SpeechStatus =
  | 'queued'
  | 'preparing'
  | 'submitting'
  | 'running'
  | 'saving'
  | 'succeeded'
  | 'failed'
  | 'unknown'
  | 'cancelling'
  | 'cancelled';
export interface SpeechSummary {
  id: string;
  connectionId: string;
  projectId: string;
  projectRevision: number;
  providerId: string;
  modelId: string;
  mode: SpeechMode;
  status: SpeechStatus;
  message: string;
  sourceAssetId?: string;
  sourceFingerprint?: string;
  sourceDurationUs?: number;
  completedParts: number;
  totalParts: number;
  canResumeLocal: boolean;
  acknowledged?: boolean;
  createdAt: string;
  updatedAt: string;
  audio?: Asset;
  voice?: SpeechVoice;
  warnings: string[];
  acceptedTranscriptId?: string;
  audioImported: boolean;
  audioOnTimeline: boolean;
  timingRequested: boolean;
  timingAvailable: boolean;
  timingComplete: boolean;
  timingReason?: string;
  normalizedTimingAvailable: boolean;
  normalizedTimingComplete: boolean;
  normalizedTimingReason?: string;
  captionTimingSource?: 'original' | 'normalized';
}
export interface SpeechPartState {
  id: string;
  requestId: string;
  fingerprint: string;
  offsetUs: number;
  durationUs: number;
  audioDurationUs: number;
  status: string;
  remoteId?: string;
  message: string;
}
export interface SpeechJob extends SpeechSummary {
  input: StartSpeechInput;
  parts: SpeechPartState[];
  transcription?: SpeechTranscription;
  partResults?: SpeechTranscription[];
  timing?: SpeechTimedAudioResult;
  timingChoices?: SpeechTimingChoice[];
}
export interface SpeechAPI extends ConnectionAPI {
  getSpeechCatalog(): Promise<SpeechCatalog>;
  listVoices(connectionId: string, request: SpeechVoiceListRequest): Promise<SpeechVoicePage>;
  startSpeechJob(input: StartSpeechInput): Promise<SpeechSummary>;
  listSpeechJobs(): Promise<SpeechSummary[]>;
  getSpeechJob(id: string): Promise<SpeechJob>;
  cancelSpeechJob(id: string): Promise<SpeechSummary>;
  resumeSpeechLocal(id: string): Promise<SpeechSummary>;
  acknowledgeSpeechUnknown(id: string): Promise<SpeechSummary>;
  acceptSpeechTranscript(
    id: string,
    options: { trackId: string; generateCaptions: boolean },
  ): Promise<Bootstrap>;
  correctSourceTranscript(input: SpeechTranscriptCorrection): Promise<Bootstrap>;
  importSpeechAudio(id: string, options: SpeechAudioImportOptions): Promise<Bootstrap>;
  detachCaption(input: { projectId: string; clipId: string }): Promise<Bootstrap>;
  previewCaptionLayout(input: CaptionLayoutInput): Promise<CaptionLayoutPreview>;
  applyCaptionLayout(id: string): Promise<Bootstrap>;
}

export interface SpeechFeature {
  supported: boolean;
  reason: string;
}
export interface SpeechCapabilities {
  providerId: string;
  asrModels: string[];
  ttsModels: string[];
  timestampGranularities: string[];
  outputFormats: string[];
  transcription: SpeechFeature;
  speechSynthesis: SpeechFeature;
  speechTiming: SpeechFeature;
  voiceListing: SpeechFeature;
  instantCloning: SpeechFeature;
  diarization: SpeechFeature;
  characterTimestamps: SpeechFeature;
  remoteCancellation: SpeechFeature;
  streaming: SpeechFeature;
  voiceVerification: SpeechFeature;
  maxInputBytes: number;
  maxTextCharacters: number;
  maxCloneFiles: number;
}
export interface SpeechTranscribeRequest {
  id?: string;
  assetId: string;
  modelId?: string;
  language?: string;
  granularity?: string;
  diarize: boolean;
  tagAudioEvents: boolean;
}
export interface SpeechSynthesizeRequest {
  id?: string;
  modelId?: string;
  voiceId: string;
  text: string;
  outputFormat?: string;
  withTiming?: boolean;
}
export interface SpeechCloneRequest {
  id?: string;
  name: string;
  description?: string;
  assetIds: string[];
  consent: { confirmed: boolean; subject: string; authority: 'self' | 'authorized' };
}
export interface SpeechTimedText {
  text: string;
  startUs: number | null;
  endUs: number | null;
}
// 对应speech.CharacterAlignment；原文与朗读规范化结果必须分开保存。
export interface SpeechCharacterAlignment {
  characters: string[];
  startTimesSeconds: Array<number | null>;
  endTimesSeconds: Array<number | null>;
}
export interface SpeechTimedAudioResult extends SpeechOperation {
  voiceId: string;
  outputFormat: string;
  mime: string;
  alignment: SpeechCharacterAlignment | null;
  normalizedAlignment: SpeechCharacterAlignment | null;
  timingComplete: boolean;
  warnings: string[];
  rawAlignment?: unknown;
  rawNormalizedAlignment?: unknown;
}
export interface SpeechTimingChoice {
  source: 'original' | 'normalized';
  available: boolean;
  reason: string;
  text: string;
  wordCount: number;
}
export interface SpeechAudioImportOptions {
  trackId: string;
  startFrame: number;
  addToTimeline: boolean;
  generateCaptions?: boolean;
  captionTrackId?: string;
  captionLayout?: CaptionLayout;
  alignmentSource?: 'original' | 'normalized';
}
export interface CaptionLayoutInput {
  projectId: string;
  projectRevision: number;
  transcriptId: string;
  transcriptRevision: number;
  trackId?: string;
  createNewTrack?: boolean;
  layout: CaptionLayout;
}
export interface CaptionLayoutPreview {
  id: string;
  projectId: string;
  projectRevision: number;
  transcriptId: string;
  transcriptRevision: number;
  layout: CaptionLayout;
  summary: string;
  beforeCount: number;
  afterCount: number;
  protectedCount: number;
  warnings: string[];
  captions: Clip[];
  truncated: boolean;
  createdAt: string;
  applied: boolean;
}
export interface SpeechWord extends SpeechTimedText {
  timingComplete: boolean;
  type: string;
  speakerId?: string;
  logProbability?: number | null;
  characters?: SpeechTimedText[];
}
export interface SpeechOperation {
  id: string;
  providerId: string;
  modelId?: string;
  remoteId?: string;
  status: string;
  submissionUncertain: boolean;
}
export interface SpeechTranscription extends SpeechOperation {
  assetId: string;
  sourceSha256: string;
  sourceDurationUs?: number;
  reportedAudioDurationUs?: number | null;
  language: string;
  languageProbability?: number | null;
  text: string;
  words: SpeechWord[];
  granularity: string;
  timingComplete: boolean;
  warnings: string[];
}
export interface SpeechVoice {
  id: string;
  name: string;
  description?: string;
  category?: string;
  labels?: Record<string, string>;
  verificationState: string;
  requiresVerification?: boolean | null;
  providerReportedVerified?: boolean | null;
}
export interface SpeechVoiceListRequest {
  search?: string;
  pageSize?: number;
  nextPageToken?: string;
}
export interface SpeechVoicePage {
  voices: SpeechVoice[];
  hasMore: boolean;
  nextPageToken?: string;
}
export interface SpeechTranscriptCorrection {
  projectId: string;
  transcriptId: string;
  transcriptRevision: number;
  words: SourceWord[];
}
export type SpeechConnectionAPI = ConnectionAPI;
