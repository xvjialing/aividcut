// @vitest-environment jsdom
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { ShotIndexPanel } from '../src/components/ShotIndexPanel';
import type { ShotIndexAPI, ShotIndexInput, ShotIndexView } from '../src/shot-index-types';
import type { Asset } from '../src/types';
const asset: Asset = {
  id: 'source',
  name: '视频',
  kind: 'video',
  path: '/fixture.mp4',
  fingerprint: 'sha256:fixture',
  durationUs: 2e6,
  width: 640,
  height: 360,
  fpsNum: 25,
  fpsDen: 1,
  hasAudio: false,
  missing: false,
};
function response(
  input: ShotIndexInput,
  status: ShotIndexView['status'] = 'missing',
): ShotIndexView {
  return {
    status,
    projectId: input.projectId,
    sourceEpoch: input.sourceEpoch,
    assetId: input.assetId,
    cached: false,
  };
}
function ready(input: ShotIndexInput): ShotIndexView {
  return {
    ...response(input, 'ready'),
    cached: true,
    index: {
      version: 'scdet-source-v1',
      key: 'a'.repeat(64),
      sourceFingerprint: asset.fingerprint!,
      sourceDurationUs: 2e6,
      detectorVersion: 'fixture',
      config: { threshold: 10, minDurationUs: 250000 },
      coverageStartUs: 0,
      coverageEndUs: 2e6,
      decodedFrames: 50,
      rawCutCount: 1,
      shots: [
        { id: 'shot-one', startUs: 0, endUs: 1e6, sampleUs: 500000 },
        { id: 'shot-two', startUs: 1e6, endUs: 2e6, sampleUs: 1500000 },
      ],
    },
  };
}
let host: HTMLDivElement, root: Root;
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
function api() {
  return {
    get: vi.fn<ShotIndexAPI['get']>().mockImplementation(async (input) => response(input)),
    start: vi.fn<ShotIndexAPI['start']>().mockImplementation(async (input) => ({
      ...response(input, 'running'),
      job: {
        id: 'job',
        type: 'shot-index',
        status: 'running',
        message: '已扫描到1秒',
        createdAt: '',
      },
    })),
    cancel: vi.fn<ShotIndexAPI['cancel']>().mockResolvedValue(undefined),
  };
}
async function render(value: ShotIndexAPI, epoch = 0, onSelect = vi.fn()) {
  await act(async () =>
    root.render(
      <ShotIndexPanel
        projectId="project"
        sourceEpoch={epoch}
        asset={asset}
        onSelect={onSelect}
        api={value}
      />,
    ),
  );
}
async function expand() {
  await act(async () => {
    const details = document.querySelector('details')!;
    details.open = true;
    details.dispatchEvent(new Event('toggle'));
    await Promise.resolve();
  });
}
function button(label: string) {
  const node = [...document.querySelectorAll('button')].find(
    (value) => value.textContent?.trim() === label,
  );
  if (!node) throw Error(label);
  return node;
}
async function click(label: string) {
  await act(async () => {
    button(label).click();
    await Promise.resolve();
  });
}
describe('本地镜头索引面板', () => {
  it('展开只查询元数据，明确检测才启动，可取消且不会自动重复检测', async () => {
    const value = api();
    await render(value);
    expect(value.get).not.toHaveBeenCalled();
    expect(value.start).not.toHaveBeenCalled();
    await expand();
    expect(value.get).toHaveBeenCalledTimes(1);
    expect(value.start).not.toHaveBeenCalled();
    await click('检测/读取镜头');
    expect(value.start).toHaveBeenCalledWith(
      expect.objectContaining({ threshold: 10, minDurationUs: 250000, sourceEpoch: 0 }),
    );
    expect(document.body.textContent).toContain('已扫描到1秒');
    await click('取消镜头检测');
    expect(value.cancel).toHaveBeenCalledWith('job');
    expect(value.start).toHaveBeenCalledTimes(1);
  });
  it('实际覆盖与取样显示，点击段内取样不编辑工程或运行模型', async () => {
    const value = api();
    value.get.mockImplementation(async (input) => ready(input));
    const select = vi.fn();
    await render(value, 0, select);
    await expand();
    expect(document.body.textContent).toContain('实际视频覆盖 0.000–2.000秒');
    await click('浏览该段取样 · 1.500秒');
    expect(select).toHaveBeenCalledWith(1500000);
    expect(value.start).not.toHaveBeenCalled();
  });
  it('失败可重试、旧工程实例的迟到结果不恢复', async () => {
    const value = api();
    value.get.mockRejectedValueOnce(new Error('缓存读取失败'));
    await render(value);
    await expand();
    expect(document.body.textContent).toContain('缓存读取失败');
    let resolve!: (result: ShotIndexView) => void;
    let input!: ShotIndexInput;
    value.get.mockImplementation((q) => {
      input = q;
      return new Promise((r) => {
        resolve = r;
      });
    });
    await click('刷新镜头状态');
    const staleResolve = resolve,
      staleInput = input;
    await render(value, 1);
    await act(async () => staleResolve(ready(staleInput)));
    expect(document.body.textContent).not.toContain('2段候选');
    expect(value.start).not.toHaveBeenCalled();
  });
});
