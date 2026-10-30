import type { Asset, Clip, Settings } from '../types';
import type { WaveformAPI, WaveformRequest, WaveformTile } from '../waveform-types';
import { browserAudioWindow } from './legacy-media';
import { waveformAPI } from './bridge';

export const WAVEFORM_TILE_US = 4_000_000;
const aborted = () => new DOMException('波形读取已取消', 'AbortError');
interface Entry {
  request: WaveformRequest;
  started: boolean;
  dead: boolean;
  listeners: Set<{ resolve: (tile: WaveformTile) => void; reject: (e: unknown) => void }>;
}

// 一个片段依次读取分块，共享在途请求；完成缓存由会检查源文件身份的Go层持有。
export class WaveformQueue {
  private entries = new Map<string, Entry>();
  private queue: Entry[] = [];
  private running = 0;
  private serial = 0;
  constructor(private api: WaveformAPI) {}
  read(projectId: string, asset: Asset, index: number, signal: AbortSignal): Promise<WaveformTile> {
    if (signal.aborted) return Promise.reject(aborted());
    const key = JSON.stringify([projectId, asset.id, asset.fingerprint, asset.path, index]);
    let entry = this.entries.get(key);
    if (!entry) {
      if (this.entries.size >= 128) return Promise.reject(new Error('波形等待较多，请稍后重试。'));
      entry = {
        request: {
          requestId: `wave-${Date.now()}-${++this.serial}`,
          projectId,
          assetId: asset.id,
          fingerprint: asset.fingerprint || '',
          index,
        },
        started: false,
        dead: false,
        listeners: new Set(),
      };
      this.entries.set(key, entry);
      this.queue.push(entry);
    }
    const owned = entry;
    return new Promise((resolve, reject) => {
      const onAbort = () => {
        owned.listeners.delete(listener);
        reject(aborted());
        if (!owned.listeners.size) {
          owned.dead = true;
          if (this.entries.get(key) === owned) this.entries.delete(key);
          if (owned.started) void this.api.cancel(owned.request.requestId).catch(() => {});
          else this.queue = this.queue.filter((e) => e !== owned);
        }
      };
      const listener = {
        resolve: (tile: WaveformTile) => {
          signal.removeEventListener('abort', onAbort);
          resolve(tile);
        },
        reject: (e: unknown) => {
          signal.removeEventListener('abort', onAbort);
          reject(e);
        },
      };
      owned.listeners.add(listener);
      signal.addEventListener('abort', onAbort, { once: true });
      this.pump();
    });
  }
  private pump() {
    while (this.running < 2 && this.queue.length) {
      const entry = this.queue.shift()!;
      if (entry.dead) continue;
      entry.started = true;
      this.running++;
      void this.run(entry);
    }
  }
  private detach(entry: Entry) {
    for (const [key, value] of this.entries) if (value === entry) this.entries.delete(key);
  }
  private async run(entry: Entry) {
    try {
      const tile = await this.api.read(entry.request);
      this.detach(entry);
      if (!entry.dead) for (const listener of entry.listeners) listener.resolve(tile);
    } catch (error) {
      this.detach(entry);
      for (const listener of entry.listeners) listener.reject(error);
    } finally {
      entry.listeners.clear();
      this.running--;
      this.pump();
    }
  }
}
export const waveformQueue = new WaveformQueue(waveformAPI);

export function waveformWindow(
  clip: Clip,
  asset: Asset,
  settings: Settings,
  pixelsPerFrame: number,
  viewLeft: number,
  viewWidth: number,
) {
  const audio = browserAudioWindow(clip, asset, settings);
  if (!audio || asset.missing || !asset.hasAudio) return null;
  const pixelsPerSecond = (pixelsPerFrame * settings.fpsNum) / settings.fpsDen;
  const clipLeft = clip.startFrame * pixelsPerFrame;
  const left = Math.max(clipLeft, viewLeft);
  const right = Math.min(clipLeft + clip.durationFrames * pixelsPerFrame, viewLeft + viewWidth);
  const start = Math.max(left / pixelsPerSecond, audio.timelineStart);
  const end = Math.min(right / pixelsPerSecond, audio.timelineEnd);
  if (end <= start || pixelsPerSecond <= 0) return null;
  const speed = clip.speedNum / clip.speedDen;
  const sourceStartUs = (audio.sourceStart + (start - audio.timelineStart) * speed) * 1e6;
  const sourceEndUs = (audio.sourceStart + (end - audio.timelineStart) * speed) * 1e6;
  const canvasLeft = start * pixelsPerSecond - clipLeft;
  return {
    left: canvasLeft,
    width: (end - start) * pixelsPerSecond,
    sourceStartUs,
    sourceEndUs,
    first: Math.max(0, Math.floor(sourceStartUs / WAVEFORM_TILE_US)),
    last: Math.ceil(sourceEndUs / WAVEFORM_TILE_US) - 1,
  };
}

// 多桶落在同一像素时合并极值，保留短瞬态；无覆盖像素不画成零振幅。
export function addWaveformTile(
  tile: WaveformTile,
  sourceStartUs: number,
  sourceEndUs: number,
  mins: Float32Array,
  maxs: Float32Array,
  coverage: Uint8Array,
) {
  const span = sourceEndUs - sourceStartUs;
  if (span <= 0 || tile.sampleRate <= 0) return;
  let sample = 0;
  for (const peak of tile.peaks) {
    const start = tile.sourceStartUs + (sample * 1e6) / tile.sampleRate;
    const end = start + (peak.samples * 1e6) / tile.sampleRate;
    sample += peak.samples;
    if (end <= sourceStartUs || start >= sourceEndUs) continue;
    const first = Math.max(0, Math.floor(((start - sourceStartUs) / span) * mins.length));
    const last = Math.min(
      mins.length - 1,
      Math.ceil(((end - sourceStartUs) / span) * mins.length) - 1,
    );
    for (let x = first; x <= last; x++) {
      if (!coverage[x]) {
        mins[x] = peak.min;
        maxs[x] = peak.max;
        coverage[x] = 1;
      } else {
        mins[x] = Math.min(mins[x], peak.min);
        maxs[x] = Math.max(maxs[x], peak.max);
      }
    }
  }
}
