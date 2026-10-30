import { describe, expect, it, vi } from 'vitest';
import { ThumbnailQueue, thumbnailPositions, thumbnailEnd } from '../src/lib/thumbnail';
import type { Asset } from '../src/types';
import type { Thumbnail } from '../src/thumbnail-types';

export const asset: Asset = {
  id: 'a',
  name: '镜头',
  path: '/a.mp4',
  kind: 'video',
  durationUs: 4e6,
  width: 160,
  height: 90,
  fpsNum: 10,
  fpsDen: 1,
  hasAudio: false,
  missing: false,
  fingerprint: 'v1',
};
const thumbnail: Thumbnail = {
  assetId: 'a',
  positionUs: 0,
  url: 'http://127.0.0.1/controlled.jpg',
  width: 320,
  height: 180,
};
const tick = async () => {
  for (let i = 0; i < 8; i++) await Promise.resolve();
};
function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((r) => (resolve = r));
  return { promise, resolve };
}
describe('源画面请求队列', () => {
  it('同源取样共享在途请求，最后一个订阅者退出才取消', async () => {
    const pending = deferred<Thumbnail>();
    const api = { read: vi.fn(() => pending.promise), cancel: vi.fn(async () => {}) };
    const queue = new ThumbnailQueue(api);
    const a = new AbortController(),
      b = new AbortController();
    const one = queue.read('p', asset, 0, a.signal).catch((e) => e);
    const two = queue.read('p', asset, 0, b.signal).catch((e) => e);
    a.abort();
    expect((await one).name).toBe('AbortError');
    expect(api.cancel).not.toHaveBeenCalled();
    b.abort();
    expect((await two).name).toBe('AbortError');
    expect(api.cancel).toHaveBeenCalledTimes(1);
    pending.resolve(thumbnail);
    await tick();
    expect(api.read).toHaveBeenCalledTimes(1);
  });
  it('限制两个并发，排队取消不启动，过期完成不删除新请求', async () => {
    const list = Array.from({ length: 5 }, () => deferred<Thumbnail>());
    let n = 0;
    const api = { read: vi.fn(() => list[n++].promise), cancel: vi.fn(async () => {}) };
    const queue = new ThumbnailQueue(api);
    const a = new AbortController();
    const old = queue.read('p', asset, 0, a.signal).catch((e) => e);
    const two = queue.read('p', asset, 1, new AbortController().signal);
    const canceled = new AbortController();
    const three = queue.read('p', asset, 2, canceled.signal).catch((e) => e);
    expect(api.read).toHaveBeenCalledTimes(2);
    canceled.abort();
    a.abort();
    await old;
    await three;
    const fresh = queue.read('p', asset, 0, new AbortController().signal);
    list[0].resolve(thumbnail);
    await tick();
    expect(api.read).toHaveBeenCalledTimes(3);
    const shared = queue.read('p', asset, 0, new AbortController().signal);
    list[2].resolve(thumbnail);
    list[1].resolve(thumbnail);
    await Promise.all([two, fresh, shared]);
    expect(api.read).toHaveBeenCalledTimes(3);
  });
  it('128个在途条目上界，失败后可以重试', async () => {
    const pending = deferred<Thumbnail>();
    const api = { read: vi.fn(() => pending.promise), cancel: vi.fn(async () => {}) };
    const queue = new ThumbnailQueue(api);
    const controller = new AbortController();
    const promises = Array.from({ length: 128 }, (_, i) =>
      queue.read('p', asset, i, controller.signal).catch((e) => e),
    );
    await expect(queue.read('p', asset, 129, controller.signal)).rejects.toThrow('等待');
    controller.abort();
    await Promise.all(promises);
    pending.resolve(thumbnail);
    await tick();
    const fail = {
      read: vi.fn().mockRejectedValueOnce(new Error('源缺失')).mockResolvedValue(thumbnail),
      cancel: vi.fn(async () => {}),
    };
    const retry = new ThumbnailQueue(fail);
    await expect(retry.read('p', asset, 0, new AbortController().signal)).rejects.toThrow('源缺失');
    await expect(retry.read('p', asset, 0, new AbortController().signal)).resolves.toEqual(
      thumbnail,
    );
  });
  it('取样保留近末帧，极短片段去重，不请求边界外位置', () => {
    expect(thumbnailPositions(asset)).toEqual([0, 975000, 1950000, 2925000, 3900000]);
    expect(thumbnailEnd({ ...asset, durationUs: 1 })).toBe(0);
    expect(thumbnailPositions({ ...asset, durationUs: 1 })).toEqual([0]);
    expect(thumbnailPositions({ ...asset, kind: 'image' })).toEqual([0]);
  });
});
