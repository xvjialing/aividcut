import { describe, expect, it } from 'vitest';
import {
  originalShotTiming,
  replacementTargetIssue,
  reviewContextIssue,
  formatSourceSeconds,
  sourceSecondsToMicroseconds,
} from '../src/lib/action-review';
import { defaultClip } from '../src/lib/editor';
import { generationProject } from './generation-fixtures';
import type { Project } from '../src/types';

const clip = {
  ...defaultClip(
    'video',
    'video-track',
    90,
    generationProject.settings,
    generationProject.assets[2],
  ),
  id: 'shot',
  durationFrames: 120,
  sourceInUs: 33367,
  speedNum: 2,
  speedDen: 1,
};
const project: Project = {
  ...generationProject,
  revision: 4,
  tracks: [
    {
      id: 'video-track',
      name: '视频 1',
      kind: 'video',
      locked: false,
      muted: false,
      hidden: false,
    },
  ],
  clips: [clip],
};
describe('操作核对的本地上下文门槛', () => {
  it('秒数精确转整数微秒，最多6位小数，展示至少3位秒', () => {
    expect(sourceSecondsToMicroseconds('0.033367')).toBe(33367);
    expect(sourceSecondsToMicroseconds('3.000000')).toBe(3000000);
    expect(sourceSecondsToMicroseconds('.000001')).toBe(1);
    for (const value of ['-1', '0.0000001', '1e3', '', '9007199254.740992'])
      expect(sourceSecondsToMicroseconds(value)).toBeNull();
    expect(formatSourceSeconds(3000000)).toBe('3.000');
    expect(formatSourceSeconds(33367)).toBe('0.033367');
  });
  it('挂起冲突即使Inspector已卸载仍阻止核对；草稿/手势和版本分别检查', () => {
    const context = {
      project,
      projectId: project.id,
      revision: 4,
      pendingConflict: false,
      inspectorDraft: false,
      timelineGesture: false,
    };
    expect(reviewContextIssue(context)).toBe('');
    expect(reviewContextIssue({ ...context, pendingConflict: true })).toContain('挂起');
    expect(reviewContextIssue({ ...context, inspectorDraft: true })).toContain('属性草稿');
    expect(reviewContextIssue({ ...context, timelineGesture: true })).toContain('拖动');
    expect(reviewContextIssue({ ...context, revision: 3 })).toContain('版本');
  });
});
describe('候选替换前的界面目标核对', () => {
  it('只接受同工程的视觉候选与未锁定视觉片段，不冒充源文件校验', () => {
    expect(replacementTargetIssue(project, clip.id, 'image', project.id)).toBe('');
    expect(replacementTargetIssue(project, clip.id, 'video', 'other-project')).toContain(
      '所属工程',
    );
    expect(replacementTargetIssue(project, clip.id, 'audio', project.id)).toContain('图片或视频');
    expect(replacementTargetIssue(project, null, 'video', project.id)).toContain('选择');
    expect(
      replacementTargetIssue(
        { ...project, tracks: [{ ...project.tracks[0], locked: true }] },
        clip.id,
        'video',
        project.id,
      ),
    ).toContain('锁定');
  });
  it('原镜头时序展示保留整数帧、精确源微秒和有理数速度，不修改原对象', () => {
    const before = structuredClone(project);
    expect(originalShotTiming(clip)).toEqual({
      startFrame: 90,
      durationFrames: 120,
      sourceInUs: 33367,
      speedNum: 2,
      speedDen: 1,
    });
    expect(project).toEqual(before);
  });
});
