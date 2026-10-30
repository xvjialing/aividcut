// @vitest-environment jsdom
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { PreviewProxyPanel } from '../src/components/PreviewProxyPanel';
import type { PreviewProxyAPI, PreviewProxyView } from '../src/proxy-types';
import { speechSource } from './speech-fixtures';
let root: Root, container: HTMLDivElement;
let fetchGuard: ReturnType<typeof vi.fn>;
const asset = { ...speechSource, kind: 'video' as const };
const missing: PreviewProxyView = {
  status: 'missing',
  projectId: 'p',
  sourceEpoch: 4,
  assetId: asset.id,
  profile: '720p',
  reason: '尚无代理',
};
const ready: PreviewProxyView = {
  ...missing,
  status: 'ready',
  reason: '代理已核对',
  proof: {
    originUs: 0,
    frames: 60,
    width: 960,
    height: 540,
    bytes: 1048576,
    timelineSha256: 'digest',
    proxyFingerprint: 'fingerprint',
  },
};
function api(): PreviewProxyAPI {
  return {
    get: vi.fn().mockResolvedValue(missing),
    start: vi.fn().mockResolvedValue({
      ...missing,
      status: 'running',
      job: {
        id: 'job',
        type: 'preview-proxy',
        status: 'running',
        message: '正在核查帧时间',
        createdAt: '',
      },
    }),
    clear: vi.fn().mockResolvedValue(null),
    cancel: vi.fn().mockResolvedValue(null),
  };
}
beforeEach(() => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
  fetchGuard = vi.fn().mockRejectedValue(Error('禁止真实模型/网络'));
  vi.stubGlobal('fetch', fetchGuard);
  container = document.createElement('div');
  document.body.append(container);
  root = createRoot(container);
});
afterEach(async () => {
  await act(async () => root.unmount());
  container.remove();
  expect(fetchGuard).not.toHaveBeenCalled();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});
async function render(service: PreviewProxyAPI, epoch = 4) {
  await act(async () =>
    root.render(
      <PreviewProxyPanel projectId="p" sourceEpoch={epoch} asset={asset} api={service} />,
    ),
  );
}
async function expand() {
  await act(async () => {
    const details = container.querySelector('details')!;
    details.open = true;
    details.dispatchEvent(new Event('toggle'));
  });
}
async function click(text: string) {
  const value = Array.from(container.querySelectorAll('button')).find(
    (n) => n.textContent?.trim() === text,
  );
  if (!value) throw Error(`未找到${text}`);
  await act(async () => value.click());
}
it('折叠不发起代理任务，明确生成和取消复用本地任务接口', async () => {
  const service = api();
  await render(service);
  expect(service.get).not.toHaveBeenCalled();
  await expand();
  expect(service.start).not.toHaveBeenCalled();
  await click('生成/核对代理');
  expect(service.start).toHaveBeenCalledWith(
    expect.objectContaining({ projectId: 'p', sourceEpoch: 4, profile: '720p' }),
  );
  expect(container.textContent).toContain('正在核查帧时间');
  await click('取消代理任务');
  expect(service.cancel).toHaveBeenCalledWith('job');
});
it('就绪显示真实尺寸/帧数，活跃播放清理失败保留来源和重试', async () => {
  const service = api();
  service.get = vi.fn().mockResolvedValue(ready);
  service.clear = vi
    .fn()
    .mockRejectedValueOnce(Error('请先回到工作区预览'))
    .mockResolvedValue(null);
  await render(service);
  await expand();
  expect(container.textContent).toContain('960×540 · 60帧');
  await click('清理该档代理');
  expect(container.textContent).toContain('请先回到工作区预览');
  await click('清理该档代理');
  expect(service.clear).toHaveBeenCalledTimes(2);
});
it('工程实例变化丢弃旧结果，失败后保留档位可重试', async () => {
  const service = api();
  let resolve!: (v: PreviewProxyView) => void;
  service.get = vi
    .fn()
    .mockImplementationOnce(
      () =>
        new Promise<PreviewProxyView>((r) => {
          resolve = r;
        }),
    )
    .mockResolvedValue(missing);
  await render(service);
  await expand();
  await render(service, 5);
  await act(async () => resolve(ready));
  expect(container.textContent).not.toContain('960×540');
  service.start = vi.fn().mockRejectedValueOnce(Error('编码器暂不可用')).mockResolvedValue(ready);
  await click('生成/核对代理');
  expect(container.textContent).toContain('编码器暂不可用');
  await click('生成/核对代理');
  expect(container.textContent).toContain('960×540');
  expect(service.start).toHaveBeenLastCalledWith(expect.objectContaining({ sourceEpoch: 5 }));
});
it('冷缓存需明确核对，双击只提交一次', async () => {
  const service = api();
  service.get = vi.fn().mockResolvedValue({ ...missing, status: 'cached' });
  let resolve!: (v: PreviewProxyView) => void;
  service.start = vi.fn(
    () =>
      new Promise<PreviewProxyView>((r) => {
        resolve = r;
      }),
  );
  await render(service);
  await expand();
  const button = Array.from(container.querySelectorAll('button')).find(
    (n) => n.textContent === '核对已有代理',
  )!;
  await act(async () => {
    button.click();
    button.click();
  });
  expect(service.start).toHaveBeenCalledOnce();
  await act(async () => resolve(ready));
  expect(container.textContent).toContain('代理已核对');
});

it('全部代理清理必须先核对确认，取消不清理', async () => {
  const service = api();
  service.get = vi.fn().mockResolvedValue({ ...ready, cacheVideos: 4, cacheBytes: 1048576 });
  await render(service);
  await expand();
  await click('清理全部代理');
  expect(service.clear).not.toHaveBeenCalled();
  let dialog = document.querySelector('[role="dialog"]')!;
  expect(dialog.textContent).toContain('所有工程');
  const cancel = Array.from(dialog.querySelectorAll('button')).find(
    (n) => n.textContent === '保留代理',
  )!;
  await act(async () => cancel.click());
  expect(service.clear).not.toHaveBeenCalled();
  await click('清理全部代理');
  dialog = document.querySelector('[role="dialog"]')!;
  const confirm = Array.from(dialog.querySelectorAll('button')).find(
    (n) => n.textContent === '清理全部代理',
  )!;
  await act(async () => confirm.click());
  expect(service.clear).toHaveBeenCalledWith(
    expect.objectContaining({ clearAll: true, sourceEpoch: 4 }),
  );
  expect(document.querySelector('[role="dialog"]')).toBeNull();
});
