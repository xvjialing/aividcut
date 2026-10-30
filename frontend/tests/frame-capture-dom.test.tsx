// @vitest-environment jsdom
import { act, StrictMode } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { beforeEach, afterEach, it, expect, vi } from 'vitest';
import { FrameCapturePanel } from '../src/components/FrameCapturePanel';
import type { FrameCaptureAPI, FrameCaptureResult } from '../src/frame-capture-types';
import { speechProject, speechBootstrap, speechSource } from './speech-fixtures';
let root: Root, host: HTMLDivElement;
let fetchGuard: ReturnType<typeof vi.fn>;
const result: FrameCaptureResult = {
  bootstrap: speechBootstrap,
  assetId: 'captured-test',
  imported: true,
  duplicate: false,
  origin: {
    version: 'frame-capture-v1',
    input: {
      id: 'capture',
      kind: 'timeline',
      frame: 5,
      projectId: speechProject.id,
      projectRevision: 3,
      sourceEpoch: 4,
    },
    signature: 'hash',
    projectHash: 'project',
    sources: [],
    renderer: 'native-full-original',
    approximate: false,
    applied: true,
    asset: {
      ...speechSource,
      id: 'captured-test',
      kind: 'image',
      name: '合成参考 · 帧5',
      path: '/fixture/frame.png',
      width: 640,
      height: 360,
    },
  },
};
function api(): FrameCaptureAPI {
  return { capture: vi.fn().mockResolvedValue(result), cancel: vi.fn().mockResolvedValue(null) };
}
const shared = vi.fn(async (action: () => Promise<typeof speechBootstrap>) => action());
const captured = vi.fn();
beforeEach(() => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
  fetchGuard = vi.fn().mockRejectedValue(Error('禁止真实网络/生成'));
  vi.stubGlobal('fetch', fetchGuard);
  host = document.createElement('div');
  document.body.append(host);
  root = createRoot(host);
  shared.mockClear();
  captured.mockClear();
});
afterEach(async () => {
  await act(async () => root.unmount());
  host.remove();
  expect(fetchGuard).not.toHaveBeenCalled();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});
async function render(service: FrameCaptureAPI, revision = 3, epoch = 4) {
  await act(async () =>
    root.render(
      <StrictMode>
        <FrameCapturePanel
          project={{ ...speechProject, revision }}
          sourceEpoch={epoch}
          currentFrame={() => 5}
          intent={{
            id: 'intent',
            projectId: speechProject.id,
            sourceEpoch: epoch,
            kind: 'timeline',
            frame: 5,
          }}
          applyToProject={shared}
          onCaptured={captured}
          api={service}
        />
      </StrictMode>,
    ),
  );
}
async function click(text: string) {
  const value = Array.from(host.querySelectorAll('button')).find((n) => n.textContent === text);
  if (!value) throw Error(text);
  await act(async () => value.click());
}
it('无连接也可准备本机图片，明确保存才调用共享通道', async () => {
  const service = api();
  await render(service);
  expect(service.capture).not.toHaveBeenCalled();
  expect(host.textContent).toContain('不使用低精度/代理画面');
  await click('保存为参考图片');
  expect(service.capture).toHaveBeenCalledWith(
    expect.objectContaining({ kind: 'timeline', frame: 5, projectRevision: 3, sourceEpoch: 4 }),
  );
  expect(shared).toHaveBeenCalledOnce();
  expect(captured).toHaveBeenCalledWith(result);
  expect(host.textContent).toContain('尚未上传或生成');
});
it('未知结果重试保持原操作和原修订，不重新绑定新版本', async () => {
  const service = api();
  (service.capture as ReturnType<typeof vi.fn>)
    .mockRejectedValueOnce(Error('响应丢失'))
    .mockResolvedValue(result);
  await render(service);
  await click('保存为参考图片');
  const first = (service.capture as ReturnType<typeof vi.fn>).mock.calls[0][0];
  await render(service, 4);
  await click('保存为参考图片');
  expect((service.capture as ReturnType<typeof vi.fn>).mock.calls[1][0]).toEqual(first);
});
it('取消使用正在保存的操作ID，晚结果不跨工程实例', async () => {
  const service = api();
  let resolve!: (value: FrameCaptureResult) => void;
  service.capture = vi.fn(
    () =>
      new Promise<FrameCaptureResult>((r) => {
        resolve = r;
      }),
  );
  await render(service);
  await click('保存为参考图片');
  await click('取消取图');
  expect(service.cancel).toHaveBeenCalledWith(
    expect.objectContaining({
      projectId: speechProject.id,
      sourceEpoch: 4,
      id: expect.any(String),
    }),
  );
  await render(service, 3, 5);
  await act(async () => resolve(result));
  expect(captured).not.toHaveBeenCalled();
});
it('撤销后的已完成取图不会选入缺失参考', async () => {
  const service = api();
  (service.capture as ReturnType<typeof vi.fn>).mockResolvedValue({
    ...result,
    duplicate: true,
    imported: false,
  });
  await render(service);
  await click('保存为参考图片');
  expect(captured).not.toHaveBeenCalled();
  expect(host.textContent).toContain('撤销后未重新导入');
});
