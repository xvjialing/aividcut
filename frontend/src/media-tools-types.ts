export type MediaToolKey = 'ffmpeg' | 'ffprobe' | 'worker' | 'repository' | 'profiles';
export type MediaToolSettings = Record<MediaToolKey, string> & { useBundled?: boolean };
export type ToolFeature = { available: boolean; reason: string };
export type ToolCapabilities = {
  ffmpeg: ToolFeature;
  ffprobe: ToolFeature;
  drawText: ToolFeature;
  lut3d: ToolFeature;
  exportVideo: ToolFeature;
  encoders: Record<string, ToolFeature>;
  ffmpegVersion: string;
  ffprobeVersion: string;
};
export type MediaToolsView = {
  saved: MediaToolSettings;
  active: MediaToolSettings;
  origins: Record<string, string>;
  restartRequired: boolean;
  warning?: string;
  capabilities: ToolCapabilities;
};
export type MediaToolsInspection = {
  id: string;
  settings: MediaToolSettings;
  canSave: boolean;
  errors: Record<string, string>;
  capabilities: ToolCapabilities;
  native: {
    available: boolean;
    reason?: string;
    mltVersion: string;
    text: { supported: boolean; reason?: string };
    audio: { supported: boolean; reason?: string };
  } | null;
  nativeReason: string;
  checkedAt: string;
};
export type MediaToolsAPI = {
  get(): Promise<MediaToolsView>;
  inspect(input: MediaToolSettings): Promise<MediaToolsInspection>;
  save(id: string): Promise<MediaToolsView>;
  choose(kind: MediaToolKey): Promise<string>;
};
