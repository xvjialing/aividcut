// @vitest-environment jsdom

import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { Inspector } from '../src/components/Inspector';
import { commitActiveDraft, prepareToClose, settleProjectState } from '../src/lib/close-protection';
import { defaultClip } from '../src/lib/editor';
import type { Bootstrap, Project } from '../src/types';

let container: HTMLDivElement;
let root: Root;

const settings = { width: 1920, height: 1080, fpsNum: 30, fpsDen: 1, sampleRate: 48000 };
const clip = defaultClip('text', 'text-1', 10, settings);
const project: Project = {
  id: 'project',
  name: '影片',
  schemaVersion: 1,
  revision: 0,
  settings,
  assets: [],
  tracks: [
    { id: 'text-1', name: '文字 1', kind: 'text', locked: false, muted: false, hidden: false },
  ],
  clips: [clip],
};
const snapshot = (dirty: boolean): Bootstrap => ({
  project,
  projectPath: '',
  dirty,
  canUndo: false,
  canRedo: false,
  capabilities: {},
});

beforeEach(() => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
  container = document.createElement('div');
  document.body.append(container);
  root = createRoot(container);
});
afterEach(async () => {
  await act(async () => root.unmount());
  container.remove();
});

async function mount(onUpdate = vi.fn()) {
  await act(async () =>
    root.render(
      <Inspector
        project={project}
        clip={clip}
        busy={false}
        onUpdate={onUpdate}
        onAddAsset={vi.fn()}
        onProjectName={vi.fn()}
      />,
    ),
  );
  const label = Array.from(container.querySelectorAll('label')).find((item) =>
    item.textContent?.includes('起点 · 帧'),
  );
  const input = label?.querySelector('input');
  if (!(input instanceof HTMLInputElement)) throw new Error('没有渲染实际的起点数值控件');
  return input;
}

async function typeValue(input: HTMLInputElement, value: string) {
  await act(async () => {
    input.focus();
    const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!;
    setter.call(input, value);
    input.dispatchEvent(new Event('input', { bubbles: true }));
    input.dispatchEvent(new Event('change', { bubbles: true }));
  });
}

describe('实际属性控件的草稿校验', () => {
  it('默认属性数值满足真实DOM约束，不因缩放步长产生假错误', async () => {
    await mount();
    const numbers = Array.from(
      container.querySelectorAll<HTMLInputElement>('input[type="number"]'),
    );
    expect(numbers.length).toBeGreaterThan(2);
    expect(numbers.every((input) => input.validity.valid)).toBe(true);
  });
  it('仅聚焦并失焦不会把展示精度回写到源素材入点', async () => {
    const sourceClip = { ...clip, kind: 'video' as const, assetId: 'source', sourceInUs: 33367 };
    const currentProject = {
      ...project,
      tracks: [{ ...project.tracks[0], kind: 'video' as const }],
      clips: [sourceClip],
    };
    const update = vi.fn();
    await act(async () =>
      root.render(
        <Inspector
          project={currentProject}
          clip={sourceClip}
          busy={false}
          onUpdate={update}
          onAddAsset={vi.fn()}
          onProjectName={vi.fn()}
        />,
      ),
    );
    const label = Array.from(container.querySelectorAll('label')).find((item) =>
      item.textContent?.includes('素材入点 · 秒'),
    );
    const input = label?.querySelector('input');
    if (!(input instanceof HTMLInputElement)) throw new Error('未渲染素材入点');
    await act(async () => {
      input.focus();
      input.blur();
    });
    expect(update).not.toHaveBeenCalled();
  });
  it('渲染的必填数值留空时，DOM validity与提交路径一致且不能自动关闭', async () => {
    const update = vi.fn();
    const input = await mount(update);
    expect(input.required).toBe(true);
    expect(input.getAttribute('aria-label')).toBe('起点 · 帧');
    await typeValue(input, '');
    expect(input.validity.valueMissing).toBe(true);
    expect(input.validity.valid).toBe(false);
    const read = vi.fn().mockResolvedValue(snapshot(false));
    const discard = vi.fn();
    let result: string | undefined;
    await act(async () => {
      result = await prepareToClose({
        commitDraft: commitActiveDraft,
        waitForIdle: async () => true,
        read,
        accept: vi.fn(),
        discard,
      });
    });
    expect(result).toBe('invalid');
    expect(update).not.toHaveBeenCalled();
    expect(read).not.toHaveBeenCalled();
    expect(discard).not.toHaveBeenCalled();
    expect(input.getAttribute('aria-invalid')).toBe('true');
    expect(input.getAttribute('aria-label')).toBe('起点 · 帧');
    expect(container.textContent).toContain('请输入');
  });
  it('已经失焦的无效数值仍阻止新建/打开前的状态切换', async () => {
    const input = await mount();
    await typeValue(input, '');
    await act(async () => input.blur());
    expect(document.activeElement).not.toBe(input);
    const read = vi.fn().mockResolvedValue(snapshot(false));
    const result = await settleProjectState({
      commitDraft: commitActiveDraft,
      waitForIdle: async () => true,
      read,
      accept: vi.fn(),
    });
    expect(result.kind).toBe('invalid');
    expect(read).not.toHaveBeenCalled();
  });
  it('新建/打开前先blur真实输入，再等待提交并读取fresh dirty', async () => {
    let latest = snapshot(false);
    const order: string[] = [];
    const update = vi.fn(() => {
      order.push('commit');
      latest = snapshot(true);
    });
    const input = await mount(update);
    await typeValue(input, '25');
    expect(latest.dirty).toBe(false);
    let result: Awaited<ReturnType<typeof settleProjectState>> | undefined;
    await act(async () => {
      result = await settleProjectState({
        commitDraft: commitActiveDraft,
        waitForIdle: async () => {
          order.push('wait');
          return true;
        },
        read: async () => {
          order.push('read');
          return latest;
        },
        accept: vi.fn(),
      });
    });
    expect(update).toHaveBeenCalledOnce();
    expect(order).toEqual(['commit', 'wait', 'read']);
    expect(result?.kind).toBe('ready');
    if (result?.kind === 'ready') expect(result.snapshot.dirty).toBe(true);
  });
  it('生成或连接表单的无效输入不阻塞工程切换或提交工程草稿', async () => {
    const update = vi.fn();
    await mount(update);
    const unrelated = document.createElement('input');
    unrelated.required = true;
    unrelated.setAttribute('aria-invalid', 'true');
    unrelated.setAttribute('aria-label', '独立生成表单输入');
    container.append(unrelated);
    unrelated.focus();
    expect(unrelated.validity.valid).toBe(false);
    const read = vi.fn().mockResolvedValue(snapshot(false));
    const result = await settleProjectState({
      commitDraft: commitActiveDraft,
      waitForIdle: async () => true,
      read,
      accept: vi.fn(),
    });
    expect(result.kind).toBe('ready');
    expect(read).toHaveBeenCalledOnce();
    expect(update).not.toHaveBeenCalled();
    expect(document.activeElement).not.toBe(unrelated);
  });
});
