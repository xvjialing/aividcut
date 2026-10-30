// @vitest-environment jsdom
import { act, useState } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { Timeline } from '../src/components/Timeline';
import { SelectionInspector } from '../src/components/SelectionInspector';
import { TooltipProvider } from '../src/components/ui';
import { defaultClip, PlaybackClock } from '../src/lib/editor';
import { commitActiveDraft, hasTimelineGesture, hasEditorDraft } from '../src/lib/close-protection';
import type { Project } from '../src/types';

let host: HTMLDivElement, root: Root, clock: PlaybackClock;
const settings = { width: 320, height: 180, fpsNum: 30, fpsDen: 1, sampleRate: 48000 };
function project(): Project {
  return {
    id: 'p',
    name: '多选',
    schemaVersion: 1,
    revision: 0,
    settings,
    assets: [],
    tracks: [{ id: 't', kind: 'text', name: '标题', locked: false, muted: false, hidden: false }],
    clips: ['a', 'b', 'c'].map((id, i) => ({
      ...defaultClip('text', 't', 30 + i * 60, settings),
      id,
      name: id,
      text: id,
      durationFrames: 30,
    })),
  };
}
const move = vi.fn(),
  split = vi.fn(),
  duplicate = vi.fn(),
  trimSelection = vi.fn(),
  update = vi.fn(),
  remove = vi.fn();
beforeEach(() => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
  host = document.createElement('div');
  document.body.append(host);
  root = createRoot(host);
  clock = new PlaybackClock();
  clock.configure(300, 30);
  move.mockClear();
  split.mockClear();
  duplicate.mockClear();
  trimSelection.mockClear();
  update.mockClear();
  remove.mockClear();
  vi.stubGlobal(
    'ResizeObserver',
    class {
      observe() {}
      disconnect() {}
    },
  );
  vi.stubGlobal('requestAnimationFrame', (f: FrameRequestCallback) => {
    f(0);
    return 1;
  });
  vi.stubGlobal('cancelAnimationFrame', vi.fn());
  vi.spyOn(HTMLElement.prototype, 'clientWidth', 'get').mockReturnValue(1000);
  vi.spyOn(HTMLElement.prototype, 'clientHeight', 'get').mockReturnValue(330);
  Object.assign(HTMLElement.prototype, {
    setPointerCapture: vi.fn(),
    hasPointerCapture: () => false,
    releasePointerCapture: vi.fn(),
  });
});
afterEach(async () => {
  await act(async () => root.unmount());
  host.remove();
  clock.destroy();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});
function Harness({
  initial = [],
  guard = true,
  locked = false,
}: {
  initial?: string[];
  guard?: boolean;
  locked?: boolean;
}) {
  const [ids, setIds] = useState(initial);
  const p = project();
  p.tracks[0].locked = locked;
  return (
    <TooltipProvider>
      <Timeline
        project={p}
        clock={clock}
        selected={ids[0] || null}
        selectedIds={ids}
        busy={false}
        onSelect={(id, add) =>
          setIds((old) =>
            add ? (old.includes(id) ? old.filter((v) => v !== id) : [...old, id]) : [id],
          )
        }
        onSelectMany={(next) => {
          if (!hasTimelineGesture()) setIds(next);
        }}
        onMoveSelection={move}
        onUpdateClip={update}
        onUpdateTrack={vi.fn()}
        onSplit={split}
        onDuplicate={duplicate}
        onTrimSelection={trimSelection}
        onDelete={remove}
        onText={vi.fn()}
        onAddTrack={vi.fn()}
        canBeginGesture={() => guard}
      />
    </TooltipProvider>
  );
}
const face = (id: string) =>
  host.querySelector<HTMLButtonElement>(`[data-clip-id="${id}"] .clip-face`)!;
