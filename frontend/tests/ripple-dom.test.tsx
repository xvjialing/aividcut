// @vitest-environment jsdom
import { act, StrictMode, useState } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { RippleRangeTools } from '../src/components/RippleRangeTools';
import { PlaybackClock } from '../src/lib/editor';
import type { RippleAPI, TimelineRange } from '../src/ripple-types';
import type { WorkflowPreview } from '../src/workflow-types';
import { speechProject, speechBootstrap } from './speech-fixtures';
let root: Root, host: HTMLDivElement, clock: PlaybackClock;
let fetchGuard: ReturnType<typeof vi.fn>;
const plan: WorkflowPreview = {
  id: 'range-preview',
  workflowId: 'ripple-delete-range',
  workflowVersion: '1',
  projectId: speechProject.id,
  projectRevision: speechProject.revision,
  createdAt: '',
  applied: false,
  request: {
    baseRevision: 3,
    idempotencyKey: 'range-preview',
    label: '全轨删除测试',
    commands: [],
  },
  summary: '全轨删除[5,11)',
  warnings: ['含未选轨'],
  effects: [],
  evidence: [],
  affectedClipIds: ['source-clip'],
  savedFrames: 6,
  rangeSummary: {
    fromFrame: 5,
    toFrame: 11,
    beforeFrames: 60,
    afterFrames: 54,
    deleted: 0,
    split: 1,
    trimmed: 0,
    moved: 1,
    captionChanges: 1,
    affectedTracks: 2,
    totalAffectedClips: 4,
  },
};
const api = () =>
  ({
    preview: vi.fn().mockResolvedValue(plan),
    apply: vi.fn().mockResolvedValue(speechBootstrap),
  }) satisfies RippleAPI;
const apply = vi.fn(async (action: () => Promise<typeof speechBootstrap>) => action());
beforeEach(() => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
  fetchGuard = vi.fn().mockRejectedValue(Error('禁止真实网络'));
  vi.stubGlobal('fetch', fetchGuard);
  host = document.createElement('div');
  document.body.append(host);
  root = createRoot(host);
  clock = new PlaybackClock();
  clock.configure(60, 30);
  apply.mockClear();
});
afterEach(async () => {
  await act(async () => root.unmount());
  clock.pause();
  host.remove();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  expect(fetchGuard).not.toHaveBeenCalled();
});
function Shell({ service, revision = 3 }: { service: RippleAPI; revision?: number }) {
  const [range, setRange] = useState<TimelineRange>({ from: null, to: null });
  return (
    <>
      <RippleRangeTools
        project={{ ...speechProject, revision }}
        sourceEpoch={4}
        clock={clock}
        range={range}
        onRange={setRange}
        busy={false}
        applyToProject={apply}
        api={service}
      />
      <input aria-label="普通文本输入" />
    </>
  );
}
async function render(service: RippleAPI, revision = 3) {
  await act(async () =>
    root.render(
      <StrictMode>
        <Shell service={service} revision={revision} />
      </StrictMode>,
    ),
  );
}
function button(label: string) {
  const n = Array.from(document.querySelectorAll('button')).find(
    (n) => n.textContent?.trim() === label,
  );
  if (!n) throw Error(label);
  return n;
}
async function click(label: string) {
  await act(async () => button(label).click());
}
async function input(label: string, value: string) {
  const node = Array.from(document.querySelectorAll('label'))
    .find((n) => n.querySelector('.field-label')?.textContent === label)
    ?.querySelector('input')!;
  await act(async () => {
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(node, value);
    node.dispatchEvent(new Event('input', { bubbles: true }));
    node.dispatchEvent(new Event('change', { bubbles: true }));
  });
}
function frame(label: string) {
  return Array.from(document.querySelectorAll('label'))
    .find((n) => n.querySelector('.field-label')?.textContent === label)
    ?.querySelector('input')!;
}
it('I/O标记不写工程，O包含当前帧且输入/IME不拦截', async () => {
  const service = api();
  await render(service);
  clock.seek(5);
  await act(async () =>
    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'i', bubbles: true })),
  );
  clock.seek(10);
  await act(async () =>
    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'o', bubbles: true })),
  );
  expect(host.textContent).toContain('I 5 · O 11');
  const text = host.querySelector('input')!;
  await act(async () =>
    text.dispatchEvent(new KeyboardEvent('keydown', { key: 'i', bubbles: true })),
  );
  await act(async () =>
    window.dispatchEvent(
      new KeyboardEvent('keydown', { key: 'i', isComposing: true, bubbles: true }),
    ),
  );
  expect(host.textContent).toContain('I 5 · O 11');
  expect(service.preview).not.toHaveBeenCalled();
  await click('时间区间');
  expect(frame('入点 · 帧').value).toBe('5');
  expect(frame('出点 · 帧（不含）').value).toBe('11');
});
it('数值预览再明确应用，共享通道清除标记与定位', async () => {
  const service = api();
  await render(service);
  await click('时间区间');
  await input('入点 · 帧', '5');
  await input('出点 · 帧（不含）', '11');
  await click('预览全轨影响');
  expect(service.preview).toHaveBeenCalledWith(
    expect.objectContaining({ fromFrame: 5, toFrame: 11, sourceEpoch: 4 }),
  );
  expect(service.apply).not.toHaveBeenCalled();
  expect(document.querySelector('[role=dialog]')?.textContent).toContain('含未选轨');
  await click('应用全轨删除');
  expect(apply).toHaveBeenCalledOnce();
  expect(service.apply).toHaveBeenCalledWith('range-preview');
  expect(document.querySelector('[role=dialog]')).toBeNull();
  expect(clock.getFrame()).toBe(5);
});
it('非法范围关联输入错误，锁轨失败保留参数可重试', async () => {
  const service = api();
  service.preview.mockRejectedValueOnce(Error('音频轨已锁定')).mockResolvedValue(plan);
  await render(service);
  await click('时间区间');
  await input('入点 · 帧', '12');
  await input('出点 · 帧（不含）', '10');
  await click('预览全轨影响');
  expect(service.preview).not.toHaveBeenCalled();
  expect(frame('出点 · 帧（不含）').getAttribute('aria-invalid')).toBe('true');
  await input('入点 · 帧', '5');
  await input('出点 · 帧（不含）', '11');
  await click('预览全轨影响');
  expect(document.body.textContent).toContain('音频轨已锁定');
  expect(frame('入点 · 帧').value).toBe('5');
  await click('预览全轨影响');
  expect(service.preview).toHaveBeenCalledTimes(2);
});
it('迟到预览不能覆盖新修订，旧候选不可应用', async () => {
  const service = api();
  let resolve!: (value: WorkflowPreview) => void;
  service.preview = vi.fn(
    () =>
      new Promise<WorkflowPreview>((r) => {
        resolve = r;
      }),
  );
  await render(service);
  await click('时间区间');
  await input('入点 · 帧', '5');
  await input('出点 · 帧（不含）', '11');
  await click('预览全轨影响');
  await render(service, 4);
  await act(async () => resolve(plan));
  expect(button('应用全轨删除').disabled).toBe(true);
  expect(service.apply).not.toHaveBeenCalled();
});
