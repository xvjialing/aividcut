import type { Asset, Clip, Settings } from '../types';
import { framesToSeconds } from './editor';
export function browserAudioFadeGain(clip: Clip, settings: Settings, seconds: number) {
  const f = clip.audioFade;
  if (!f) return 1;
  const start = framesToSeconds(clip.startFrame - f.offsetFrames, settings);
  const end = start + framesToSeconds(f.spanFrames, settings);
  return Math.max(
    0,
    Math.min(
      1,
      f.inUs > 0 ? ((seconds - start) * 1e6) / f.inUs : 1,
      f.outUs > 0 ? ((end - seconds) * 1e6) / f.outUs : 1,
    ),
  );
}
export interface BrowserAudioWindow {
  sourceStart: number;
  sourceEnd: number;
  timelineStart: number;
  timelineEnd: number;
  reason: string;
}
export function browserAudioWindow(
  clip: Clip,
  asset: Asset,
  settings: Settings,
): BrowserAudioWindow | null {
  if (!asset.hasAudio || clip.speedNum <= 0 || clip.speedDen <= 0) return null;
  const speed = clip.speedNum / clip.speedDen;
  const clipStart = framesToSeconds(clip.startFrame, settings);
  const range = clip.audioRange;
  let sourceStart = Math.max(clip.sourceInUs, asset.audioStartUs || 0) / 1e6;
  let sourceEnd = Math.min(
    asset.durationUs / 1e6,
    clip.sourceInUs / 1e6 + framesToSeconds(clip.durationFrames, settings) * speed,
  );
  let offset = (sourceStart - clip.sourceInUs / 1e6) / speed;
  let reason = '浏览器工作区声音为近似预览，不能保证采样级精度。';
  if (range) {
    if (
      ![range.start, range.end].every(Number.isSafeInteger) ||
      range.start < 0 ||
      range.end < range.start
    )
      return null;
    if (range.unit === 'samples') {
      if (
        asset.audioSampleCount == null ||
        !Number.isSafeInteger(asset.audioSampleCount) ||
        !range.sampleRate ||
        range.sampleRate !== asset.audioSampleRate ||
        (range.sourceOriginUs || 0) !== (asset.audioStartUs || 0) ||
        range.end > asset.audioSampleCount
      )
        return null;
      sourceStart = (range.sourceOriginUs || 0) / 1e6 + range.start / range.sampleRate;
      sourceEnd = (range.sourceOriginUs || 0) / 1e6 + range.end / range.sampleRate;
    } else if (range.unit === 'us') {
      if (range.sampleRate || range.sourceOriginUs || range.end > asset.durationUs) return null;
      sourceStart = range.start / 1e6;
      sourceEnd = range.end / 1e6;
    } else return null;
    const den = range.offsetDen || ((range.offsetNum || 0) === 0 ? 1 : 0);
    if (den <= 0) return null;
    offset = (range.offsetNum || 0) / den / 1e6;
    if (range.unit === 'samples' || den !== 1)
      reason = '此声音范围含样本或分数偏移，浏览器工作区只能近似限界。';
  } else if (asset.audioSampleCount != null && asset.audioSampleRate) {
    sourceEnd = Math.min(
      sourceEnd,
      (asset.audioStartUs || 0) / 1e6 + asset.audioSampleCount / asset.audioSampleRate,
    );
  }
  if (![sourceStart, sourceEnd, offset].every(Number.isFinite) || sourceEnd < sourceStart)
    return null;
  const timelineStart = clipStart + offset;
  return {
    sourceStart,
    sourceEnd,
    timelineStart,
    timelineEnd: timelineStart + (sourceEnd - sourceStart) / speed,
    reason,
  };
}

