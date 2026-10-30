// @vitest-environment jsdom
import { act, useState } from 'react';
import { flushSync } from 'react-dom';
import { createRoot, type Root } from 'react-dom/client';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { InspectorDraftsDialog } from '../src/components/InspectorDraftsDialog';
import { Inspector } from '../src/components/Inspector';
import { useInspectorDrafts } from '../src/lib/use-inspector-drafts';
import { inspectorDraftAPI } from '../src/lib/bridge';
import { defaultClip } from '../src/lib/editor';
import type { InspectorDraft, InspectorDraftAPI } from '../src/inspector-draft-types';

const scope = { projectId: 'p', projectRevision: 3, sourceEpoch: 7 };
const record: InspectorDraft = {
  id: 'draft-aaa',
  projectId: 'p',
  name: '原工程',
  target: '',
  originalPath: '',
  baseRevision: 3,
  updatedAt: '2026-10-27T10:00:00Z',
  baseSha256: 'b'.repeat(64),
  sha256: 'a'.repeat(64),
  fields: [{ key: 'projectName', raw: '未失焦名字', base: '原工程' }],
};
let root: Root, host: HTMLDivElement, api: InspectorDraftAPI;
beforeEach(() => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
  host = document.createElement('div');
  document.body.append(host);
  root = createRoot(host);
  api = {
    save: vi.fn().mockImplementation(async (input) => ({ ...record, fields: input.fields })),
    list: vi.fn().mockResolvedValue({ records: [record], unreadable: 0 }),
    remove: vi.fn().mockResolvedValue(undefined),
    preview: vi.fn().mockResolvedValue({ id: 'preview-a', canApply: true, rows: [] }),
    apply: vi.fn(),
    restoreBase: vi.fn(),
  };
});
afterEach(async () => {
  await act(async () => root.unmount());
  host.remove();
  vi.useRealTimers();
  vi.restoreAllMocks();
});
function Harness() {
  const drafts = useInspectorDrafts(scope, '', api);
  return (
    <div {...drafts.events}>
      <input aria-label="工程" data-inspector-key="projectName" data-inspector-base={'"原工程"'} />
      <input aria-label="密钥" type="password" />
      <input
        aria-label="精确入点"
        data-inspector-key="sourceInUs"
        data-inspector-base="137001"
        data-inspector-display="0.137"
      />
      <input aria-label="数值" data-inspector-key="scale" data-inspector-base="1" />
      <p role="status">{drafts.status}</p>
    </div>
  );
}
function ControlledCaptureHarness({ kind }: { kind: 'number' | 'text' }) {
  const drafts = useInspectorDrafts(scope, '', api);
  const [value, setValue] = useState(kind === 'number' ? '360' : '原标题');
  const props = {
    'aria-label': '受控属性',
    'data-inspector-key': 'controlled',
    'data-inspector-base': JSON.stringify(kind === 'number' ? 360 : '原标题'),
    value,
    onChange: (event: React.ChangeEvent<HTMLInputElement | HTMLTextAreaElement>) =>
      setValue(event.target.value),
  };
  return (
    <div {...drafts.events}>
      {kind === 'number' ? <input type="number" {...props} /> : <textarea {...props} />}
      <p role="status">{drafts.status}</p>
    </div>
  );
}
it.each(['number', 'text'] as const)('捕获阶段的草稿提示不能覆盖%s属性的首字符', async (kind) => {
  vi.useFakeTimers();
  await act(async () => root.render(<ControlledCaptureHarness kind={kind} />));
  const node = host.querySelector<HTMLInputElement | HTMLTextAreaElement>(
    '[aria-label="受控属性"]',
  )!;
  // 模拟原生事件在根捕获与冒泡之间刷新离散更新，不能用 act 批次掩盖此边界。
  node.addEventListener('input', () => flushSync(() => {}), true);
  const prototype = kind === 'number' ? HTMLInputElement.prototype : HTMLTextAreaElement.prototype;
  for (const value of kind === 'number' ? ['1', '120'] : ['首', '首字完整保留']) {
    await act(async () => {
      Object.getOwnPropertyDescriptor(prototype, 'value')!.set!.call(node, value);
      node.dispatchEvent(new Event('input', { bubbles: true }));
    });
    expect(node.value).toBe(value);
    await tick();
    expect(vi.mocked(api.save).mock.calls.at(-1)?.[0].fields[0].raw).toBe(value);
    expect(node.value).toBe(value);
  }
});
async function input(label: string, value: string) {
  await act(async () => {
    const node = document.querySelector<HTMLInputElement>(`input[aria-label="${label}"]`)!;
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(node, value);
    node.dispatchEvent(new Event('input', { bubbles: true }));
  });
}
async function tick() {
  await act(async () => {
    await vi.advanceTimersByTimeAsync(1000);
  });
}
const click = async (text: string) => {
  await act(async () => {
    const b = [...document.querySelectorAll<HTMLButtonElement>('button')].find((x) =>
      x.textContent?.includes(text),
    );
    if (!b) throw Error('找不到' + text);
    b.click();
  });
};
it('稳定输入合并保护且不收集任意密钥，输入保持焦点', async () => {
  vi.useFakeTimers();
  await act(async () => root.render(<Harness />));
  const node = document.querySelector<HTMLInputElement>('input')!;
  node.focus();
  await input('工程', '未');
  await input('工程', '未提交的中文名');
  await input('密钥', '不可保存');
  expect(api.save).not.toHaveBeenCalled();
  await tick();
  expect(api.save).toHaveBeenCalledTimes(1);
  expect(api.save).toHaveBeenCalledWith({
    ...scope,
    target: '',
    fields: [{ key: 'projectName', raw: '未提交的中文名', base: '原工程' }],
  });
  expect(document.activeElement).toBe(node);
});
it('IME组合期间不保护半成品，结束后保留空数字字符串', async () => {
  vi.useFakeTimers();
  await act(async () => root.render(<Harness />));
  const node = document.querySelector<HTMLInputElement>('input')!;
  await act(async () =>
    node.dispatchEvent(new CompositionEvent('compositionstart', { bubbles: true })),
  );
  await input('工程', 'pin');
  await tick();
  expect(api.save).not.toHaveBeenCalled();
  await input('工程', '拼音');
  await act(async () =>
    node.dispatchEvent(new CompositionEvent('compositionend', { bubbles: true })),
  );
  await input('数值', '');
  await tick();
  expect(api.save).toHaveBeenCalledTimes(1);
  expect(vi.mocked(api.save).mock.calls[0][0].fields).toEqual([
    { key: 'projectName', raw: '拼音', base: '原工程' },
    { key: 'scale', raw: '', base: 1 },
  ]);
});
it('在途请求期间只排队最新输入，旧回执不清除更新', async () => {
  vi.useFakeTimers();
  let finish!: (v: InspectorDraft) => void;
  vi.mocked(api.save).mockImplementationOnce(
    () =>
      new Promise((resolve) => {
        finish = resolve;
      }),
  );
  await act(async () => root.render(<Harness />));
  await input('工程', '第一版');
  await tick();
  await input('工程', '第二版');
  await tick();
  await input('工程', '最终版');
  await tick();
  expect(api.save).toHaveBeenCalledTimes(1);
  await act(async () =>
    finish({ ...record, fields: [{ key: 'projectName', raw: '第一版', base: '原工程' }] }),
  );
  expect(api.save).toHaveBeenCalledTimes(2);
  expect(vi.mocked(api.save).mock.calls[1][0].fields[0].raw).toBe('最终版');
});
it('写入失败显示可恢复原因并保留输入', async () => {
  vi.useFakeTimers();
  vi.mocked(api.save).mockRejectedValue(new Error('磁盘空间不足'));
  await act(async () => root.render(<Harness />));
  await input('工程', '仍需保留');
  await tick();
  expect(document.body.textContent).toContain('草稿未保护：磁盘空间不足');
  expect(document.querySelector<HTMLInputElement>('input')?.value).toBe('仍需保留');
});
it('旧写入完成但新输入尚未写入时不能显示最新草稿已保护', async () => {
  vi.useFakeTimers();
  let finish!: (value: InspectorDraft) => void;
  vi.mocked(api.save).mockImplementationOnce(
    () =>
      new Promise((resolve) => {
        finish = resolve;
      }),
  );
  await act(async () => root.render(<Harness />));
  await input('工程', '第一版');
  await tick();
  await input('工程', '最新未写入');
  await act(async () => {
    await vi.advanceTimersByTimeAsync(0);
  });
  await act(async () =>
    finish({ ...record, fields: [{ key: 'projectName', raw: '第一版', base: '原工程' }] }),
  );
  expect(host.textContent).toContain('输入草稿等待保护');
  expect(host.textContent).not.toContain('输入草稿已保护');
  expect(api.save).toHaveBeenCalledTimes(1);
  await tick();
  expect(vi.mocked(api.save).mock.calls.at(-1)?.[0].fields[0].raw).toBe('最新未写入');
  expect(host.textContent).toContain('输入草稿已保护');
});
it('中文组合开始取消迟到提示，组合结束再保存完整输入', async () => {
  vi.useFakeTimers();
  await act(async () => root.render(<ControlledCaptureHarness kind="text" />));
  const node = host.querySelector<HTMLTextAreaElement>('textarea')!;
  const set = (value: string) =>
    Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')!.set!.call(node, value);
  await act(async () => {
    set('A');
    node.dispatchEvent(new Event('input', { bubbles: true }));
    node.dispatchEvent(new CompositionEvent('compositionstart', { bubbles: true }));
    set('pin');
    node.dispatchEvent(new InputEvent('input', { bubbles: true, isComposing: true }));
    await vi.advanceTimersByTimeAsync(1000);
  });
  expect(node.value).toBe('pin');
  expect(api.save).not.toHaveBeenCalled();
  expect(host.querySelector('[role="status"]')?.textContent).toBe('');
  await act(async () => {
    set('拼音');
    node.dispatchEvent(new CompositionEvent('compositionend', { bubbles: true }));
    node.dispatchEvent(new InputEvent('input', { bubbles: true }));
  });
  await tick();
  expect(node.value).toBe('拼音');
  expect(vi.mocked(api.save).mock.calls.at(-1)?.[0].fields[0].raw).toBe('拼音');
});
it('属性卸载取消等待提示且仍保护已采集输入', async () => {
  vi.useFakeTimers();
  await act(async () => root.render(<ControlledCaptureHarness kind="text" />));
  const node = host.querySelector<HTMLTextAreaElement>('textarea')!;
  await act(async () => {
    Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')!.set!.call(
      node,
      '保留首字',
    );
    node.dispatchEvent(new Event('input', { bubbles: true }));
  });
  await act(async () => root.render(<p>属性已卸载</p>));
  expect(api.save).toHaveBeenCalledTimes(1);
  expect(vi.mocked(api.save).mock.calls[0][0].fields[0].raw).toBe('保留首字');
  expect(vi.getTimerCount()).toBe(0);
});
it('真实Inspector登记文本与数值，但不捕获立即提交颜色', async () => {
  vi.useFakeTimers();
  const save = vi.spyOn(inspectorDraftAPI, 'save').mockImplementation(api.save);
  const settings = { width: 640, height: 360, fpsNum: 30, fpsDen: 1, sampleRate: 48000 };
  const clip = defaultClip('text', 't', 0, settings);
  await act(async () =>
    root.render(
      <Inspector
        project={{
          schemaVersion: 2,
          id: 'p',
          name: '工程',
          revision: 3,
          settings,
          assets: [],
          tracks: [
            { id: 't', name: '文字', kind: 'text', locked: false, muted: false, hidden: false },
          ],
          clips: [clip],
        }}
        clip={clip}
        sourceEpoch={7}
        busy={false}
        onUpdate={vi.fn()}
        onAddAsset={vi.fn()}
        onProjectName={vi.fn()}
      />,
    ),
  );
  const node = document.querySelector<HTMLTextAreaElement>('textarea')!;
  await act(async () => {
    Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')!.set!.call(
      node,
      '真实未提交字幕',
    );
    node.dispatchEvent(new Event('input', { bubbles: true }));
  });
  await tick();
  expect(save).toHaveBeenCalledWith(
    expect.objectContaining({
      target: clip.id,
      fields: [expect.objectContaining({ key: 'text', raw: '真实未提交字幕' })],
    }),
  );
  expect(document.querySelector('input[type="color"]')?.hasAttribute('data-inspector-key')).toBe(
    false,
  );
});
it('冲突核对与应用分开，恢复和永久删除均可安全取消', async () => {
  const onChange = vi.fn(),
    close = vi.fn();
  vi.mocked(api.preview)
    .mockResolvedValueOnce({
      id: '',
      canApply: false,
      rows: [
        {
          key: 'projectName',
          label: '工程名称',
          raw: record.fields[0].raw,
          base: '原工程',
          current: '较新工程',
          conflict: true,
          error: '当前属性已变化',
        },
      ],
    })
    .mockResolvedValueOnce({ id: 'new-review', canApply: true, rows: [] });
  await act(async () =>
    root.render(
      <InspectorDraftsDialog
        scope={scope}
        dirty
        onChange={onChange}
        onClose={close}
        onReturnFocus={vi.fn()}
        api={api}
      />,
    ),
  );
  await click('原工程');
  await click('核对当前属性');
  expect(document.body.textContent).toContain('当前属性：较新工程');
  expect(api.apply).not.toHaveBeenCalled();
  await click('以当前属性重新核对');
  expect(api.preview).toHaveBeenLastCalledWith(expect.objectContaining({ recheckCurrent: true }));
  expect(onChange).not.toHaveBeenCalled();
  await click('恢复基础工程');
  expect(document.activeElement?.textContent).toBe('取消操作');
  await click('取消操作');
  expect(api.restoreBase).not.toHaveBeenCalled();
  await click('删除草稿');
  expect(document.body.textContent).toContain('永久删除');
  await click('取消操作');
  expect(api.remove).not.toHaveBeenCalled();
});

it('重输入舍入显示值不会制造精度草稿，恢复显示值会清理已保护字段', async () => {
  vi.useFakeTimers();
  await act(async () => root.render(<Harness />));
  await input('精确入点', '0.137');
  await tick();
  expect(vi.mocked(api.save).mock.calls.at(-1)?.[0].fields).toEqual([]);
  await input('精确入点', '0.2');
  await tick();
  expect(vi.mocked(api.save).mock.calls.at(-1)?.[0].fields).toEqual([
    { key: 'sourceInUs', raw: '0.2', base: 137001 },
  ]);
  await input('精确入点', '0.137');
  await tick();
  expect(vi.mocked(api.save).mock.calls.at(-1)?.[0].fields).toEqual([]);
});
