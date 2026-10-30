// @vitest-environment jsdom
import { act, useState } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { GenerationPanel } from '../src/components/GenerationPanel';
import type { GenerationReusePlan, GenerationReuseAPI } from '../src/generation-reuse-types';
import {
  configuredConnection,
  fakeGenerationAPI,
  generationBootstrap,
  generationJob,
  generationProject,
  openAICapability,
  h3Capability,
} from './generation-fixtures';
import type { Project } from '../src/types';

let root: Root, host: HTMLDivElement, fetchGuard: ReturnType<typeof vi.fn>;
const image = {
  ...generationProject.assets[0],
  id: 'candidate',
  name: '候选.png',
  path: '/saved/candidate.png',
  fingerprint: 'sha256:candidate',
};
const source = generationJob({
  id: 'source-job',
  status: 'succeeded',
  assets: [
    {
      id: image.id,
      kind: 'image',
      name: image.name,
      path: image.path,
      mime: 'image/png',
      sha256: 'candidate',
      bytes: 100,
      recipeId: 'source-job',
    },
  ],
});
const plan: GenerationReusePlan = {
  input: { jobId: source.id, projectId: generationProject.id, sourceEpoch: 3 },
  projectRevision: 0,
  connectionId: configuredConnection.id,
  request: {
    id: '',
    providerId: 'openai',
    modelId: configuredConnection.modelId,
    mode: 'image.generate',
    prompt: '已保存的运镜描述',
    size: '1536x864',
    aspectRatio: '16:9',
    quality: 'high',
    outputFormat: 'png',
    background: 'transparent',
    count: 2,
    parentRecipeId: source.id,
  },
  referenceFingerprints: {},
  warnings: [],
  needsImport: false,
};
beforeEach(() => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
  fetchGuard = vi.fn().mockRejectedValue(Error('禁止真实网络和付费模型'));
  vi.stubGlobal('fetch', fetchGuard);
  vi.spyOn(HTMLMediaElement.prototype, 'pause').mockImplementation(() => {});
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
  await new Promise((r) => setTimeout(r, 20));
}
async function click(element: HTMLElement) {
  await act(async () => {
    element.click();
    await flush();
  });
}
function button(text: string, parent: ParentNode = document) {
  const el = Array.from(parent.querySelectorAll('button')).find(
    (b) => b.textContent?.trim() === text,
  );
  if (!el) throw Error(text);
  return el;
}
function field(text: string) {
  const label = Array.from(document.querySelectorAll('label')).find(
    (l) => l.querySelector('.field-label')?.textContent === text,
  );
  const el = label?.querySelector('input,select,textarea');
  if (!(
    el instanceof HTMLInputElement ||
    el instanceof HTMLSelectElement ||
    el instanceof HTMLTextAreaElement
  ))
    throw Error(text);
  return el;
}
async function enter(
  el: HTMLInputElement | HTMLSelectElement | HTMLTextAreaElement,
  value: string,
) {
  await act(async () => {
    const type =
      el instanceof HTMLSelectElement
        ? HTMLSelectElement
        : el instanceof HTMLTextAreaElement
          ? HTMLTextAreaElement
          : HTMLInputElement;
    Object.getOwnPropertyDescriptor(type.prototype, 'value')!.set!.call(el, value);
    el.dispatchEvent(new Event('input', { bubbles: true }));
    el.dispatchEvent(new Event('change', { bubbles: true }));
    await flush();
  });
}
const reuseAPI = (value = plan): GenerationReuseAPI => ({
  prepare: vi.fn().mockResolvedValue(value),
});
async function render(
  api: ReturnType<typeof fakeGenerationAPI>,
  reuse: GenerationReuseAPI,
  epoch = 3,
  currentProject: Project = generationProject,
) {
  await act(async () => {
    root.render(
      <GenerationPanel
        project={currentProject}
        sourceEpoch={epoch}
        onBootstrap={vi.fn()}
        api={api}
        reuseAPI={reuse}
      />,
    );
    await flush();
  });
}
async function openReuse(count = 1) {
  await click(button(`生成任务 ${count}`));
  await click(button('沿用参数…'));
  await act(async () => flush());
}

