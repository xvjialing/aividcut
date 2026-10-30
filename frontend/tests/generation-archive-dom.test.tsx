// @vitest-environment jsdom
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { GenerationArchiveDialog } from '../src/components/GenerationArchiveDialog';
import { GenerationPanel } from '../src/components/GenerationPanel';
import type { GenerationArchiveRecord, GenerationArchivePage } from '../src/generation-types';
import { fakeGenerationAPI, generationJob, generationProject } from './generation-fixtures';

let host: HTMLDivElement, root: Root, fetchGuard: ReturnType<typeof vi.fn>;
beforeEach(() => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
  fetchGuard = vi.fn().mockRejectedValue(new Error('禁止外网'));
  vi.stubGlobal('fetch', fetchGuard);
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
async function settle() {
  await new Promise((resolve) => setTimeout(resolve, 8));
}
async function render(node: React.ReactNode) {
  await act(async () => {
    root.render(node);
    await settle();
  });
}
function button(text: string) {
  const node = [...document.querySelectorAll<HTMLButtonElement>('button')].find(
    (n) => n.textContent?.trim() === text || n.getAttribute('aria-label') === text,
  );
  if (!node) throw new Error('找不到按钮：' + text);
  return node;
}
async function click(text: string) {
  await act(async () => {
    button(text).click();
    await settle();
  });
}
function archived(id = 'archive-one'): GenerationArchiveRecord {
  const base = generationJob({ status: 'succeeded', message: '结果已保存在本机' });
  return {
    id,
    archivedAt: '2026-10-28T10:00:00Z',
    canRestore: true,
    job: {
      ...base,
      id,
      request: { ...base.request, id, prompt: id },
      estimate: {
        basis: 'manual-unit-v1',
        currency: 'CNY',
        unit: 'request',
        unitPrice: '0.29',
        quantity: 1,
        amount: '0.29',
      },
    },
  };
}
const page = (
  items: GenerationArchiveRecord[],
  offset = 0,
  total = items.length,
  active = 0,
): GenerationArchivePage => ({ items, offset, total, active, limit: 20 });

describe('默认生成记录归档', () => {
  it('核对/取消不归档，明确归档后移出默认列表，还原不重新生成且保留估价', async () => {
    const api = fakeGenerationAPI(),
      item = archived(),
      job = item.job!;
    let isArchived = false;
    api.listGenerationJobs.mockImplementation(async () => (isArchived ? [] : [job]));
    api.archiveGenerationJob.mockImplementation(async () => {
      isArchived = true;
      return item;
    });
    api.listArchivedGenerationJobs.mockImplementation(async () => page(isArchived ? [item] : []));
    api.restoreArchivedGenerationJob.mockImplementation(async () => {
      isArchived = false;
      return job;
    });
    await render(<GenerationPanel project={generationProject} onBootstrap={vi.fn()} api={api} />);
    await click('生成任务 1');
    await click('归档记录…');
    expect(document.querySelector('[role="dialog"]')?.textContent).toContain('费用参考');
    expect(document.querySelector('[role="dialog"]')?.textContent).toContain('CNY 0.29');
    await click('返回任务');
    expect(api.archiveGenerationJob).not.toHaveBeenCalled();
    await click('归档记录…');
    await click('移入归档');
    expect(api.archiveGenerationJob).toHaveBeenCalledExactlyOnceWith(job.id);
    expect(document.body.textContent).toContain('记录已移入归档');
    expect(document.querySelectorAll('.generation-job')).toHaveLength(0);
    await click('归档管理');
    expect(document.querySelector('[role="dialog"]')?.textContent).toContain('CNY 0.29');
    await click('还原记录');
    expect(api.restoreArchivedGenerationJob).toHaveBeenCalledExactlyOnceWith(job.id);
    expect(document.querySelector('[role="dialog"]')?.textContent).toContain('尚无归档记录');
    await click('关闭归档');
    expect(document.querySelectorAll('.generation-job')).toHaveLength(1);
    expect(document.activeElement).toBe(button('归档管理'));
    expect(api.submitGeneration).not.toHaveBeenCalled();
    expect(api.importGenerationResult).not.toHaveBeenCalled();
  });
  it('归档失败保留任务与核对窗口，重试期间防止重复激活', async () => {
    const api = fakeGenerationAPI();
    const job = generationJob({ status: 'succeeded' });
    api.listGenerationJobs.mockResolvedValue([job]);
    api.archiveGenerationJob.mockRejectedValueOnce(new Error('文件暂不可写'));
    let finish!: (value: GenerationArchiveRecord) => void;
    api.archiveGenerationJob.mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          finish = resolve;
        }),
    );
    await render(<GenerationPanel project={generationProject} onBootstrap={vi.fn()} api={api} />);
    await click('生成任务 1');
    await click('归档记录…');
    await click('移入归档');
    expect(document.querySelector('[role="dialog"]')?.textContent).toContain('文件暂不可写');
    expect(document.querySelectorAll('.generation-job')).toHaveLength(1);
    await click('移入归档');
    expect(button('移入归档').disabled).toBe(true);
    await click('移入归档');
    expect(api.archiveGenerationJob).toHaveBeenCalledTimes(2);
    await act(async () => {
      finish(archived(job.id));
      await settle();
    });
    expect(document.querySelector('[role="dialog"]')).toBeNull();
    expect(api.submitGeneration).not.toHaveBeenCalled();
  });
  it('未核对unknown和可恢复waiting不提供归档操作', async () => {
    const api = fakeGenerationAPI();
    api.listGenerationJobs.mockResolvedValue([
      generationJob({
        id: 'unknown',
        status: 'unknown',
        acknowledged: false,
        remoteId: undefined,
        canResume: false,
      }),
      generationJob({ id: 'waiting', status: 'waiting', canResume: true, remoteId: 'remote' }),
    ]);
    await render(<GenerationPanel project={generationProject} onBootstrap={vi.fn()} api={api} />);
    await click('生成任务 2');
    expect(document.body.textContent).not.toContain('归档记录…');
    expect(api.archiveGenerationJob).not.toHaveBeenCalled();
  });
});

