// @vitest-environment jsdom
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { ClipWaveform } from '../src/components/ClipWaveform';
import { Timeline } from '../src/components/Timeline';
import { TooltipProvider } from '../src/components/ui';
import { WaveformQueue } from '../src/lib/waveform';
import { defaultClip, PlaybackClock } from '../src/lib/editor';
import type { Asset, Settings } from '../src/types';
import type { WaveformTile } from '../src/waveform-types';

const settings: Settings = { width: 32, height: 32, fpsNum: 30, fpsDen: 1, sampleRate: 48000 };
const asset: Asset = {
  id: 'a',
  name: '讲解',
  kind: 'audio',
  path: '/a.wav',
  durationUs: 2e6,
  width: 0,
  height: 0,
  fpsNum: 0,
  fpsDen: 0,
  hasAudio: true,
  missing: false,
};
const clip = defaultClip('audio', 't', 0, settings, asset);
const tile: WaveformTile = {
  index: 0,
  sourceStartUs: 0,
  sampleRate: 48000,
  channels: 1,
  bucketSamples: 480,
  sampleCount: 480,
  peaks: [{ min: -0.3, max: 0.8, samples: 480 }],
};
let element: HTMLDivElement, root: Root;
let stroke: ReturnType<typeof vi.fn>;
let lineTo: ReturnType<typeof vi.fn>;
beforeEach(() => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
  stroke = vi.fn();
  lineTo = vi.fn();
  vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue({
    clearRect: vi.fn(),
    beginPath: vi.fn(),
    moveTo: vi.fn(),
    lineTo,
    stroke,
  } as unknown as CanvasRenderingContext2D);
  vi.stubGlobal('requestAnimationFrame', (fn: FrameRequestCallback) => setTimeout(() => fn(0), 0));
  vi.stubGlobal('cancelAnimationFrame', clearTimeout);
  vi.stubGlobal(
    'ResizeObserver',
    class {
      observe() {}
      disconnect() {}
    },
  );
  Object.assign(HTMLElement.prototype, {
    setPointerCapture: vi.fn(),
    hasPointerCapture: () => false,
    releasePointerCapture: vi.fn(),
  });
  element = document.createElement('div');
  document.body.append(element);
  root = createRoot(element);
});
afterEach(async () => {
  await act(async () => root.unmount());
  element.remove();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});
async function render(queue: WaveformQueue, input = asset) {
  await act(async () => {
    root.render(
      <ClipWaveform
        projectId="p"
        asset={input}
        clip={clip}
        settings={settings}
        pixelsPerFrame={2}
        viewLeft={0}
        viewWidth={60}
        queue={queue}
      />,
    );
  });
}

