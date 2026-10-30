// @vitest-environment jsdom
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { SnapshotStorageDialog } from '../src/components/SnapshotStorageDialog';
import type { SnapshotStorageAPI, StorageItem, StoragePage } from '../src/snapshot-storage-types';

const scope = { projectId: 'project', projectRevision: 5, sourceEpoch: 2 };
const item: StorageItem = {
  id: 'version-a',
  name: '粗剪版本',
  revision: 3,
  time: '2026-10-27T00:00:00Z',
  bytes: 4096,
  readable: true,
  canPurge: true,
  locked: false,
  token: 'opaque-proof',
};
const page = (items: StorageItem[] = [item]): StoragePage => ({
  items,
  total: items.length,
  totalBytes: items.reduce((n, v) => n + v.bytes, 0),
  offset: 0,
  limit: 20,
});
let root: Root,
  host: HTMLDivElement,
  api: SnapshotStorageAPI,
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
    change: vi
      .fn()
      .mockResolvedValue({ kind: 'version', id: item.id, action: 'trash', alreadyApplied: false }),
  };
  close = vi.fn();
});
afterEach(async () => {
  await act(async () => root.unmount());
  host.remove();
  vi.unstubAllGlobals();
});
const settle = () => new Promise((resolve) => setTimeout(resolve, 0));
async function render(next = scope) {
  await act(async () => {
    root.render(
      <SnapshotStorageDialog scope={next} initialKind="version" onClose={close} api={api} />,
    );
    await settle();
  });
}
function button(text: string) {
  const node = [...document.querySelectorAll<HTMLButtonElement>('button')].find((b) =>
    b.textContent?.includes(text),
  );
  if (!node) throw new Error('找不到' + text);
  return node;
}
async function click(text: string) {
  await act(async () => {
    button(text).click();
    await settle();
  });
}
async function tab(label: string) {
  await act(async () => {
    [...document.querySelectorAll<HTMLButtonElement>('[role="tab"]')]
      .find((b) => b.textContent === label)!
      .click();
    await settle();
  });
}

it('类型/位置只读取资料，展示文件大小，不修改工程', async () => {
  await render();
  expect(api.list).toHaveBeenCalledWith({
    ...scope,
    kind: 'version',
    view: 'active',
    offset: 0,
    limit: 20,
  });
  expect(api.change).not.toHaveBeenCalled();
  expect(document.body.textContent).toContain('回收站仍占磁盘空间');
  await tab('恢复副本');
  expect(api.list).toHaveBeenLastCalledWith({
    ...scope,
    kind: 'recovery',
    view: 'active',
    offset: 0,
    limit: 20,
  });
  await tab('回收站');
  expect(api.list).toHaveBeenLastCalledWith({
    ...scope,
    kind: 'recovery',
    view: 'trash',
    offset: 0,
    limit: 20,
  });
});
it('移至回收站前可取消，成功刷新，重复回执不声称当前仍在原位置', async () => {
  await render();
  await click('查看操作');
  expect(api.change).not.toHaveBeenCalled();
  expect(document.activeElement).toBe(button('取消整理'));
  await click('取消整理');
  expect(api.change).not.toHaveBeenCalled();
  await click('查看操作');
  vi.mocked(api.list).mockResolvedValueOnce(page([]));
  await click('移至回收站');
  expect(api.change).toHaveBeenCalledWith({
    ...scope,
    token: item.token,
    action: 'trash',
    confirmPermanent: false,
  });
  expect(document.body.textContent).toContain('已移至回收站');
  expect(close).not.toHaveBeenCalled();
});
it('永久删除有独立核对且初始安全焦点，明确确认才发purge', async () => {
  await render();
  await tab('回收站');
  await click('查看操作');
  await click('永久删除…');
  expect(api.change).not.toHaveBeenCalled();
  expect(document.body.textContent).toContain('此操作不能撤销');
  expect(document.activeElement).toBe(button('取消永久删除'));
  await click('取消永久删除');
  expect(api.change).not.toHaveBeenCalled();
  await click('永久删除…');
  await click('永久删除此资料');
  expect(api.change).toHaveBeenCalledWith({
    ...scope,
    token: item.token,
    action: 'purge',
    confirmPermanent: true,
  });
});
it('锁定和额外内容不能被操作；损坏摘要不伪造修订', async () => {
  vi.mocked(api.list).mockResolvedValue(
    page([{ ...item, locked: true, readable: false, reason: '当前保护点' }]),
  );
  await render();
  await click('查看操作');
  expect(button('移至回收站').disabled).toBe(true);
  expect(document.body.textContent).toContain('当前保护点');
  await click('取消整理');
  vi.mocked(api.list).mockResolvedValueOnce(
    page([{ ...item, canPurge: false, readable: false, reason: '包含额外文件' }]),
  );
  await tab('回收站');
  expect(document.body.textContent).toContain('修订 未提供');
  await click('查看操作');
  expect(button('永久删除…').disabled).toBe(true);
  expect(button('还原到活动列表').disabled).toBe(false);
  expect(api.change).not.toHaveBeenCalled();
});
it('失败保留核对，挂起不重复操作；旧范围响应忽略', async () => {
  await render();
  await click('查看操作');
  vi.mocked(api.change).mockRejectedValueOnce(new Error('资料已变化，请刷新'));
  await click('移至回收站');
  expect(document.body.textContent).toContain('资料已变化');
  expect(close).not.toHaveBeenCalled();
  let resolve!: (value: Awaited<ReturnType<SnapshotStorageAPI['change']>>) => void;
  vi.mocked(api.change).mockImplementationOnce(
    () =>
      new Promise((done) => {
        resolve = done;
      }),
  );
  await click('移至回收站');
  await click('移至回收站');
  expect(api.change).toHaveBeenCalledTimes(2);
  expect(button('取消整理').disabled).toBe(true);
  await act(async () => {
    resolve({ kind: 'version', id: item.id, action: 'trash', alreadyApplied: true });
    await settle();
  });
  expect(document.body.textContent).toContain('此前已处理');
  let old!: (value: StoragePage) => void;
  vi.mocked(api.list).mockImplementationOnce(
    () =>
      new Promise((done) => {
        old = done;
      }),
  );
  await click('刷新资料');
  await render({ ...scope, sourceEpoch: 3 });
  await act(async () => {
    old(page([{ ...item, name: '旧范围' }]));
    await settle();
  });
  expect(document.body.textContent).not.toContain('旧范围');
});
