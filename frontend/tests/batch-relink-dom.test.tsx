// @vitest-environment jsdom
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { BatchRelinkDialog } from '../src/components/BatchRelinkDialog';
import type { BatchRelinkAPI, BatchRelinkPreview } from '../src/batch-relink-types';
import type { ProjectScope, Bootstrap } from '../src/types';

const scope = { projectId: 'project', projectRevision: 3, sourceEpoch: 8 };
const preview: BatchRelinkPreview = {
  id: 'preview',
  oldRoot: '/old',
  newRoot: '/new',
  matched: 2,
  outside: 1,
  available: 2,
  readBytes: 123,
  entries: Array.from({ length: 3 }, (_, i) => ({
    assetId: String(i),
    name: `素材${i}`,
    kind: 'image',
    originalPath: `/old/${i}.png`,
    candidatePath: `/new/${i}.png`,
    status: i === 2 ? 'different' : 'matched',
    reason: i === 2 ? '文件内容不同' : '内容一致',
  })),
};
let root: Root,
  host: HTMLDivElement,
  api: BatchRelinkAPI,
  close: ReturnType<typeof vi.fn<() => void>>,
  run: ReturnType<
    typeof vi.fn<(operation: () => Promise<Bootstrap>, label: string) => Promise<Bootstrap | null>>
  >;
const settle = () => new Promise((resolve) => setTimeout(resolve, 0));
beforeEach(() => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
  host = document.createElement('div');
  document.body.append(host);
  root = createRoot(host);
  api = {
    inspect: vi
      .fn()
      .mockResolvedValue({ missingCount: 4, availableCount: 2, suggestedOldRoot: '/old' }),
    preview: vi.fn().mockImplementation(async (input) => ({ ...preview, id: input.id })),
    apply: vi.fn().mockResolvedValue({}),
    cancel: vi.fn().mockResolvedValue(undefined),
    chooseDirectory: vi.fn().mockResolvedValue('/new'),
  };
  close = vi.fn();
  run = vi.fn(async (operation) => operation());
});
afterEach(async () => {
  await act(async () => root.unmount());
  host.remove();
  vi.restoreAllMocks();
});
async function mount(value: ProjectScope = scope) {
  await act(async () => {
    root.render(
      <BatchRelinkDialog
        scope={value}
        desktop
        onApply={run}
        onClose={close}
        onReturnFocus={() => {}}
        api={api}
      />,
    );
    await settle();
  });
}
function buttons(text: string) {
  return [...document.querySelectorAll<HTMLButtonElement>('button')].filter(
    (b) => b.textContent === text,
  );
}
async function click(text: string) {
  await act(async () => {
    buttons(text)[0]!.click();
    await settle();
  });
}
async function change(index: number, value: string) {
  await act(async () => {
    const input = document.querySelectorAll<HTMLInputElement>('input')[index];
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(input, value);
    input.dispatchEvent(new Event('input', { bubbles: true }));
    input.dispatchEvent(new Event('change', { bubbles: true }));
  });
}
async function ready() {
  await mount();
  await click('选择当前文件夹');
  await click('核对候选文件');
}

