import { describe, expect, it } from 'vitest';
import {
  audioMessages,
  audioProcessor,
  audioProgress,
  pcmBlock,
  renderQuantum,
} from './audio-processor-fixture';

describe('Worklet真实PCM有界预缓冲与恢复', () => {
  it('reset不立即消费；首包不足目标保持等待，启动等待与运行欠载分别计数', () => {
    const processor = audioProcessor();
    processor.receive({ type: 'reset', epoch: 1, playing: true });
    for (let index = 0; index < 4; index++) renderQuantum(processor);
    expect(audioProgress(processor)).toMatchObject({
      bufferPhase: 'startup',
      running: false,
      buffering: true,
      sample: null,
      consumed: 0,
      startupWaitFrames: 512,
      underrun: 0,
      underrunEvents: 0,
    });
    processor.receive(pcmBlock({ samples: 1600 }));
    for (let index = 0; index < 4; index++) {
      expect(renderQuantum(processor)[0].every((sample) => sample === 0)).toBe(true);
    }
    expect(audioMessages(processor, 'consumed')).toEqual([]);
    expect(audioProgress(processor)).toMatchObject({
      bufferPhase: 'startup',
      bufferedSamples: 1600,
      startupWaitFrames: 1024,
      underrun: 0,
    });
    processor.receive(
      pcmBlock({ samples: 1600, sequence: 2, position: 1, audioStartSample: 1600 }),
    );
    expect(renderQuantum(processor)[0][0]).toBe(0.25);
    expect(audioProgress(processor)).toMatchObject({
      bufferPhase: 'playing',
      running: true,
      sample: 128,
      consumed: 128,
      startupWaitFrames: 1024,
      underrun: 0,
      bufferedSamples: 3072,
      prebufferTargetSamples: 2400,
    });
  });
  it.each([1, 2])('仅%d包的短流见end立即开始，末量子余部不算欠载也不等第三包', (count) => {
    const processor = audioProcessor();
    processor.receive({ type: 'reset', epoch: 1, playing: true });
    for (let index = 0; index < count; index++) {
      processor.receive(
        pcmBlock({
          samples: 30,
          sequence: index + 1,
          position: index,
          audioStartSample: index * 30,
          end: index === count - 1,
        }),
      );
    }
    const output = renderQuantum(processor);
    expect(output[0].slice(0, count * 30).every((sample) => sample === 0.25)).toBe(true);
    expect(output[0].slice(count * 30).every((sample) => sample === 0)).toBe(true);
    expect(audioProgress(processor)).toMatchObject({
      bufferPhase: 'ended',
      consumed: count * 30,
      sample: count * 30,
      running: false,
      underrun: 0,
      startupWaitFrames: 0,
    });
    expect(audioMessages(processor, 'consumed').map((message) => message.sequence)).toEqual(
      Array.from({ length: count }, (_, index) => index + 1),
    );
  });
  it('2fps的大单包满足时长即可启动，120fps三包达上限即可启动，不为50ms目标死锁', () => {
    const slow = audioProcessor();
    slow.receive({ type: 'reset', epoch: 1, playing: true });
    slow.receive(pcmBlock({ samples: 24000 }));
    expect(renderQuantum(slow)[0][0]).toBe(0.25);
    expect(audioProgress(slow)).toMatchObject({ bufferPhase: 'playing', bufferedSamples: 23872 });
    const fast = audioProcessor();
    fast.receive({ type: 'reset', epoch: 1, playing: true });
    for (let index = 0; index < 2; index++) {
      fast.receive(
        pcmBlock({
          samples: 400,
          sequence: index + 1,
          position: index,
          audioStartSample: index * 400,
        }),
      );
    }
    expect(renderQuantum(fast)[0][0]).toBe(0);
    fast.receive(pcmBlock({ samples: 400, sequence: 3, position: 2, audioStartSample: 800 }));
    expect(renderQuantum(fast)[0][0]).toBe(0.25);
    expect(audioProgress(fast)).toMatchObject({
      bufferPhase: 'playing',
      bufferedSamples: 1072,
      underrun: 0,
    });
  });
  it('真正欠载冻结在最后真实样本；不足恢复阈值不反复起播，补够后连续输出', () => {
    const processor = audioProcessor();
    processor.receive({ type: 'reset', epoch: 1, playing: true });
    processor.receive(pcmBlock({ samples: 2400, audioStartSample: 500 }));
    for (let index = 0; index < 19; index++) renderQuantum(processor);
    expect(audioProgress(processor)).toMatchObject({
      bufferPhase: 'rebuffering',
      sample: 2900,
      consumed: 2400,
      underrun: 32,
      underrunEvents: 1,
    });
    for (let index = 0; index < 4; index++) renderQuantum(processor);
    expect(audioProgress(processor)).toMatchObject({ sample: 2900, consumed: 2400, underrun: 544 });
    processor.receive(
      pcmBlock({ samples: 1600, sequence: 2, position: 1, audioStartSample: 2900, value: 0.5 }),
    );
    for (let index = 0; index < 4; index++) expect(renderQuantum(processor)[0][0]).toBe(0);
    expect(audioProgress(processor)).toMatchObject({
      bufferPhase: 'rebuffering',
      sample: 2900,
      underrun: 1056,
    });
    processor.receive(
      pcmBlock({ samples: 1600, sequence: 3, position: 2, audioStartSample: 4500, value: 0.75 }),
    );
    expect(renderQuantum(processor)[0][0]).toBe(0.5);
    expect(audioProgress(processor)).toMatchObject({
      bufferPhase: 'playing',
      sample: 3028,
      consumed: 2528,
      underrun: 1056,
      underrunEvents: 1,
    });
  });
  it('欠载后仅剩一个end包也能恢复，不等待不存在的后续数据', () => {
    const processor = audioProcessor();
    processor.receive({ type: 'reset', epoch: 1, playing: true });
    processor.receive(pcmBlock({ samples: 2400 }));
    for (let index = 0; index < 19; index++) renderQuantum(processor);
    processor.receive(
      pcmBlock({ samples: 3, sequence: 2, position: 1, audioStartSample: 2400, end: true }),
    );
    renderQuantum(processor);
    expect(audioProgress(processor)).toMatchObject({
      bufferPhase: 'ended',
      consumed: 2403,
      sample: 2403,
      underrun: 32,
      underrunEvents: 1,
    });
  });
  it('29.97的1601/1602真实样本按连续anchor消费，不改成固定帧长', () => {
    const processor = audioProcessor();
    processor.receive({ type: 'reset', epoch: 1, playing: true });
    processor.receive(pcmBlock({ samples: 1601 }));
    processor.receive(
      pcmBlock({ samples: 1602, sequence: 2, position: 1, audioStartSample: 1601, end: true }),
    );
    for (let index = 0; index < 26; index++) renderQuantum(processor);
    expect(audioMessages(processor, 'consumed').map((message) => message.sample)).toEqual([
      1601, 3203,
    ]);
    expect(audioProgress(processor)).toMatchObject({
      consumed: 3203,
      sample: 3203,
      bufferPhase: 'ended',
      underrun: 0,
    });
  });
  it.each(['sequence', 'position', 'audioStartSample'] as const)(
    '乱序或缺口%s失败停止，不用错误PCM推进',
    (field) => {
      const processor = audioProcessor();
      processor.receive({ type: 'reset', epoch: 1, playing: true });
      processor.receive(pcmBlock({ samples: 1600 }));
      const next = pcmBlock({ samples: 1600, sequence: 2, position: 1, audioStartSample: 1600 });
      next[field]++;
      processor.receive(next);
      expect(audioMessages(processor, 'error')).toHaveLength(1);
      expect(renderQuantum(processor)[0].every((sample) => sample === 0)).toBe(true);
      expect(audioMessages(processor, 'consumed')).toEqual([]);
    },
  );
  it('startup/欠载等待均能flush取消，新epoch不保留旧PCM和旧计数', () => {
    const processor = audioProcessor();
    processor.receive({ type: 'reset', epoch: 1, playing: true });
    processor.receive(pcmBlock({ samples: 30 }));
    renderQuantum(processor);
    processor.receive({ type: 'flush', epoch: 1 });
    processor.receive(
      pcmBlock({ samples: 30, sequence: 2, position: 1, audioStartSample: 30, end: true }),
    );
    expect(renderQuantum(processor)[0][0]).toBe(0);
    expect(audioMessages(processor, 'consumed')).toEqual([]);
    processor.receive({ type: 'reset', epoch: 2, playing: true });
    processor.receive(pcmBlock({ samples: 2400, epoch: 2 }));
    for (let index = 0; index < 19; index++) renderQuantum(processor);
    expect(audioProgress(processor).bufferPhase).toBe('rebuffering');
    processor.receive({ type: 'flush', epoch: 2 });
    processor.receive({ type: 'reset', epoch: 3, playing: true });
    processor.receive(pcmBlock({ samples: 30, epoch: 2, end: true }));
    processor.receive(pcmBlock({ samples: 30, epoch: 3, audioStartSample: 9000, end: true }));
    renderQuantum(processor);
    expect(audioProgress(processor)).toMatchObject({
      epoch: 3,
      bufferPhase: 'ended',
      consumed: 30,
      sample: 9030,
      startupWaitFrames: 0,
      underrun: 0,
    });
  });
});
