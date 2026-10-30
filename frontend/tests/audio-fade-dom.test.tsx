// @vitest-environment jsdom
import { act, useState } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { beforeEach, afterEach, it, expect, vi } from 'vitest';
import { Inspector } from '../src/components/Inspector';
import { defaultClip } from '../src/lib/editor';
import { browserAudioFadeGain } from '../src/lib/legacy-media';
import { editedAudioFade } from '../src/lib/audio-fade';
import type { Project, Clip } from '../src/types';
const settings = { width: 640, height: 360, fpsNum: 30, fpsDen: 1, sampleRate: 48000 };
const initial = {
  ...defaultClip('audio', 'a', 0, settings),
  assetId: 'source',
  durationFrames: 60,
};
let host: HTMLDivElement, root: Root;
const updated = vi.fn();
function Harness({ start = initial }: { start?: Clip }) {
  const [c, set] = useState<Clip>(start);
  const p: Project = {
    id: 'fade',
    name: '声音',
    schemaVersion: 4,
    revision: 0,
    settings,
    tracks: [{ id: 'a', name: '声音', kind: 'audio', locked: false, muted: false, hidden: false }],
    assets: [
      {
        id: 'source',
        name: '本机声音',
        kind: 'audio',
        path: '/fixture.wav',
        durationUs: 2000000,
        width: 0,
        height: 0,
        fpsNum: 0,
        fpsDen: 0,
        hasAudio: true,
        missing: false,
      },
    ],
    clips: [c],
  };
  return (
    <Inspector
      project={p}
      clip={c}
      busy={false}
      onUpdate={(v) => {
        updated(v);
        set(v);
      }}
      onAddAsset={vi.fn()}
      onProjectName={vi.fn()}
    />
  );
}
beforeEach(async () => {
  vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new Error('测试禁止真实网络与收费调用')));
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
  updated.mockClear();
  host = document.createElement('div');
  document.body.append(host);
  root = createRoot(host);
  await act(async () => root.render(<Harness />));
});
afterEach(async () => {
  await act(async () => root.unmount());
  host.remove();
  vi.unstubAllGlobals();
});
async function edit(label: string, value: string) {
  await act(async () => {
    const n = document.querySelector<HTMLInputElement>(`input[aria-label="${label}"]`)!;
    n.focus();
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(n, value);
    n.dispatchEvent(new Event('input', { bubbles: true }));
  });
  await act(async () =>
    document.querySelector<HTMLInputElement>(`input[aria-label="${label}"]`)!.blur(),
  );
}
it('真实属性控件提交淡化、登记草稿并可清除，非法输入保留', async () => {
  expect(document.querySelector('[data-inspector-key="fadeInUs"]')).not.toBeNull();
  await edit('淡入 · 秒', '-1');
  expect(updated).not.toHaveBeenCalled();
  expect(
    document.querySelector('input[aria-label="淡入 · 秒"]')?.getAttribute('aria-invalid'),
  ).toBe('true');
  await edit('淡入 · 秒', '0.2');
  await edit('淡出 · 秒', '0.3');
  expect(updated.mock.lastCall?.[0].audioFade).toEqual({
    inUs: 200000,
    outUs: 300000,
    spanFrames: 60,
    offsetFrames: 0,
  });
  await act(async () =>
    [...document.querySelectorAll('button')].find((b) => b.textContent === '清除淡入淡出')!.click(),
  );
  expect(updated.mock.lastCall?.[0].audioFade).toBeUndefined();
});
it('派生窗重新设置缩短另一端，工作区按输出包络近似', () => {
  const c = {
    ...initial,
    startFrame: 10,
    durationFrames: 3,
    audioFade: { inUs: 200000, outUs: 300000, spanFrames: 60, offsetFrames: 20 },
  };
  expect(editedAudioFade(c, settings, 'in', 0.03)).toEqual({
    inUs: 30000,
    outUs: 70000,
    spanFrames: 3,
    offsetFrames: 0,
  });
  expect(
    browserAudioFadeGain(
      { ...initial, audioFade: { inUs: 200000, outUs: 300000, spanFrames: 60, offsetFrames: 0 } },
      settings,
      0.1,
    ),
  ).toBeCloseTo(0.5);
  expect(
    browserAudioFadeGain(
      { ...initial, audioFade: { inUs: 200000, outUs: 300000, spanFrames: 60, offsetFrames: 0 } },
      settings,
      1.85,
    ),
  ).toBeCloseTo(0.5);
});

it('派生窗原曲线数值保持可读，重新输入等价格式不会回写或制造错误', async () => {
  const c = {
    ...initial,
    durationFrames: 3,
    audioFade: { inUs: 200000, outUs: 300000, spanFrames: 60, offsetFrames: 20 },
  };
  await act(async () => root.render(<Harness key="derived" start={c} />));
  expect(
    document.querySelector<HTMLInputElement>('input[aria-label="淡入 · 秒"]')!.validity.valid,
  ).toBe(true);
  await edit('淡入 · 秒', '0.200');
  expect(updated).not.toHaveBeenCalled();
  expect(
    document.querySelector('input[aria-label="淡入 · 秒"]')!.getAttribute('aria-invalid'),
  ).toBe('false');
});

it('数值输入的IME确认Enter不触发失焦提交', async () => {
  const n = document.querySelector<HTMLInputElement>('input[aria-label="淡入 · 秒"]')!;
  await act(async () => {
    n.focus();
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(n, '0.2');
    n.dispatchEvent(new Event('input', { bubbles: true }));
    n.dispatchEvent(
      new KeyboardEvent('keydown', { key: 'Enter', isComposing: true, bubbles: true }),
    );
  });
  expect(document.activeElement).toBe(n);
  expect(updated).not.toHaveBeenCalled();
});
