import { describe, expect, it } from 'vitest';
import {
  alignmentDisplayState,
  alignmentRows,
  displayTimingSeconds,
  sourceCaptionOverrides,
  speechAudioPlacements,
} from '../src/lib/speech-timing';
import {
  sourceTranscript,
  speechCaption,
  speechProject,
  speechSource,
  speechSourceClip,
} from './speech-fixtures';
import type { SpeechCharacterAlignment } from '../src/speech-types';
const original: SpeechCharacterAlignment = {
  characters: ['1', '2'],
  startTimesSeconds: [0, null],
  endTimesSeconds: [0.1, 0.2],
};
const spoken: SpeechCharacterAlignment = {
  characters: ['十', '二'],
  startTimesSeconds: [0, 0.1],
  endTimesSeconds: [0.1, 0.2],
};
describe('配音两套真实字符时间的显示语义', () => {
  it('原文缺失/不完整不借用朗读规范化结果补成完整', () => {
    expect(alignmentDisplayState(null)).toBe('missing');
    expect(alignmentDisplayState(original)).toBe('incomplete');
    expect(alignmentDisplayState(spoken)).toBe('complete');
    expect(original.characters.join('')).toBe('12');
    expect(spoken.characters.join('')).toBe('十二');
  });
  it('真实零时间保留为0秒，null不变成0，分批显示不改原数组', () => {
    expect(displayTimingSeconds(0)).toBe('0.000秒');
    expect(displayTimingSeconds(null)).toBe('未提供');
    expect(alignmentRows(original, 1, 40)).toEqual([
      { index: 1, character: '2', startSeconds: null, endSeconds: 0.2 },
    ]);
    expect(original.startTimesSeconds).toEqual([0, null]);
    expect(
      alignmentDisplayState({ characters: [' '], startTimesSeconds: [0], endTimesSeconds: [0] }),
    ).toBe('complete');
  });
  it('数组不一致/倒置/乱序不能冒充完整，显示函数不生成字幕', () => {
    expect(alignmentDisplayState({ ...spoken, endTimesSeconds: [0.1] })).toBe('invalid');
    expect(alignmentDisplayState({ ...spoken, startTimesSeconds: [0.2, 0.1] })).toBe('invalid');
    expect(alignmentDisplayState({ ...spoken, endTimesSeconds: [0.1, -1] })).toBe('invalid');
  });
});
describe('现有配音与人工字幕状态只读核对', () => {
  it('列出每个实际音频片段范围而不根据已上轨布尔值复制配音', () => {
    const project = {
      ...speechProject,
      clips: [
        ...speechProject.clips,
        { ...speechSourceClip, id: 'second-placement', startFrame: 180, durationFrames: 30 },
      ],
    };
    const before = structuredClone(project);
    const values = speechAudioPlacements(project, speechSource.id);
    expect(values.map((value) => value.clipId)).toEqual([speechSourceClip.id, 'second-placement']);
    expect(values[1]).toEqual(expect.objectContaining({ startFrame: 180, durationFrames: 30 }));
    expect(project).toEqual(before);
  });
  it('文字和样式人工覆盖分别保留身份，不自行猜测能合并或重置', () => {
    expect(sourceCaptionOverrides(speechProject, sourceTranscript.id)).toEqual({
      text: [speechCaption.id],
      style: [speechCaption.id],
    });
  });
});
