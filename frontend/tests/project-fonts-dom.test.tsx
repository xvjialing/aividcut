// @vitest-environment jsdom
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { webcrypto } from 'node:crypto';
import { useProjectFonts, projectFontFamily } from '../src/lib/use-project-fonts';
import { TooltipProvider } from '../src/components/ui';
import { Inspector } from '../src/components/Inspector';
import { defaultClip } from '../src/lib/editor';
import type { Project } from '../src/types';
let root: Root, host: HTMLDivElement, faces: Set<unknown>;
const data = new Uint8Array([1, 2, 3, 4]);
const hex = async (bytes: Uint8Array) =>
  [...new Uint8Array(await webcrypto.subtle.digest('SHA-256', new Uint8Array(bytes)))]
    .map((b) => b.toString(16).padStart(2, '0'))
    .join('');
async function fixture(): Promise<Project> {
  const settings = { width: 640, height: 360, fpsNum: 30, fpsDen: 1, sampleRate: 48000 };
  const c = defaultClip('text', 't', 0, settings);
  c.textStyle.fontAssetId = 'font';
  return {
    schemaVersion: 3,
    id: 'p',
    name: '字体',
    revision: 1,
    settings,
    assets: [
      {
        id: 'font',
        kind: 'font',
        name: '工程字体.ttf',
        path: '/font.ttf',
        url: 'http://local/font',
        fingerprint: 'sha256:' + (await hex(data)),
        durationUs: 0,
        width: 0,
        height: 0,
        fpsNum: 0,
        fpsDen: 0,
        hasAudio: false,
        missing: false,
        font: {
          family: 'Go',
          style: 'Regular',
          fullName: 'Go Regular',
          weight: 400,
          italic: false,
        },
      },
    ],
    tracks: [{ id: 't', kind: 'text', name: '文字', locked: false, muted: false, hidden: false }],
    clips: [c],
  };
}
function Harness({ p }: { p: Project }) {
  const state = useProjectFonts(p, 7, true);
  return <div>{state.error || (state.ready.has('font') ? '字体已加载' : '字体未就绪')}</div>;
}
const settle = () => new Promise((resolve) => setTimeout(resolve, 0));
beforeEach(() => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
  host = document.createElement('div');
  document.body.append(host);
  root = createRoot(host);
  faces = new Set();
  Object.defineProperty(document, 'fonts', {
    configurable: true,
    value: { add: (f: unknown) => faces.add(f), delete: (f: unknown) => faces.delete(f) },
  });
  vi.stubGlobal('crypto', webcrypto);
  vi.stubGlobal(
    'FontFace',
    class {
      async load() {
        return this;
      }
    },
  );
  vi.stubGlobal(
    'fetch',
    vi.fn(async () => new Response(data)),
  );
});
afterEach(async () => {
  await act(async () => root.unmount());
  host.remove();
  vi.unstubAllGlobals();
});
it('从已登记本机URL读字节核对SHA，再加载字体，卸载时释放', async () => {
  const p = await fixture();
  await act(async () => root.render(<Harness p={p} />));
  await vi.waitFor(async () => {
    await act(async () => {
      await settle();
    });
    expect(host.textContent).toBe('字体已加载');
  });
  expect(faces.size).toBe(1);
  expect(projectFontFamily(p.assets[0].fingerprint!)).not.toContain('sha256:');
  expect(fetch).toHaveBeenCalledWith(
    p.assets[0].url,
    expect.objectContaining({ signal: expect.any(AbortSignal) }),
  );
  await act(async () => root.unmount());
  expect(faces.size).toBe(0);
  root = createRoot(host);
});
it('内容变化时显示实际原因，不注册不同字体或修改项目', async () => {
  const p = await fixture();
  p.assets[0].fingerprint = 'sha256:' + 'a'.repeat(64);
  await act(async () => root.render(<Harness p={p} />));
  await vi.waitFor(async () => {
    await act(async () => {
      await settle();
    });
    expect(host.textContent).toContain('内容已变化');
  });
  expect(faces.size).toBe(0);
  expect(p.clips[0].textStyle.fontAssetId).toBe('font');
});
it('缺失字体直接显示修复入口说明，不读取任意路径', async () => {
  const p = await fixture();
  p.assets[0].missing = true;
  await act(async () => {
    root.render(<Harness p={p} />);
    await settle();
  });
  expect(host.textContent).toContain('重新定位原字体');
  expect(fetch).not.toHaveBeenCalled();
});
it('字体加载的迟到响应不能注册到已切换工程', async () => {
  const p = await fixture();
  let finish!: () => void;
  vi.stubGlobal(
    'FontFace',
    class {
      load() {
        return new Promise((resolve) => {
          finish = () => resolve(this);
        });
      }
    },
  );
  await act(async () => root.render(<Harness p={p} />));
  await act(async () => {
    await vi.waitFor(() => expect(finish).toBeTypeOf('function'));
  });
  await act(async () => root.unmount());
  await act(async () => {
    finish();
    await settle();
  });
  expect(faces.size).toBe(0);
  root = createRoot(host);
});
it('属性字体选择保留其它样式，导入入口共用，锁轨禁用选择', async () => {
  const p = await fixture(),
    onUpdate = vi.fn(),
    onImportFont = vi.fn();
  async function render() {
    await act(async () =>
      root.render(
        <Inspector
          project={p}
          clip={p.clips[0]}
          busy={false}
          onUpdate={onUpdate}
          onImportFont={onImportFont}
          onAddAsset={vi.fn()}
          onProjectName={vi.fn()}
        />,
      ),
    );
  }
  await render();
  const input = document.querySelector<HTMLSelectElement>('select[aria-label="字体"]')!;
  await act(async () => {
    input.value = '';
    input.dispatchEvent(new Event('change', { bubbles: true }));
  });
  expect(onUpdate).toHaveBeenCalledWith({
    ...p.clips[0],
    textStyle: { ...p.clips[0].textStyle, fontAssetId: undefined },
  });
  expect(p.clips[0].textStyle.fontAssetId).toBe('font');
  await act(async () => {
    [...document.querySelectorAll<HTMLButtonElement>('button')]
      .find((b) => b.textContent === '导入工程字体')!
      .click();
  });
  expect(onImportFont).toHaveBeenCalledTimes(1);
  p.tracks[0].locked = true;
  await render();
  expect(document.querySelector<HTMLSelectElement>('select[aria-label="字体"]')!.disabled).toBe(
    true,
  );
});