describe('片段波形状态', () => {
  it('浮点源超过满幅时仍显示在画布边缘，负向过载不消失', async () => {
    const api = {
      read: vi.fn(async () => ({ ...tile, peaks: [{ min: -2, max: -1.5, samples: 480 }] })),
      cancel: vi.fn(async () => {}),
    };
    await render(new WaveformQueue(api));
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 10));
    });
    expect(lineTo).toHaveBeenCalled();
    const height = element.querySelector('canvas')!.height;
    expect(lineTo.mock.calls.every(([, y]) => y >= 0 && y <= height)).toBe(true);
  });
  it('纵向不可见时不读取，离开视区取消，重新进入后忽略旧结果', async () => {
    let notify!: (entries: { isIntersecting: boolean }[]) => void;
    vi.stubGlobal(
      'IntersectionObserver',
      class {
        constructor(callback: typeof notify) {
          notify = callback;
        }
        observe() {}
        disconnect() {}
      },
    );
    let resolve!: (value: WaveformTile) => void;
    const api = {
      read: vi
        .fn()
        .mockImplementationOnce(
          () =>
            new Promise<WaveformTile>((done) => {
              resolve = done;
            }),
        )
        .mockResolvedValue(tile),
      cancel: vi.fn(async () => {}),
    };
    await render(new WaveformQueue(api));
    expect(api.read).not.toHaveBeenCalled();
    await act(async () => notify([{ isIntersecting: true }]));
    expect(api.read).toHaveBeenCalledTimes(1);
    await act(async () => notify([{ isIntersecting: false }]));
    expect(api.cancel).toHaveBeenCalledTimes(1);
    expect(element.querySelector('canvas')!.width).toBe(0);
    await act(async () => notify([{ isIntersecting: true }]));
    await act(async () => resolve({ ...tile, sampleCount: 0, peaks: [] }));
    expect(element.querySelector('[data-waveform-state="ready"]')).not.toBeNull();
    expect(api.read).toHaveBeenCalledTimes(2);
  });
  it('已选片段把手点击后保留焦点，方向键直接修剪，不重复选择造成失焦', async () => {
    const source = { ...asset, kind: 'video', hasAudio: false };
    const video = defaultClip('video', 't', 0, settings, source);
    const onSelect = vi.fn(() => (document.activeElement as HTMLElement).blur());
    const onUpdate = vi.fn();
    const clock = new PlaybackClock();
    await act(async () =>
      root.render(
        <TooltipProvider>
          <Timeline
            project={{
              id: 'p',
              schemaVersion: 1,
              name: '片段',
              revision: 0,
              settings,
              assets: [source],
              clips: [video],
              tracks: [
                {
                  id: 't',
                  name: '视频',
                  kind: 'video',
                  locked: false,
                  hidden: false,
                  muted: false,
                },
              ],
            }}
            selected={video.id}
            busy={false}
            clock={clock}
            onSelect={onSelect}
            onUpdateClip={onUpdate}
            onUpdateTrack={vi.fn()}
            onSplit={vi.fn()}
            onDelete={vi.fn()}
            onText={vi.fn()}
            onAddTrack={vi.fn()}
          />
        </TooltipProvider>,
      ),
    );
    const handle = element.querySelector<HTMLButtonElement>('.clip-trim-start')!;
    const owner = element.querySelector<HTMLElement>('.timeline-grid')!;
    Object.defineProperties(owner, {
      scrollHeight: { configurable: true, value: 600 },
      clientHeight: { configurable: true, value: 200 },
    });
    const vertical = new WheelEvent('wheel', { bubbles: true, cancelable: true, deltaY: 80 });
    handle.dispatchEvent(vertical);
    expect(owner.scrollTop).toBe(80);
    expect(vertical.defaultPrevented).toBe(true);
    const horizontal = new WheelEvent('wheel', {
      bubbles: true,
      cancelable: true,
      deltaY: 80,
      shiftKey: true,
    });
    handle.dispatchEvent(horizontal);
    expect(owner.scrollTop).toBe(80);
    expect(horizontal.defaultPrevented).toBe(false);
    await act(async () => {
      handle.dispatchEvent(new MouseEvent('pointerdown', { bubbles: true, button: 0 }));
      handle.dispatchEvent(new MouseEvent('pointerup', { bubbles: true, button: 0 }));
      handle.click();
    });
    expect(document.activeElement).toBe(handle);
    expect(onSelect).not.toHaveBeenCalled();
    await act(async () =>
      handle.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowRight', bubbles: true })),
    );
    expect(onUpdate).toHaveBeenCalledWith(
      expect.objectContaining({ startFrame: 1, durationFrames: video.durationFrames - 1 }),
    );
    clock.destroy();
  });
  it('稳定占位、失败和键盘可操作重试；成功后保留真实Canvas', async () => {
    let reject!: (e: Error) => void;
    const api = {
      read: vi
        .fn()
        .mockImplementationOnce(
          () =>
            new Promise<WaveformTile>((_, fail) => {
              reject = fail;
            }),
        )
        .mockResolvedValue(tile),
      cancel: vi.fn(async () => {}),
    };
    await render(new WaveformQueue(api));
    expect(element.textContent).toContain('读取波形');
    const width = element.querySelector('canvas')!.width;
    expect(width).toBe(60);
    await act(async () => {
      reject(new Error('源文件无法读取'));
    });
    const button = element.querySelector('button')!;
    expect(button.getAttribute('aria-label')).toContain('源文件无法读取');
    button.focus();
    expect(document.activeElement).toBe(button);
    await act(async () => button.click());
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 10));
    });
    expect(api.read).toHaveBeenCalledTimes(2);
    expect(element.querySelector('[data-waveform-state="ready"]')).not.toBeNull();
    expect(stroke).toHaveBeenCalled();
    expect(element.querySelector('canvas')!.width).toBe(width);
  });
  it('切换源取消旧请求，旧成功不覆盖新源错误；缺失素材不请求', async () => {
    let resolve!: (v: WaveformTile) => void;
    const api = {
      read: vi
        .fn()
        .mockImplementationOnce(
          () =>
            new Promise<WaveformTile>((done) => {
              resolve = done;
            }),
        )
        .mockRejectedValue(new Error('新源不可用')),
      cancel: vi.fn(async () => {}),
    };
    const queue = new WaveformQueue(api);
    await render(queue);
    await render(queue, { ...asset, fingerprint: 'new' });
    expect(api.cancel).toHaveBeenCalledTimes(1);
    await act(async () => resolve(tile));
    expect(element.querySelector('[data-waveform-state="error"]')).not.toBeNull();
    await render(queue, { ...asset, missing: true });
    expect(element.querySelector('canvas')).toBeNull();
    expect(api.read).toHaveBeenCalledTimes(2);
  });
  it('样本为空与真实静音区分，不画伪波形', async () => {
    const api = {
      read: vi.fn(async () => ({ ...tile, peaks: [], sampleCount: 0 })),
      cancel: vi.fn(async () => {}),
    };
    await render(new WaveformQueue(api));
    expect(element.textContent).toContain('此区间无音频样本');
  });
});
