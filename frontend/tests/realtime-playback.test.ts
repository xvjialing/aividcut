// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { RealtimePlayback, type PlaybackRuntime } from '../src/lib/realtime-playback';
import { PlaybackClock } from '../src/lib/editor';
import {
  fakePlaybackAPI,
  playbackPacket,
  playbackProject,
  playbackStatus,
} from './playback-fixtures';
import type { PlaybackFrame, PlaybackStart, PlaybackStatus } from '../src/playback-types';
import {
  audioMessages,
  audioProcessor,
  audioProgress,
  renderQuantum,
} from './audio-processor-fixture';

class FakeSocket {
  readyState = 1;
  binaryType = '';
  onmessage: ((event: MessageEvent) => void) | null = null;
  onclose: (() => void) | null = null;
  onerror: (() => void) | null = null;
  send = vi.fn();
  close = vi.fn(() => {
    this.readyState = 3;
    this.onclose?.();
  });
  emit(data: unknown) {
    this.onmessage?.({ data } as MessageEvent);
  }
  state(value: PlaybackStatus) {
    this.emit(JSON.stringify({ type: 'state', status: value }));
  }
}
class FakeContext {
  sampleRate = 48000;
  state: AudioContextState = 'suspended';
  destination = {};
  audioWorklet = { addModule: vi.fn(async () => {}) };
  onstatechange: (() => void) | null = null;
  resume = vi.fn(async () => {
    this.state = 'running';
  });
  close = vi.fn(async () => {
    this.state = 'closed';
  });
}
class FakeNode {
  port = { postMessage: vi.fn(), onmessage: null as ((event: MessageEvent) => void) | null };
  connect = vi.fn();
  disconnect = vi.fn();
  onprocessorerror: (() => void) | null = null;
  emit(data: unknown) {
    this.port.onmessage?.({ data } as MessageEvent);
  }
}
let callbacks: Map<number, FrameRequestCallback>;
let nextRAF = 0;
let fetchGuard: ReturnType<typeof vi.fn>;
let open: RealtimePlayback[];
beforeEach(() => {
  callbacks = new Map();
  open = [];
  fetchGuard = vi.fn().mockRejectedValue(new Error('测试禁止真实播放网络'));
  vi.stubGlobal('fetch', fetchGuard);
  vi.stubGlobal('requestAnimationFrame', (fn: FrameRequestCallback) => {
    callbacks.set(++nextRAF, fn);
    return nextRAF;
  });
  vi.stubGlobal('cancelAnimationFrame', (id: number) => callbacks.delete(id));
});
afterEach(() => {
  open.forEach((value) => value.dispose());
  expect(fetchGuard).not.toHaveBeenCalled();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});
const flushRAF = () => {
  for (const [id, fn] of [...callbacks]) {
    callbacks.delete(id);
    fn(performance.now());
  }
};
const settle = async () => {
  await new Promise((resolve) => setTimeout(resolve, 0));
};

