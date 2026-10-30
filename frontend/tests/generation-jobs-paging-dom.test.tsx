// @vitest-environment jsdom
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { GenerationJobs } from '../src/components/GenerationJobs';
import { generationJob, generationProject } from './generation-fixtures';
import type { GenerationJob } from '../src/generation-types';

let host: HTMLDivElement, root: Root;
const pause = vi.fn(),
  actions = {
    onStop: vi.fn(async () => true),
    onResume: vi.fn(async () => true),
    onAcknowledge: vi.fn(async () => true),
    onImport: vi.fn(async () => true),
  };
beforeEach(() => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
  host = document.createElement('div');
  document.body.append(host);
  root = createRoot(host);
  pause.mockClear();
  vi.spyOn(HTMLMediaElement.prototype, 'pause').mockImplementation(pause);
});
afterEach(async () => {
  await act(async () => root.unmount());
  host.remove();
  vi.restoreAllMocks();
});
function jobs(count: number) {
  return Array.from({ length: count }, (_, i) => {
    const base = generationJob();
    const id = `history-${i}`;
    return {
      ...base,
      id,
      status: 'succeeded' as const,
      request: { ...base.request, id, prompt: `第${i + 1}条记录` },
      assets: [
        {
          id: `asset-${i}`,
          name: `候选${i + 1}.mp4`,
          path: `/saved/${i}.mp4`,
          url: `/local/${i}.mp4`,
          mime: 'video/mp4',
          kind: 'video',
          sha256: 'a'.repeat(64),
          bytes: 100,
          recipeId: id,
        },
      ],
    };
  });
}
async function render(values: GenerationJob[], pendingID = '') {
  await act(async () => {
    root.render(
      <GenerationJobs
        jobs={values}
        projectID={generationProject.id}
        pendingID={pendingID}
        errors={{}}
        onBeforeCompare={pause}
        {...actions}
      />,
    );
    await new Promise((r) => setTimeout(r, 8));
  });
}
function button(label: string) {
  const node = [...document.querySelectorAll<HTMLButtonElement>('button')].find(
    (n) => n.textContent?.trim() === label || n.getAttribute('aria-label') === label,
  );
  if (!node) throw Error('找不到' + label);
  return node;
}
async function click(label: string) {
  await act(async () => {
    button(label).click();
    await new Promise((r) => setTimeout(r, 8));
  });
}
it('2000条默认历史只挂载20个任务卡和20个候选媒体', async () => {
  await render(jobs(2000));
  expect(host.querySelectorAll('.generation-job')).toHaveLength(20);
  expect(host.querySelectorAll('video')).toHaveLength(20);
  expect(host.textContent).toContain('1–20 / 2000');
});
it('末页与缩页保持非空范围，操作在途时不能翻页', async () => {
  const values = jobs(41);
  await render(values);
  await click('下一页任务');
  expect(host.textContent).toContain('21–40 / 41');
  await click('下一页任务');
  expect(host.querySelectorAll('.generation-job')).toHaveLength(1);
  expect(host.textContent).toContain('41–41 / 41');
  await render(values.slice(0, 40));
  expect(host.textContent).toContain('21–40 / 40');
  expect(button('下一页任务').disabled).toBe(true);
  await render(values.slice(0, 40), 'history-20');
  expect(button('上一页任务').disabled).toBe(true);
  expect(host.querySelectorAll('.generation-job')).toHaveLength(20);
});
it('跨页候选选择保留，翻页卸载旧媒体并暂停', async () => {
  await render(jobs(21));
  await click('候选1.mp4 · 加入对比');
  await click('下一页任务');
  expect(pause).toHaveBeenCalled();
  expect(host.querySelectorAll('video')).toHaveLength(1);
  await click('候选21.mp4 · 加入对比');
  expect(host.textContent).toContain('已选 2/2');
  expect(button('并排对比').disabled).toBe(false);
  await click('上一页任务');
  expect(host.textContent).toContain('A · 候选1.mp4');
  expect(host.textContent).toContain('B · 候选21.mp4');
  await click('并排对比');
  expect(button('下一页任务').disabled).toBe(true);
});