it('核对取消保留草稿，跨连接加载不被默认值覆盖，新ID与父配方只在最后确认提交', async () => {
  const api = fakeGenerationAPI(),
    reuse = reuseAPI();
  api.listGenerationJobs.mockResolvedValue([source]);
  api.getConnections.mockResolvedValue([
    {
      ...configuredConnection,
      id: 'h3-connection',
      providerId: 'minimax',
      modelId: h3Capability.modelId,
    },
    configuredConnection,
  ]);
  api.getGenerationCatalog.mockResolvedValue([h3Capability, openAICapability]);
  await render(api, reuse);
  await enter(field('内容描述'), '尚未提交的草稿');
  await openReuse();
  await click(button('保留当前草稿'));
  expect(document.activeElement?.textContent).toBe('沿用参数…');
  await click(button('创作'));
  expect(field('内容描述').value).toBe('尚未提交的草稿');
  await openReuse();
  await click(button('加载到创作草稿'));
  await act(async () => flush());
  expect(document.activeElement).toBe(field('内容描述'));
  expect(field('内容描述').value).toBe(plan.request.prompt);
  expect(field('生成数量').value).toBe('2');
  expect(field('质量').value).toBe('high');
  expect(field('模型连接').value).toBe(configuredConnection.id);
  expect(api.submitGeneration).not.toHaveBeenCalled();
  await click(button('生成前核对…'));
  const d = document.querySelector('[role="dialog"]')!;
  expect(d.textContent).toContain('transparent');
  expect(d.textContent).toContain(source.id);
  await click(button('确认并生成（可能计费）'));
  const sent = api.submitGeneration.mock.calls[0][0];
  expect(sent.request).toMatchObject({ ...plan.request, id: expect.any(String) });
  expect(sent.request.id).not.toBe(source.id);
  expect(sent.context).toEqual({ projectId: generationProject.id, revision: 0, sourceEpoch: 3 });
  expect(source.request.parentRecipeId).toBeUndefined();
});
it('未核对的相同内容任务仍阻止带新父配方的重复生成', async () => {
  const api = fakeGenerationAPI();
  api.listGenerationJobs.mockResolvedValue([
    source,
    generationJob({
      id: 'unknown',
      status: 'unknown',
      remoteId: undefined,
      request: { ...plan.request, id: 'unknown', parentRecipeId: undefined },
    }),
  ]);
  await render(api, reuseAPI());
  await openReuse(2);
  await click(button('加载到创作草稿'));
  expect(button('生成前核对…').disabled).toBe(true);
  expect(api.submitGeneration).not.toHaveBeenCalled();
});
it('图片继续修改明确加载才导入，失败保留核对，成功成为唯一参考且不自动生成', async () => {
  const api = fakeGenerationAPI(),
    value = {
      ...plan,
      input: { ...plan.input, candidateId: image.id },
      request: {
        ...plan.request,
        mode: 'image.edit',
        count: 1,
        references: [{ assetId: image.id, role: 'reference_image' }],
      },
      referenceFingerprints: { candidate: image.fingerprint },
      candidate: image,
      needsImport: true,
    };
  api.listGenerationJobs.mockResolvedValue([source]);
  api.importGenerationAssets.mockRejectedValueOnce(Error('本机夹具导入失败')).mockResolvedValue({
    ...generationBootstrap,
    project: { ...generationProject, revision: 1, assets: [...generationProject.assets, image] },
  });
  const service = reuseAPI(value);
  function StableHarness() {
    const [p, setP] = useState<Project>(generationProject);
    return (
      <GenerationPanel
        project={p}
        sourceEpoch={3}
        api={api}
        reuseAPI={service}
        onBootstrap={(b) => setP(b.project)}
      />
    );
  }
  await act(async () => {
    root.render(<StableHarness />);
    await flush();
  });
  await click(button('生成任务 1'));
  await click(button('以此图片继续修改…'));
  await act(async () => flush());
  expect(api.importGenerationAssets).not.toHaveBeenCalled();
  await click(button('加载并加入图片'));
  expect(document.querySelector('[role="dialog"]')?.textContent).toContain('本机夹具导入失败');
  await click(button('加载并加入图片'));
  expect(api.importGenerationAssets).toHaveBeenLastCalledWith(source.id, [image.id]);
  expect(field('创作任务').value).toBe('image.edit');
  expect(field('生成数量').value).toBe('1');
  await click(button('生成前核对…'));
  expect(document.querySelector('[role="dialog"]')?.textContent).toContain(image.name);
  expect(api.submitGeneration).not.toHaveBeenCalled();
});
it('缺少相同型号连接时禁止加载，晚准备响应不覆盖同ID重开的新草稿', async () => {
  const api = fakeGenerationAPI();
  api.listGenerationJobs.mockResolvedValue([source]);
  const missing = { ...plan, request: { ...plan.request, modelId: 'retired-model' } };
  await render(api, reuseAPI(missing));
  await openReuse();
  expect(button('加载到创作草稿').disabled).toBe(true);
  expect(document.querySelector('[role="dialog"]')?.textContent).toContain('没有相同服务商和型号');
  await click(button('保留当前草稿'));
  let resolve!: (v: GenerationReusePlan) => void;
  const late: GenerationReuseAPI = {
    prepare: vi.fn().mockImplementation(
      () =>
        new Promise<GenerationReusePlan>((r) => {
          resolve = r;
        }),
    ),
  };
  await render(api, late);
  await click(button('沿用参数…'));
  await render(api, late, 4);
  await act(async () => {
    resolve(plan);
    await flush();
  });
  expect(document.querySelector('[role="dialog"]')).toBeNull();
  expect(api.submitGeneration).not.toHaveBeenCalled();
});

it('最终核对期间工程版本变化拒绝提交，不能沿用旧参考确认', async () => {
  const api = fakeGenerationAPI();
  api.listGenerationJobs.mockResolvedValue([source]);
  const service = reuseAPI();
  await render(api, service);
  await openReuse();
  await click(button('加载到创作草稿'));
  await click(button('生成前核对…'));
  await render(api, service, 3, { ...generationProject, revision: 1 });
  await click(button('确认并生成（可能计费）'));
  expect(api.submitGeneration).not.toHaveBeenCalled();
  expect(document.body.textContent).toContain('工程或参考素材已变化');
});