it('同ID同修订重开时按sourceEpoch重新核对预览能力', async () => {
  const { Preview } = await import('../src/components/Preview');
  const { PlaybackClock } = await import('../src/lib/editor');
  const { fakePlaybackAPI } = await import('./playback-fixtures');
  const api = fakePlaybackAPI();
  const p = await fixture();
  p.assets = [];
  p.clips = [];
  vi.stubGlobal(
    'ResizeObserver',
    class {
      observe() {}
      disconnect() {}
    },
  );
  vi.stubGlobal(
    'requestAnimationFrame',
    vi.fn(() => 1),
  );
  vi.stubGlobal('cancelAnimationFrame', vi.fn());
  const clock = new PlaybackClock();
  try {
    await act(async () =>
      root.render(
        <TooltipProvider>
          <Preview
            project={p}
            composed={null}
            sourceEpoch={1}
            clock={clock}
            onImport={vi.fn()}
            playbackAPI={api}
            clientReason={() => ''}
          />
        </TooltipProvider>,
      ),
    );
    expect(api.getPlaybackCapabilities).toHaveBeenCalledTimes(1);
    await act(async () =>
      root.render(
        <TooltipProvider>
          <Preview
            project={p}
            composed={null}
            sourceEpoch={2}
            clock={clock}
            onImport={vi.fn()}
            playbackAPI={api}
            clientReason={() => ''}
          />
        </TooltipProvider>,
      ),
    );
    expect(api.getPlaybackCapabilities).toHaveBeenCalledTimes(2);
  } finally {
    clock.destroy();
  }
});

it('新增一个标题字体时复用仍被引用的字幕字体，不重复读取', async () => {
  const p = await fixture();
  await act(async () => root.render(<Harness p={p} />));
  await vi.waitFor(async () => {
    await act(async () => {
      await settle();
    });
    expect(faces.size).toBe(1);
  });
  const original = [...faces][0];
  const extraData = new Uint8Array([7, 8, 9]);
  const other = {
    ...p.assets[0],
    id: 'font-2',
    url: 'http://local/font-2',
    fingerprint: 'sha256:' + (await hex(extraData)),
  };
  vi.mocked(fetch).mockImplementation(
    async (url) => new Response(String(url).endsWith('font-2') ? extraData : data),
  );
  const next = {
    ...p,
    assets: [...p.assets, other],
    clips: [
      ...p.clips,
      {
        ...p.clips[0],
        id: 'title-2',
        textStyle: { ...p.clips[0].textStyle, fontAssetId: 'font-2' },
      },
    ],
  };
  await act(async () => root.render(<Harness p={next} />));
  expect(host.textContent).toBe('字体已加载');
  await vi.waitFor(async () => {
    await act(async () => {
      await settle();
    });
    expect(faces.size).toBe(2);
  });
  expect(faces.has(original)).toBe(true);
  expect(fetch).toHaveBeenCalledTimes(2);
});