async function pointer(target: Element, type: string, x: number, y: number, shiftKey = false) {
  await act(async () => {
    const e = new MouseEvent(type, { bubbles: true, button: 0, clientX: x, clientY: y, shiftKey });
    Object.defineProperty(e, 'pointerId', { value: 1 });
    target.dispatchEvent(e);
  });
}
describe('多片段时间线交互', () => {
  it('修饰单击增删，Cmd/Ctrl+A与Escape作用在时间线', async () => {
    await act(async () => root.render(<Harness />));
    await act(async () => face('a').click());
    await act(async () =>
      face('b').dispatchEvent(new MouseEvent('click', { bubbles: true, shiftKey: true })),
    );
    expect(host.querySelectorAll('.clip-face[aria-pressed="true"]').length).toBe(2);
    await act(async () =>
      face('b').dispatchEvent(new MouseEvent('click', { bubbles: true, ctrlKey: true })),
    );
    expect(host.querySelectorAll('.clip-face[aria-pressed="true"]').length).toBe(1);
    await act(async () =>
      face('a').dispatchEvent(
        new KeyboardEvent('keydown', { key: 'a', ctrlKey: true, bubbles: true }),
      ),
    );
    expect(host.querySelectorAll('.clip-face[aria-pressed="true"]').length).toBe(3);
    await act(async () =>
      face('a').dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true })),
    );
    expect(host.querySelectorAll('.clip-face[aria-pressed="true"]').length).toBe(0);
  });
  it('稳定属性草稿时保留时间线焦点，允许连续全选和清选快捷键', async () => {
    await act(async () => root.render(<Harness />));
    face('a').focus();
    expect(commitActiveDraft()).toBe(true);
    expect(document.activeElement).toBe(face('a'));
  });
  it('空白框选使用索引，保留选中数量且不提交工程写入', async () => {
    await act(async () => root.render(<Harness />));
    const rows = host.querySelector('.timeline-rows')!;
    await pointer(rows, 'pointerdown', 45, 2);
    await pointer(rows, 'pointermove', 195, 64);
    await pointer(rows, 'pointerup', 195, 64);
    expect(host.querySelectorAll('.clip-face[aria-pressed="true"]').length).toBe(2);
    expect(move).not.toHaveBeenCalled();
    expect(update).not.toHaveBeenCalled();
  });
  it('组内普通单击恢复单选，按下拖动前仍保留整组', async () => {
    await act(async () => root.render(<Harness initial={['a', 'b']} />));
    const a = face('a');
    await pointer(a, 'pointerdown', 100, 100);
    expect(host.querySelectorAll('.clip-face[aria-pressed="true"]').length).toBe(2);
    await pointer(a, 'pointerup', 100, 100);
    await act(async () => a.click());
    expect(host.querySelectorAll('.clip-face[aria-pressed="true"]').length).toBe(1);
    expect(a.getAttribute('aria-pressed')).toBe('true');
    expect(move).not.toHaveBeenCalled();
    expect(update).not.toHaveBeenCalled();
  });
  it('整组暂态绘制后只提交一次delta，多选允许分割复制和修剪', async () => {
    await act(async () => root.render(<Harness initial={['a', 'b']} />));
    expect(
      host.querySelector<HTMLButtonElement>('[data-clip-id="a"] .clip-trim-start')?.disabled,
    ).toBe(false);
    const a = face('a');
    await pointer(a, 'pointerdown', 100, 100);
    await pointer(a, 'pointermove', 116, 100);
    expect(host.querySelector<HTMLElement>('[data-clip-id="b"]')!.style.left).toBe('160px');
    await pointer(a, 'pointerup', 116, 100);
    expect(move).toHaveBeenCalledExactlyOnceWith(10, 0);
    expect(update).not.toHaveBeenCalled();
    const splitButton = host.querySelector<HTMLButtonElement>(
      '[aria-label="在播放头分割片段 · C"]',
    )!;
    const copyButton = host.querySelector<HTMLButtonElement>(
      '[aria-label="复制到播放头 · ⌘/Ctrl D"]',
    )!;
    expect(splitButton.disabled).toBe(false);
    expect(copyButton.disabled).toBe(false);
    await act(async () => {
      splitButton.click();
      copyButton.click();
    });
    expect(split).toHaveBeenCalledOnce();
    expect(duplicate).toHaveBeenCalledOnce();
  });
  it('锁定的多选不允许构造部分分割或副本', async () => {
    await act(async () => root.render(<Harness initial={['a', 'b']} locked />));
    expect(
      host.querySelector<HTMLButtonElement>('[aria-label="在播放头分割片段 · C"]')!.disabled,
    ).toBe(true);
    expect(
      host.querySelector<HTMLButtonElement>('[aria-label="复制到播放头 · ⌘/Ctrl D"]')!.disabled,
    ).toBe(true);
  });
  it('把手同量修剪预览宽度和位置，一次提交且取消不写工程', async () => {
    await act(async () => root.render(<Harness initial={['a', 'b']} />));
    const handle = host.querySelector<HTMLButtonElement>('[data-clip-id="a"] .clip-trim-start')!;
    await pointer(handle, 'pointerdown', 100, 100);
    await pointer(handle, 'pointermove', 116, 100);
    expect(host.querySelector<HTMLElement>('[data-clip-id="b"]')!.style.left).toBe('160px');
    expect(host.querySelector<HTMLElement>('[data-clip-id="b"]')!.style.width).toBe('32px');
    await pointer(handle, 'pointerup', 116, 100);
    expect(trimSelection).toHaveBeenCalledExactlyOnceWith(['a', 'b'], 'start', 10);
    expect(hasTimelineGesture()).toBe(false);
    expect(update).not.toHaveBeenCalled();
    trimSelection.mockClear();
    await pointer(handle, 'pointerdown', 100, 100);
    await pointer(handle, 'pointermove', 116, 100);
    await act(async () =>
      handle.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true })),
    );
    await pointer(handle, 'pointerup', 116, 100);
    expect(trimSelection).not.toHaveBeenCalled();
    expect(host.querySelector<HTMLElement>('[data-clip-id="b"]')!.style.width).toBe('48px');
    expect(host.querySelectorAll('.clip-face[aria-pressed="true"]').length).toBe(2);
  });
  it('把手Shift方向键统一修剪10帧，不推进播放头', async () => {
    await act(async () => root.render(<Harness initial={['a', 'b']} />));
    const handle = host.querySelector<HTMLButtonElement>('[data-clip-id="a"] .clip-trim-end')!;
    await act(async () =>
      handle.dispatchEvent(
        new KeyboardEvent('keydown', { key: 'ArrowLeft', shiftKey: true, bubbles: true }),
      ),
    );
    expect(trimSelection).toHaveBeenCalledExactlyOnceWith(['a', 'b'], 'end', -10);
    expect(clock.getFrame()).toBe(0);
  });
  it('Escape取消整组手势，草稿守卫不允许开始新框选', async () => {
    await act(async () => root.render(<Harness initial={['a', 'b']} />));
    const a = face('a');
    await pointer(a, 'pointerdown', 100, 100);
    await pointer(a, 'pointermove', 116, 100);
    await act(async () =>
      a.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true })),
    );
    await pointer(a, 'pointerup', 116, 100);
    expect(move).not.toHaveBeenCalled();
    await act(async () => root.render(<Harness initial={['a', 'b']} guard={false} />));
    await pointer(host.querySelector('.timeline-rows')!, 'pointerdown', 0, 0);
    expect(host.querySelector('.timeline-marquee')).toBeNull();
  });
  it('修剪参数失败保留，无效命令参数不冒充工程草稿', async () => {
    const p = project(),
      onTrim = vi.fn(async () => false);
    await act(async () =>
      root.render(
        <SelectionInspector
          project={p}
          clips={p.clips.slice(0, 2)}
          busy={false}
          onMove={async () => true}
          onTrim={onTrim}
          onDelete={remove}
        />,
      ),
    );
    const input = [...host.querySelectorAll('label')]
      .find((l) => l.textContent?.includes('整组修剪量'))!
      .querySelector('input')!;
    const fill = async (value: string) =>
      act(async () => {
        Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(
          input,
          value,
        );
        input.dispatchEvent(new Event('input', { bubbles: true }));
      });
    await fill('5');
    await act(async () =>
      [...host.querySelectorAll('button')].find((b) => b.textContent === '修剪选中片段')!.click(),
    );
    expect(onTrim).toHaveBeenCalledWith('start', 5);
    expect(input.value).toBe('5');
    await fill('30');
    expect(input.getAttribute('aria-invalid')).toBe('true');
    expect(hasEditorDraft()).toBe(false);
  });
  it('汇总位移失败保留参数，锁定选择不允许部分删除', async () => {
    const p = project();
    const onMove = vi.fn(async () => false);
    await act(async () =>
      root.render(
        <SelectionInspector
          project={p}
          clips={p.clips.slice(0, 2)}
          busy={false}
          onMove={onMove}
          onDelete={remove}
        />,
      ),
    );
    await act(async () => {
      const input = host.querySelector('input')!;
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(input, '7');
      input.dispatchEvent(new Event('input', { bubbles: true }));
    });
    await act(async () =>
      [...host.querySelectorAll('button')].find((b) => b.textContent === '移动选中片段')!.click(),
    );
    expect(host.querySelector<HTMLInputElement>('input')?.value).toBe('7');
    expect(onMove).toHaveBeenCalledWith(7);
    p.tracks[0].locked = true;
    await act(async () =>
      root.render(
        <SelectionInspector
          project={p}
          clips={p.clips}
          busy={false}
          onMove={onMove}
          onDelete={remove}
        />,
      ),
    );
    expect(
      [...host.querySelectorAll('button')].find((b) => b.textContent === '删除选中片段')?.disabled,
    ).toBe(true);
  });
});