it('零PCM的纯画面定位立即显示并确认，随后仍由真实音频包推进至结尾', async () => {
  const value = fixture(48000, true);
  await value.controller.open();
  value.socket.state(value.status);
  value.socket.emit(playbackPacket({ samples: 0, pictureOnly: true }));
  expect(value.draw).toHaveBeenCalledOnce();
  expect(sent(value.socket).filter((message) => message.type === 'ack')).toHaveLength(1);
  expect(value.context.resume).not.toHaveBeenCalled();
  expect(value.context.state).toBe('suspended');
  value.clock.play();
  await settle();
  value.socket.state({ ...value.status, status: 'playing', epoch: 2 });
  value.socket.emit(playbackPacket({ epoch: 2, samples: 0, pictureOnly: true }));
  expect(
    value.node.port.postMessage.mock.calls.filter(([message]) => message.type === 'pcm'),
  ).toHaveLength(0);
  playingBundle(value, 0);
  playingBundle(value, 1);
  playingBundle(value, 2, true);
  for (let i = 0; i < 38; i++) {
    renderQuantum(value.processor!);
    flushRAF();
  }
  expect(audioProgress(value.processor!)).toMatchObject({
    consumed: 4800,
    bufferPhase: 'ended',
    underrun: 0,
  });
  expect(value.clock.getFrame()).toBe(3);
  expect(audioMessages(value.processor!, 'error')).toEqual([]);
});
function fixture(
  rate = 48000,
  realAudio = false,
  totalFrames = 3,
  fpsNum = 30,
  renderQuality?: 'full' | 'half' | 'quarter',
  proxyProfile?: '720p' | '540p',
) {
  const api = fakePlaybackAPI();
  const project = {
    ...playbackProject,
    settings: { ...playbackProject.settings, fpsNum },
    clips: playbackProject.clips.map((clip) => ({ ...clip, durationFrames: totalFrames })),
  };
  const status = { ...playbackStatus, durationFrames: totalFrames, fpsNum };
  api.startPlayback.mockResolvedValue({
    status,
    streamUrl: 'ws://127.0.0.1:43111/playback/test?token=fixture-only-secret',
  });
  const context = new FakeContext();
  context.sampleRate = rate;
  const node = new FakeNode();
  const processor = realAudio ? audioProcessor() : null;
  if (processor) {
    node.port.postMessage.mockImplementation((message) => processor.receive(message));
    processor.port.postMessage.mockImplementation((message) => node.emit(message));
  }
  const socket = new FakeSocket();
  const draw = vi.fn();
  const clear = vi.fn();
  const canvas = {
    width: 2,
    height: 1,
    getContext: () => ({ putImageData: draw, clearRect: clear }),
  } as unknown as HTMLCanvasElement;
  const runtime: PlaybackRuntime = {
    audioContext: () => context as unknown as AudioContext,
    audioNode: () => node as unknown as AudioWorkletNode,
    socket: vi.fn(() => socket as unknown as WebSocket),
    image: vi.fn(
      (frame: PlaybackFrame) =>
        ({ data: frame.rgba, width: frame.header.width, height: frame.header.height }) as ImageData,
    ),
  };
  const clock = new PlaybackClock();
  clock.configure(totalFrames, fpsNum);
  const state = vi.fn();
  const controller = new RealtimePlayback({
    project,
    clock,
    canvas,
    api,
    runtime,
    onState: state,
    renderQuality,
    proxyProfile,
    sourceEpoch: 7,
  });
  open.push(controller);
  return {
    api,
    context,
    node,
    socket,
    draw,
    clear,
    clock,
    state,
    controller,
    runtime,
    processor,
    status,
  };
}

