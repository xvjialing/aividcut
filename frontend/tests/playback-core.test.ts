// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { audioProcessor as processor } from './audio-processor-fixture';
import {
  decodePlaybackFrame,
  decodePlaybackState,
  playbackStreamAllowed,
} from '../src/lib/playback-protocol';
import { PlaybackBundleBuffer } from '../src/lib/playback-buffer';
import { PlaybackClock } from '../src/lib/editor';
import { playbackPacket, playbackStatus } from './playback-fixtures';
beforeEach(() => {
  vi.stubGlobal('cancelAnimationFrame', vi.fn());
  vi.stubGlobal('requestAnimationFrame', vi.fn());
});
afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});
describe('AIV1二进制有界协议', () => {
  it('RGBA不走base64，未对齐PCM仍按little endian正确读取，29.97不猜1600', () => {
    const frame = decodePlaybackFrame(
      playbackPacket(
        { playing: true, fpsNum: 30000, fpsDen: 1001, samples: 1602, audioStartSample: 1601 },
        [0.25, -0.5],
      ),
    );
    expect(frame.rgba.length).toBe(8);
    expect(frame.pcm[0]).toBe(0.25);
    expect(frame.pcm[1]).toBe(-0.5);
    expect(frame.header.samples).toBe(1602);
    expect(frame.header.audioStartSample).toBe(1601);
  });
  it('拒绝截断/错标识/错误采样率/空播放PCM/NaN，不静默播放坏数据', () => {
    const magic = playbackPacket();
    new DataView(magic).setUint32(0, 0, false);
    for (const value of [
      magic,
      playbackPacket().slice(0, -1),
      playbackPacket({ sampleRate: 44100 }),
      playbackPacket({ playing: true, samples: 0 }),
      playbackPacket({ playing: true }, [NaN]),
    ])
      expect(() => decodePlaybackFrame(value)).toThrow('数据格式无效');
    expect(() => decodePlaybackState('{broken')).toThrow();
    expect(
      decodePlaybackState(JSON.stringify({ type: 'state', status: playbackStatus })).epoch,
    ).toBe(1);
  });
  it('只允许受控loopback会话地址，不接受外网或URL登录信息', () => {
    expect(playbackStreamAllowed('ws://127.0.0.1:43111/session?token=fixture')).toBe(true);
    expect(playbackStreamAllowed('wss://example.invalid/session')).toBe(false);
    expect(playbackStreamAllowed('ws://user:password@localhost/session')).toBe(false);
  });
});
describe('三bundle背压与真实消费', () => {
  it('接收不能ACK，必须PCM已消费且画面呈现/丢弃，未来帧不提前选择', () => {
    const queue = new PlaybackBundleBuffer();
    queue.reset(1);
    for (let index = 0; index < 3; index++)
      queue.push(
        decodePlaybackFrame(
          playbackPacket({ sequence: index + 1, position: index, playing: true }),
        ),
      );
    expect(queue.acknowledge()).toEqual([]);
    expect(() =>
      queue.push(decodePlaybackFrame(playbackPacket({ sequence: 4, playing: true }))),
    ).toThrow();
    expect(queue.candidate(0.5)?.header.position).toBe(0);
    queue.consumed(1);
    expect(queue.acknowledge()).toEqual([]);
    queue.release(1);
    expect(queue.acknowledge().map((frame) => frame.header.sequence)).toEqual([1]);
    queue.consumed(2);
    queue.consumed(3);
    expect(queue.candidate(2)?.header.sequence).toBe(3);
    expect(queue.acknowledge().map((frame) => frame.header.sequence)).toEqual([2]);
    queue.release(3);
    expect(queue.acknowledge()[0].header.sequence).toBe(3);
  });
  it('新epoch丢旧队列与迟到帧，不沿用旧确认顺序', () => {
    const queue = new PlaybackBundleBuffer();
    queue.reset(1);
    queue.push(decodePlaybackFrame(playbackPacket()));
    queue.reset(2);
    expect(queue.push(decodePlaybackFrame(playbackPacket()))).toBe(false);
    expect(queue.length).toBe(0);
    expect(queue.acknowledge()).toEqual([]);
  });
});
describe('Clock外部驱动', () => {
  it('用户命令走driver，外部同步不反发RPC、不启动墙钟RAF，卸载后旧路径仍可用', () => {
    const clock = new PlaybackClock();
    clock.configure(3, 30);
    const driver = { play: vi.fn(), pause: vi.fn(), seek: vi.fn(), invalidate: vi.fn() };
    const remove = clock.attachExternal(driver);
    clock.play();
    clock.seek(2);
    clock.pause();
    expect(driver.play).toHaveBeenCalledOnce();
    expect(driver.seek).toHaveBeenCalledWith(2);
    expect(driver.pause).toHaveBeenCalledOnce();
    clock.synchronizeExternal(1.5, true);
    expect(clock.getFrame()).toBe(1);
    expect(requestAnimationFrame).not.toHaveBeenCalled();
    expect(driver.seek).toHaveBeenCalledOnce();
    remove();
    clock.play();
    expect(requestAnimationFrame).toHaveBeenCalledOnce();
    clock.destroy();
  });
});
describe('AudioWorklet真实PCM主时钟', () => {
  it('末端不足一量子也只消费实际样本，剩余输出0并停；不伪造源样本总量', () => {
    const value = processor();
    value.receive({ type: 'reset', epoch: 1, playing: true });
    value.receive({
      type: 'pcm',
      epoch: 1,
      sequence: 1,
      position: 0,
      samples: 3,
      audioStartSample: 500,
      end: true,
      buffer: new Float32Array([0.1, 0.2, 0.3, 0.4, 0.5, 0.6]).buffer,
    });
    const output = [new Float32Array(128), new Float32Array(128)];
    value.process([], [output]);
    expect(output[0][0]).toBeCloseTo(0.1);
    expect(output[1][2]).toBeCloseTo(0.6);
    expect(output[0][3]).toBe(0);
    expect(value.port.postMessage).toHaveBeenCalledWith(
      expect.objectContaining({ type: 'consumed', sample: 503, sequence: 1, end: true }),
    );
    const count = value.port.postMessage.mock.calls.length;
    value.process([], [output]);
    expect(value.port.postMessage.mock.calls.length).toBe(count);
  });
  it('预缓冲只出0且不推进内容时间，新epoch后旧PCM不得发声', () => {
    const value = processor();
    value.receive({ type: 'reset', epoch: 2, playing: true });
    value.receive({ type: 'pcm', epoch: 1, samples: 1, buffer: new Float32Array([1, 1]).buffer });
    const output = [new Float32Array(128), new Float32Array(128)];
    for (let index = 0; index < 4; index++) value.process([], [output]);
    expect(output[0].every((sample) => sample === 0)).toBe(true);
    expect(value.port.postMessage).toHaveBeenCalledWith(
      expect.objectContaining({
        type: 'progress',
        sample: null,
        consumed: 0,
        buffering: true,
        bufferPhase: 'startup',
        startupWaitFrames: 512,
        underrun: 0,
      }),
    );
  });
  it('超过三块PCM明确停止，不用丢声音假装流畅', () => {
    const value = processor();
    value.receive({ type: 'reset', epoch: 1, playing: true });
    for (let index = 0; index < 4; index++)
      value.receive({
        type: 'pcm',
        epoch: 1,
        samples: 1,
        sequence: index + 1,
        position: index,
        audioStartSample: index,
        end: false,
        buffer: new Float32Array([1, 1]).buffer,
      });
    expect(value.port.postMessage).toHaveBeenCalledWith(
      expect.objectContaining({ type: 'error', epoch: 1 }),
    );
    const output = [new Float32Array(128), new Float32Array(128)];
    value.process([], [output]);
    expect(output[0][0]).toBe(0);
  });
});
