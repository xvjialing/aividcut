// @vitest-environment jsdom
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { RecoveryCenterDialog } from '../src/components/RecoveryCenterDialog';
import type {
  RecoveryAPI,
  RecoveryPage,
  RecoveryRecord,
  RecoveryStatus,
} from '../src/recovery-types';

const scope = { projectId: 'current', projectRevision: 3, sourceEpoch: 7 };
const record: RecoveryRecord = {
  formatVersion: 1,
  id: 'recovery-a',
  projectId: 'old',
  name: '遗留工程',
  projectPath: '/projects/old.aividcut',
  revision: 5,
  updatedAt: '2026-10-26T10:00:00Z',
  assets: 3,
  clips: 4,
  sha256: 'a'.repeat(64),
  beforeRestore: false,
};
const page = (records: RecoveryRecord[] = [record]): RecoveryPage => ({
  records,
  total: records.length,
  unreadable: 0,
  offset: 0,
  limit: 20,
});
const status: RecoveryStatus = {
  scope,
  enabled: true,
  intervalSeconds: 5,
  pending: 1,
  startupPending: 1,
  unreadable: 0,
  protectedAt: '2026-10-26T11:00:00Z',
  protectedRevision: 2,
};
let root: Root,
  host: HTMLDivElement,
  api: RecoveryAPI,
  restore: ReturnType<typeof vi.fn<(...args: unknown[]) => Promise<void>>>,
  close: ReturnType<typeof vi.fn<() => void>>;
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
    status: vi.fn().mockResolvedValue(status),
    list: vi.fn().mockResolvedValue(page()),
    checkpoint: vi.fn().mockResolvedValue(status),
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
async function render(value = scope) {
  await act(async () => {
    root.render(
      <RecoveryCenterDialog scope={value} dirty onRestore={restore} onClose={close} api={api} />,
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

it('读取不写入或恢复，明确周期/未提交草稿/未保存副本和来源', async () => {
  await render();
  expect(api.list).toHaveBeenCalledWith({ offset: 0, limit: 20 });
  expect(api.checkpoint).not.toHaveBeenCalled();
  expect(restore).not.toHaveBeenCalled();
  expect(document.body.textContent).toContain('每 5 秒');
  expect(document.body.textContent).toContain('属性输入草稿');
  expect(document.body.textContent).toContain('原文件不自动覆盖');
  expect(document.body.textContent).toContain('/projects/old.aividcut');
  await click('暂不恢复');
  expect(close).toHaveBeenCalledOnce();
  expect(restore).not.toHaveBeenCalled();
});
it('明确核对并保留当前工作，失败保留选择，成功才关闭', async () => {
  await render();
  await click('核对恢复');
  expect(restore).not.toHaveBeenCalled();
  expect(document.body.textContent).toContain('当前工程有未保存的更改');
  restore.mockRejectedValueOnce(new Error('无法保留当前工程的恢复副本'));
  await click('保留当前状态并恢复');
  expect(close).not.toHaveBeenCalled();
  expect(document.body.textContent).toContain('无法保留当前工程');
  expect(restore).toHaveBeenCalledWith({ ...scope, id: record.id, sha256: record.sha256 });
  await click('保留当前状态并恢复');
  expect(close).toHaveBeenCalledOnce();
});
it('手动保护失败保留中心，成功刷新状态；在途不重复提交', async () => {
  await render();
  api.checkpoint = vi.fn().mockRejectedValueOnce(new Error('磁盘不可写')).mockResolvedValue(status);
  await click('立即保护当前工程');
  expect(document.body.textContent).toContain('磁盘不可写');
  expect(close).not.toHaveBeenCalled();
  let resolve!: (value: RecoveryStatus) => void;
  vi.mocked(api.checkpoint).mockImplementationOnce(
    () =>
      new Promise((done) => {
        resolve = done;
      }),
  );
  await click('立即保护当前工程');
  await click('立即保护当前工程');
  expect(api.checkpoint).toHaveBeenCalledTimes(2);
  expect(button('暂不恢复').disabled).toBe(true);
  await act(async () => {
    resolve(status);
    await settle();
  });
  expect(document.body.textContent).toContain('已写入恢复副本');
  expect(close).not.toHaveBeenCalled();
});
it('实例变化丢弃旧列表，读取失败可刷新并保留不可读数量', async () => {
  let resolve!: (value: RecoveryPage) => void;
  vi.mocked(api.list)
    .mockImplementationOnce(
      () =>
        new Promise((done) => {
          resolve = done;
        }),
    )
    .mockResolvedValueOnce(page([{ ...record, name: '新实例记录' }]));
  await render();
  await render({ ...scope, sourceEpoch: 8 });
  await act(async () => {
    resolve(page([{ ...record, name: '旧实例记录' }]));
    await settle();
  });
  expect(document.body.textContent).toContain('新实例记录');
  expect(document.body.textContent).not.toContain('旧实例记录');
  vi.mocked(api.list).mockRejectedValueOnce(new Error('读取失败'));
  await click('刷新恢复列表');
  expect(document.body.textContent).toContain('读取失败');
  vi.mocked(api.list).mockResolvedValueOnce({ ...page([]), unreadable: 1 });
  await click('刷新恢复列表');
  expect(document.body.textContent).toContain('1 份恢复摘要无法读取');
});
it('恢复期间不能重复或Escape关闭', async () => {
  let resolve!: () => void;
  restore.mockImplementation(
    () =>
      new Promise<void>((done) => {
        resolve = done;
      }),
  );
  await render();
  await click('核对恢复');
  await click('保留当前状态并恢复');
  await click('保留当前状态并恢复');
  expect(restore).toHaveBeenCalledOnce();
  await act(async () => {
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));
    await settle();
  });
  expect(close).not.toHaveBeenCalled();
  expect(button('取消恢复').disabled).toBe(true);
  await act(async () => {
    resolve();
    await settle();
  });
  expect(close).toHaveBeenCalledOnce();
});
