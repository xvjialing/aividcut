// @vitest-environment jsdom
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { ProjectCollectDialog } from '../src/components/ProjectCollectDialog';
import type { ProjectCollectAPI, ProjectCollectionSummary } from '../src/project-collect-types';
import type { Job } from '../src/types';
import {
  absoluteCollectionParent,
  collectionSize,
  defaultCollectionName,
  projectParentDirectory,
  validCollectionName,
} from '../src/lib/project-collect';

const scope = { projectId: 'project', projectRevision: 4, sourceEpoch: 7 };
const summary: ProjectCollectionSummary = {
  assetCount: 5,
  sourceBytes: 10485760,
  unprovenSourceCount: 1,
};
const job: Job = {
  id: 'copy-task',
  type: 'project-collect',
  status: 'queued',
  message: '正在核对',
  createdAt: '2026-10-26T00:00:00Z',
};
let root: Root,
  host: HTMLDivElement,
  api: ProjectCollectAPI,
  started: ReturnType<typeof vi.fn<(...args: unknown[]) => void>>,
  closed: ReturnType<typeof vi.fn<() => void>>;
beforeEach(() => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
  vi.stubGlobal(
    'fetch',
    vi.fn(() => {
      throw new Error('不允许真实网络');
    }),
  );
  host = document.createElement('div');
  document.body.append(host);
  root = createRoot(host);
  api = {
    inspect: vi.fn().mockResolvedValue(summary),
    chooseDirectory: vi.fn().mockResolvedValue(''),
    start: vi.fn().mockResolvedValue(job),
  };
  started = vi.fn();
  closed = vi.fn();
});
afterEach(async () => {
  await act(async () => root.unmount());
  host.remove();
  vi.unstubAllGlobals();
});
const settle = () => new Promise((resolve) => setTimeout(resolve, 0));
async function render(value = scope) {
  await act(async () => {
    root.render(
      <ProjectCollectDialog
        scope={value}
        projectName="讲解剪辑"
        projectPath="/工程/当前.aividcut"
        desktop
        onStarted={started}
        onClose={closed}
        api={api}
      />,
    );
    await settle();
  });
}
function button(text: string) {
  const b = [...document.querySelectorAll<HTMLButtonElement>('button')].find((b) =>
    b.textContent?.includes(text),
  );
  if (!b) throw new Error('找不到' + text);
  return b;
}
async function click(text: string) {
  await act(async () => {
    button(text).click();
    await settle();
  });
}
async function fill(index: number, value: string) {
  await act(async () => {
    const input = document.querySelectorAll<HTMLInputElement>('input')[index];
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(input, value);
    input.dispatchEvent(new Event('input', { bubbles: true }));
  });
}
async function submit() {
  await act(async () => {
    document
      .querySelector('form')!
      .dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
    await settle();
  });
}

it('核对数量和体积但不复制，路径与名称校验关联字段并恢复焦点', async () => {
  await render();
  expect(api.inspect).toHaveBeenCalledWith(scope);
  expect(api.start).not.toHaveBeenCalled();
  expect(document.body.textContent).toContain('5 个素材');
  expect(document.body.textContent).toContain('10 MiB');
  await fill(0, '相对路径');
  await submit();
  let input = document.querySelectorAll('input')[0];
  expect(document.activeElement).toBe(input);
  expect(input.getAttribute('aria-invalid')).toBe('true');
  expect(document.getElementById(input.getAttribute('aria-describedby')!)?.textContent).toContain(
    '完整路径',
  );
  await fill(0, '/备份');
  await fill(1, 'CON.txt');
  await submit();
  input = document.querySelectorAll('input')[1];
  expect(input.getAttribute('aria-invalid')).toBe('true');
  expect(document.activeElement).toBe(input);
  expect(api.start).not.toHaveBeenCalled();
});
it('源缺失时阻止启动，重新核对成功后可继续，保留目录草稿', async () => {
  vi.mocked(api.inspect).mockRejectedValueOnce(new Error('素材缺失，请重新定位'));
  await render();
  expect(button('开始收集副本').disabled).toBe(true);
  expect(document.body.textContent).toContain('素材缺失');
  await fill(0, '/保留目录');
  await click('重新核对素材');
  expect(button('开始收集副本').disabled).toBe(false);
  expect(document.querySelector('input')!.value).toBe('/保留目录');
});
it('原生文件夹选择取消保留输入，成功才填入新目录', async () => {
  await render();
  await fill(0, '/已有输入');
  await click('选择文件夹');
  expect(document.querySelector('input')!.value).toBe('/已有输入');
  vi.mocked(api.chooseDirectory).mockResolvedValueOnce('/新目录');
  await click('选择文件夹');
  expect(document.querySelector('input')!.value).toBe('/新目录');
  expect(api.start).not.toHaveBeenCalled();
});
it('启动失败保留输入与错误，挂起时阻止重复启动，成功交给任务面板', async () => {
  vi.mocked(api.start).mockRejectedValueOnce(new Error('目标已存在'));
  await render();
  await fill(0, '/备份');
  await fill(1, '讲解 副本');
  await submit();
  expect(document.body.textContent).toContain('目标已存在');
  expect(closed).not.toHaveBeenCalled();
  expect(document.querySelectorAll('input')[1].value).toBe('讲解 副本');
  let resolve!: (job: Job) => void;
  vi.mocked(api.start).mockImplementationOnce(
    () =>
      new Promise((done) => {
        resolve = done;
      }),
  );
  await submit();
  await submit();
  expect(api.start).toHaveBeenCalledTimes(2);
  expect(button('取消收集').disabled).toBe(true);
  expect(api.start).toHaveBeenLastCalledWith({
    ...scope,
    parentDirectory: '/备份',
    folderName: '讲解 副本',
  });
  await act(async () => {
    resolve(job);
    await settle();
  });
  expect(started).toHaveBeenCalledWith(job);
  expect(closed).toHaveBeenCalledOnce();
});
it('旧工程实例的核对结果不能覆盖新结果', async () => {
  let resolve!: (value: ProjectCollectionSummary) => void;
  vi.mocked(api.inspect)
    .mockImplementationOnce(
      () =>
        new Promise((done) => {
          resolve = done;
        }),
    )
    .mockResolvedValueOnce({ ...summary, assetCount: 9 });
  await render();
  await render({ ...scope, sourceEpoch: 8 });
  await act(async () => {
    resolve({ ...summary, assetCount: 100 });
    await settle();
  });
  expect(document.body.textContent).toContain('9 个素材');
  expect(document.body.textContent).not.toContain('100 个素材');
});
it('跨平台名称、根目录与体积格式保持明确且有界', () => {
  expect(projectParentDirectory('/file.aividcut')).toBe('/');
  expect(projectParentDirectory('C:\\file.aividcut')).toBe('C:\\');
  expect(projectParentDirectory('C:\\folder\\file.aividcut')).toBe('C:\\folder');
  expect(absoluteCollectionParent('C:\\')).toBe(true);
  expect(absoluteCollectionParent('/')).toBe(true);
  expect(absoluteCollectionParent('relative')).toBe(false);
  for (const name of ['../escape', 'COM1.txt', 'name.', 'a:b'])
    expect(validCollectionName(name)).toBe(false);
  const name = defaultCollectionName('CON.cut');
  expect(validCollectionName(name)).toBe(true);
  expect(Array.from(defaultCollectionName('字'.repeat(200))).length).toBeLessThanOrEqual(80);
  expect(collectionSize(7)).toBe('7 字节');
});
