export type TrackKind = 'video' | 'audio' | 'text' | 'caption';
export type ClipKind = TrackKind | 'image';

export interface ProjectScope {
  projectId: string;
  projectRevision: number;
  sourceEpoch: number;
}

export interface Settings {
  width: number;
  height: number;
  fpsNum: number;
  fpsDen: number;
  sampleRate: number;
}
export interface FontInfo {
  copyright?: string;
  license?: string;
  licenseUrl?: string;
  family: string;
  style: string;
  fullName: string;
  weight: number;
  italic: boolean;
}
export interface Asset {
  font?: FontInfo;
  id: string;
  name: string;
  kind: string;
  path: string;
  url?: string;
  durationUs: number;
  width: number;
  height: number;
  fpsNum: number;
  fpsDen: number;
  hasAudio: boolean;
  missing: boolean;
  thumbnailUrl?: string;
  fingerprint?: string;
  audioSampleRate?: number;
  audioSampleCount?: number | null;
  audioStartUs?: number;
}
export interface AudioRange {
  unit: 'us' | 'samples';
  start: number;
  end: number;
  sampleRate?: number;
  sourceOriginUs?: number;
  offsetNum?: number;
  offsetDen?: number;
}
// 由Go核心维护的原处理配方；属性更新必须原样回传，不能从派生入点重建。
export interface SourcePhase {
  recipe: {
    assetId: string;
    sourceFingerprint?: string;
    kind: 'audio' | 'video';
    startFrame: number;
    durationFrames: number;
    sourceInUs: number;
    speedNum: number;
    speedDen: number;
    fpsNum: number;
    fpsDen: number;
    audioRange?: AudioRange;
  };
  offsetFrames: number;
}
export interface Track {
  id: string;
  name: string;
  kind: TrackKind;
  locked: boolean;
  muted: boolean;
  hidden: boolean;
}
export interface Transform {
  x: number;
  y: number;
  scale: number;
  rotation: number;
}
export interface TextStyle {
  fontAssetId?: string;
  fontSize: number;
  color: string;
  background: string;
  align: string;
}
// 词时间使用源素材微秒；字幕绑定由编辑核心维护。
export interface SourceWord {
  id: string;
  startUs: number;
  endUs: number;
  text: string;
  speakerId?: string;
}
export interface CaptionExclusion {
  sourceClipId: string;
  wordId: string;
}
export interface Transcript {
  id: string;
  assetId: string;
  sourceFingerprint: string;
  sourceDurationUs: number;
  language?: string;
  rawWords: SourceWord[];
  acceptedWords: SourceWord[];
  revision: number;
  captionTrackId?: string;
  stale?: boolean;
  captionExclusions?: CaptionExclusion[];
  origin?: 'asr' | 'tts';
  timingSource?: 'original' | 'normalized';
  captionLayout?: CaptionLayout;
}
export interface CaptionLayout {
  mode: 'word' | 'readable';
  maxCharsPerLine?: number;
  maxLines?: number;
  maxDurationUs?: number;
  pauseThresholdUs?: number;
}
export interface CaptionBinding {
  transcriptId: string;
  sourceClipId: string;
  wordIds: string[];
  textOverride?: boolean;
  styleOverride?: boolean;
  partial?: boolean;
  stale?: boolean;
}
export interface Clip {
  audioFade?: { inUs: number; outUs: number; spanFrames: number; offsetFrames: number };
  id: string;
  trackId: string;
  assetId?: string;
  kind: ClipKind;
  name: string;
  startFrame: number;
  durationFrames: number;
  sourceInUs: number;
  audioRange?: AudioRange;
  sourcePhase?: SourcePhase;
  speedNum: number;
  speedDen: number;
  volume: number;
  muted: boolean;
  transform: Transform;
  text?: string;
  textStyle: TextStyle;
  lut?: { assetId: string; intensity: number };
  linkGroup?: string;
  captionSource?: CaptionBinding;
}
export interface Project {
  sequenceId?: string;
  sequenceName?: string;
  activeSequenceProjection?: boolean;
  schemaVersion: number;
  id: string;
  name: string;
  revision: number;
  settings: Settings;
  assets: Asset[];
  tracks: Track[];
  clips: Clip[];
  transcripts?: Transcript[];
  visualAnalyses?: import('./visual-analysis-types').VisualAnalysis[];
}
export interface Command {
  clipIds?: string[];
  frameDelta?: number;
  edge?: 'start' | 'end';
  trackDelta?: number;
  type: string;
  clip?: Clip;
  clipId?: string;
  track?: Track;
  trackId?: string;
  beforeTrackId?: string;
  asset?: Asset;
  atFrame?: number;
  name?: string;
  settings?: Settings;
  includeLinked?: boolean;
  transcript?: Transcript;
  transcriptId?: string;
  transcriptRevision?: number;
  words?: SourceWord[];
}
export interface EditRequest {
  baseRevision: number;
  idempotencyKey: string;
  label: string;
  commands: Command[];
}
export interface Capability {
  available: boolean;
  reason: string;
}
export interface Bootstrap {
  sequences?: import('./sequence-types').SequenceSummary[];
  sourceEpoch?: number;
  project: Project;
  projectPath: string;
  dirty: boolean;
  canUndo: boolean;
  canRedo: boolean;
  capabilities: Record<string, Capability>;
  warnings?: string[];
}
export interface Job {
  id: string;
  type: string;
  status: 'queued' | 'running' | 'succeeded' | 'failed' | 'cancelled';
  progress?: number;
  message: string;
  outputPath?: string;
  createdAt: string;
}
export interface PreviewFrame {
  url: string;
  frame: number;
  revision: number;
}
export interface ExportOptions {
  resourceMode?: 'standard' | 'lower_memory';
  renderer?: 'auto' | 'native' | 'ffmpeg';
  path?: string;
  preset?: string;
  fromFrame?: number;
  toFrame?: number;
  videoEncoder?: string;
  overwrite?: boolean;
}

declare global {
  interface Window {
    _wails?: { environment?: { OS?: string } };
  }
}
