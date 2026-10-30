// @vitest-environment jsdom
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { GenerationJobs } from '../src/components/GenerationJobs';
import { GenerationPanel } from '../src/components/GenerationPanel';
import type { GenerationJob } from '../src/generation-types';
import { fakeGenerationAPI, generationJob, generationProject } from './generation-fixtures';

let root: Root, host: HTMLDivElement;
let fetchGuard: ReturnType<typeof vi.fn>;
const onImport = vi.fn(async () => true);
const onReplace = vi.fn();
const pause = vi.fn();
const jobs: GenerationJob[] = ['one', 'two', 'three'].map((id, index) =>
  generationJob({
    id,
    status: 'succeeded',
    assets: [
      {
        id: `asset-${id}`,
        kind: index === 0 ? 'image' : 'video',
        name: `${id}.${index === 0 ? 'png' : 'mp4'}`,
        path: `/saved/${id}`,
        url: `/local/${id}`,
        mime: index === 0 ? 'image/png' : 'video/mp4',
        sha256: `sha-${id}`,
        bytes: 100,
        recipeId: `recipe-${id}`,
      },
    ],
    imported: false,
  }),
);

beforeEach(() => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
  fetchGuard = vi.fn().mockRejectedValue(Error('测试禁止真实网络或模型调用'));
  vi.stubGlobal('fetch', fetchGuard);
  vi.spyOn(HTMLMediaElement.prototype, 'pause').mockImplementation(pause);
  onImport.mockReset().mockResolvedValue(true);
  onReplace.mockReset();
  pause.mockClear();
  host = document.createElement('div');
  document.body.append(host);
  root = createRoot(host);
});
afterEach(async () => {
  await act(async () => root.unmount());
  host.remove();
  expect(fetchGuard).not.toHaveBeenCalled();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});
async function flush() {
  await new Promise((resolve) => setTimeout(resolve, 15));
}
async function render(
  current = jobs,
  errors: Record<string, string> = {},
  projectID = generationProject.id,
) {
  await act(async () => {
    root.render(
      <GenerationJobs
        jobs={current}
        projectID={projectID}
        pendingID=""
        errors={errors}
        onImport={onImport}
        onReplace={onReplace}
        onStop={vi.fn()}
        onResume={vi.fn()}
        onAcknowledge={vi.fn()}
      />,
    );
    await flush();
  });
}
function button(label: string, parent: ParentNode = document) {
  const node = Array.from(parent.querySelectorAll('button')).find(
    (n) => n.getAttribute('aria-label') === label || n.textContent?.trim() === label,
  );
  if (!node) throw Error(`未找到按钮：${label}`);
  return node;
}
async function click(node: HTMLElement) {
  await act(async () => {
    node.click();
    await flush();
  });
}
async function choose(first = 0, second = 1) {
  await click(button(`${jobs[first].assets[0].name} · 加入对比`));
  await click(button(`${jobs[second].assets[0].name} · 加入对比`));
  await click(button('并排对比'));
  await waitForComparison();
}
async function waitForComparison() {
  const deadline = Date.now() + 1500;
  while (!document.querySelector('[role="dialog"]') && Date.now() < deadline) {
    await act(async () => flush());
  }
  if (!document.querySelector('[role="dialog"]')) throw Error('候选对比未完成加载');
}
function dialog() {
  const node = document.querySelector('[role="dialog"]');
  if (!node) throw Error('未打开对比');
  return node;
}

it('相同结果文件名仍按任务与内容身份分别选择，选择摘要保留描述', async () => {
  const duplicateNames = jobs.slice(0, 2).map((job, index) => ({
    ...job,
    request: { ...job.request, prompt: index === 0 ? '人物特写版本' : '环境空镜版本' },
    assets: [{ ...job.assets[0], name: 'result-01.mp4' }],
  }));
  await render(duplicateNames);
  for (const article of Array.from(host.querySelectorAll('article')))
    await click(button('result-01.mp4 · 加入对比', article));
  const selected = host.querySelector('[aria-label="候选对比选择"]')!;
  expect(selected.textContent).toContain('人物特写版本');
  expect(selected.textContent).toContain('环境空镜版本');
  await click(button('并排对比'));
  await waitForComparison();
  expect(dialog().querySelectorAll('.candidate-compare-pane')).toHaveLength(2);
  expect(onImport).not.toHaveBeenCalled();
});

it('跨任务选择至多两项，对比与导入选择独立，已导入仍可比较', async () => {
  await render([{ ...jobs[0], imported: true }, ...jobs.slice(1)]);
  expect(button('并排对比').disabled).toBe(true);
  await choose();
  expect(dialog().textContent).toContain('A · one.png');
  expect(dialog().textContent).toContain('B · two.mp4');
  expect(button('已加入素材库', dialog()).disabled).toBe(true);
  expect(host.querySelectorAll('.candidate-select input:checked')).toHaveLength(1);
  expect(onImport).not.toHaveBeenCalled();
  expect(onReplace).not.toHaveBeenCalled();
  expect(host.querySelectorAll('video')).toHaveLength(0);
  await click(button('返回生成任务', dialog()));
  expect(document.activeElement).toBe(button('并排对比'));
  expect(button('three.mp4 · 加入对比').disabled).toBe(true);
  await click(button('移除对比候选A'));
  expect(button('three.mp4 · 加入对比').disabled).toBe(false);
  await click(button('清空对比'));
  expect(button('并排对比').disabled).toBe(true);
});

