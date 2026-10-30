// @vitest-environment jsdom
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { Timeline } from '../src/components/Timeline';
import { TooltipProvider } from '../src/components/ui';
import { defaultClip, PlaybackClock } from '../src/lib/editor';
import type { Project } from '../src/types';

let host: HTMLDivElement, root: Root, clock: PlaybackClock;
let callbacks: Map<number, FrameRequestCallback>, serial: number;
function fixture(): Project {
  const settings = { width: 320, height: 180, fpsNum: 30, fpsDen: 1, sampleRate: 48000 };
  const tracks = Array.from({ length: 200 }, (_, i) => ({
    id: `v${i}`,
    name: `视频 ${i + 1}`,
    kind: 'video' as const,
    hidden: false,
    muted: false,
    locked: false,
  }));
  return {
    id: 'viewport',
    schemaVersion: 1,
    name: '大工程',
    revision: 0,
    settings,
    assets: [],
    tracks,
    clips: tracks.flatMap((track, i) =>
      Array.from({ length: 50 }, (_, j) => ({
        ...defaultClip('image', track.id, j * 240 + i, settings),
        id: `c${i}-${j}`,
        name: `镜头${i}-${j}`,
        durationFrames: 1,
      })),
    ),
  };
}
beforeEach(() => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
  serial = 0;
  callbacks = new Map();
  vi.stubGlobal('requestAnimationFrame', (fn: FrameRequestCallback) => {
    callbacks.set(++serial, fn);
    return serial;
  });
  vi.stubGlobal('cancelAnimationFrame', (id: number) => callbacks.delete(id));
  vi.stubGlobal(
    'ResizeObserver',
    class {
      observe() {}
      disconnect() {}
    },
  );
  vi.spyOn(HTMLElement.prototype, 'clientWidth', 'get').mockReturnValue(1000);
  vi.spyOn(HTMLElement.prototype, 'clientHeight', 'get').mockReturnValue(330);
  Object.assign(HTMLElement.prototype, {
    setPointerCapture: vi.fn(),
    hasPointerCapture: () => false,
    releasePointerCapture: vi.fn(),
  });
  host = document.createElement('div');
  document.body.append(host);
  root = createRoot(host);
  clock = new PlaybackClock();
});
afterEach(async () => {
  await act(async () => root.unmount());
  clock.destroy();
  host.remove();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});
