import type { Asset, Clip, ClipKind, Project, Settings, Track } from '../types';

export const fps = (settings: Settings) => settings.fpsNum / settings.fpsDen;
export const framesToSeconds = (frames: number, settings: Settings) =>
  (frames * settings.fpsDen) / settings.fpsNum;
export const secondsToFrames = (seconds: number, settings: Settings) =>
  Math.round(seconds * fps(settings));
export const projectEnd = (project: Project) =>
  Math.max(0, ...project.clips.map((clip) => clip.startFrame + clip.durationFrames));
export const trackNames: Record<string, string> = {
  video: '视频',
  audio: '音频',
  text: '文字',
  caption: '字幕',
  image: '图片',
  lut: 'LUT',
  font: '字体',
};
export const canPlace = (track: Track, kind: ClipKind) =>
  track.kind === kind || (track.kind === 'video' && kind === 'image');

export function timecode(frame: number, settings: Settings): string {
  const whole = Math.max(0, Math.floor(frame));
  const rate = Math.round(fps(settings));
  const totalSeconds = Math.floor(framesToSeconds(whole, settings));
  const ff = Math.min(rate - 1, Math.floor(whole - secondsToFrames(totalSeconds, settings)));
  return [
    Math.floor(totalSeconds / 3600),
    Math.floor(totalSeconds / 60) % 60,
    totalSeconds % 60,
    Math.max(0, ff),
  ]
    .map((n) => String(n).padStart(2, '0'))
    .join(':');
}

export function defaultClip(
  kind: ClipKind,
  trackId: string,
  startFrame: number,
  settings: Settings,
  asset?: Asset,
): Clip {
  return {
    id: crypto.randomUUID(),
    trackId,
    kind,
    assetId: asset?.id,
    name: asset?.name || (kind === 'caption' ? '字幕' : '标题文字'),
    startFrame,
    durationFrames:
      asset && asset.durationUs > 0
        ? Math.max(1, Math.floor((asset.durationUs * settings.fpsNum) / (1e6 * settings.fpsDen)))
        : Math.round(fps(settings) * 4),
    sourceInUs: 0,
    speedNum: 1,
    speedDen: 1,
    volume: 1,
    muted: false,
    transform: { x: 0, y: 0, scale: 1, rotation: 0 },
    text: kind === 'text' ? '在这里输入标题' : kind === 'caption' ? '在这里输入字幕' : undefined,
    textStyle: {
      fontSize: kind === 'caption' ? 42 : 64,
      color: '#ffffff',
      background: '',
      align: 'center',
    },
  };
}

export function clipAt(project: Project, frame: number): Clip[] {
  return project.clips.filter((c) => {
    if (c.startFrame > frame || c.startFrame + c.durationFrames <= frame) return false;
    const track = project.tracks.find((t) => t.id === c.trackId);
    if (!track?.hidden) return true;
    // 隐藏仅控制画面；仍可发声的视频/音频保留媒体元素。
    return (
      (c.kind === 'video' || c.kind === 'audio') &&
      !track.muted &&
      !c.muted &&
      c.volume > 0 &&
      Boolean(project.assets.find((a) => a.id === c.assetId)?.hasAudio)
    );
  });
}

export function filterCaptions(project: Project, query = ''): Clip[] {
  const needle = query.toLocaleLowerCase();
  return project.clips
    .filter(
      (clip) =>
        clip.kind === 'caption' &&
        `${clip.name}\n${clip.text || ''}`.toLocaleLowerCase().includes(needle),
    )
    .sort((left, right) => left.startFrame - right.startFrame);
}

