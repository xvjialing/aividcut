// @vitest-environment jsdom
import { installDesktop } from './desktop-fixture';
vi.mock('../src/lib/desktop', async () => (await import('./desktop-fixture')).desktopModule);

import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import App from '../src/App';
import type { Bootstrap } from '../src/types';

let container: HTMLDivElement;
let root: Root;

function snapshot(missing = true): Bootstrap {
  return {
    dirty: false,
    projectPath: '/影片.aividcut',
    canUndo: false,
    canRedo: false,
    capabilities: {
      exportVideo: { available: false, reason: '测试环境' },
      previewFrame: { available: false, reason: '测试环境' },
    },
    project: {
      id: 'project',
      schemaVersion: 1,
      name: '影片',
      revision: 0,
      settings: { width: 1920, height: 1080, fpsNum: 30, fpsDen: 1, sampleRate: 48000 },
      assets: [
        {
          id: 'asset-stable',
          name: '镜头.mp4',
          kind: 'video',
          path: '/旧位置/镜头.mp4',
          durationUs: 3003000,
          width: 1920,
          height: 1080,
          fpsNum: 30,
          fpsDen: 1,
          hasAudio: false,
          missing,
          fingerprint: 'original-fingerprint',
        },
      ],
      tracks: [
        {
          id: 'video-1',
          kind: 'video',
          name: '视频 1',
          locked: false,
          muted: false,
          hidden: false,
        },
      ],
      clips: [
        {
          id: 'clip-stable',
          trackId: 'video-1',
          assetId: 'asset-stable',
          kind: 'video',
          name: '镜头片段',
          startFrame: 10,
          durationFrames: 60,
          sourceInUs: 33367,
          speedNum: 1,
          speedDen: 1,
          volume: 1,
          muted: false,
          transform: { x: 0, y: 0, scale: 1, rotation: 0 },
          textStyle: { fontSize: 48, color: '#ffffff', background: '', align: 'center' },
        },
      ],
    },
  };
}

beforeEach(() => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
  vi.stubGlobal(
    'ResizeObserver',
    class {
      observe() {}
      disconnect() {}
      unobserve() {}
    },
  );
  vi.stubGlobal('cancelAnimationFrame', vi.fn());
  vi.spyOn(HTMLElement.prototype, 'clientWidth', 'get').mockReturnValue(1000);
  vi.spyOn(HTMLMediaElement.prototype, 'pause').mockImplementation(() => {});
  container = document.createElement('div');
  document.body.append(container);
  root = createRoot(container);
});
afterEach(async () => {
  await act(async () => root.unmount());
  container.remove();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

async function mount(original: Bootstrap, relink: (...args: unknown[]) => Promise<unknown>) {
  const apply = vi.fn();
  const importMedia = vi.fn();
  const fetch = vi.fn();
  vi.stubGlobal('fetch', fetch);
  installDesktop({
    GetBootstrap: vi.fn().mockResolvedValue(original),
    ListJobs: vi.fn().mockResolvedValue([]),
    RelinkAsset: relink,
    ApplyEdit: apply,
    ImportMedia: importMedia,
  });
  await act(async () => {
    root.render(<App />);
    await new Promise((resolve) => setTimeout(resolve, 5));
  });
  return { apply, importMedia, fetch };
}

async function click(button: Element | null) {
  if (!(button instanceof HTMLButtonElement)) throw new Error('未找到实际操作按钮');
  await act(async () => {
    button.click();
    await new Promise((resolve) => setTimeout(resolve, 5));
  });
}

describe('缺失素材重新定位界面', () => {
  it('缺失卡调用稳定素材ID，并接受返回状态而不重建片段或编排', async () => {
    const original = snapshot();
    const repaired: Bootstrap = {
      ...original,
      dirty: true,
      canUndo: true,
      project: {
        ...original.project,
        revision: 1,
        assets: [{ ...original.project.assets[0], path: '/新位置/镜头.mp4', missing: false }],
      },
    };
    const relink = vi.fn().mockResolvedValue(repaired);
    const { apply, importMedia, fetch } = await mount(original, relink);
    await click(container.querySelector('.clip-face'));
    const before = container.querySelector<HTMLElement>('[data-clip-id="clip-stable"]');
    expect(before).not.toBeNull();
    const geometry = before!.style.cssText;
    await click(container.querySelector('button[aria-label="重新定位素材 镜头.mp4"]'));
    expect(relink).toHaveBeenCalledWith('asset-stable');
    expect(apply).not.toHaveBeenCalled();
    expect(importMedia).not.toHaveBeenCalled();
    expect(fetch).not.toHaveBeenCalled();
    const after = container.querySelector<HTMLElement>('[data-clip-id="clip-stable"]');
    expect(after?.style.cssText).toBe(geometry);
    expect(container.textContent).not.toContain('源文件缺失');
    expect(container.querySelector('.inspector-content')?.textContent).toContain(
      '/新位置/镜头.mp4',
    );
    expect(container.querySelector('.clip-face')?.getAttribute('aria-pressed')).toBe('true');
    expect(repaired.project.clips).toEqual(original.project.clips);
  });
  it('非缺失素材也可从片段源区定位，失败保留当前工程和可恢复错误', async () => {
    const original = snapshot(false);
    const relink = vi.fn().mockRejectedValue(new Error('所选文件与原素材内容不同'));
    const { importMedia } = await mount(original, relink);
    await click(container.querySelector('.clip-face'));
    const button = Array.from(container.querySelectorAll('button')).find(
      (item) => item.textContent === '重新定位素材',
    );
    await click(button || null);
    expect(relink).toHaveBeenCalledWith('asset-stable');
    expect(importMedia).not.toHaveBeenCalled();
    expect(container.querySelector('.global-alert')?.textContent).toContain(
      '所选文件与原素材内容不同',
    );
    expect(container.querySelector('[data-clip-id="clip-stable"]')).not.toBeNull();
    expect(original.project.clips[0].startFrame).toBe(10);
    expect(original.project.clips[0].sourceInUs).toBe(33367);
  });
});
