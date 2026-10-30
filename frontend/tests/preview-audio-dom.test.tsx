// @vitest-environment jsdom
import { installDesktop } from './desktop-fixture';
vi.mock('../src/lib/desktop', async () => (await import('./desktop-fixture')).desktopModule);
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import App from '../src/App';
import { Preview } from '../src/components/Preview';
import { TooltipProvider } from '../src/components/ui';
import { browserAudioWindow, LegacyMediaBoundary } from '../src/lib/legacy-media';
import { PlaybackClock, defaultClip } from '../src/lib/editor';
import { fakePlaybackAPI, playbackCapabilities, playbackProject } from './playback-fixtures';
import type { Asset, Bootstrap } from '../src/types';
let container: HTMLDivElement;
let root: Root;
let fetchGuard: ReturnType<typeof vi.fn>;
beforeEach(() => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
  fetchGuard = vi.fn().mockRejectedValue(new Error('测试禁止真实播放服务/设备'));
  vi.stubGlobal('fetch', fetchGuard);
  vi.stubGlobal(
    'ResizeObserver',
    class {
      observe() {}
      unobserve() {}
      disconnect() {}
    },
  );
  vi.stubGlobal('cancelAnimationFrame', vi.fn());
  vi.spyOn(HTMLElement.prototype, 'clientWidth', 'get').mockReturnValue(1000);
  vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockReturnValue({
    width: 900,
    height: 500,
  } as DOMRect);
  vi.spyOn(HTMLMediaElement.prototype, 'pause').mockImplementation(() => {});
  vi.spyOn(HTMLMediaElement.prototype, 'play').mockResolvedValue();
  container = document.createElement('div');
  document.body.append(container);
  root = createRoot(container);
});
afterEach(async () => {
  await act(async () => root.unmount());
  container.remove();
  delete window._wails;
  vi.useRealTimers();
  expect(fetchGuard).not.toHaveBeenCalled();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});
