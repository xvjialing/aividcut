import { describe, expect, it, vi } from 'vitest';
import { addWaveformTile, WaveformQueue, waveformWindow } from '../src/lib/waveform';
import { defaultClip } from '../src/lib/editor';
import type { Asset, Settings } from '../src/types';
import type { WaveformTile } from '../src/waveform-types';

const settings: Settings = { width: 32, height: 32, fpsNum: 30, fpsDen: 1, sampleRate: 48000 };
const asset: Asset = {
  id: 'voice',
  name: '讲解',
  kind: 'audio',
  path: '/voice.wav',
  fingerprint: 'a',
  durationUs: 12e6,
  width: 0,
  height: 0,
  fpsNum: 0,
  fpsDen: 0,
  hasAudio: true,
  missing: false,
};
const tile: WaveformTile = {
  index: 0,
  sourceStartUs: 0,
  sampleRate: 48000,
  channels: 2,
  bucketSamples: 480,
  sampleCount: 960,
  peaks: [
    { min: -0.9, max: 0.7, samples: 480 },
    { min: 0, max: 0, samples: 480 },
  ],
};
const flush = async () => {
  for (let i = 0; i < 8; i++) await Promise.resolve();
};
function deferred<T>() {
  let resolve!: (v: T) => void;
  let reject!: (e: unknown) => void;
  const promise = new Promise<T>((a, b) => {
    resolve = a;
    reject = b;
  });
  return { promise, resolve, reject };
}

describe('源音频波形时间与像素', () => {
  it('可见区、修剪和两倍速共同裁剪源窗口，不分配整条片段宽度', () => {
    const clip = {
      ...defaultClip('audio', 'a', 30, settings, asset),
      durationFrames: 150,
      sourceInUs: 1e6,
      speedNum: 2,
    };
    const view = waveformWindow(clip, asset, settings, 100 / 30, 200, 100)!;
    expect(view.left).toBe(100);
    expect(view.width).toBe(100);
    expect(view.sourceStartUs).toBe(3e6);
    expect(view.sourceEndUs).toBe(5e6);
    expect([view.first, view.last]).toEqual([0, 1]);
  });
  it('分数offset和29.97fps保留真实声音起点及末样本，不绘制显示帧的尾部空白', () => {
    const fractional = { ...settings, fpsNum: 30000, fpsDen: 1001 };
    const measured = { ...asset, audioSampleRate: 48000, audioSampleCount: 5777, audioStartUs: 0 };
    const clip = {
      ...defaultClip('audio', 'a', 30, fractional, measured),
      durationFrames: 4,
      audioRange: {
        unit: 'samples' as const,
        start: 1,
        end: 5777,
        sampleRate: 48000,
        offsetNum: 1e6,
        offsetDen: 48000,
      },
    };
    const view = waveformWindow(clip, measured, fractional, 1, 0, 1000)!;
    expect(view.sourceStartUs).toBeCloseTo(1e6 / 48000, 8);
    expect(view.sourceEndUs).toBeCloseTo(5777e6 / 48000, 6);
    expect(view.left).toBeCloseTo(((1 / 48000) * 30000) / 1001, 10);
    expect(view.width).toBeCloseTo(((5776 / 48000) * 30000) / 1001, 10);
  });
  it('晚起音轨留出真实前空白，缺失/无音轨/视区外不请求', () => {
    const clip = defaultClip('video', 'v', 0, settings, asset);
    expect(waveformWindow(clip, { ...asset, audioStartUs: 500000 }, settings, 1, 0, 30)?.left).toBe(
      15,
    );
    expect(waveformWindow(clip, { ...asset, missing: true }, settings, 1, 0, 30)).toBeNull();
    expect(waveformWindow(clip, { ...asset, hasAudio: false }, settings, 1, 0, 30)).toBeNull();
    expect(waveformWindow(clip, asset, settings, 1, 1000, 100)).toBeNull();
    const longSource = { ...asset, durationUs: 25 * 3600e6 };
    const far = waveformWindow(
      { ...clip, sourceInUs: 24 * 3600e6, durationFrames: 30 },
      longSource,
      settings,
      1,
      0,
      30,
    )!;
    expect([far.first, far.last]).toEqual([21600, 21600]); // 超出后端上界时明确报错，不能被截断成假空覆盖。
  });
  it('密集桶合并保留瞬态，静音有覆盖，未采样区不伪装静音', () => {
    const mins = new Float32Array(2),
      maxs = new Float32Array(2),
      coverage = new Uint8Array(2);
    addWaveformTile(tile, 0, 40000, mins, maxs, coverage);
    expect(Array.from(coverage)).toEqual([1, 0]);
    expect(mins[0]).toBeCloseTo(-0.9);
    expect(maxs[0]).toBeCloseTo(0.7);
  });
});