export function trimClip(
  clip: Clip,
  edge: 'start' | 'end',
  deltaFrames: number,
  settings: Settings,
): Clip {
  if (edge === 'end')
    return { ...clip, durationFrames: Math.max(1, clip.durationFrames + deltaFrames) };
  const bounded = Math.max(-clip.startFrame, Math.min(clip.durationFrames - 1, deltaFrames));
  const sourceChange = clip.assetId
    ? Math.round((framesToSeconds(bounded, settings) * 1e6 * clip.speedNum) / clip.speedDen)
    : 0;
  if (clip.sourceInUs + sourceChange < 0) return clip;
  return {
    ...clip,
    startFrame: clip.startFrame + bounded,
    durationFrames: clip.durationFrames - bounded,
    sourceInUs: clip.sourceInUs + sourceChange,
  };
}

type Listener = () => void;
export interface ExternalPlaybackDriver {
  play(): void;
  pause(): void;
  seek(frame: number): void;
  invalidate(): void;
}

// 播放时钟独立于 React 状态树；只有订阅者更新播放头、媒体元素和时间码。
export class PlaybackClock {
  private listeners = new Set<Listener>();
  private frame = 0;
  private playing = false;
  private end = 0;
  private rate = 30;
  private originTime = 0;
  private originFrame = 0;
  private raf = 0;
  private external: ExternalPlaybackDriver | null = null;
  hasExternalDriver = () => this.external !== null;
  private seekSerial = 0;
  getSeekSerial = () => this.seekSerial;
  getTimeSeconds = () =>
    this.external || !this.playing
      ? this.frame / this.rate
      : Math.min(
          this.end / this.rate,
          this.originFrame / this.rate + (performance.now() - this.originTime) / 1000,
        );
  getFrame = () => this.frame;
  isPlaying = () => this.playing;
  subscribe = (listener: Listener) => {
    this.listeners.add(listener);
    return () => {
      this.listeners.delete(listener);
    };
  };
  private emit = () => this.listeners.forEach((listener) => listener());
  configure(end: number, rate: number) {
    this.end = end;
    this.rate = rate;
    if (this.frame > end) {
      if (this.external) this.synchronizeExternal(end, false);
      else this.seek(end);
    }
  }
  attachExternal(driver: ExternalPlaybackDriver): () => void {
    if (this.external && this.external !== driver) this.external.invalidate();
    cancelAnimationFrame(this.raf);
    this.external = driver;
    this.playing = false;
    this.emit();
    return () => {
      if (this.external !== driver) return;
      this.external = null;
      this.playing = false;
      this.emit();
    };
  }
  synchronizeExternal(frame: number, playing: boolean) {
    if (!this.external || !Number.isFinite(frame)) return;
    const next = Math.max(0, Math.floor(frame));
    if (this.frame === next && this.playing === playing) return;
    this.frame = next;
    this.playing = playing;
    this.emit();
  }
  invalidateExternal() {
    this.external?.invalidate();
  }
  seek(frame: number) {
    this.seekSerial++;
    if (this.external) {
      this.external.seek(Math.max(0, Math.round(frame)));
      return;
    }
    this.frame = Math.max(0, Math.round(frame));
    this.originFrame = this.frame;
    this.originTime = performance.now();
    this.emit();
  }
  toggle() {
    this.playing ? this.pause() : this.play();
  }
  play() {
    if (this.external) {
      this.external.play();
      return;
    }
    if (!this.end || this.playing) return;
    if (this.frame >= this.end) {
      this.frame = 0;
      this.seekSerial++;
    }
    this.playing = true;
    this.originTime = performance.now();
    this.originFrame = this.frame;
    this.emit();
    this.raf = requestAnimationFrame(this.tick);
  }
  private tick = (now: number) => {
    this.frame = Math.min(
      this.end,
      this.originFrame + Math.floor(((now - this.originTime) / 1000) * this.rate),
    );
    if (this.frame >= this.end) {
      this.pause();
      return;
    }
    this.emit();
    this.raf = requestAnimationFrame(this.tick);
  };
  pause() {
    if (this.external) {
      this.external.pause();
      return;
    }
    this.playing = false;
    cancelAnimationFrame(this.raf);
    this.emit();
  }
  destroy() {
    this.external?.invalidate();
    this.external = null;
    cancelAnimationFrame(this.raf);
    this.listeners.clear();
  }
}
