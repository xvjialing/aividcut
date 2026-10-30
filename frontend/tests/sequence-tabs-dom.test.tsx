// @vitest-environment jsdom
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { afterEach, beforeEach, it, expect, vi } from 'vitest';
import { SequenceTabs } from '../src/components/SequenceTabs';
import { TooltipProvider } from '../src/components/ui';
import type { Bootstrap } from '../src/types';
let host: HTMLDivElement, root: Root;
const settings = { width: 320, height: 180, fpsNum: 30, fpsDen: 1, sampleRate: 48000 };
const snapshot: Bootstrap = {
  sourceEpoch: 2,
  project: {
    schemaVersion: 5,
    id: 'p',
    name: '序列',
    revision: 4,
    sequenceId: 's1',
    sequenceName: '第一版',
    settings,
    assets: [],
    tracks: [],
    clips: [],
  },
  sequences: Array.from({ length: 32 }, (_, i) => ({
    id: `s${i + 1}`,
    name: i === 0 ? '第一版' : i === 31 ? '很长的版本名称'.repeat(8) : `版本${i + 1}`,
    settings,
    trackCount: 4,
    clipCount: 2,
    durationFrames: 90,
    locked: false,
    current: i === 0,
  })),
  projectPath: '',
  dirty: false,
  canUndo: false,
  canRedo: false,
  capabilities: {},
};
beforeEach(() => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
  host = document.createElement('div');
  document.body.append(host);
  root = createRoot(host);
});
afterEach(async () => {
  await act(async () => root.unmount());
  host.remove();
});
async function mount(disabled = false, change = vi.fn()) {
  await act(async () =>
    root.render(
      <TooltipProvider>
        <SequenceTabs
          snapshot={snapshot}
          disabled={disabled}
          onSwitch={change}
          onManage={() => {}}
        />
      </TooltipProvider>,
    ),
  );
  return change;
}
it('32序列都以原生按钮直达，当前项不重交，长名完整可访问', async () => {
  const change = await mount();
  const tabs = document.querySelectorAll<HTMLButtonElement>('[role="tab"]');
  expect(tabs).toHaveLength(32);
  expect(tabs[0].getAttribute('aria-selected')).toBe('true');
  expect(tabs[31].title).toBe(snapshot.sequences![31].name);
  await act(async () => {
    tabs[0].click();
    tabs[2].click();
  });
  expect(change).toHaveBeenCalledTimes(1);
  expect(change).toHaveBeenCalledWith('s3');
  expect(tabs[2].getAttribute('aria-controls')).toBe('timeline-sequences-panel');
});
it('方向键仅移动焦点，不在浏览途中发起切换；Enter交给按钮激活且不冒泡播放快捷键', async () => {
  const change = await mount();
  const tabs = document.querySelectorAll<HTMLButtonElement>('[role="tab"]');
  const bubble = vi.fn();
  document.body.addEventListener('keydown', bubble);
  await act(async () => {
    tabs[0].focus();
    tabs[0].dispatchEvent(
      new KeyboardEvent('keydown', { key: 'ArrowRight', bubbles: true, cancelable: true }),
    );
  });
  expect(document.activeElement).toBe(tabs[1]);
  expect(change).not.toHaveBeenCalled();
  expect(tabs[1].tabIndex).toBe(0);
  expect(tabs[0].tabIndex).toBe(-1);
  await act(async () =>
    tabs[1].dispatchEvent(
      new KeyboardEvent('keydown', { key: 'Enter', bubbles: true, cancelable: true }),
    ),
  );
  expect(bubble).not.toHaveBeenCalled();
  await act(async () => tabs[1].click());
  expect(change).toHaveBeenCalledWith('s2');
  document.body.removeEventListener('keydown', bubble);
});
it('忙碌时所有序列按钮与管理入口均禁用', async () => {
  const change = await mount(true);
  expect([...document.querySelectorAll<HTMLButtonElement>('button')].every((b) => b.disabled)).toBe(
    true,
  );
  await act(async () => document.querySelector<HTMLButtonElement>('[role="tab"]')!.click());
  expect(change).not.toHaveBeenCalled();
});
