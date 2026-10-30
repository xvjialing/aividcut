// @vitest-environment jsdom
import { act } from 'react';
import { readFileSync } from 'node:fs';
import { createRoot, type Root } from 'react-dom/client';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { RealtimePreviewSurface } from '../src/components/RealtimePreviewSurface';
import { PlaybackClock } from '../src/lib/editor';
import type { RealtimeViewState } from '../src/lib/realtime-playback';
import { fakePlaybackAPI, playbackProject } from './playback-fixtures';

const playback = vi.hoisted(() => ({
  publish: null as ((state: RealtimeViewState) => void) | null,
}));
vi.mock('../src/lib/realtime-playback', () => ({
  RealtimePlayback: class {
    constructor(options: { onState: (state: RealtimeViewState) => void }) {
      playback.publish = options.onState;
    }
    async open() {}
    block() {}
    dispose() {}
  },
}));

let container: HTMLDivElement;
let root: Root;
let style: HTMLStyleElement;
let clock: PlaybackClock;
let fetchGuard: ReturnType<typeof vi.fn>;
beforeEach(() => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
  fetchGuard = vi.fn().mockRejectedValue(new Error('布局测试禁止真实播放服务'));
  vi.stubGlobal('fetch', fetchGuard);
  container = document.createElement('div');
  document.body.append(container);
  style = document.createElement('style');
  style.textContent = readFileSync(`${process.cwd()}/src/styles/app.css`, 'utf8');
  document.head.append(style);
  root = createRoot(container);
  clock = new PlaybackClock();
});
afterEach(async () => {
  await act(async () => root.unmount());
  clock.destroy();
  container.remove();
  style.remove();
  playback.publish = null;
  expect(fetchGuard).not.toHaveBeenCalled();
  vi.unstubAllGlobals();
});
async function render() {
  const onRetry = vi.fn();
  const onFallback = vi.fn();
  await act(async () => {
    root.render(
      <RealtimePreviewSurface
        project={playbackProject}
        clock={clock}
        api={fakePlaybackAPI()}
        allowed
        reason=""
        onRetry={onRetry}
        onFallback={onFallback}
      />,
    );
  });
  return { onRetry, onFallback };
}
describe('实时画面与状态独立布局', () => {
  it('准备、暂停和结尾状态位于画面之后的独立行，Canvas等比完整呈现', async () => {
    await render();
    for (const phase of ['ready', 'paused', 'ended'] as const) {
      await act(async () => {
        playback.publish!({ phase, reason: '', width: 640, height: 360 });
      });
      const surface = container.querySelector<HTMLElement>('.realtime-preview')!;
      const picture = surface.querySelector<HTMLElement>('.realtime-preview-picture')!;
      const status = surface.querySelector<HTMLElement>('[role="status"]')!;
      const canvas = picture.querySelector('canvas')!;
      expect(picture.nextElementSibling).toBe(status);
      expect(picture.contains(status)).toBe(false);
      expect(getComputedStyle(surface).display).toBe('grid');
      expect(getComputedStyle(surface).gridTemplateRows).toBe('minmax(0, 1fr) auto');
      expect(getComputedStyle(status).position).not.toBe('absolute');
      expect(getComputedStyle(canvas).objectFit).toBe('contain');
      expect(status.textContent).toContain('640×360');
    }
  });
  it('失败说明仍在独立状态行，保留明确重试与返回入口', async () => {
    const actions = await render();
    await act(async () => {
      playback.publish!({ phase: 'failed', reason: '此WebView无法创建音频输出。' });
    });
    const status = container.querySelector('[role="alert"]')!;
    expect(status.textContent).toContain('此WebView无法创建音频输出');
    expect(status.previousElementSibling?.className).toBe('realtime-preview-picture');
    const buttons = Array.from(status.querySelectorAll('button'));
    expect(buttons.map((button) => button.textContent)).toEqual(['重新准备', '返回工作区预览']);
    await act(async () => {
      buttons[0].click();
      buttons[1].click();
    });
    expect(actions.onRetry).toHaveBeenCalledOnce();
    expect(actions.onFallback).toHaveBeenCalledOnce();
  });
});