it('把显式精度传给只读预览请求，工程时间与快照保持原值', async () => {
  const value = fixture(48000, false, 3, 30, 'half');
  await value.controller.open();
  expect(value.api.startPlayback).toHaveBeenCalledWith({
    projectId: playbackProject.id,
    projectRevision: playbackProject.revision,
    frame: 0,
    renderQuality: 'half',
  });
  expect(playbackProject.settings.fpsNum).toBe(30);
});
const sent = (socket: FakeSocket) => socket.send.mock.calls.map(([value]) => JSON.parse(value));
async function beginBuffered(value: ReturnType<typeof fixture>) {
  await value.controller.open();
  value.socket.state(value.status);
  value.socket.emit(playbackPacket({ fpsNum: value.status.fpsNum }));
  value.clock.play();
  await settle();
  value.socket.state({ ...value.status, status: 'playing', epoch: 2 });
  value.socket.emit(playbackPacket({ epoch: 2, fpsNum: value.status.fpsNum }));
}
function playingBundle(value: ReturnType<typeof fixture>, position: number, end = false) {
  const samples = 48000 / value.status.fpsNum;
  value.socket.emit(
    playbackPacket({
      epoch: 2,
      sequence: position + 2,
      position,
      playing: true,
      end,
      samples,
      fpsNum: value.status.fpsNum,
      audioStartSample: position * samples,
    }),
  );
}
describe('实际Worklet与会话背压联合回归', () => {
  it('窗口绘制被遮挡时，末包真实消费仍立即发布结尾状态', async () => {
    const value = fixture(48000, true);
    await beginBuffered(value);
    playingBundle(value, 0);
    playingBundle(value, 1);
    playingBundle(value, 2, true);
    for (let i = 0; i < 38; i++) renderQuantum(value.processor!);
    expect(callbacks.size).toBeGreaterThan(0);
    expect(value.clock.getFrame()).toBe(3);
    expect(value.clock.isPlaying()).toBe(false);
    expect(value.state.mock.calls.at(-1)?.[0]).toMatchObject({
      phase: 'ended',
      reason: '已播放到结尾',
    });
    expect(audioProgress(value.processor!)).toMatchObject({
      consumed: 4800,
      bufferPhase: 'ended',
      underrun: 0,
    });
  });
  it('首包等待不消费不ACK，真实预缓冲就绪后播完整工程且连续释放全部包', async () => {
    const value = fixture(48000, true);
    await beginBuffered(value);
    playingBundle(value, 0);
    for (let index = 0; index < 4; index++) renderQuantum(value.processor!);
    flushRAF();
    expect(audioProgress(value.processor!)).toMatchObject({
      bufferPhase: 'startup',
      underrun: 0,
      startupWaitFrames: 512,
    });
    expect(value.clock.getFrame()).toBe(0);
    expect(value.state).toHaveBeenLastCalledWith(
      expect.objectContaining({ phase: 'buffering', reason: expect.stringContaining('预缓冲') }),
    );
    expect(
      sent(value.socket).filter((message) => message.type === 'ack' && message.epoch === 2),
    ).toHaveLength(1);
    playingBundle(value, 1);
    playingBundle(value, 2, true);
    for (let index = 0; index < 38; index++) {
      renderQuantum(value.processor!);
      flushRAF();
    }
    expect(audioProgress(value.processor!)).toMatchObject({
      bufferPhase: 'ended',
      consumed: 4800,
      underrun: 0,
      underrunEvents: 0,
    });
    expect(value.clock.getFrame()).toBe(3);
    expect(value.clock.isPlaying()).toBe(false);
    expect(
      sent(value.socket)
        .filter((message) => message.type === 'ack' && message.epoch === 2)
        .map((message) => message.sequence),
    ).toEqual([1, 2, 3, 4]);
    expect(audioMessages(value.processor!, 'error')).toEqual([]);
  });
  it('运行欠载保持内容钟，尾包到达恢复且不重发play或重启会话', async () => {
    const value = fixture(48000, true);
    await beginBuffered(value);
    playingBundle(value, 0);
    playingBundle(value, 1);
    for (let index = 0; index < 30; index++) {
      renderQuantum(value.processor!);
      flushRAF();
    }
    expect(audioProgress(value.processor!)).toMatchObject({
      bufferPhase: 'rebuffering',
      sample: 3200,
      underrun: 640,
      underrunEvents: 1,
    });
    const frame = value.clock.getFrame();
    expect(frame).toBe(2);
    expect(value.state).toHaveBeenLastCalledWith(
      expect.objectContaining({ phase: 'buffering', reason: expect.stringContaining('正在补充') }),
    );
    for (let index = 0; index < 4; index++) {
      renderQuantum(value.processor!);
      flushRAF();
    }
    expect(value.clock.getFrame()).toBe(frame);
    playingBundle(value, 2, true);
    for (let index = 0; index < 13; index++) {
      renderQuantum(value.processor!);
      flushRAF();
    }
    expect(audioProgress(value.processor!)).toMatchObject({
      bufferPhase: 'ended',
      consumed: 4800,
      underrun: 1152,
      underrunEvents: 1,
    });
    expect(value.clock.getFrame()).toBe(3);
    expect(sent(value.socket).filter((message) => message.type === 'play')).toHaveLength(1);
    expect(
      sent(value.socket)
        .filter((message) => message.type === 'ack' && message.epoch === 2)
        .map((message) => message.sequence),
    ).toEqual([1, 2, 3, 4]);
  });
  it('高帧率画面确认延迟时仍可释放连续ACK，补满三包后恢复而不背压死锁', async () => {
    const value = fixture(48000, true, 6, 120);
    await beginBuffered(value);
    for (let index = 0; index < 3; index++) playingBundle(value, index);
    for (let index = 0; index < 10; index++) renderQuantum(value.processor!);
    expect(audioProgress(value.processor!)).toMatchObject({
      bufferPhase: 'rebuffering',
      consumed: 1200,
    });
    const ackBeforePaint = sent(value.socket)
      .filter((message) => message.type === 'ack' && message.epoch === 2)
      .map((message) => message.sequence);
    expect(ackBeforePaint).toEqual([1, 2, 3]);
    // 服务端至多3包未ACK：剩下1包已消费待画，因此先只能补2包。
    playingBundle(value, 3);
    playingBundle(value, 4);
    for (let index = 0; index < 4; index++) renderQuantum(value.processor!);
    expect(audioProgress(value.processor!)).toMatchObject({
      bufferPhase: 'rebuffering',
      consumed: 1200,
      bufferedSamples: 800,
    });
    flushRAF();
    expect(sent(value.socket).at(-1)).toEqual({ type: 'ack', epoch: 2, sequence: 4, position: 2 });
    playingBundle(value, 5, true);
    for (let index = 0; index < 10; index++) {
      renderQuantum(value.processor!);
      flushRAF();
    }
    expect(audioProgress(value.processor!)).toMatchObject({ bufferPhase: 'ended', consumed: 2400 });
    expect(value.clock.getFrame()).toBe(6);
    expect(
      sent(value.socket)
        .filter((message) => message.type === 'ack' && message.epoch === 2)
        .map((message) => message.sequence),
    ).toEqual([1, 2, 3, 4, 5, 6, 7]);
    expect(audioMessages(value.processor!, 'error')).toEqual([]);
  });
  it('startup时快速seek/pause清空旧队列；新epoch单包结尾可播放，旧消息不能推进', async () => {
    const value = fixture(48000, true);
    await beginBuffered(value);
    playingBundle(value, 0);
    renderQuantum(value.processor!);
    value.clock.seek(1);
    value.clock.seek(2);
    value.clock.pause();
    value.socket.state({ ...value.status, status: 'playing', epoch: 3, frame: 1 });
    value.socket.emit(playbackPacket({ epoch: 3, position: 1 }));
    value.node.emit({ type: 'progress', epoch: 2, sample: 1600, buffering: false });
    expect(value.clock.getFrame()).toBe(2);
    value.socket.state({ ...value.status, epoch: 5, frame: 2 });
    value.socket.emit(playbackPacket({ epoch: 5, position: 2, end: true }));
    expect(audioMessages(value.processor!, 'consumed')).toEqual([]);
    expect(renderQuantum(value.processor!)[0].every((sample) => sample === 0)).toBe(true);
    value.clock.play();
    await settle();
    value.socket.state({ ...value.status, status: 'playing', epoch: 6, frame: 2 });
    value.socket.emit(playbackPacket({ epoch: 6, position: 2, end: true }));
    value.socket.emit(
      playbackPacket({
        epoch: 6,
        position: 2,
        sequence: 2,
        playing: true,
        end: true,
        audioStartSample: 3200,
      }),
    );
    for (let index = 0; index < 13; index++) {
      renderQuantum(value.processor!);
      flushRAF();
    }
    expect(audioProgress(value.processor!)).toMatchObject({
      epoch: 6,
      bufferPhase: 'ended',
      consumed: 1600,
      sample: 4800,
      underrun: 0,
      startupWaitFrames: 0,
    });
    expect(value.clock.getFrame()).toBe(3);
    expect(value.clock.isPlaying()).toBe(false);
    expect(
      sent(value.socket)
        .filter((message) => message.type === 'ack' && message.epoch === 6)
        .map((message) => message.sequence),
    ).toEqual([1, 2]);
  });
  it('非连续bundle在消费前明确失败，不因预缓冲吞掉乱序内容', async () => {
    const value = fixture(48000, true);
    await beginBuffered(value);
    playingBundle(value, 0);
    value.socket.emit(
      playbackPacket({ epoch: 2, sequence: 4, position: 1, audioStartSample: 1600, playing: true }),
    );
    expect(value.state).toHaveBeenLastCalledWith(
      expect.objectContaining({ phase: 'failed', reason: expect.stringContaining('顺序无效') }),
    );
    expect(value.context.close).toHaveBeenCalledOnce();
    expect(audioMessages(value.processor!, 'consumed')).toEqual([]);
    expect(value.clock.getFrame()).toBe(0);
  });
});
describe('实时合成播放会话与音频主时钟', () => {
  it('暂停首帧丢PCM立即ACK；播放仅消费后推进/ACK，未来画面不得抢先', async () => {
    const value = fixture();
    await value.controller.open();
    value.clock.play();
    expect(sent(value.socket)).toEqual([]);
    value.socket.state(playbackStatus);
    value.socket.emit(playbackPacket());
    expect(value.draw).toHaveBeenCalledOnce();
    expect(sent(value.socket)).toEqual([{ type: 'ack', epoch: 1, sequence: 1, position: 0 }]);
    expect(value.node.port.postMessage.mock.calls.some(([message]) => message.type === 'pcm')).toBe(
      false,
    );
    value.clock.play();
    expect(value.context.resume).toHaveBeenCalledOnce();
    await settle();
    expect(sent(value.socket).at(-1)).toEqual({ type: 'play', frame: 0 });
    const playing = { ...playbackStatus, status: 'playing' as const, epoch: 2 };
    value.socket.state(playing);
    value.socket.emit(playbackPacket({ epoch: 2 }));
    for (let position = 0; position < 3; position++)
      value.socket.emit(
        playbackPacket({
          epoch: 2,
          sequence: position + 2,
          position,
          playing: true,
          audioStartSample: position * 1600,
          end: position === 2,
        }),
      );
    expect(
      value.node.port.postMessage.mock.calls.filter(([message]) => message.type === 'pcm'),
    ).toHaveLength(3);
    expect(
      sent(value.socket).filter((message) => message.type === 'ack' && message.epoch === 2),
    ).toHaveLength(1);
    value.node.emit({ type: 'progress', epoch: 2, sample: 800, buffering: false });
    flushRAF();
    expect(value.clock.getFrame()).toBe(0);
    expect(
      (value.runtime.image as ReturnType<typeof vi.fn>).mock.calls.at(-1)![0].header.position,
    ).toBe(0);
    expect(
      sent(value.socket).filter((message) => message.type === 'ack' && message.epoch === 2),
    ).toHaveLength(1);
    value.node.emit({
      type: 'consumed',
      epoch: 2,
      sequence: 2,
      position: 0,
      sample: 1600,
      end: false,
    });
    expect(sent(value.socket).at(-1)).toEqual({ type: 'ack', epoch: 2, sequence: 2, position: 0 });
    value.node.emit({ type: 'progress', epoch: 2, sample: 1600, buffering: true });
    flushRAF();
    expect(value.clock.getFrame()).toBe(1);
    const frame = value.clock.getFrame();
    flushRAF();
    expect(value.clock.getFrame()).toBe(frame);
    expect(JSON.stringify(value.state.mock.calls)).not.toContain('fixture-only-secret');
  });
  it('pause/seek先flush；旧epoch迟到不得发声，末包实际消费后停并连续ACK', async () => {
    const value = fixture();
    await value.controller.open();
    value.socket.state(playbackStatus);
    value.socket.emit(playbackPacket());
    value.clock.play();
    await settle();
    value.socket.state({ ...playbackStatus, status: 'playing', epoch: 2 });
    value.socket.emit(playbackPacket({ epoch: 2 }));
    value.node.emit({ type: 'progress', epoch: 2, sample: 1600, buffering: false });
    value.clock.pause();
    expect(sent(value.socket).at(-1)).toEqual({ type: 'pause', frame: 1 });
    expect(value.node.port.postMessage.mock.calls.at(-1)![0].type).toBe('flush');
    value.socket.state({ ...playbackStatus, epoch: 3, frame: 1 });
    value.socket.emit(playbackPacket({ epoch: 3, position: 1 }));
    value.clock.seek(2);
    expect(sent(value.socket).at(-1)).toEqual({ type: 'seek', frame: 2 });
    const pcmCount = value.node.port.postMessage.mock.calls.filter(
      ([message]) => message.type === 'pcm',
    ).length;
    value.socket.emit(playbackPacket({ epoch: 3, sequence: 2, playing: true }));
    expect(
      value.node.port.postMessage.mock.calls.filter(([message]) => message.type === 'pcm'),
    ).toHaveLength(pcmCount);
    value.socket.state({ ...playbackStatus, epoch: 4, frame: 2 });
    value.socket.emit(playbackPacket({ epoch: 4, position: 2, end: true }));
    expect(value.clock.getFrame()).toBe(2);
    value.clock.play();
    await settle();
    expect(sent(value.socket).at(-1)).toEqual({ type: 'play', frame: 2 });
    value.socket.state({ ...playbackStatus, status: 'playing', epoch: 5, frame: 2 });
    value.socket.emit(playbackPacket({ epoch: 5, position: 2, end: true }));
    value.socket.emit(
      playbackPacket({
        epoch: 5,
        sequence: 2,
        position: 2,
        end: true,
        playing: true,
        audioStartSample: 3200,
      }),
    );
    value.node.emit({
      type: 'consumed',
      epoch: 5,
      sequence: 2,
      position: 2,
      sample: 4800,
      end: true,
    });
    expect(value.clock.getFrame()).toBe(3);
    expect(value.clock.isPlaying()).toBe(false);
    expect(sent(value.socket).at(-1)).not.toEqual({
      type: 'ack',
      epoch: 5,
      sequence: 2,
      position: 2,
    });
    flushRAF();
    expect(sent(value.socket).at(-1)).toEqual({ type: 'ack', epoch: 5, sequence: 2, position: 2 });
    value.socket.state({ ...playbackStatus, epoch: 5, frame: 3 });
    expect(value.clock.getFrame()).toBe(3);
  });
  it('end游标快照只画最后一帧，不能把end游标倒退；快速控制不采纳中间epoch', async () => {
    const value = fixture();
    await value.controller.open();
    value.socket.state(playbackStatus);
    value.socket.emit(playbackPacket());
    value.clock.seek(1);
    value.clock.seek(3);
    value.socket.state({ ...playbackStatus, epoch: 2, frame: 1 });
    value.socket.emit(playbackPacket({ epoch: 2, position: 1 }));
    expect(value.clock.getFrame()).toBe(3);
    value.socket.state({ ...playbackStatus, epoch: 3, frame: 3 });
    value.socket.emit(playbackPacket({ epoch: 3, position: 2, end: true }));
    expect(value.clock.getFrame()).toBe(3);
  });
  it('采样率不符不Start、不变调，失败driver继续阻止假RAF直到退出实验模式', async () => {
    const value = fixture(44100);
    await value.controller.open();
    expect(value.api.startPlayback).not.toHaveBeenCalled();
    expect(value.context.close).toHaveBeenCalledOnce();
    expect(value.state).toHaveBeenLastCalledWith(
      expect.objectContaining({ phase: 'failed', reason: expect.stringContaining('48kHz') }),
    );
    value.clock.play();
    expect(callbacks.size).toBe(0);
    expect(value.clock.hasExternalDriver()).toBe(true);
    value.controller.dispose();
    expect(value.clock.hasExternalDriver()).toBe(false);
  });
  it('关闭/工程失效释放socket与设备，迟到旧socket不能更新新时钟', async () => {
    const value = fixture();
    await value.controller.open();
    value.socket.state(playbackStatus);
    value.socket.emit(playbackPacket());
    value.controller.invalidate();
    expect(value.api.stopPlayback).toHaveBeenCalledWith(playbackStatus.id);
    expect(value.context.close).toHaveBeenCalledOnce();
    expect(value.node.disconnect).toHaveBeenCalledOnce();
    const draws = value.draw.mock.calls.length;
    value.socket.emit(playbackPacket({ sequence: 99, position: 2, playing: true, end: true }));
    expect(value.draw).toHaveBeenCalledTimes(draws);
    value.clock.play();
    expect(callbacks.size).toBe(0);
    value.controller.dispose();
  });
  it('工程更新停止旧会话后仍接纳新的定位，且不启动假播放或发送旧控制', async () => {
    const value = fixture();
    await value.controller.open();
    value.socket.state(playbackStatus);
    value.socket.emit(playbackPacket());
    value.clock.seek(3);
    value.clock.invalidateExternal();
    const controls = value.socket.send.mock.calls.length;
    value.clock.pause();
    value.clock.seek(1);
    expect(value.clock.getFrame()).toBe(1);
    expect(value.clock.hasExternalDriver()).toBe(true);
    value.clock.play();
    expect(value.clock.isPlaying()).toBe(false);
    expect(callbacks.size).toBe(0);
    value.socket.state({ ...playbackStatus, epoch: 99, frame: 2 });
    expect(value.clock.getFrame()).toBe(1);
    expect(value.socket.send).toHaveBeenCalledTimes(controls);
    value.controller.dispose();
    expect(value.clock.getFrame()).toBe(1);
  });
  it('Start迟到于卸载也显式Stop，不打开旧会话URI', async () => {
    const value = fixture();
    let resolve!: (start: PlaybackStart) => void;
    value.api.startPlayback.mockImplementation(
      () =>
        new Promise((done) => {
          resolve = done;
        }),
    );
    const opening = value.controller.open();
    await settle();
    value.controller.dispose();
    resolve({ status: playbackStatus, streamUrl: 'ws://127.0.0.1:1/late?token=never-open' });
    await opening;
    expect(value.api.stopPlayback).toHaveBeenCalledWith(playbackStatus.id);
    expect(value.runtime.socket).not.toHaveBeenCalled();
    expect(value.clock.hasExternalDriver()).toBe(false);
  });
});

it('代理配置使用当前实例且不改变工程，状态呈现实际来源数量', async () => {
  const value = fixture(48000, false, 3, 30, 'quarter', '540p');
  const before = JSON.stringify(playbackProject);
  value.api.startPlayback.mockResolvedValueOnce({
    status: { ...value.status, proxyProfile: '540p', proxyAssets: 1, originalVideoAssets: 2 },
    streamUrl: 'ws://127.0.0.1:43111/playback/test?token=fixture-only-secret',
  });
  await value.controller.open();
  expect(value.api.startPlayback).toHaveBeenCalledWith(
    expect.objectContaining({ sourceEpoch: 7, proxyProfile: '540p', renderQuality: 'quarter' }),
  );
  value.socket.state({
    ...value.status,
    proxyProfile: '540p',
    proxyAssets: 1,
    originalVideoAssets: 2,
  });
  value.socket.emit(playbackPacket({ samples: 0, pictureOnly: true }));
  expect(value.state).toHaveBeenCalledWith(
    expect.objectContaining({ proxySummary: '画面代理1 · 原片2；声音/导出原片' }),
  );
  expect(JSON.stringify(playbackProject)).toBe(before);
});
