// @vitest-environment jsdom
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { beforeEach, afterEach, it, expect, vi } from 'vitest';
import { GenerationPlacementDialog } from '../src/components/GenerationPlacementDialog';
import { GenerationPanel } from '../src/components/GenerationPanel';
import type {
  GenerationPlacementAPI,
  GenerationPlacementPreview,
} from '../src/generation-placement-types';
import { defaultClip } from '../src/lib/editor';
import {
  generationProject,
  generationBootstrap,
  generationJob,
  fakeGenerationAPI,
} from './generation-fixtures';
import type { Bootstrap } from '../src/types';

let root: Root, host: HTMLDivElement, fetchGuard: ReturnType<typeof vi.fn>;
const candidate = {
  id: 'candidate',
  kind: 'image',
  name: '候选.png',
  path: '/saved/candidate.png',
  url: '/local/candidate',
  mime: 'image/png',
  sha256: 'hash',
  bytes: 200,
  recipeId: 'job',
};
const asset = {
  ...generationProject.assets[0],
  ...candidate,
  fingerprint: 'sha256:hash',
  missing: false,
};
const track = {
  id: 'new-video',
  name: '补充镜头',
  kind: 'video' as const,
  locked: false,
  hidden: false,
  muted: false,
};
const oldClip = defaultClip(
  'image',
  'track-video-1',
  0,
  generationProject.settings,
  generationProject.assets[0],
);
const project = {
  ...generationProject,
  tracks: [{ ...track, id: 'track-video-1' }],
  clips: [oldClip],
};
const job = generationJob({ id: 'job', status: 'succeeded', assets: [candidate] });
const clip = {
  ...defaultClip('image', track.id, 10, project.settings, asset),
  id: 'placed',
  durationFrames: 10,
};
const view: GenerationPlacementPreview = {
  id: 'placement-preview',
  projectId: project.id,
  projectRevision: project.revision,
  sourceEpoch: 3,
  createdAt: '2026-10-26T00:00:00Z',
  asset,
  clip,
  track,
  importRequired: true,
  newTrack: true,
  captionCount: 0,
  audioMode: 'muted',
  summary: '在10–20帧新增画面',
  warnings: [],
  applied: false,
};
const result: Bootstrap = {
  ...generationBootstrap,
  sourceEpoch: 3,
  project: {
    ...project,
    revision: 1,
    assets: [...project.assets, asset],
    tracks: [track, ...project.tracks],
    clips: [...project.clips, clip],
  },
};
const onApplied = vi.fn(),
  onClose = vi.fn(),
  pause = vi.fn();
beforeEach(() => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
  fetchGuard = vi.fn().mockRejectedValue(Error('测试禁止网络/模型'));
  vi.stubGlobal('fetch', fetchGuard);
  vi.spyOn(HTMLMediaElement.prototype, 'pause').mockImplementation(() => {});
  host = document.createElement('div');
  document.body.append(host);
  root = createRoot(host);
  onApplied.mockClear();
  onClose.mockClear();
  pause.mockClear();
});
afterEach(async () => {
  await act(async () => root.unmount());
  host.remove();
  expect(fetchGuard).not.toHaveBeenCalled();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});
const service = (): GenerationPlacementAPI => ({
  preview: vi.fn().mockResolvedValue(view),
  apply: vi.fn().mockResolvedValue(result),
});
async function flush() {
  await new Promise((r) => setTimeout(r, 15));
}
async function render(
  api: GenerationPlacementAPI,
  revision = 0,
  video = false,
  shared: (action: () => Promise<Bootstrap>) => Promise<Bootstrap | null> = (action) => action(),
) {
  await act(async () => {
    root.render(
      <GenerationPlacementDialog
        project={{ ...project, revision }}
        sourceEpoch={3}
        job={job}
        candidate={
          video ? { ...candidate, kind: 'video', name: '候选.mp4', mime: 'video/mp4' } : candidate
        }
        currentFrame={() => 7}
        timeRange={{ from: 10, to: 20 }}
        pausePlayback={pause}
        applyToProject={shared}
        onApplied={onApplied}
        onClose={onClose}
        api={api}
      />,
    );
    await flush();
  });
}
function button(text: string, parent: ParentNode = document) {
  const value = Array.from(parent.querySelectorAll('button')).find(
    (b) => b.textContent?.trim() === text,
  );
  if (!value) throw Error(text);
  return value;
}
function field(name: string) {
  const node = Array.from(document.querySelectorAll('label'))
    .find((l) => l.querySelector('.field-label')?.textContent === name)
    ?.querySelector('input,select');
  if (!(node instanceof HTMLInputElement || node instanceof HTMLSelectElement)) throw Error(name);
  return node;
}
async function click(node: HTMLElement) {
  await act(async () => {
    node.click();
    await flush();
  });
}
async function enter(node: HTMLInputElement | HTMLSelectElement, value: string) {
  await act(async () => {
    const type = node instanceof HTMLSelectElement ? HTMLSelectElement : HTMLInputElement;
    Object.getOwnPropertyDescriptor(type.prototype, 'value')!.set!.call(node, value);
    node.dispatchEvent(new Event('input', { bubbles: true }));
    node.dispatchEvent(new Event('change', { bubbles: true }));
    await flush();
  });
}

