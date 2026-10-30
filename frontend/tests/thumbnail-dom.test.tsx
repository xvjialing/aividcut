// @vitest-environment jsdom
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { SourceThumbnail } from '../src/components/SourceThumbnail';
import { AssetSourceBrowser } from '../src/components/AssetSourceBrowser';
import { ThumbnailQueue } from '../src/lib/thumbnail';
import type { Asset } from '../src/types';
import type { Thumbnail, ThumbnailRequest } from '../src/thumbnail-types';

const api = vi.hoisted(() => ({ read: vi.fn(), cancel: vi.fn(async () => {}) }));
vi.mock('../src/lib/bridge', () => ({
  thumbnailAPI: api,
  previewProxyAPI: { get: vi.fn(), start: vi.fn(), clear: vi.fn(), cancel: vi.fn() },
}));
const asset: Asset = {
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
const value: Thumbnail = {
  assetId: 'a',
  positionUs: 0,
  url: 'http://127.0.0.1/source.jpg',
  width: 320,
  height: 180,
};
let host: HTMLDivElement, root: Root;
beforeEach(() => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
  host = document.createElement('div');
  document.body.append(host);
  root = createRoot(host);
  api.read.mockReset();
  api.cancel.mockClear();
  vi.stubGlobal('IntersectionObserver', undefined);
});
afterEach(async () => {
  await act(async () => root.unmount());
  host.remove();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});
describe('源画面组件', () => {
  it('普通工程读模型刷新保留画面，同一工程重新打开后重新读取', async () => {
    const request = {
      read: vi.fn(async (_request: ThumbnailRequest) => value),
      cancel: vi.fn(async () => {}),
    };
    const queue = new ThumbnailQueue(request);
    const render = (sourceEpoch: number) =>
      act(async () =>
        root.render(
          <SourceThumbnail
            projectId="p"
            asset={{ ...asset }}
            queue={queue}
            sourceEpoch={sourceEpoch}
          />,
        ),
      );
    await render(1);
    await act(async () => host.querySelector('img')!.dispatchEvent(new Event('load')));
    await render(1);
    expect(request.read).toHaveBeenCalledTimes(1);
    expect(host.querySelector('[data-thumbnail-state="ready"]')).not.toBeNull();
    await render(2);
    expect(request.read).toHaveBeenCalledTimes(2);
    expect(request.read.mock.calls[1][0].sourceEpoch).toBe(2);
  });
  it('可见后才开始，滚出取消，卸载断开观察', async () => {
    let notify!: (entries: { isIntersecting: boolean }[]) => void;
    const disconnect = vi.fn();
    vi.stubGlobal(
      'IntersectionObserver',
      class {
        constructor(cb: typeof notify) {
          notify = cb;
        }
        observe() {}
        disconnect = disconnect;
      },
    );
    const request = {
      read: vi.fn(() => new Promise<Thumbnail>(() => {})),
      cancel: vi.fn(async () => {}),
    };
    const queue = new ThumbnailQueue(request);
    await act(async () =>
      root.render(<SourceThumbnail projectId="p" asset={asset} queue={queue} />),
    );
    expect(request.read).not.toHaveBeenCalled();
    await act(async () => notify([{ isIntersecting: true }]));
    expect(request.read).toHaveBeenCalledTimes(1);
    await act(async () => notify([{ isIntersecting: false }]));
    expect(request.cancel).toHaveBeenCalledTimes(1);
    await act(async () => root.render(null));
    expect(disconnect).toHaveBeenCalledTimes(1);
  });
  it('旧源延迟结果不能覆盖新素材，图像完成解码才显示成功', async () => {
    let finish!: (v: Thumbnail) => void;
    const request = {
      read: vi
        .fn()
        .mockImplementationOnce(() => new Promise<Thumbnail>((r) => (finish = r)))
        .mockResolvedValue({ ...value, url: 'http://127.0.0.1/new.jpg' }),
      cancel: vi.fn(async () => {}),
    };
    const queue = new ThumbnailQueue(request);
    await act(async () =>
      root.render(<SourceThumbnail projectId="p" asset={asset} queue={queue} />),
    );
    await act(async () =>
      root.render(<SourceThumbnail projectId="q" asset={{ ...asset, id: 'b' }} queue={queue} />),
    );
    await act(async () => finish(value));
    expect(host.querySelector('img')?.getAttribute('src')).toContain('new.jpg');
    expect(host.querySelector('[data-thumbnail-state="loading"]')).not.toBeNull();
    await act(async () => host.querySelector('img')!.dispatchEvent(new Event('load')));
    expect(host.querySelector('[data-thumbnail-state="ready"]')).not.toBeNull();
  });
  it('失败与图片访问失效保留原因，显式重试可恢复，缺失/音频不请求', async () => {
    const request = {
      read: vi.fn().mockRejectedValueOnce(new Error('源缺失')).mockResolvedValue(value),
      cancel: vi.fn(async () => {}),
    };
    const queue = new ThumbnailQueue(request);
    const render = (refresh: number, input = asset) =>
      act(async () =>
        root.render(
          <SourceThumbnail projectId="p" asset={input} refresh={refresh} queue={queue} />,
        ),
      );
    await render(0);
    expect(host.textContent).toContain('源缺失');
    await render(1);
    await act(async () => host.querySelector('img')!.dispatchEvent(new Event('error')));
    expect(host.textContent).toContain('访问已失效');
    await render(2);
    expect(host.querySelector('img')).not.toBeNull();
    await render(3, { ...asset, missing: true });
    await render(4, { ...asset, kind: 'audio' });
    expect(request.read).toHaveBeenCalledTimes(3);
  });
  it('源浏览取样可用键盘按钮选择，切换素材复位且不会自动编辑', async () => {
    api.read.mockImplementation(async (r) => ({ ...value, positionUs: r.positionUs }));
    await act(async () => root.render(<AssetSourceBrowser projectId="p" asset={asset} />));
    expect(host.querySelectorAll('.source-sample')).toHaveLength(5);
    const last = host.querySelectorAll<HTMLButtonElement>('.source-sample')[4];
    await act(async () => last.click());
    expect(host.querySelector<HTMLInputElement>('input[type="range"]')?.value).toBe('3900000');
    expect(last.getAttribute('aria-pressed')).toBe('true');
    await act(async () =>
      root.render(
        <AssetSourceBrowser projectId="p" asset={{ ...asset, id: 'b', durationUs: 1e6 }} />,
      ),
    );
    expect(host.querySelector<HTMLInputElement>('input[type="range"]')?.value).toBe('0');
    expect(host.querySelectorAll('button button')).toHaveLength(0);
    expect(host.textContent).toContain('近似时间');
  });
});
