export interface PlaybackFeature {
  supported: boolean;
  reason?: string;
}
export type RenderQuality = 'full' | 'half' | 'quarter';
export interface PlaybackCapabilities {
  available: boolean;
  reason: string;
  protocol: 'AIV1';
  mltVersion: string;
  display: 'webview_binary_canvas_audio';
  video: PlaybackFeature;
  image: PlaybackFeature;
  audio: PlaybackFeature;
  pictureSeek?: PlaybackFeature;
  sourceProxies?: PlaybackFeature;
  text: PlaybackFeature;
  fontFiles?: PlaybackFeature;
  transform: PlaybackFeature;
  speed: PlaybackFeature;
  lut: PlaybackFeature;
  producers: string[];
  filters: string[];
  transitions: string[];
  projectSupported: boolean;
  projectReason: string;
  maxWidth: number;
  maxHeight: number;
}
export interface PlaybackStatus {
  proxyProfile?: string;
  proxyAssets?: number;
  originalVideoAssets?: number;
  id: string;
  projectId: string;
  projectRevision: number;
  status: 'idle' | 'paused' | 'playing' | 'stale' | 'failed' | 'closed';
  frame: number;
  durationFrames: number;
  width: number;
  height: number;
  fpsNum: number;
  fpsDen: number;
  sampleRate: number;
  channels: number;
  epoch: number;
  reason: string;
  connected: boolean;
  inFlight: number;
}
export interface PlaybackStart {
  status: PlaybackStatus;
  streamUrl: string;
}
export interface PlaybackFrameHeader {
  type: 'frame';
  epoch: number;
  sequence: number;
  position: number;
  width: number;
  height: number;
  fpsNum: number;
  fpsDen: number;
  sampleRate: number;
  channels: number;
  samples: number;
  audioStartSample: number;
  end: boolean;
  playing: boolean;
  pictureOnly?: boolean;
}
export interface PlaybackFrame {
  header: PlaybackFrameHeader;
  rgba: Uint8ClampedArray<ArrayBuffer>;
  pcm: Float32Array<ArrayBuffer>;
}
export interface PlaybackAPI {
  getPlaybackCapabilities(): Promise<PlaybackCapabilities>;
  startPlayback(input: {
    renderQuality?: RenderQuality;
    proxyProfile?: import('./proxy-types').ProxyProfile;
    sourceEpoch?: number;
    projectId: string;
    projectRevision: number;
    frame: number;
  }): Promise<PlaybackStart>;
  controlPlayback(input: {
    id: string;
    action: 'play' | 'pause' | 'seek';
    frame?: number;
  }): Promise<PlaybackStatus>;
  getPlaybackStatus(): Promise<PlaybackStatus>;
  stopPlayback(id: string): Promise<PlaybackStatus>;
}
