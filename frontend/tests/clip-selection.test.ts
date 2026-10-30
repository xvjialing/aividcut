import { describe, expect, it } from 'vitest';
import {
  emptySelection,
  selectClip,
  linkedSelection,
  groupMoveDraft,
  pruneClipSelection,
  groupTrimDraft,
} from '../src/lib/clip-selection';
import { defaultClip } from '../src/lib/editor';
import type { Project } from '../src/types';

export function selectionProject(): Project {
  const settings = { width: 320, height: 180, fpsNum: 30, fpsDen: 1, sampleRate: 48000 };
  return {
    id: 'selection',
    name: '多选',
    schemaVersion: 1,
    revision: 0,
    settings,
    assets: [],
    tracks: [
      { id: 't1', kind: 'text', name: '标题1', locked: false, muted: false, hidden: false },
      { id: 't2', kind: 'text', name: '标题2', locked: false, muted: false, hidden: false },
    ],
    clips: [
      ['a', 't1', 30],
      ['b', 't1', 90],
      ['c', 't2', 150],
    ].map(([id, track, start]) => ({
      ...defaultClip('text', String(track), Number(start), settings),
      id: String(id),
      name: String(id),
      text: String(id),
      durationFrames: 30,
    })),
  };
}
describe('片段选择集合', () => {
  it('同量修剪草稿不逐成员钳位，不改变工程数据', () => {
    const p = selectionProject(),
      before = JSON.stringify(p);
    const head = groupTrimDraft(p, ['a', 'b'], 'start', 5);
    expect(head.valid).toBe(true);
    expect(head.drafts.map((c) => [c.startFrame, c.durationFrames, c.sourceInUs])).toEqual([
      [35, 25, 0],
      [95, 25, 0],
    ]);
    expect(groupTrimDraft(p, ['a', 'b'], 'start', 30).valid).toBe(false);
    expect(groupTrimDraft(p, ['a', 'b'], 'end', -30).valid).toBe(false);
    expect(JSON.stringify(p)).toBe(before);
  });
  it('旧读模型的延迟清理不能清空新修订刚选中的副本', () => {
    const before = selectionProject();
    const after = {
      ...before,
      revision: 1,
      clips: [...before.clips, { ...before.clips[0], id: 'new-copy', startFrame: 300 }],
    };
    const selected = { projectId: before.id, ids: ['new-copy'], primary: 'new-copy' };
    expect(pruneClipSelection(selected, before, after)).toBe(selected);
    expect(pruneClipSelection(selected, after, after)).toBe(selected);
  });
  it('当前权威模型仍清理已删除对象并恢复剩余活动片段', () => {
    const before = selectionProject();
    const deleted = { ...before, revision: 2, clips: before.clips.filter((c) => c.id !== 'a') };
    expect(
      pruneClipSelection({ projectId: before.id, ids: ['a', 'b'], primary: 'a' }, deleted, deleted),
    ).toMatchObject({ ids: ['b'], primary: 'b' });
  });
  it('不同工程或尚未接受的清理不修改当前选择', () => {
    const project = selectionProject();
    const selected = { projectId: 'other', ids: ['chosen'], primary: 'chosen' };
    expect(pruneClipSelection(selected, project, project)).toBe(selected);
    expect(pruneClipSelection(selected, project)).toBe(selected);
  });
  it('普通替换、修饰增删和主片段稳定，新工程不继承旧集合', () => {
    let s = selectClip(emptySelection, 'p', 'a');
    s = selectClip(s, 'p', 'b', true);
    expect(s.ids).toEqual(['a', 'b']);
    expect(s.primary).toBe('b');
    s = selectClip(s, 'p', 'b', true);
    expect(s).toMatchObject({ ids: ['a'], primary: 'a' });
    expect(selectClip(s, 'q', 'z', true).ids).toEqual(['z']);
  });
  it('联动只展开一次，草稿保留相对位置，锁轨与负时间无效', () => {
    const p = selectionProject();
    p.clips[0].linkGroup = p.clips[1].linkGroup = 'pair';
    expect(linkedSelection(p, ['a', 'b']).length).toBe(2);
    const before = JSON.stringify(p),
      result = groupMoveDraft(p, ['a'], 7, 0);
    expect(result.valid).toBe(true);
    expect(result.drafts.map((c) => c.startFrame)).toEqual([37, 97]);
    expect(JSON.stringify(p)).toBe(before);
    expect(groupMoveDraft(p, ['a'], -31, 0).valid).toBe(false);
    p.tracks[0].locked = true;
    expect(groupMoveDraft(p, ['a'], 1, 0).valid).toBe(false);
  });
  it('跨轨按相同行偏移，目标不存在或类型不兼容时整体标记无效', () => {
    const p = selectionProject();
    expect(groupMoveDraft(p, ['a', 'b'], 0, 1)).toMatchObject({ valid: true });
    expect(groupMoveDraft(p, ['a', 'c'], 0, 1).valid).toBe(false);
    p.tracks[1].kind = 'audio';
    expect(groupMoveDraft(p, ['a', 'b'], 0, 1).valid).toBe(false);
  });
});
