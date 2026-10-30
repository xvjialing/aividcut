// @vitest-environment jsdom
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { beforeEach, afterEach, it, expect, vi } from 'vitest';
import { SequenceManagerDialog } from '../src/components/SequenceManagerDialog';
import type { Bootstrap } from '../src/types';
import type { SequenceAPI } from '../src/sequence-types';
import {
  currentSequenceID,
  sequenceChoices,
  sequenceDraft,
  uniqueSequenceName,
} from '../src/lib/sequences';
const settings = { width: 1920, height: 1080, fpsNum: 30, fpsDen: 1, sampleRate: 48000 };
const snapshot: Bootstrap = {
  sourceEpoch: 8,
  project: {
    id: 'p',
    name: '测试工程',
    schemaVersion: 5,
    revision: 3,
    settings,
    assets: [],
    tracks: [],
    clips: [],
  },
  projectPath: '',
  dirty: false,
  canUndo: false,
  canRedo: false,
  capabilities: {},
  sequences: [
    {
      id: 'sequence-main',
      name: '主时间线',
      settings,
      clipCount: 2,
      trackCount: 4,
      durationFrames: 90,
      locked: false,
      current: true,
    },
    {
      id: 'other',
      name: '已存在序列',
      settings,
      clipCount: 4,
      trackCount: 4,
      durationFrames: 120,
      locked: false,
      current: false,
    },
  ],
};
let host: HTMLDivElement,
  root: Root,
  api: SequenceAPI,
  closed: ReturnType<typeof vi.fn<() => void>>;
const settle = () => new Promise((resolve) => setTimeout(resolve, 0));
beforeEach(() => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
  host = document.createElement('div');
  document.body.append(host);
  root = createRoot(host);
  api = { change: vi.fn().mockResolvedValue(snapshot) };
  closed = vi.fn();
});
afterEach(async () => {
  await act(async () => root.unmount());
  host.remove();
});
async function mount(value = snapshot) {
  await act(async () => {
    root.render(
      <SequenceManagerDialog
        snapshot={value}
        api={api}
        onApply={(operation) => operation()}
        onClose={closed}
        onReturnFocus={() => {}}
      />,
    );
    await settle();
  });
}
function button(text: string) {
  return [...document.querySelectorAll<HTMLButtonElement>('button')].find(
    (b) => b.textContent === text,
  )!;
}
async function click(text: string) {
  await act(async () => {
    button(text).click();
    await settle();
  });
}
async function change(selector: string, value: string) {
  await act(async () => {
    const el = document.querySelector<HTMLInputElement | HTMLSelectElement>(selector)!;
    Object.getOwnPropertyDescriptor(
      el.tagName === 'SELECT' ? HTMLSelectElement.prototype : HTMLInputElement.prototype,
      'value',
    )!.set!.call(el, value);
    el.dispatchEvent(new Event('input', { bubbles: true }));
    el.dispatchEvent(new Event('change', { bubbles: true }));
  });
}
it('进入指定序列保留完整来源范围，不提交片段命令', async () => {
  await mount();
  expect(button('进入此序列').disabled).toBe(true);
  await change('select', 'other');
  await click('进入此序列');
  expect(api.change).toHaveBeenCalledWith(
    expect.objectContaining({
      projectId: 'p',
      projectRevision: 3,
      sourceEpoch: 8,
      action: 'switch',
      sequenceId: 'other',
    }),
  );
  expect(closed).toHaveBeenCalledOnce();
});
it('新建复用画幅帧率控件，无效名称关联字段并阻止IME提交', async () => {
  await mount();
  await click('新建空序列');
  await change('input', '已存在序列');
  await click('新建并进入');
  const input = document.querySelector<HTMLInputElement>('input')!;
  expect(input.getAttribute('aria-invalid')).toBe('true');
  expect(document.activeElement).toBe(input);
  expect(document.getElementById(input.getAttribute('aria-describedby')!)).toBeTruthy();
  await change('input', '竖屏序列');
  const form = document.querySelector('form')!;
  await act(async () => {
    input.dispatchEvent(new CompositionEvent('compositionstart', { bubbles: true }));
    form.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
  });
  expect(api.change).not.toHaveBeenCalled();
  await act(async () =>
    input.dispatchEvent(new CompositionEvent('compositionend', { bubbles: true })),
  );
  await change('select', 'portrait');
  await click('新建并进入');
  expect(api.change).toHaveBeenCalledWith(
    expect.objectContaining({
      action: 'create',
      sequenceId: '',
      name: '竖屏序列',
      settings: expect.objectContaining({ width: 1080, height: 1920, fpsNum: 30 }),
    }),
  );
});
it('复制保留来源ID，不接受额外时基，重复确认只提交一次', async () => {
  await mount();
  await click('复制此序列');
  expect(document.body.textContent).toContain('后续编辑相互独立');
  await act(async () => {
    button('复制并进入').click();
    button('复制并进入').click();
    await settle();
  });
  expect(api.change).toHaveBeenCalledOnce();
  expect(api.change).toHaveBeenCalledWith(
    expect.objectContaining({
      action: 'duplicate',
      sequenceId: 'sequence-main',
      name: '主时间线 副本',
    }),
  );
  expect((api.change as ReturnType<typeof vi.fn>).mock.calls[0][0].settings).toBeUndefined();
});
it('删除在同一窗口核对名称数量，初始焦点在返回，只有明确确认才发请求', async () => {
  await mount();
  await change('select', 'other');
  await click('删除序列');
  expect(api.change).not.toHaveBeenCalled();
  expect(document.body.textContent).toContain('已存在序列');
  expect(document.body.textContent).toContain('4 个片段');
  expect(document.activeElement).toBe(button('返回'));
  await click('返回');
  expect(api.change).not.toHaveBeenCalled();
  await click('删除序列');
  await click('删除此序列');
  expect(api.change).toHaveBeenCalledWith(
    expect.objectContaining({ action: 'delete', sequenceId: 'other', confirmDelete: true }),
  );
});
it('锁轨和最后一个序列不可删除，失败保留名称及原范围', async () => {
  const locked = {
    ...snapshot,
    sequences: snapshot.sequences!.map((s) => ({ ...s, locked: true })),
  };
  await mount(locked);
  expect(button('删除序列').disabled).toBe(true);
  await click('重命名');
  await change('input', '仍保留的名称');
  api.change = vi.fn().mockRejectedValue(new Error('工程或实例已变化，请重新核对'));
  await click('保存名称');
  expect(document.body.textContent).toContain('工程或实例已变化');
  expect(document.querySelector<HTMLInputElement>('input')?.value).toBe('仍保留的名称');
  expect(closed).not.toHaveBeenCalled();
});
it('旧客户端单序列与自定义有理帧率摘要可读', () => {
  const old = { ...snapshot, sequences: undefined };
  expect(currentSequenceID(old)).toBe('sequence-main');
  expect(sequenceChoices(old)).toHaveLength(1);
  expect(uniqueSequenceName(snapshot, '已存在序列')).toBe('已存在序列 2');
  expect(sequenceDraft({ ...settings, fpsNum: 30000, fpsDen: 1001 }).frameRate).toBe('30000/1001');
});