describe('波形共享队列', () => {
  it('完成回调立即重读同一分块不会挂在已完成请求上；同步错误不占用队列', async () => {
    const api = {
      read: vi
        .fn()
        .mockImplementationOnce(() => {
          throw new Error('同步错误');
        })
        .mockResolvedValue(tile),
      cancel: vi.fn(async () => {}),
    };
    const queue = new WaveformQueue(api);
    await expect(queue.read('p', asset, 0, new AbortController().signal)).rejects.toThrow(
      '同步错误',
    );
    await queue
      .read('p', asset, 0, new AbortController().signal)
      .then(() => queue.read('p', asset, 0, new AbortController().signal));
    expect(api.read).toHaveBeenCalledTimes(3);
  });
  it('同分块合并；一个订阅取消不影响另一个，最后一个取消传到Go', async () => {
    const pending = deferred<WaveformTile>();
    const api = { read: vi.fn(() => pending.promise), cancel: vi.fn(async () => {}) };
    const queue = new WaveformQueue(api);
    const a = new AbortController(),
      b = new AbortController();
    const pa = queue.read('p', asset, 0, a.signal).catch((e: Error) => e.name);
    const pb = queue.read('p', asset, 0, b.signal).catch((e: Error) => e.name);
    expect(api.read).toHaveBeenCalledTimes(1);
    a.abort();
    expect(api.cancel).not.toHaveBeenCalled();
    b.abort();
    expect(api.cancel).toHaveBeenCalledTimes(1);
    expect(await pa).toBe('AbortError');
    expect(await pb).toBe('AbortError');
    pending.resolve(tile);
    await flush();
  });
  it('最多两个读取；排队取消不启动；错误后可重新请求', async () => {
    const pending = [deferred<WaveformTile>(), deferred<WaveformTile>(), deferred<WaveformTile>()];
    let next = 0;
    const api = { read: vi.fn(() => pending[next++].promise), cancel: vi.fn(async () => {}) };
    const queue = new WaveformQueue(api);
    const controllers = [0, 1, 2].map(() => new AbortController());
    const results = controllers.map((c, i) =>
      queue.read('p', asset, i, c.signal).catch((e: Error) => e.message),
    );
    expect(api.read).toHaveBeenCalledTimes(2);
    controllers[2].abort();
    pending[0].reject(new Error('工具不可用'));
    pending[1].resolve(tile);
    await Promise.all(results);
    await flush();
    expect(api.read).toHaveBeenCalledTimes(2);
    const retry = queue.read('p', asset, 0, new AbortController().signal);
    expect(api.read).toHaveBeenCalledTimes(3);
    pending[2].resolve(tile);
    expect(await retry).toEqual(tile);
  });
  it('工程和源指纹分离请求，不复用其他工程的在途结果', async () => {
    const api = { read: vi.fn(async () => tile), cancel: vi.fn(async () => {}) };
    const queue = new WaveformQueue(api);
    await Promise.all([
      queue.read('p', asset, 0, new AbortController().signal),
      queue.read('p2', asset, 0, new AbortController().signal),
      queue.read('p', { ...asset, fingerprint: 'changed' }, 0, new AbortController().signal),
    ]);
    expect(api.read).toHaveBeenCalledTimes(3);
  });
});