async function flush() {
  await new Promise((resolve) => setTimeout(resolve, 5));
}
async function render(element: React.ReactNode) {
  await act(async () => {
    root.render(element);
    await flush();
  });
}
async function select(element: HTMLSelectElement, value: string) {
  await act(async () => {
    Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype, 'value')!.set!.call(
      element,
      value,
    );
    element.dispatchEvent(new Event('change', { bubbles: true }));
    await flush();
  });
}
describe('旧HTML工作区的真实音尾门槛', () => {
  const settings = playbackProject.settings;
  const asset: Asset = {
    id: 'tiny',
    name: '极短音频.wav',
    kind: 'audio',
    path: '/fixture/tiny.wav',
    url: 'http://127.0.0.1/fixture-only',
    durationUs: 5000,
    hasAudio: true,
    missing: false,
    fpsNum: 0,
    fpsDen: 1,
    width: 0,
    height: 0,
    audioSampleRate: 48000,
    audioSampleCount: 240,
  };
  const clip = {
    ...defaultClip('audio', 'a', 0, settings, asset),
    durationFrames: 1,
    audioRange: {
      unit: 'samples' as const,
      start: 0,
      end: 240,
      sampleRate: 48000,
      sourceOriginUs: 0,
      offsetDen: 1,
    },
  };
  it('不从时长造样本，已测0和未知区分，样本时钟副本不匹配就不发声', () => {
    expect(browserAudioWindow(clip, asset, settings)?.timelineEnd).toBe(0.005);
    expect(browserAudioWindow(clip, { ...asset, audioSampleCount: null }, settings)).toBeNull();
    expect(
      browserAudioWindow(
        { ...clip, audioRange: { ...clip.audioRange, start: 0, end: 0 } },
        { ...asset, audioSampleCount: 0 },
        settings,
      )?.sourceEnd,
    ).toBe(0);
    expect(
      browserAudioWindow(
        { ...clip, audioRange: { ...clip.audioRange, sampleRate: 44100 } },
        asset,
        settings,
      ),
    ).toBeNull();
    expect(browserAudioWindow(clip, asset, settings)?.reason).toContain('近似');
  });
  it('极短音频在ceil占位结束前已耗尽，不能反复play从0重播', async () => {
    const element = document.createElement('audio');
    const boundary = new LegacyMediaBoundary();
    boundary.sync(element, clip, asset, settings, 0, true, true, false, 'rev0:seek0');
    await Promise.resolve();
    expect(element.play).toHaveBeenCalledOnce();
    boundary.mediaEnded(element);
    for (let index = 0; index < 4; index++)
      boundary.sync(element, clip, asset, settings, 0, true, true, false, 'rev0:seek0');
    expect(element.play).toHaveBeenCalledOnce();
    expect(element.muted).toBe(true);
    expect(element.loop).toBe(false);
    boundary.sync(element, clip, asset, settings, 0, true, true, false, 'rev0:seek1');
    await Promise.resolve();
    expect(element.play).toHaveBeenCalledTimes(2);
    boundary.destroy(element);
  });
  it('首激活/seek强制源定位，小于0.15秒也不错误从0播；范围已过就静音停音', async () => {
    const element = document.createElement('audio');
    const boundary = new LegacyMediaBoundary();
    const shifted = {
      ...clip,
      sourceInUs: 1000,
      audioRange: { unit: 'us' as const, start: 1000, end: 5000, offsetDen: 1 },
    };
    boundary.sync(element, shifted, asset, settings, 0, true, true, false, 'seek0');
    expect(element.currentTime).toBe(0.001);
    await Promise.resolve();
    boundary.sync(element, shifted, asset, settings, 0.005, true, true, false, 'seek0');
    expect(element.muted).toBe(true);
    expect(element.pause).toHaveBeenCalled();
    boundary.destroy(element);
  });
  it('媒体自身尚未ended时也按范围计时停音，视频画面不被音尾提前停掉', async () => {
    vi.useFakeTimers();
    const audio = document.createElement('audio');
    const boundary = new LegacyMediaBoundary();
    boundary.sync(audio, clip, asset, settings, 0, true, true, false, 'a');
    await Promise.resolve();
    await vi.advanceTimersByTimeAsync(6);
    expect(audio.muted).toBe(true);
    boundary.sync(audio, clip, asset, settings, 0, true, true, false, 'a');
    expect(audio.play).toHaveBeenCalledOnce();
    boundary.destroy(audio);
    const video = document.createElement('video');
    const visual = new LegacyMediaBoundary();
    visual.sync(
      video,
      { ...clip, kind: 'video' },
      { ...asset, kind: 'video', durationUs: 1000000 },
      settings,
      0.01,
      true,
      true,
      false,
      'v',
    );
    await Promise.resolve();
    expect(video.muted).toBe(true);
    expect(video.play).toHaveBeenCalled();
    visual.destroy(video);
  });
});
describe('实验模式能力/标签和旧媒体互斥', () => {
  const supportedClient = () => '';
  it('当前工程不支持时不可选，仍明确保留工作区而不是改名native成功', async () => {
    const api = fakePlaybackAPI();
    api.getPlaybackCapabilities.mockResolvedValue({
      ...playbackCapabilities,
      projectSupported: false,
      projectReason: '该工程的分数音频偏移尚不支持',
    });
    const clock = new PlaybackClock();
    clock.configure(3, 30);
    await render(
      <TooltipProvider>
        <Preview
          project={playbackProject}
          clock={clock}
          composed={null}
          onImport={vi.fn()}
          playbackAPI={api}
          clientReason={supportedClient}
        />
      </TooltipProvider>,
    );
    const mode = document.querySelector('[aria-label="预览模式"]') as HTMLSelectElement;
    expect(mode.value).toBe('workspace');
    expect(mode.querySelector('option[value="realtime"]')?.hasAttribute('disabled')).toBe(true);
    expect(document.body.textContent).toContain('分数音频偏移');
    expect(api.startPlayback).not.toHaveBeenCalled();
    clock.destroy();
  });
  it('只有显式选择才切实验模式；进入后卸载HTML工程媒体，WebView失败不假走RAF', async () => {
    const api = fakePlaybackAPI();
    const clock = new PlaybackClock();
    clock.configure(3, 30);
    await render(
      <TooltipProvider>
        <Preview
          project={playbackProject}
          clock={clock}
          composed={null}
          onImport={vi.fn()}
          playbackAPI={api}
          clientReason={supportedClient}
        />
      </TooltipProvider>,
    );
    expect(document.querySelector('.preview-stage video')).not.toBeNull();
    await select(
      document.querySelector('[aria-label="预览模式"]') as HTMLSelectElement,
      'realtime',
    );
    expect(document.querySelector('.preview-stage')).toBeNull();
    expect(document.querySelector('.stage-layer video')).toBeNull();
    expect(document.body.textContent).toContain('实时合成预览（实验）');
    expect(clock.hasExternalDriver()).toBe(true);
    clock.play();
    expect(clock.isPlaying()).toBe(false);
    expect(api.startPlayback).not.toHaveBeenCalled();
    clock.destroy();
  });
});
describe('实时预览合成精度', () => {
  const supportedClient = () => '';
  it('工作区隐藏视频画面仍保留并播放未静音原声', async () => {
    const clock = new PlaybackClock();
    clock.configure(3, 30);
    const project = {
      ...playbackProject,
      tracks: playbackProject.tracks.map((t) => ({ ...t, hidden: true })),
      assets: playbackProject.assets.map((a) => ({
        ...a,
        audioSampleRate: 48000,
        audioSampleCount: 480000,
      })),
    };
    await render(
      <TooltipProvider>
        <Preview
          project={project}
          clock={clock}
          composed={null}
          onImport={vi.fn()}
          playbackAPI={fakePlaybackAPI()}
          clientReason={supportedClient}
        />
      </TooltipProvider>,
    );
    const video = document.querySelector<HTMLVideoElement>('.preview-stage video')!;
    expect(video).not.toBeNull();
    await act(async () => clock.play());
    expect(video.style.visibility).toBe('hidden');
    expect(video.play).toHaveBeenCalled();
    expect(video.muted).toBe(false);
    clock.destroy();
  });
  it('精度切换保持工程对象并暂停，其他工程默认全精度', async () => {
    const api = fakePlaybackAPI(),
      clock = new PlaybackClock();
    clock.configure(3, 30);
    const before = JSON.stringify(playbackProject);
    const scene = (project = playbackProject) => (
      <TooltipProvider>
        <Preview
          project={project}
          clock={clock}
          composed={null}
          onImport={vi.fn()}
          playbackAPI={api}
          clientReason={() => ''}
        />
      </TooltipProvider>
    );
    await render(scene());
    await select(
      document.querySelector('[aria-label="预览模式"]') as HTMLSelectElement,
      'realtime',
    );
    const quality = () =>
      document.querySelector('[aria-label="预览合成精度"]') as HTMLSelectElement;
    expect(quality().value).toBe('full');
    await select(quality(), 'quarter');
    expect(quality().value).toBe('quarter');
    expect(clock.isPlaying()).toBe(false);
    expect(JSON.stringify(playbackProject)).toBe(before);
    await render(scene({ ...playbackProject, id: 'another-project' }));
    expect(quality().value).toBe('full');
    clock.destroy();
  });
  it('未通过当前草稿核对时保留精度选择', async () => {
    const api = fakePlaybackAPI(),
      clock = new PlaybackClock();
    clock.configure(3, 30);
    const prepare = vi.fn().mockResolvedValueOnce(playbackProject).mockResolvedValueOnce(null);
    await render(
      <TooltipProvider>
        <Preview
          project={playbackProject}
          clock={clock}
          composed={null}
          onImport={vi.fn()}
          playbackAPI={api}
          clientReason={() => ''}
          prepareProject={prepare}
        />
      </TooltipProvider>,
    );
    await select(
      document.querySelector('[aria-label="预览模式"]') as HTMLSelectElement,
      'realtime',
    );
    await select(
      document.querySelector('[aria-label="预览合成精度"]') as HTMLSelectElement,
      'half',
    );
    expect((document.querySelector('[aria-label="预览合成精度"]') as HTMLSelectElement).value).toBe(
      'full',
    );
    expect(prepare).toHaveBeenCalledTimes(2);
    clock.destroy();
  });
});
describe('普通音频完整上轨', () => {
  it('传稳定ID和工程revision给专用API，由后端ceil/选轨/真实范围决定，不发floor Clip', async () => {
    const asset: Asset = {
      id: 'audio-source',
      name: '音尾素材.wav',
      kind: 'audio',
      path: '/fixture/tail.wav',
      durationUs: 1000001,
      hasAudio: true,
      missing: false,
      fpsNum: 0,
      fpsDen: 1,
      width: 0,
      height: 0,
      audioSampleCount: null,
    };
    const original: Bootstrap = {
      project: { ...playbackProject, revision: 0, assets: [asset], tracks: [], clips: [] },
      projectPath: '',
      dirty: false,
      canUndo: false,
      canRedo: false,
      capabilities: {},
    };
    const apply = vi.fn();
    const add = vi.fn().mockImplementation(async (input) => ({
      ...original,
      dirty: true,
      canUndo: true,
      warnings: ['音尾素材.wav：未实测样本数；本次按声明时长添加音频。'],
      project: {
        ...original.project,
        revision: 1,
        tracks: [
          {
            id: 'backend-audio-track',
            name: '音频 1',
            kind: 'audio',
            locked: false,
            muted: false,
            hidden: false,
          },
        ],
        clips: [
          {
            ...defaultClip(
              'audio',
              'backend-audio-track',
              input.startFrame,
              original.project.settings,
              asset,
            ),
            id: input.clipId,
            durationFrames: 31,
            audioRange: { unit: 'us', start: 0, end: 1000001 },
          },
        ],
      },
    }));
    installDesktop({
      GetBootstrap: vi.fn().mockResolvedValue(original),
      ListJobs: vi.fn().mockResolvedValue([]),
      GetMCPStatus: vi.fn().mockResolvedValue({ running: false }),
      AddAudioToTimeline: add,
      ApplyEdit: apply,
    });
    await render(<App />);
    const source = document.querySelector(
      '[aria-label="选择素材 音尾素材.wav"]',
    ) as HTMLButtonElement;
    await act(async () => {
      source.dispatchEvent(new MouseEvent('dblclick', { bubbles: true }));
      await flush();
    });
    expect(add).toHaveBeenCalledOnce();
    const request = add.mock.calls[0][0];
    expect(request).toEqual(
      expect.objectContaining({
        projectId: playbackProject.id,
        projectRevision: 0,
        assetId: asset.id,
        startFrame: 0,
        clipId: expect.any(String),
      }),
    );
    expect(request).not.toHaveProperty('durationFrames');
    expect(request).not.toHaveProperty('audioRange');
    expect(request).not.toHaveProperty('trackId');
    expect(apply).not.toHaveBeenCalled();
    const node = document.querySelector(`[data-clip-id="${request.clipId}"]`);
    expect(node).not.toBeNull();
    expect(node?.querySelector('button')?.getAttribute('aria-pressed')).toBe('true');
    const hints = document.querySelector('[aria-label="本次操作提示"]');
    expect(hints).not.toBeNull();
    const details = hints!.querySelector('details')!;
    await act(async () => details.querySelector('summary')!.click());
    expect(details.open).toBe(true);
    expect(details.textContent).toContain('未实测样本数；本次按声明时长添加音频');
  });
});

