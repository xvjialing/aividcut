// @vitest-environment jsdom
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { SourceFrameEvidenceViewer } from '../src/components/SourceFrameEvidenceViewer';
import type { SourceFrameAPI, SourceFrameEvidence } from '../src/source-frame-types';
import { generationProject } from './generation-fixtures';
const frame: SourceFrameEvidence = {
  id: 'a'.repeat(64),
  assetId: 'frame-source',
  assetName: '源视频',
  sourceFingerprint: 'sha256:source',
  requestedPositionUs: 500000,
  positionUs: 500000,
  imageSha256: 'b'.repeat(64),
  width: 960,
  height: 540,
  profile: 'detail',
  recipeVersion: 'detail-jpeg-v1',
};
const project = {
  ...generationProject,
  assets: [
    {
      ...generationProject.assets[0],
      id: frame.assetId,
      name: frame.assetName,
      fingerprint: frame.sourceFingerprint,
      kind: 'video',
    },
  ],
};
let root: Root, host: HTMLDivElement;
beforeEach(() => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
  host = document.createElement('div');
  document.body.append(host);
  root = createRoot(host);
  vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new Error('禁止组件真实网络')));
});
afterEach(async () => {
  await act(async () => root.unmount());
  host.remove();
  expect(fetch).not.toHaveBeenCalled();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});
const api = () => ({
  read: vi
    .fn<SourceFrameAPI['read']>()
    .mockResolvedValue({ evidence: frame, url: 'http://127.0.0.1/derived-frame.jpg' }),
  cancel: vi.fn<SourceFrameAPI['cancel']>().mockResolvedValue(undefined),
});
async function render(value: SourceFrameAPI, epoch = 0, missing = false) {
  await act(async () =>
    root.render(
      <SourceFrameEvidenceViewer
        project={{ ...project, assets: project.assets.map((asset) => ({ ...asset, missing })) }}
        sourceEpoch={epoch}
        runProjectID={project.id}
        frames={[frame]}
        api={value}
      />,
    ),
  );
}
async function click() {
  await act(async () => {
    (document.querySelector('section button') as HTMLButtonElement).click();
    await Promise.resolve();
  });
}
describe('画面证据回看', () => {
  it('记录展示不读图片，明确点击后核对完整身份/哈希才显示', async () => {
    const value = api();
    await render(value);
    expect(value.read).not.toHaveBeenCalled();
    expect(document.querySelector('img')).toBeNull();
    await click();
    expect(value.read).toHaveBeenCalledWith(
      expect.objectContaining({
        sourceEpoch: 0,
        assetId: frame.assetId,
        fingerprint: frame.sourceFingerprint,
        positionUs: 500000,
        profile: 'detail',
      }),
    );
    expect(document.querySelector('img')?.getAttribute('src')).toBe(
      'http://127.0.0.1/derived-frame.jpg',
    );
    expect(document.body.textContent).toContain('不证明CLI模型已收到或理解');
  });
  it('源缺失禁回看，不匹配历史字节时保留错误并隐藏图片', async () => {
    const value = api();
    await render(value, 0, true);
    expect((document.querySelector('section button') as HTMLButtonElement).disabled).toBe(true);
    expect(value.read).not.toHaveBeenCalled();
    await render(value);
    value.read.mockResolvedValue({
      evidence: { ...frame, imageSha256: 'c'.repeat(64) },
      url: 'http://127.0.0.1/wrong.jpg',
    });
    await click();
    expect(document.querySelector('img')).toBeNull();
    expect(document.querySelector('[role=alert]')?.textContent).toContain('历史图像指纹不一致');
  });
  it('同ID重开取消读取，迟到图片不进入新实例', async () => {
    const value = api();
    let resolve!: (v: { evidence: SourceFrameEvidence; url: string }) => void;
    value.read.mockImplementation(
      () =>
        new Promise((r) => {
          resolve = r;
        }),
    );
    await render(value);
    await click();
    expect(document.body.textContent).toContain('正在核对源画面');
    await render(value, 1);
    expect(value.cancel).toHaveBeenCalledTimes(1);
    await act(async () => resolve({ evidence: frame, url: 'http://127.0.0.1/old.jpg' }));
    expect(document.querySelector('img')).toBeNull();
    expect(document.querySelector('[role=dialog]')).toBeNull();
  });
});
