import type { Asset } from '../types';
import type { ThumbnailAPI, ThumbnailRequest, Thumbnail } from '../thumbnail-types';
import { thumbnailAPI } from './bridge';

const aborted = () => new DOMException('缩略图读取已取消', 'AbortError');
interface Entry {
  request: ThumbnailRequest;
  started: boolean;
  dead: boolean;
  listeners: Set<{ resolve: (tile: Thumbnail) => void; reject: (e: unknown) => void }>;
}

// 可见源画面按需读取，共享在途请求；完成缓存由会检查源文件身份的Go层持有。
export class ThumbnailQueue {
  private entries = new Map<string, Entry>();
  private queue: Entry[] = [];
  private running = 0;
  private serial = 0;
  constructor(private api: ThumbnailAPI) {}
  read(
    projectId: string,
    asset: Asset,
    positionUs: number,
    signal: AbortSignal,
    sourceEpoch = 0,
  ): Promise<Thumbnail> {
    if (signal.aborted) return Promise.reject(aborted());
    const key = JSON.stringify([
      sourceEpoch,
      projectId,
      asset.id,
      asset.fingerprint,
      asset.path,
      positionUs,
    ]);
    let entry = this.entries.get(key);
    if (!entry) {
      if (this.entries.size >= 128)
        return Promise.reject(new Error('缩略图等待较多，请稍后重试。'));
      entry = {
        request: {
          sourceEpoch,
          requestId: `thumb-${Date.now()}-${++this.serial}`,
          projectId,
          assetId: asset.id,
          fingerprint: asset.fingerprint || '',
          positionUs,
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
        resolve: (tile: Thumbnail) => {
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
export const thumbnailQueue = new ThumbnailQueue(thumbnailAPI);

// 近末端留出一帧；VFR或尾部无图像时由界面明确报告空取样。
export function thumbnailEnd(asset: Asset): number {
  const frame =
    asset.fpsNum > 0 && asset.fpsDen > 0 ? Math.ceil((1e6 * asset.fpsDen) / asset.fpsNum) : 100_000;
  return Math.max(0, asset.durationUs - Math.max(1, frame));
}
export function thumbnailPositions(asset: Asset): number[] {
  if (asset.kind !== 'video') return [0];
  const end = thumbnailEnd(asset);
  return [...new Set([0, 0.25, 0.5, 0.75, 1].map((part) => Math.round(end * part)))];
}