describe('归档管理分页与恢复', () => {
  it('空/损坏/容量状态可读取，容量满时不允许还原', async () => {
    const api = fakeGenerationAPI();
    api.listArchivedGenerationJobs.mockResolvedValue(
      page(
        [
          {
            id: 'broken',
            archivedAt: '2026-10-28T10:00:00Z',
            canRestore: false,
            issue: '原记录损坏，标识仍保留',
          },
          archived(),
        ],
        0,
        2,
        2000,
      ),
    );
    await render(
      <GenerationArchiveDialog
        api={api}
        onClose={vi.fn()}
        onReturnFocus={vi.fn()}
        onRestored={vi.fn()}
      />,
    );
    expect(document.body.textContent).toContain('原记录损坏');
    expect(document.body.textContent).toContain('默认历史已满');
    expect(
      [...document.querySelectorAll<HTMLButtonElement>('button')]
        .filter((n) => n.textContent === '还原记录')
        .every((n) => n.disabled),
    ).toBe(true);
    expect(api.restoreArchivedGenerationJob).not.toHaveBeenCalled();
    api.listArchivedGenerationJobs.mockResolvedValue(page([]));
    await click('刷新归档');
    expect(document.body.textContent).toContain('尚无归档记录');
  });
  it('还原末页后采用后端收敛页，失败保留记录并允许刷新', async () => {
    const api = fakeGenerationAPI();
    const items = Array.from({ length: 21 }, (_, i) => archived(`arch-${i}`));
    let total = 21;
    api.listArchivedGenerationJobs.mockImplementation(async (input) =>
      input.offset >= 20 && total === 21
        ? page([items[20]], 20, total)
        : page(items.slice(0, 20), 0, total),
    );
    api.restoreArchivedGenerationJob
      .mockRejectedValueOnce(new Error('还原移动失败'))
      .mockImplementationOnce(async () => {
        total = 20;
        return items[20].job!;
      });
    const onRestored = vi.fn();
    await render(
      <GenerationArchiveDialog
        api={api}
        onClose={vi.fn()}
        onReturnFocus={vi.fn()}
        onRestored={onRestored}
      />,
    );
    await click('下一页归档');
    expect(document.body.textContent).toContain('21–21 / 21');
    await click('还原记录');
    expect(document.body.textContent).toContain('还原移动失败');
    expect(onRestored).not.toHaveBeenCalled();
    await click('还原记录');
    expect(onRestored).toHaveBeenCalledOnce();
    expect(document.body.textContent).toContain('1–20 / 20');
    expect(button('下一页归档').disabled).toBe(true);
    expect(button('上一页归档').disabled).toBe(true);
    expect(api.submitGeneration).not.toHaveBeenCalled();
  });
  it('关闭后或API改变前的旧读取不能覆盖当前归档', async () => {
    const first = fakeGenerationAPI(),
      second = fakeGenerationAPI();
    let finish!: (value: GenerationArchivePage) => void;
    first.listArchivedGenerationJobs.mockImplementation(
      () =>
        new Promise((resolve) => {
          finish = resolve;
        }),
    );
    second.listArchivedGenerationJobs.mockResolvedValue(page([archived('current-record')]));
    const props = { onClose: vi.fn(), onReturnFocus: vi.fn(), onRestored: vi.fn() };
    await render(<GenerationArchiveDialog api={first} {...props} />);
    expect(document.body.textContent).toContain('正在读取');
    await render(<GenerationArchiveDialog api={second} {...props} />);
    await act(async () => {
      finish(page([archived('old-record')]));
      await settle();
    });
    expect(second.listArchivedGenerationJobs).toHaveBeenCalledOnce();
    expect(document.querySelector('[aria-label="归档记录列表"]')?.textContent).not.toContain(
      'old-record',
    );
    expect(document.querySelectorAll('.generation-job')).toHaveLength(1);
  });
});