// HTML媒体仅作近似工作区预览；真实音尾独立于显示帧占位，耗尽后不自动重播。
export class LegacyMediaBoundary {
  private key = '';
  private initialized = false;
  private nativeEnded = false;
  private audioEnded = false;
  private timer: number | null = null;
  private playing = false;
  private pendingPlay = false;
  private window: BrowserAudioWindow | null = null;
  private audioOnly = false;
  private rate = 1;
  private wasAudible = false;
  private disposed = false;
  reset(key: string) {
    if (key === this.key) return;
    this.clearTimer();
    this.key = key;
    this.initialized = false;
    this.nativeEnded = false;
    this.audioEnded = false;
    this.pendingPlay = false;
    this.wasAudible = false;
  }
  private clearTimer() {
    if (this.timer !== null) window.clearTimeout(this.timer);
    this.timer = null;
  }
  private endAudio(element: HTMLMediaElement) {
    this.audioEnded = true;
    element.muted = true;
    this.clearTimer();
    if (this.audioOnly) element.pause();
  }
  mediaEnded(element: HTMLMediaElement) {
    this.nativeEnded = true;
    this.endAudio(element);
    element.pause();
  }
  checkEnd(element: HTMLMediaElement) {
    if (this.window && element.currentTime >= this.window.sourceEnd) this.endAudio(element);
    if (element.ended) this.mediaEnded(element);
  }
  mediaPlaying(element: HTMLMediaElement) {
    this.clearTimer();
    if (!this.playing || !this.window || this.audioEnded || element.muted) return;
    const remaining = (this.window.sourceEnd - element.currentTime) / this.rate;
    if (remaining <= 0) {
      this.endAudio(element);
      return;
    }
    this.timer = window.setTimeout(
      () => this.endAudio(element),
      Math.min(2147483647, remaining * 1000),
    );
  }
  sync(
    element: HTMLMediaElement,
    clip: Clip,
    asset: Asset,
    settings: Settings,
    timelineSeconds: number,
    playing: boolean,
    active: boolean,
    muted: boolean,
    generation: string,
  ) {
    if (this.disposed) {
      this.disposed = false;
      this.key = '';
    }
    this.reset(`${clip.id}:${generation}`);
    this.audioOnly = clip.kind === 'audio';
    this.rate = clip.speedNum / clip.speedDen;
    this.playing = playing;
    this.window = browserAudioWindow(clip, asset, settings);
    element.loop = false;
    const inAudio =
      active &&
      this.window &&
      timelineSeconds >= this.window.timelineStart &&
      timelineSeconds < this.window.timelineEnd &&
      this.window.sourceEnd > this.window.sourceStart &&
      !this.audioEnded;
    if (inAudio && !this.wasAudible) this.initialized = false;
    this.wasAudible = Boolean(inAudio);
    element.muted = muted || !inAudio;
    element.volume = Math.max(
      0,
      Math.min(1, clip.volume * browserAudioFadeGain(clip, settings, timelineSeconds)),
    );
    try {
      element.playbackRate = this.rate;
    } catch {
      element.muted = true;
      element.pause();
      return;
    }
    if (!active) {
      this.clearTimer();
      element.pause();
      return;
    }
    const target =
      clip.sourceInUs / 1e6 +
      (timelineSeconds - framesToSeconds(clip.startFrame, settings)) * this.rate;
    if (Number.isFinite(element.duration) && element.duration > 0 && target >= element.duration)
      this.mediaEnded(element);
    if (
      !this.nativeEnded &&
      (!this.initialized || Math.abs(element.currentTime - target) > (playing ? 0.15 : 0.015))
    ) {
      try {
        element.currentTime = Math.max(0, target);
        this.initialized = true;
      } catch {
        /* 元数据到达后重试，不从0假播。 */
      }
    }
    if (this.initialized) this.checkEnd(element);
    if (!playing || this.nativeEnded || (this.audioOnly && !inAudio) || !this.initialized) {
      this.clearTimer();
      element.pause();
      return;
    }
    if (element.paused && !this.pendingPlay) {
      this.pendingPlay = true;
      void element
        .play()
        .then(() => {
          if (this.disposed) return;
          this.pendingPlay = false;
          this.mediaPlaying(element);
        })
        .catch(() => {
          this.pendingPlay = false;
        });
    } else if (this.timer === null) this.mediaPlaying(element);
  }
  destroy(element?: HTMLMediaElement) {
    this.disposed = true;
    this.playing = false;
    this.clearTimer();
    if (element) element.pause();
  }
}
