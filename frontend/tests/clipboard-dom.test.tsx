// @vitest-environment jsdom
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { useTimelineClipboard, timelineClipboardShortcut } from '../src/lib/use-timeline-clipboard';
import type { ClipboardStatus } from '../src/clipboard-types';
const api = vi.hoisted(() => ({ status: vi.fn() }));
vi.mock('../src/lib/bridge', () => ({ bridge: { clipboardStatus: api.status } }));
let host: HTMLDivElement, root: Root;
let current: ReturnType<typeof useTimelineClipboard>;
const state = (projectId = 'a', version = 1, sourceEpoch = 1): ClipboardStatus => ({
  projectId,
  version,
  sourceEpoch,
  available: true,
  id: `clipboard-${version}`,
  clipCount: 2,
  detachedCaptions: 0,
});
function Probe({ projectId = 'a', epoch = 1 }: { projectId?: string; epoch?: number }) {
  current = useTimelineClipboard(projectId, epoch);
  return (
    <div>
      {current.status?.id || 'empty'}
      {current.error}
    </div>
  );
}
beforeEach(() => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
  host = document.createElement('div');
  document.body.append(host);
  root = createRoot(host);
  api.status.mockReset();
});
afterEach(async () => {
  await act(async () => root.unmount());
  host.remove();
  vi.restoreAllMocks();
});
describe('工程剪贴板状态与快捷键边界', () => {
  it('延迟摘要不能倒退更高版本的复制结果', async () => {
    let resolve!: (value: ClipboardStatus) => void;
    api.status.mockImplementation(
      () =>
        new Promise<ClipboardStatus>((r) => {
          resolve = r;
        }),
    );
    await act(async () => root.render(<Probe />));
    await act(async () => {
      current.accept(state('a', 5));
      resolve(state('a', 1));
    });
    expect(current.status?.version).toBe(5);
  });
  it('旧工程异步结果和旧调用者不能覆盖新工程状态', async () => {
    let resolve!: (value: ClipboardStatus) => void;
    api.status
      .mockImplementationOnce(
        () =>
          new Promise<ClipboardStatus>((r) => {
            resolve = r;
          }),
      )
      .mockResolvedValue(state('b', 2, 2));
    await act(async () => root.render(<Probe />));
    const oldAccept = current.accept;
    await act(async () => root.render(<Probe projectId="b" epoch={2} />));
    await act(async () => {
      resolve(state('a', 100));
      oldAccept(state('a', 200));
    });
    expect(current.status?.projectId).toBe('b');
    expect(current.status?.version).toBe(2);
  });
  it('相同作用域的刷新共用在途读取，失败保留已知剪贴板', async () => {
    api.status.mockResolvedValueOnce(state());
    await act(async () => root.render(<Probe />));
    let reject!: (reason: Error) => void;
    api.status.mockImplementation(
      () =>
        new Promise<ClipboardStatus>((_, r) => {
          reject = r;
        }),
    );
    let first!: Promise<void>, second!: Promise<void>;
    await act(async () => {
      first = current.refresh();
      second = current.refresh();
    });
    expect(first).toBe(second);
    expect(api.status).toHaveBeenCalledTimes(2);
    await act(async () => {
      reject(Error('刷新失败'));
      await first;
    });
    expect(current.status?.id).toBe('clipboard-1');
    expect(current.error).toContain('刷新失败');
  });
  it('复制剪切粘贴只作用于时间线，输入/其它面板/输入法保持原行为', () => {
    host.innerHTML =
      '<section class="timeline-panel"><button>片段</button><input><div contenteditable="true"></div></section><button class="outside">别处</button>';
    const dispatch = (element: Element, options: KeyboardEventInit) => {
      let action: ReturnType<typeof timelineClipboardShortcut>;
      const listener = (event: Event) => {
        action = timelineClipboardShortcut(event as KeyboardEvent);
      };
      element.addEventListener('keydown', listener);
      element.dispatchEvent(
        new KeyboardEvent('keydown', { bubbles: true, key: 'c', ctrlKey: true, ...options }),
      );
      element.removeEventListener('keydown', listener);
      return action!;
    };
    const face = host.querySelector('.timeline-panel button')!;
    expect(dispatch(face, {})).toBe('copy');
    expect(dispatch(face, { key: 'x' })).toBe('cut');
    expect(dispatch(face, { key: 'v', ctrlKey: false, metaKey: true })).toBe('paste');
    for (const target of [
      host.querySelector('input')!,
      host.querySelector('[contenteditable]')!,
      host.querySelector('.outside')!,
    ])
      expect(dispatch(target, {})).toBeUndefined();
    expect(dispatch(face, { isComposing: true })).toBeUndefined();
    expect(dispatch(face, { shiftKey: true, key: 'v' })).toBeUndefined();
  });
});