describe('本次素材操作的降级提示', () => {
  it.each([
    { label: '未提供', warnings: undefined },
    { label: '空数组', warnings: [] },
  ])('导入成功即使dirty也可查看全部原因；后续warnings$label清除旧提示', async ({ warnings }) => {
    const original: Bootstrap = {
      project: { ...playbackProject, revision: 0, clips: [] },
      projectPath: '',
      dirty: false,
      canUndo: false,
      canRedo: false,
      capabilities: {},
    };
    const first = '采访.mp4：超过每文件5分钟上限；未实测样本数，保留声明时长。';
    const second = '音乐.wav：本次计数预算用尽；未实测样本数，保留声明时长。';
    const imported: Bootstrap = {
      ...original,
      dirty: true,
      canUndo: true,
      project: { ...original.project, revision: 1 },
      warnings: [first, second, first],
    };
    const importMedia = vi.fn().mockResolvedValue(imported);
    const undo = vi.fn().mockResolvedValue({ ...original, warnings });
    installDesktop({
      GetBootstrap: vi.fn().mockResolvedValue(original),
      ListJobs: vi.fn().mockResolvedValue([]),
      GetMCPStatus: vi.fn().mockResolvedValue({ running: false }),
      ImportMedia: importMedia,
      Undo: undo,
    });
    await render(<App />);
    await act(async () => {
      document.querySelector<HTMLButtonElement>('[aria-label="导入素材"]')!.click();
      await flush();
    });
    expect(importMedia).toHaveBeenCalledOnce();
    const hints = document.querySelector('[aria-label="本次操作提示"]');
    expect(hints).not.toBeNull();
    const details = hints!.querySelector('details')!;
    expect(details.querySelector('summary')!.textContent).toContain('2 条提示');
    await act(async () => details.querySelector('summary')!.click());
    expect(details.open).toBe(true);
    expect(Array.from(details.querySelectorAll('p'), (item) => item.textContent)).toEqual([
      first,
      second,
    ]);
    expect(document.querySelector('.status-message')?.textContent).toContain('有未保存的更改');
    await act(async () => {
      document.querySelector<HTMLButtonElement>('[aria-label="撤销 · ⌘/Ctrl Z"]')!.click();
      await flush();
    });
    expect(undo).toHaveBeenCalledOnce();
    expect(document.querySelector('[aria-label="本次操作提示"]')).toBeNull();
  });
});

