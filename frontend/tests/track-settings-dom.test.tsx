// @vitest-environment jsdom
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { beforeEach, afterEach, it, expect, vi } from 'vitest';
import { TrackSettingsDialog, trackDeleteReason } from '../src/components/TrackSettingsDialog';
import type { Bootstrap, Track } from '../src/types';
let host: HTMLDivElement, root: Root;
const track: Track = {
  id: 'v2',
  name: '补充镜头',
  kind: 'video',
  locked: false,
  muted: false,
  hidden: false,
};
const snapshot: Bootstrap = {
  sourceEpoch: 4,
  project: {
    id: 'p',
    schemaVersion: 4,
    revision: 2,
    name: '轨道',
    settings: { width: 1920, height: 1080, fpsNum: 30, fpsDen: 1, sampleRate: 48000 },
    assets: [],
    clips: [],
    tracks: [{ ...track, id: 'v1', name: '主画面' }, track],
  },
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
async function mount(apply = vi.fn().mockResolvedValue(snapshot), close = vi.fn()) {
  await act(async () =>
    root.render(
      <TrackSettingsDialog
        snapshot={snapshot}
        track={track}
        onApply={apply}
        onClose={close}
        onReturnFocus={() => {}}
      />,
    ),
  );
  return { apply, close };
}
async function change(selector: string, value: string) {
  await act(async () => {
    const el = document.querySelector(selector)! as HTMLInputElement;
    Object.getOwnPropertyDescriptor(
      el.tagName === 'SELECT' ? HTMLSelectElement.prototype : HTMLInputElement.prototype,
      'value',
    )!.set!.call(el, value);
    el.dispatchEvent(new Event('input', { bubbles: true }));
    el.dispatchEvent(new Event('change', { bubbles: true }));
  });
}
async function click(text: string) {
  await act(async () =>
    [...document.querySelectorAll<HTMLButtonElement>('button')]
      .find((b) => b.textContent === text)!
      .click(),
  );
}
it('名称错误保留原值关联字段，修正后属性与同类上移一次提交', async () => {
  const { apply } = await mount();
  await change('input:not([type="checkbox"])', ' ');
  await click('保存设置');
  const input = document.querySelector<HTMLInputElement>('input:not([type="checkbox"])')!;
  expect(input.value).toBe(' ');
  expect(input.getAttribute('aria-describedby')).toBe('track-settings-error');
  expect(document.activeElement).toBe(input);
  expect(apply).not.toHaveBeenCalled();
  await change('input:not([type="checkbox"])', '采访主画面');
  await change('select', '-1');
  await click('保存设置');
  expect(apply.mock.calls[0][0]).toEqual([
    { type: 'update_track', track: { ...track, name: '采访主画面' } },
    { type: 'move_track', trackId: 'v2', trackDelta: -1 },
  ]);
});
it('删除需明确核对，返回不提交，确认只删除原空轨', async () => {
  const { apply, close } = await mount();
  await change('input:not([type="checkbox"])', '未保存改名');
  await click('删除空轨…');
  expect(apply).not.toHaveBeenCalled();
  expect(document.activeElement?.textContent).toBe('返回设置');
  await click('返回设置');
  expect(document.querySelector<HTMLInputElement>('input:not([type="checkbox"])')!.value).toBe(
    '未保存改名',
  );
  await click('删除空轨…');
  await click('确认删除空轨');
  expect(apply.mock.calls[0][0]).toEqual([{ type: 'delete_track', trackId: 'v2' }]);
  expect(close).toHaveBeenCalledOnce();
});
it('迟到失败保留输入且请求期间阻止重复保存', async () => {
  let resolve!: (v: null) => void;
  const apply = vi.fn(
    () =>
      new Promise<null>((r) => {
        resolve = r;
      }),
  );
  await mount(apply);
  await change('input:not([type="checkbox"])', '仍保留的草稿');
  await click('保存设置');
  await click('保存设置');
  expect(apply).toHaveBeenCalledOnce();
  await act(async () => resolve(null));
  expect(document.body.textContent).toContain('工程可能已变化');
  expect(document.querySelector<HTMLInputElement>('input:not([type="checkbox"])')!.value).toBe(
    '仍保留的草稿',
  );
});
it('有片段、绑定源字幕、锁定与最后一轨分别解释不可删除', () => {
  expect(
    trackDeleteReason({ ...snapshot, project: { ...snapshot.project, tracks: [track] } }, track),
  ).toContain('至少');
  expect(trackDeleteReason(snapshot, { ...track, locked: true })).toContain('解锁');
  expect(
    trackDeleteReason(
      { ...snapshot, project: { ...snapshot.project, clips: [{ trackId: track.id } as never] } },
      track,
    ),
  ).toContain('仍有片段');
  expect(
    trackDeleteReason(
      {
        ...snapshot,
        project: { ...snapshot.project, transcripts: [{ captionTrackId: track.id } as never] },
      },
      track,
    ),
  ).toContain('绑定源字幕');
});