async function frames() {
  await act(async () => {
    const batch = [...callbacks.values()];
    callbacks.clear();
    batch.forEach((fn) => fn(0));
  });
}
async function mount(project = fixture(), selected: string | null = null, update = vi.fn()) {
  await act(async () =>
    root.render(
      <TooltipProvider>
        <Timeline
          project={project}
          clock={clock}
          selected={selected}
          busy={false}
          onSelect={vi.fn()}
          onUpdateClip={update}
          onUpdateTrack={vi.fn()}
          onSplit={vi.fn()}
          onDelete={vi.fn()}
          onText={vi.fn()}
          onAddTrack={vi.fn()}
        />
      </TooltipProvider>,
    ),
  );
  return {
    grid: host.querySelector<HTMLDivElement>('.timeline-grid')!,
    body: host.querySelector<HTMLDivElement>('.timeline-scroll')!,
  };
}
describe('虚拟时间线', () => {
  it('万片段只挂载可见轨道和片段；滚动合并，编号保持全局', async () => {
    const { grid, body } = await mount();
    expect(host.querySelectorAll('.timeline-row').length).toBeLessThan(12);
    expect(host.querySelectorAll('.timeline-clip').length).toBeLessThan(120);
    await act(async () => {
      grid.scrollTop = 32 + 100 * 66;
      body.scrollLeft = 7000;
      for (let i = 0; i < 20; i++) grid.dispatchEvent(new Event('scroll', { bubbles: true }));
    });
    expect(callbacks.size).toBe(1);
    await frames();
    expect(host.querySelector('[data-track-header="v100"]')?.textContent).toContain('V101');
    expect(host.querySelector('[data-track-id="v100"]')).not.toBeNull();
    expect(host.querySelectorAll('.timeline-row').length).toBeLessThan(13);
    expect(host.querySelectorAll('.timeline-clip').length).toBeLessThan(120);
  });
  it('外部选择和显式定位能显示远端片段，普通滚动不改变选择', async () => {
    const p = fixture();
    const { grid, body } = await mount(p, 'c199-40');
    expect(grid.scrollTop).toBeGreaterThan(12000);
    expect(body.scrollLeft).toBeGreaterThan(10000);
    expect(host.querySelector('[data-clip-id="c199-40"]')).not.toBeNull();
    await act(async () => {
      grid.scrollTop = 0;
      body.scrollLeft = 0;
      grid.dispatchEvent(new Event('scroll'));
    });
    await frames();
    expect(grid.scrollTop).toBe(0);
    expect(host.querySelector('[data-clip-id="c199-40"]')).toBeNull();
    const reveal = host.querySelector<HTMLButtonElement>('[aria-label="定位选中片段"]')!;
    await act(async () => reveal.click());
    expect(host.querySelector('[data-clip-id="c199-40"]')).not.toBeNull();
  });
  it('键盘可从首行跳到未挂载尾行，焦点行滚出仍保留', async () => {
    const { grid } = await mount();
    const header = host.querySelector<HTMLElement>('[data-track-header="v0"]')!;
    await act(async () => {
      header.focus();
      header.dispatchEvent(new KeyboardEvent('keydown', { bubbles: true, key: 'End' }));
    });
    expect(document.activeElement?.id).toBe('timeline-track-v199');
    await act(async () => {
      grid.scrollTop = 0;
      grid.dispatchEvent(new Event('scroll'));
    });
    await frames();
    expect(host.querySelector('[data-track-header="v199"]')).not.toBeNull();
    expect(host.querySelectorAll('.timeline-row').length).toBeLessThan(14);
  });
  it('缩小视区仍显示当前选区，锁轨保持与内容正确的行底色', async () => {
    const p = fixture();
    p.tracks[199].locked = true;
    const { grid } = await mount(p, 'c199-40');
    const before = grid.scrollTop;
    Object.defineProperty(grid, 'clientHeight', { configurable: true, value: 132 });
    await act(async () => grid.dispatchEvent(new Event('scroll')));
    await frames();
    expect(grid.scrollTop).toBeGreaterThan(before);
    expect(host.querySelector('[data-track-id="v199"]')?.getAttribute('data-parity')).toBe('even');
    expect(host.querySelector('[data-clip-id="c199-40"]')?.className).toContain('locked');
    expect(host.querySelector('[data-clip-id="c199-40"] .clip-trim-start')).toBeNull();
  });
  it('切换不同工程时清理视区与固定对象，回到新工程开头', async () => {
    const p = fixture();
    const { grid, body } = await mount(p, 'c199-40');
    expect(grid.scrollTop).toBeGreaterThan(10000);
    await mount(
      {
        ...p,
        id: 'another',
        tracks: p.tracks.slice(0, 20),
        clips: p.clips.filter((c) => Number(c.trackId.slice(1)) < 20),
      },
      null,
    );
    expect(grid.scrollTop).toBe(0);
    expect(body.scrollLeft).toBe(0);
    expect(host.querySelector('[data-track-header="v0"]')).not.toBeNull();
  });
  it('拖动时固定旧行并补偿滚动，结束仅提交一次；缺失对象恢复滚动区焦点', async () => {
    const p = fixture(),
      update = vi.fn();
    const { grid, body } = await mount(p, 'c0-0', update);
    const target = host.querySelector<HTMLButtonElement>('[data-clip-id="c0-0"] .clip-face')!;
    await act(async () =>
      target.dispatchEvent(
        new MouseEvent('pointerdown', { bubbles: true, button: 0, clientX: 100, clientY: 100 }),
      ),
    );
    await act(async () => {
      body.scrollLeft = 336;
      grid.scrollTop = 32 + 100 * 66;
      grid.dispatchEvent(new Event('scroll'));
    });
    await frames();
    expect(host.querySelector('[data-clip-id="c0-0"]')).not.toBeNull();
    await act(async () =>
      target.dispatchEvent(
        new MouseEvent('pointerup', { bubbles: true, button: 0, clientX: 100, clientY: 100 }),
      ),
    );
    expect(update).toHaveBeenCalledTimes(1);
    expect(update.mock.calls[0][0]).toMatchObject({ startFrame: 210, trackId: 'v100' });
    await mount({ ...p, clips: p.clips.filter((c) => c.id !== 'c0-0') }, null, update);
    expect(document.activeElement).toBe(grid);
  });
});
