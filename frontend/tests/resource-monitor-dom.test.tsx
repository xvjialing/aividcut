// @vitest-environment jsdom
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { ResourceMonitorDialog } from '../src/components/ResourceMonitorDialog';
import type { ResourceAPI, ResourceState, ResourceSummary } from '../src/resource-types';
const scope = { projectId: 'p', projectRevision: 3, sourceEpoch: 2 };
const report: ResourceSummary = {
  id: 'profile-a',
  phase: 'preview',
  status: 'running',
  startedAt: '2026-10-27T01:00:00Z',
  finishedAt: null,
  samples: 2,
  sampledPeakRss: 1048576,
  sampledPeakCpu: 50,
};
const state: ResourceState = {
  active: null,
  reports: [],
  latest: {
    project: {
      projectId: 'p',
      revision: 3,
      sourceEpoch: 2,
      width: 640,
      height: 360,
      fpsNum: 30,
      fpsDen: 1,
      clips: 4,
      assets: 6,
      tracks: 4,
    },
    snapshot: {
      at: '2026-10-27T01:00:00Z',
      queryMs: 1.2,
      windowMs: null,
      rootPid: 1,
      processes: [
        {
          pid: 1,
          ppid: 0,
          name: 'AIVidCut',
          identity: '1:100',
          role: 'application',
          association: 'root',
          rssBytes: 1048576,
          privateBytes: null,
          cpuTimeNs: 10,
          cpuPercent: null,
        },
      ],
      knownRssBytes: 1048576,
      rssBytes: 1048576,
      cpuPercent: null,
      missingRss: 0,
      missingCpu: 1,
      partial: false,
      issues: [],
      uncovered: ['独立WebKit XPC未计入'],
      os: 'darwin',
      architecture: 'arm64',
      logicalCpus: 8,
    },
  },
};
let host: HTMLDivElement,
  root: Root,
  api: ResourceAPI,
  close: ReturnType<typeof vi.fn<() => void>>,
  recording: ReturnType<typeof vi.fn<(value: boolean) => void>>;
beforeEach(() => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
  host = document.createElement('div');
  document.body.append(host);
  root = createRoot(host);
  api = {
    state: vi.fn().mockResolvedValue(state),
    status: vi.fn().mockResolvedValue(state),
    start: vi.fn().mockResolvedValue(report),
    stop: vi.fn().mockResolvedValue({ ...report, status: 'stopped' }),
    choosePath: vi.fn(),
    export: vi.fn().mockResolvedValue('/tmp/resources.json'),
  };
  close = vi.fn();
  recording = vi.fn();
});
afterEach(async () => {
  await act(async () => root.unmount());
  host.remove();
  vi.restoreAllMocks();
});
const mount = async () => {
  await act(async () =>
    root.render(
      <ResourceMonitorDialog
        scope={scope}
        onClose={close}
        onRecording={recording}
        onReturnFocus={vi.fn()}
        api={api}
      />,
    ),
  );
};
const click = async (text: string) => {
  await act(async () => {
    const button = [...document.querySelectorAll<HTMLButtonElement>('button')].find((b) =>
      b.textContent?.includes(text),
    );
    if (!button) throw Error(text);
    button.click();
  });
};
it('打开只读采样、不自动开始记录，缺失CPU/GPU不补成零', async () => {
  await mount();
  expect(api.state).toHaveBeenCalledTimes(1);
  expect(api.start).not.toHaveBeenCalled();
  expect(document.body.textContent).toContain('1.0 MiB');
  expect(document.body.textContent).toContain('未提供');
  expect(document.body.textContent).not.toContain('0.0%');
  expect(document.querySelector('table caption')?.textContent).toContain('已确认应用进程');
  await act(async () => {
    document.querySelector('summary')?.click();
  });
  expect(document.body.textContent).toContain('独立WebKit XPC未计入');
});
it('明确开始/停止、重复提交保护、关闭后保留后台记录', async () => {
  await mount();
  let finish!: (r: ResourceSummary) => void;
  vi.mocked(api.start).mockImplementationOnce(() => new Promise((resolve) => (finish = resolve)));
  vi.mocked(api.status).mockResolvedValue({ ...state, active: report, reports: [report] });
  await click('开始记录');
  expect(api.start).toHaveBeenCalledWith({ ...scope, phase: 'preview' });
  expect(document.querySelector<HTMLButtonElement>('button[aria-busy="true"]')?.disabled).toBe(
    true,
  );
  await act(async () => finish(report));
  expect(document.body.textContent).toContain('记录中');
  await click('关闭窗口，继续记录');
  expect(close).toHaveBeenCalledTimes(1);
  expect(api.stop).not.toHaveBeenCalled();
  vi.mocked(api.status).mockResolvedValue({
    ...state,
    active: null,
    reports: [{ ...report, status: 'stopped' }],
  });
  await click('停止记录');
  expect(api.stop).toHaveBeenCalledWith(report.id);
  expect(recording).toHaveBeenLastCalledWith(false);
});
it('导出只发送已选记录身份和人填写的路径，失败保留输入', async () => {
  vi.mocked(api.state).mockResolvedValue({ ...state, reports: [{ ...report, status: 'stopped' }] });
  await mount();
  const input = document.querySelector<HTMLInputElement>('section input:not([type="checkbox"])')!;
  await act(async () => {
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(
      input,
      '/tmp/resources.json',
    );
    input.dispatchEvent(new Event('input', { bubbles: true }));
  });
  vi.mocked(api.export).mockRejectedValueOnce(new Error('同名报告已存在'));
  await click('导出JSON报告');
  expect(api.export).toHaveBeenCalledWith({
    id: report.id,
    path: '/tmp/resources.json',
    overwrite: false,
  });
  expect(document.body.textContent).toContain('同名报告已存在');
  expect(input.value).toBe('/tmp/resources.json');
  await click('导出JSON报告');
  expect(document.body.textContent).toContain('诊断报告已保存');
});
it('读取失败可以刷新，关闭后的迟到响应不更新窗口', async () => {
  vi.mocked(api.state).mockRejectedValueOnce(new Error('当前平台不可读取'));
  await mount();
  expect(document.body.textContent).toContain('当前平台不可读取');
  await click('刷新');
  expect(document.body.textContent).toContain('AIVidCut');
  let finish!: (s: ResourceState) => void;
  vi.mocked(api.state).mockImplementationOnce(() => new Promise((resolve) => (finish = resolve)));
  await click('刷新');
  await act(async () => root.unmount());
  await act(async () => finish({ ...state, active: report }));
  expect(recording).not.toHaveBeenLastCalledWith(true);
  root = createRoot(host);
});