it('仅明确导入一个候选，失败仍保留对比，轮询导入状态后不重复添加', async () => {
  onImport.mockResolvedValueOnce(false).mockResolvedValue(true);
  await render();
  await choose();
  const pane = dialog().querySelector('[aria-label="候选B"]')!;
  await click(button('加入素材库', pane));
  expect(onImport).toHaveBeenCalledWith(jobs[1], ['asset-two']);
  await render(jobs, { two: '夹具导入失败，原工程保留' });
  expect(dialog().textContent).toContain('夹具导入失败');
  await click(button('加入素材库', dialog().querySelector('[aria-label="候选B"]')!));
  await render(
    jobs.map((job) => (job.id === 'two' ? { ...job, importedAssetIds: ['asset-two'] } : job)),
  );
  expect(button('已加入素材库', dialog()).disabled).toBe(true);
  expect(onImport).toHaveBeenCalledTimes(2);
  expect(onReplace).not.toHaveBeenCalled();
});

it('共享工程通道捕获异常并返回空时，对比窗仍展示实际导入失败原因', async () => {
  const api = fakeGenerationAPI();
  api.listGenerationJobs.mockResolvedValue(jobs);
  api.importGenerationAssets.mockRejectedValue(Error('本机素材暂不可读，请恢复原文件'));
  await act(async () => {
    root.render(
      <GenerationPanel
        project={generationProject}
        onBootstrap={vi.fn()}
        api={api}
        importToProject={async (action) => {
          try {
            return await action();
          } catch {
            return null;
          }
        }}
      />,
    );
    await flush();
  });
  await click(button('生成任务 3'));
  await choose();
  await click(button('加入素材库', dialog().querySelector('[aria-label="候选A"]')!));
  expect(dialog().textContent).toContain('本机素材暂不可读，请恢复原文件');
  expect(api.importGenerationAssets).toHaveBeenCalledWith(jobs[0].id, ['asset-one']);
  expect(api.submitGeneration).not.toHaveBeenCalled();
});

it('替换先退出对比，仅转交当前候选给已有替换流程', async () => {
  await render();
  await choose();
  await click(button('替换当前镜头…', dialog().querySelector('[aria-label="候选B"]')!));
  expect(onReplace).toHaveBeenCalledWith(jobs[1], jobs[1].assets[0]);
  expect(document.querySelector('[role="dialog"]')).toBeNull();
  expect(onImport).not.toHaveBeenCalled();
});

it('视频默认静音、只播放一边且只一边可发声，关闭后释放播放器', async () => {
  await render();
  await choose(1, 2);
  const videos = Array.from(dialog().querySelectorAll('video'));
  expect(videos).toHaveLength(2);
  expect(videos.every((video) => video.muted)).toBe(true);
  const otherPause = vi.fn();
  videos[1].pause = otherPause;
  await act(async () => videos[0].dispatchEvent(new Event('play', { bubbles: true })));
  expect(otherPause).toHaveBeenCalledOnce();
  videos[1].muted = false;
  videos[0].muted = false;
  await act(async () => videos[0].dispatchEvent(new Event('volumechange', { bubbles: true })));
  expect(videos[1].muted).toBe(true);
  await click(button('返回生成任务', dialog()));
  expect(videos.every((node) => !node.isConnected)).toBe(true);
  expect(otherPause.mock.calls.length).toBeGreaterThan(1);
});

it('本机图片读取失败可明确重试，身份变化关闭旧对比，不把新结果混入', async () => {
  await render();
  await choose();
  const pane = dialog().querySelector('[aria-label="候选A"]')!;
  await act(async () => pane.querySelector('img')!.dispatchEvent(new Event('error')));
  expect(pane.textContent).toContain('图片暂时无法读取');
  await click(button('重试读取', pane));
  expect(pane.querySelector('img')).not.toBeNull();
  await render(
    jobs.map((job) =>
      job.id === 'one' ? { ...job, assets: [{ ...job.assets[0], sha256: 'new-sha' }] } : job,
    ),
  );
  expect(document.querySelector('[role="dialog"]')).toBeNull();
  expect(host.textContent).toContain('请重新选择');
  expect(onImport).not.toHaveBeenCalled();
});

it('跨工程或没有地址的结果不可加入对比，同ID重开也清空选择', async () => {
  await render([
    { ...jobs[0], projectId: 'other' },
    { ...jobs[1], assets: [{ ...jobs[1].assets[0], url: undefined }] },
  ]);
  expect(button('one.png · 加入对比').disabled).toBe(true);
  expect(button('two.mp4 · 加入对比').disabled).toBe(true);
  const api = fakeGenerationAPI();
  api.listGenerationJobs.mockResolvedValue(jobs);
  const pauseClock = vi.fn();
  await act(async () => {
    root.render(
      <GenerationPanel
        project={generationProject}
        sourceEpoch={1}
        pausePlayback={pauseClock}
        onBootstrap={vi.fn()}
        api={api}
      />,
    );
    await flush();
  });
  await click(button('生成任务 3'));
  await choose();
  expect(pauseClock).toHaveBeenCalledOnce();
  await act(async () => {
    root.render(
      <GenerationPanel
        project={generationProject}
        sourceEpoch={2}
        onBootstrap={vi.fn()}
        api={api}
      />,
    );
    await flush();
  });
  expect(document.querySelector('[role="dialog"]')).toBeNull();
  expect(button('并排对比').disabled).toBe(true);
  expect(api.submitGeneration).not.toHaveBeenCalled();
});
