import { describe, expect, it } from 'vitest';
import { TimelineIndex, visibleTicks, visibleTrackIndices } from '../src/lib/timeline-index';
import { defaultClip } from '../src/lib/editor';
import type { Project } from '../src/types';

function project(count: number): Project {
  const settings = { width: 320, height: 180, fpsNum: 30, fpsDen: 1, sampleRate: 48000 };
  return {
    id: 'index',
    schemaVersion: 1,
    name: '索引',
    revision: 0,
    settings,
    assets: [],
    tracks: [{ id: 'v', name: '视频', kind: 'video', hidden: false, muted: false, locked: false }],
    clips: Array.from({ length: count }, (_, i) => ({
      ...defaultClip('image', 'v', i * 30, settings),
      id: `c${i}`,
      durationFrames: 10,
    })),
  };
}
describe('时间线只读索引', () => {
  it('随机视区与暴力参考一致，保留原始顺序和最小显示宽度', () => {
    const p = project(1000);
    p.clips.reverse();
    for (let i = 0; i < p.clips.length; i += 19) p.clips[i].durationFrames = 3000;
    const index = new TimelineIndex(p);
    let seed = 71;
    const random = () => (seed = (Math.imul(seed, 1664525) + 1013904223) >>> 0) / 2 ** 32;
    for (let i = 0; i < 100; i++) {
      const from = Math.floor(random() * 30000),
        to = from + Math.floor(random() * 1000) + 1,
        min = Math.ceil(random() * 180);
      expect(index.query('v', from, to, min).map((c) => c.id)).toEqual(
        p.clips
          .filter(
            (c) =>
              c.startFrame < to &&
              Math.max(c.startFrame + c.durationFrames, c.startFrame + min) > from,
          )
          .map((c) => c.id),
      );
    }
  });
  it('万片段远区查询跳过不可见节点，切口按半开区间', () => {
    const p = project(10000),
      index = new TimelineIndex(p),
      diagnostics = { visited: 0 };
    expect(index.query('v', 270000, 270100, 0, diagnostics).map((c) => c.id)).toEqual([
      'c9000',
      'c9001',
      'c9002',
      'c9003',
    ]);
    expect(diagnostics.visited).toBeLessThan(60);
    expect(index.query('v', 10, 30, 0)).toEqual([]);
    expect(index.query('v', 10, 30, 18).map((c) => c.id)).toEqual(['c0']);
    expect(index.query('missing', 0, 1000, 0)).toEqual([]);
  });
  it('吸附保持播放头优先、原片段顺序优先和排除自身', () => {
    const p = project(4);
    p.clips[0].startFrame = 98;
    p.clips[1].startFrame = 101;
    const index = new TimelineIndex(p);
    expect(index.snap(100, 4, 'c3', 103)).toBe(103);
    expect(index.snap(100, 4, 'c3', 500)).toBe(98);
    expect(index.snap(100, 4, 'c0', 500)).toBe(101);
    expect(index.snap(1000, 4, 'c0', 500)).toBeUndefined();
  });
  it('轨道编号来自完整工程，虚拟窗口与焦点固定行有界', () => {
    const p = project(0);
    p.tracks = Array.from({ length: 1000 }, (_, i) => ({
      id: `t${i}`,
      name: `轨道${i}`,
      kind: i % 2 ? 'audio' : 'video',
      hidden: false,
      muted: false,
      locked: false,
    }));
    const index = new TimelineIndex(p);
    expect(index.tracks[998].ordinal).toBe(500);
    const rows = visibleTrackIndices(1000, 32 + 500 * 66, 330, [0, 999]);
    expect(rows.length).toBeLessThanOrEqual(12);
    expect(rows).toContain(0);
    expect(rows).toContain(999);
    expect(rows).toContain(500);
    expect(visibleTrackIndices(0, 0, 300)).toEqual([]);
  });
  it('24小时刻度只生成当前视区，结果与原公式一致', () => {
    const ticks = visibleTicks(86400, 180, 1, 180 * 80000, 1200);
    expect(ticks.length).toBeLessThan(12);
    expect(ticks).toEqual(
      Array.from({ length: 86401 }, (_, i) => i).filter(
        (t) => t * 180 >= 180 * 80000 - 100 && t * 180 <= 180 * 80000 + 1300,
      ),
    );
  });
});
