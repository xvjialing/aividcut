// @vitest-environment jsdom
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { ProjectVersionsDialog } from '../src/components/ProjectVersionsDialog';
import type {
  ProjectVersion,
  ProjectVersionPage,
  ProjectVersionsAPI,
} from '../src/project-version-types';

const scope = { projectId: 'project-1', projectRevision: 7, sourceEpoch: 3 };
const saved: ProjectVersion = {
  id: 'version-abc',
  projectId: scope.projectId,
  name: '粗剪完成',
  createdAt: '2026-10-26T10:00:00Z',
  revision: 3,
  assets: 2,
  tracks: 4,
  clips: 5,
  sha256: 'a'.repeat(64),
  beforeRestore: false,
};
const page = (versions: ProjectVersion[] = []): ProjectVersionPage => ({
  versions,
  total: versions.length,
  unreadable: 0,
  offset: 0,
  limit: 20,
});
let root: Root,
  host: HTMLDivElement,
  api: ProjectVersionsAPI,
  restore: ReturnType<typeof vi.fn<(...args: unknown[]) => Promise<void>>>,
  close: ReturnType<typeof vi.fn<() => void>>;
beforeEach(() => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
  vi.stubGlobal(
    'fetch',
    vi.fn(() => {
      throw new Error('测试不允许真实网络');
    }),
  );
  host = document.createElement('div');
  document.body.append(host);
  root = createRoot(host);
  api = {
    list: vi.fn().mockResolvedValue(page()),
    create: vi.fn().mockResolvedValue(saved),
    restore: vi.fn(),
  };
  restore = vi.fn().mockResolvedValue(undefined);
  close = vi.fn();
});
afterEach(async () => {
  await act(async () => root.unmount());
  host.remove();
  vi.unstubAllGlobals();
});
const settle = () => new Promise((resolve) => setTimeout(resolve, 0));
async function render(nextScope = scope) {
  await act(async () => {
    root.render(
      <ProjectVersionsDialog
        scope={nextScope}
        dirty
        onRestore={restore}
        onClose={close}
        api={api}
      />,
    );
    await settle();
  });
}
function button(text: string) {
  const result = [...document.querySelectorAll<HTMLButtonElement>('button')].find((b) =>
    b.textContent?.includes(text),
  );
  if (!result) throw new Error(`找不到按钮${text}`);
  return result;
}
async function click(text: string) {
  await act(async () => {
    button(text).click();
    await settle();
  });
}
async function input(value: string) {
  await act(async () => {
    const node = document.querySelector<HTMLInputElement>('input')!;
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(node, value);
    node.dispatchEvent(new Event('input', { bubbles: true }));
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

it('空态读取不生成版本，名称校验关联字段并保留焦点', async () => {
  await render();
  expect(document.body.textContent).toContain('还没有保存版本');
  expect(api.list).toHaveBeenCalledWith({ ...scope, offset: 0, limit: 20 });
  await submit();
  const field = document.querySelector('input')!;
  expect(field.getAttribute('aria-invalid')).toBe('true');
  expect(document.getElementById(field.getAttribute('aria-describedby')!)?.textContent).toContain(
    '1到80',
  );
  expect(document.activeElement).toBe(field);
  expect(api.create).not.toHaveBeenCalled();
});
it('创建失败保留名称，重复提交只调用一次，成功后刷新实际列表', async () => {
  let reject!: (reason: Error) => void;
  vi.mocked(api.create).mockImplementationOnce(
    () =>
      new Promise((_, fail) => {
        reject = fail;
      }),
  );
  await render();
  await input('字幕校对后');
  await submit();
  await submit();
  expect(api.create).toHaveBeenCalledTimes(1);
  expect(button('保存当前版本').disabled).toBe(true);
  await act(async () => {
    reject(new Error('磁盘空间不足'));
    await settle();
  });
  expect(document.body.textContent).toContain('磁盘空间不足');
  expect(document.querySelector('input')!.value).toBe('字幕校对后');
  vi.mocked(api.list).mockResolvedValue(page([saved]));
  await submit();
  expect(api.create).toHaveBeenLastCalledWith({ ...scope, name: '字幕校对后' });
  expect(document.body.textContent).toContain('工程文件未覆盖');
  expect(document.body.textContent).toContain('核对恢复 · 粗剪完成');
  expect(document.querySelector('input')!.value).toBe('');
});
it('先核对整工程和恢复前备份，失败保留选择，成功才关闭', async () => {
  vi.mocked(api.list).mockResolvedValue(page([saved]));
  await render();
  await click('核对恢复');
  expect(restore).not.toHaveBeenCalled();
  expect(document.body.textContent).toContain('当前工程有未保存的更改');
  expect(document.body.textContent).toContain('撤销历史从此处重新开始');
  restore.mockRejectedValueOnce(new Error('版本内容已变化或损坏'));
  await click('保留当前状态为版本并恢复');
  expect(restore).toHaveBeenCalledWith({ ...scope, id: saved.id, sha256: saved.sha256 });
  expect(close).not.toHaveBeenCalled();
  expect(document.body.textContent).toContain('版本内容已变化或损坏');
  await click('取消恢复');
  expect(document.querySelector('form')).not.toBeNull();
  await click('核对恢复');
  await click('保留当前状态为版本并恢复');
  expect(close).toHaveBeenCalledOnce();
});
it('工程实例变化后忽略旧列表，不暴露旧版本操作', async () => {
  let resolve!: (value: ProjectVersionPage) => void;
  vi.mocked(api.list)
    .mockImplementationOnce(
      () =>
        new Promise((done) => {
          resolve = done;
        }),
    )
    .mockResolvedValueOnce(page([{ ...saved, name: '新实例版本' }]));
  await render();
  expect(document.body.textContent).toContain('正在读取');
  await render({ ...scope, sourceEpoch: 4 });
  await act(async () => {
    resolve(page([{ ...saved, name: '旧实例版本' }]));
    await settle();
  });
  expect(document.body.textContent).toContain('新实例版本');
  expect(document.body.textContent).not.toContain('旧实例版本');
});
it('恢复等待期间禁止重复请求与关闭', async () => {
  let resolve!: () => void;
  restore.mockImplementation(
    () =>
      new Promise<void>((done) => {
        resolve = done;
      }),
  );
  vi.mocked(api.list).mockResolvedValue(page([saved]));
  await render();
  await click('核对恢复');
  await click('保留当前状态为版本并恢复');
  await click('保留当前状态为版本并恢复');
  expect(restore).toHaveBeenCalledOnce();
  expect(button('取消恢复').disabled).toBe(true);
  await act(async () => {
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));
    await settle();
  });
  expect(close).not.toHaveBeenCalled();
  await act(async () => {
    resolve();
    await settle();
  });
  expect(close).toHaveBeenCalledOnce();
});
