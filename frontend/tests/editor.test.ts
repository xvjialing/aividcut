import { describe, expect, it } from 'vitest';
import {
  canPlace,
  clipAt,
  defaultClip,
  filterCaptions,
  framesToSeconds,
  projectEnd,
  secondsToFrames,
  timecode,
  trimClip,
} from '../src/lib/editor';
import type { Asset, Project, Settings, Track } from '../src/types';

const settings: Settings = {
  width: 1920,
  height: 1080,
  fpsNum: 30000,
  fpsDen: 1001,
  sampleRate: 48000,
};
const track: Track = {
  id: 'v1',
  name: '视频 1',
  kind: 'video',
  locked: false,
  muted: false,
  hidden: false,
};
const asset: Asset = {
  id: 'asset',
  name: '素材.mp4',
  kind: 'video',
  path: '/example.mp4',
  durationUs: 3003000,
  width: 1920,
  height: 1080,
  fpsNum: 30000,
  fpsDen: 1001,
  hasAudio: true,
  missing: false,
};

describe('工程与素材时间映射', () => {
  it('29.97fps 使用有理数换算，3.003秒准确得到90帧', () => {
    expect(framesToSeconds(90, settings)).toBe(3.003);
    expect(secondsToFrames(3.003, settings)).toBe(90);
    const clip = defaultClip('video', 'v1', 0, settings, asset);
    expect(clip.durationFrames).toBe(90);
    expect(clip.sourceInUs).toBe(0);
    expect(clip.volume).toBe(1);
  });
  it('修剪起点维持未改变的源终点，速度进入源映射', () => {
    const clip = {
      ...defaultClip('video', 'v1', 30, settings, asset),
      speedNum: 2,
      durationFrames: 45,
    };
    const trimmed = trimClip(clip, 'start', 15, settings);
    expect(trimmed.startFrame).toBe(45);
    expect(trimmed.durationFrames).toBe(30);
    expect(trimmed.sourceInUs).toBe(1001000);
    const oldEnd =
      clip.sourceInUs + Math.round(framesToSeconds(clip.durationFrames, settings) * 2e6);
    const newEnd =
      trimmed.sourceInUs + Math.round(framesToSeconds(trimmed.durationFrames, settings) * 2e6);
    expect(newEnd).toBe(oldEnd);
  });
  it('修剪不能生成负源时间或空片段', () => {
    const clip = defaultClip('video', 'v1', 30, settings, asset);
    expect(trimClip(clip, 'start', -30, settings)).toEqual(clip);
    expect(trimClip(clip, 'start', 1000, settings).durationFrames).toBe(1);
    expect(trimClip(clip, 'end', -1000, settings).durationFrames).toBe(1);
  });
  it('修剪绑定字幕保留源词与人工覆盖字段，不在展示层重建绑定', () => {
    const caption = {
      ...defaultClip('caption', 'caption-track', 30, settings),
      captionSource: {
        transcriptId: 'transcript-1',
        sourceClipId: 'source-clip',
        wordIds: ['word-1', 'word-2'],
        textOverride: true,
        styleOverride: true,
        partial: true,
        stale: false,
      },
    };
    const original = structuredClone(caption);
    for (const edge of ['start', 'end'] as const) {
      const trimmed = trimClip(caption, edge, 5, settings);
      expect(trimmed.captionSource).toEqual(original.captionSource);
      expect(trimmed.id).toBe(caption.id);
    }
    expect(caption).toEqual(original);
  });
  it('显示时间码有稳定格式，帧与时长计算分开', () => {
    expect(timecode(0, settings)).toBe('00:00:00:00');
    expect(timecode(30, settings)).toBe('00:00:01:00');
    expect(timecode(-1, settings)).toBe('00:00:00:00');
  });
});

describe('编辑器可见范围与轨道', () => {
  it('字幕页只列静态字幕并按工程时间排序，搜索内容不混入普通标题', () => {
    const late = { ...defaultClip('caption', 's1', 90, settings), id: 'late', text: '第二句字幕' };
    const early = { ...defaultClip('caption', 's1', 0, settings), id: 'early', text: '第一句字幕' };
    const title = { ...defaultClip('text', 't1', 0, settings), text: '第一句字幕' };
    const project: Project = {
      schemaVersion: 1,
      id: 'project',
      name: '影片',
      revision: 0,
      settings,
      assets: [],
      tracks: [],
      clips: [late, title, early],
    };
    expect(filterCaptions(project).map((clip) => clip.id)).toEqual(['early', 'late']);
    expect(filterCaptions(project, '第一句').map((clip) => clip.id)).toEqual(['early']);
    expect(filterCaptions(project, '不存在')).toEqual([]);
  });
  it('图片可进入视频轨，声音和文字保持各自类型', () => {
    expect(canPlace(track, 'image')).toBe(true);
    expect(canPlace(track, 'video')).toBe(true);
    expect(canPlace(track, 'audio')).toBe(false);
    expect(canPlace(track, 'caption')).toBe(false);
  });
  it('只取当前范围，隐藏画面仍保留未静音原声，无声隐藏素材不挂载', () => {
    const first = defaultClip('video', 'v1', 0, settings, asset);
    const later = { ...first, id: 'later', startFrame: 90 };
    const hidden = { ...first, id: 'hidden', trackId: 'hidden-track' };
    const project: Project = {
      schemaVersion: 1,
      id: 'project',
      name: '影片',
      revision: 0,
      settings,
      assets: [asset],
      tracks: [track, { ...track, id: 'hidden-track', hidden: true }],
      clips: [first, later, hidden],
    };
    expect(clipAt(project, 0).map((clip) => clip.id)).toEqual([first.id, hidden.id]);
    expect(
      clipAt({ ...project, assets: [{ ...asset, hasAudio: false }] }, 0).map((clip) => clip.id),
    ).toEqual([first.id]);
    expect(clipAt(project, 90).map((clip) => clip.id)).toEqual(['later']);
    expect(clipAt(project, 180)).toEqual([]);
    expect(projectEnd(project)).toBe(180);
    expect(projectEnd({ ...project, clips: [] })).toBe(0);
  });
});