it('打开不请求或编辑，I/O共享范围带入，明确预览后才可应用，改参数使预览失效', async () => {
  const api = service();
  await render(api);
  expect(api.preview).not.toHaveBeenCalled();
  expect(button('添加并保留撤销').disabled).toBe(true);
  expect(field('开始帧').value).toBe('7');
  await click(button('使用I/O标记区间'));
  expect(field('开始帧').value).toBe('10');
  expect(field('长度 · 帧').value).toBe('10');
  await click(button('预览添加变化'));
  expect(api.preview).toHaveBeenCalledWith(
    expect.objectContaining({
      startFrame: 10,
      durationFrames: 10,
      autoDuration: false,
      sourceInUs: 0,
      audioMode: 'muted',
      sourceEpoch: 3,
    }),
  );
  expect(api.apply).not.toHaveBeenCalled();
  expect(button('添加并保留撤销').disabled).toBe(false);
  await enter(field('开始帧'), '11');
  expect(button('添加并保留撤销').disabled).toBe(true);
});
it('视频可取真实可用全长，源微秒与明确声音策略传给应用层', async () => {
  const api = service();
  await render(api, 0, true);
  await enter(field('源入点 · 秒'), '0.137');
  await enter(field('候选声音'), 'embedded');
  await click(button('预览添加变化'));
  expect(api.preview).toHaveBeenCalledWith(
    expect.objectContaining({
      autoDuration: true,
      durationFrames: 0,
      sourceInUs: 137000,
      audioMode: 'embedded',
    }),
  );
});
it('非法数字关联字段并聚焦，取消不应用', async () => {
  const api = service();
  await render(api);
  await enter(field('开始帧'), '-1');
  await click(button('预览添加变化'));
  await act(async () => flush());
  expect(field('开始帧').getAttribute('aria-invalid')).toBe('true');
  expect(field('开始帧').getAttribute('aria-describedby')).toBeTruthy();
  expect(document.activeElement).toBe(field('开始帧'));
  expect(api.preview).not.toHaveBeenCalled();
  await click(button('取消添加'));
  expect(onClose).toHaveBeenCalledOnce();
  expect(api.apply).not.toHaveBeenCalled();
});
it('共享通道捕获应用失败也保留实际错误和参数，可重试成功并选择实际新片段', async () => {
  const api = service();
  (api.apply as ReturnType<typeof vi.fn>)
    .mockRejectedValueOnce(Error('本机候选暂不可读'))
    .mockResolvedValue(result);
  const shared = async (action: () => Promise<Bootstrap>) => {
    try {
      return await action();
    } catch {
      return null;
    }
  };
  await render(api, 0, false, shared);
  await click(button('预览添加变化'));
  await click(button('添加并保留撤销'));
  expect(document.querySelector('[role="dialog"]')?.textContent).toContain('本机候选暂不可读');
  expect(field('开始帧').value).toBe('7');
  expect(onApplied).not.toHaveBeenCalled();
  await click(button('添加并保留撤销'));
  expect(api.apply).toHaveBeenLastCalledWith(view.id);
  expect(onApplied).toHaveBeenCalledWith(project.id, clip);
  expect(onClose).toHaveBeenCalledOnce();
});
it('旧修订的晚预览不能恢复，撤销后的旧已应用返回也不再选择不存在片段', async () => {
  let resolve!: (v: GenerationPlacementPreview) => void;
  const api = service();
  (api.preview as ReturnType<typeof vi.fn>).mockImplementationOnce(
    () =>
      new Promise<GenerationPlacementPreview>((r) => {
        resolve = r;
      }),
  );
  await render(api);
  await click(button('预览添加变化'));
  await render(api, 1);
  await act(async () => {
    resolve(view);
    await flush();
  });
  expect(button('添加并保留撤销').disabled).toBe(true);
  expect(document.querySelector('[role="dialog"]')?.textContent).toContain('工程已变化');
  await render(api);
  await click(button('预览添加变化'));
  (api.apply as ReturnType<typeof vi.fn>).mockResolvedValue({ ...generationBootstrap, project });
  await click(button('添加并保留撤销'));
  expect(onApplied).not.toHaveBeenCalled();
  expect(document.querySelector('[role="dialog"]')?.textContent).toContain('可能已撤销');
});
it('从并排对比进入添加只保留一个Modal，取消返回原对比入口', async () => {
  const generation = fakeGenerationAPI();
  const second = { ...candidate, id: 'candidate-two', name: '第二候选.png', url: '/local/two' };
  generation.listGenerationJobs.mockResolvedValue([{ ...job, assets: [candidate, second] }]);
  const api = service();
  await act(async () => {
    root.render(
      <GenerationPanel
        project={project}
        sourceEpoch={3}
        currentFrame={() => 7}
        timeRange={{ from: 10, to: 20 }}
        onBootstrap={vi.fn()}
        applyPlacement={(action) => action()}
        placementAPI={api}
        api={generation}
      />,
    );
    await flush();
  });
  await click(button('生成任务 1'));
  const choose = (name: string) =>
    Array.from(document.querySelectorAll('button')).find(
      (b) => b.getAttribute('aria-label') === name,
    )!;
  await click(choose('候选.png · 加入对比'));
  await click(choose('第二候选.png · 加入对比'));
  await click(button('并排对比'));
  await act(async () => flush());
  const pane = document.querySelector('[aria-label="候选B"]')!;
  await click(button('添加到时间线…', pane));
  await act(async () => flush());
  expect(document.querySelectorAll('[role="dialog"]')).toHaveLength(1);
  expect(document.querySelector('[role="dialog"]')?.textContent).toContain('添加生成候选到时间线');
  await click(button('预览添加变化'));
  expect(api.preview).toHaveBeenCalledWith(expect.objectContaining({ candidateId: second.id }));
  await click(button('取消添加'));
  await act(async () => flush());
  expect(document.activeElement).toBe(button('并排对比'));
  expect(generation.submitGeneration).not.toHaveBeenCalled();
});