it('空白与时间线间隙使用工作区表面，只有可见画面或有效合成帧启用媒体画布', async () => {
  const api = fakePlaybackAPI();
  const clock = new PlaybackClock();
  clock.configure(90, 30);
  const projectWithGap = {
    ...playbackProject,
    clips: [...playbackProject.clips, { ...playbackProject.clips[0], id: 'later', startFrame: 60 }],
  };
  const view = (
    project = projectWithGap,
    composed: import('../src/types').PreviewFrame | null = null,
  ) => (
    <TooltipProvider>
      <Preview
        project={project}
        clock={clock}
        composed={composed}
        onImport={vi.fn()}
        playbackAPI={api}
        clientReason={() => ''}
      />
    </TooltipProvider>
  );
  await render(view({ ...playbackProject, clips: [], assets: [] }));
  expect(container.querySelector<HTMLElement>('.preview-stage')!.dataset.hasPicture).toBe('false');
  expect(container.querySelector<HTMLElement>('.viewer-empty')!.hidden).toBe(false);
  await render(view());
  expect(container.querySelector<HTMLElement>('.preview-stage')!.dataset.hasPicture).toBe('true');
  await act(async () => clock.seek(30));
  expect(container.querySelector<HTMLElement>('.preview-stage')!.dataset.hasPicture).toBe('false');
  expect(container.querySelector<HTMLElement>('.viewer-empty')!.hidden).toBe(false);
  await render(
    view(projectWithGap, {
      url: '/fixture/composed.png',
      frame: 30,
      revision: projectWithGap.revision,
    }),
  );
  expect(container.querySelector<HTMLElement>('.preview-stage')!.dataset.hasPicture).toBe('true');
  expect(container.querySelector<HTMLElement>('.viewer-empty')!.hidden).toBe(true);
  expect(container.querySelector<HTMLImageElement>('.composed-frame')!.hidden).toBe(false);
  await render(
    view(projectWithGap, {
      url: '/fixture/composed.png',
      frame: 30,
      revision: projectWithGap.revision - 1,
    }),
  );
  expect(container.querySelector<HTMLElement>('.preview-stage')!.dataset.hasPicture).toBe('false');
  expect(container.querySelector<HTMLImageElement>('.composed-frame')!.hidden).toBe(true);
});