it('先核对再明确恢复，范围/不可匹配项目可见，重复点击只执行一次统一提交', async () => {
  await ready();
  expect(api.apply).not.toHaveBeenCalled();
  expect(document.body.textContent).toContain('文件内容不同');
  expect(document.body.textContent).toContain('另有 1 个离线素材');
  await act(async () => {
    buttons('恢复 2 个链接')[0].click();
    buttons('恢复 2 个链接')[0].click();
    await settle();
  });
  expect(api.apply).toHaveBeenCalledTimes(1);
  expect(api.apply).toHaveBeenCalledWith(expect.objectContaining(scope));
  expect(run).toHaveBeenCalledWith(expect.any(Function), '恢复 2 个素材链接');
  expect(close).toHaveBeenCalledTimes(1);
});
it('无效目录关联字段并恢复焦点，IME输入期间不提交', async () => {
  await mount();
  await change(0, 'relative');
  await click('核对候选文件');
  const inputs = document.querySelectorAll<HTMLInputElement>('input');
  expect(inputs[0].getAttribute('aria-invalid')).toBe('true');
  expect(document.activeElement).toBe(inputs[0]);
  expect(document.getElementById(inputs[0].getAttribute('aria-describedby')!)).toBeTruthy();
  await change(0, '/old');
  await change(1, '/new');
  const form = document.querySelector('form')!;
  await act(async () => {
    inputs[1].dispatchEvent(new CompositionEvent('compositionstart', { bubbles: true }));
    form.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
  });
  expect(api.preview).not.toHaveBeenCalled();
  await act(async () =>
    inputs[1].dispatchEvent(new CompositionEvent('compositionend', { bubbles: true })),
  );
  await click('核对候选文件');
  expect(api.preview).toHaveBeenCalledOnce();
});
it('核对失败保留目录和原因，取消后晚到结果不会出现', async () => {
  let resolve!: (value: BatchRelinkPreview) => void;
  api.preview = vi.fn(
    () =>
      new Promise<BatchRelinkPreview>((r) => {
        resolve = r;
      }),
  );
  await mount();
  await click('选择当前文件夹');
  await click('核对候选文件');
  await click('取消核对');
  expect(api.cancel).toHaveBeenCalledWith(expect.objectContaining(scope));
  await act(async () => {
    resolve(preview);
    await settle();
  });
  expect(document.querySelector('[aria-label="候选核对结果"]')).toBeNull();
  expect(document.body.textContent).toContain('已取消核对');
  expect(document.querySelectorAll<HTMLInputElement>('input')[1].value).toBe('/new');
  api.preview = vi.fn().mockRejectedValue(new Error('文件夹不可读取'));
  await click('核对候选文件');
  expect(document.body.textContent).toContain('文件夹不可读取');
  expect(document.querySelectorAll<HTMLInputElement>('input')[1].value).toBe('/new');
});
it('修订变化清除旧核对，旧实例响应和被修改的目录不能继续应用', async () => {
  await ready();
  await mount({ ...scope, projectRevision: 4 });
  expect(document.body.textContent).toContain('旧核对已清除');
  expect(buttons('恢复 0 个链接')[0].disabled).toBe(true);
  expect(api.cancel).toHaveBeenCalled();
  await click('核对候选文件');
  await change(1, '/other');
  expect(buttons('恢复 0 个链接')[0].disabled).toBe(true);
});

it('旧范围读取未返回时可重新核对，旧响应及finally不能覆盖新请求', async () => {
  const resolves: ((value: BatchRelinkPreview) => void)[] = [];
  api.preview = vi.fn(() => new Promise<BatchRelinkPreview>((resolve) => resolves.push(resolve)));
  await mount();
  await click('选择当前文件夹');
  await click('核对候选文件');
  await mount({ ...scope, projectRevision: 4 });
  await click('核对候选文件');
  expect(api.preview).toHaveBeenCalledTimes(2);
  await act(async () => {
    resolves[0](preview);
    await settle();
  });
  expect(document.querySelector('[aria-label="候选核对结果"]')).toBeNull();
  expect(buttons('取消核对')[0].disabled).toBe(false);
  await act(async () => {
    resolves[1](preview);
    await settle();
  });
  expect(buttons('恢复 2 个链接')[0].disabled).toBe(false);
});
it('分页有界，未匹配结果不能应用，应用错误可读且需要重新核对', async () => {
  api.preview = vi.fn().mockImplementation(async (input) => ({
    ...preview,
    id: input.id,
    matched: 25,
    entries: Array.from({ length: 25 }, (_, i) => ({
      ...preview.entries[0],
      assetId: String(i),
      name: `片段${i}`,
    })),
  }));
  await ready();
  expect(document.querySelectorAll('.batch-relink-list li')).toHaveLength(20);
  await click('下一页');
  expect(document.querySelectorAll('.batch-relink-list li')).toHaveLength(5);
  run.mockImplementation(async (operation) => {
    try {
      return await operation();
    } catch {
      return null;
    }
  });
  api.apply = vi.fn().mockRejectedValue(new Error('候选文件已变化，请重新核对'));
  await click('恢复 25 个链接');
  expect(document.body.textContent).toContain('候选文件已变化');
  expect(buttons('恢复 0 个链接')[0].disabled).toBe(true);
});
it('空工程位置检查、无结果和选择器取消不修改工程', async () => {
  api.inspect = vi
    .fn()
    .mockResolvedValue({ missingCount: 0, availableCount: 3, suggestedOldRoot: '' });
  await mount();
  expect(buttons('核对候选文件')[0].disabled).toBe(true);
  expect(document.body.textContent).toContain('0 个离线素材');
  expect(api.preview).not.toHaveBeenCalled();
  api.inspect = vi
    .fn()
    .mockResolvedValue({ missingCount: 2, availableCount: 0, suggestedOldRoot: '/old' });
  await click('重新读取');
  api.chooseDirectory = vi.fn().mockResolvedValue('');
  await click('选择当前文件夹');
  expect(document.querySelectorAll<HTMLInputElement>('input')[1].value).toBe('');
  await change(1, '/new');
  api.preview = vi.fn().mockResolvedValue({ ...preview, matched: 0, entries: [] });
  await click('核对候选文件');
  expect(document.body.textContent).toContain('没有需要修复的素材');
  expect(buttons('恢复 0 个链接')[0].disabled).toBe(true);
  expect(api.apply).not.toHaveBeenCalled();
});
