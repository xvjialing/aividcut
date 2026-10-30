import processorURL from '../playback/audio-processor.js?url&no-inline';
import processorSource from '../playback/audio-processor.js?raw';
import type { PlaybackAPI, PlaybackFrame, PlaybackStatus, RenderQuality } from '../playback-types';
import type { Project } from '../types';
import { PlaybackClock, projectEnd, type ExternalPlaybackDriver } from './editor';
import {
  decodePlaybackFrame,
  decodePlaybackState,
  playbackStreamAllowed,
} from './playback-protocol';
import { PlaybackBundleBuffer } from './playback-buffer';

export interface RealtimeViewState {
  proxySummary?: string;
  phase: 'preparing' | 'ready' | 'playing' | 'buffering' | 'paused' | 'ended' | 'failed' | 'closed';
  reason: string;
  width?: number;
  height?: number;
}
export interface PlaybackRuntime {
  audioContext: () => AudioContext;
  audioNode: (context: AudioContext) => AudioWorkletNode;
  socket: (url: string) => WebSocket;
  image: (frame: PlaybackFrame) => ImageData;
}
const browserRuntime: PlaybackRuntime = {
  audioContext: () => {
    if (typeof AudioContext !== 'function' || typeof AudioWorkletNode !== 'function')
      throw new Error('此WebView没有AudioWorklet，不能启用音频主时钟。');
    return new AudioContext({ sampleRate: 48000, latencyHint: 'interactive' });
  },
  audioNode: (context) =>
    new AudioWorkletNode(context, 'aividcut-preview-audio', {
      numberOfInputs: 0,
      numberOfOutputs: 1,
      outputChannelCount: [2],
      channelCount: 2,
      channelCountMode: 'explicit',
    }),
  socket: (url) => new WebSocket(url),
  image: (frame) => new ImageData(frame.rgba, frame.header.width, frame.header.height),
};

// 独立播放器持有二进制与设备资源；React仅接收低频播放状态，不持有逐帧图像。
export class RealtimePlayback implements ExternalPlaybackDriver {
  private context: AudioContext | null = null;
  private node: AudioWorkletNode | null = null;
  private socket: WebSocket | null = null;
  private status: PlaybackStatus | null = null;
  private epoch = 0;
  private id = '';
  private buffer = new PlaybackBundleBuffer();
  private awaitingState = true;
  private snapshotReady = false;
  private intentPlaying = false;
  private intentVersion = 0;
  private expectedEpoch = 0;
  private outsideCursor: number | null = null;
  private mediaSample: number | null = null;
  private ended = false;
  private raf = 0;
  private lastPaint = '';
  private lastView = '';
  private disposed = false;
  private failed = false;
  private detach: () => void;
  private timeout: number | null = null;
  constructor(
    private options: {
      project: Project;
      clock: PlaybackClock;
      canvas: HTMLCanvasElement;
      api: PlaybackAPI;
      onState: (value: RealtimeViewState) => void;
      runtime?: PlaybackRuntime;
      renderQuality?: RenderQuality;
      proxyProfile?: import('../proxy-types').ProxyProfile;
      sourceEpoch?: number;
    },
  ) {
    this.detach = options.clock.attachExternal(this);
    document.addEventListener('visibilitychange', this.visibility);
  }
  private get runtime() {
    return this.options.runtime || browserRuntime;
  }
  private emit(phase: RealtimeViewState['phase'], reason = '') {
    const key = `${phase}:${reason}`;
    if (key === this.lastView || this.disposed) return;
    this.lastView = key;
    this.options.onState({
      phase,
      reason,
      proxySummary: this.status?.proxyProfile
        ? `画面代理${this.status.proxyAssets || 0} · 原片${this.status.originalVideoAssets || 0}；声音/导出原片`
        : undefined,
      width: this.status?.width,
      height: this.status?.height,
    });
  }
  async open() {
    this.emit('preparing', '正在准备本机合成预览');
    try {
      const context = this.runtime.audioContext();
      this.context = context;
      if (context.sampleRate !== 48000)
        throw new Error('音频设备未提供48kHz，实验预览已停用，不以错误采样率播放。');
      if (!context.audioWorklet?.addModule)
        throw new Error('此WebView不支持AudioWorklet，不能启用音频主时钟。');
      try {
        await context.audioWorklet.addModule(processorURL);
      } catch {
        if (this.disposed || this.failed) return;
        const url = URL.createObjectURL(
          new Blob([processorSource], { type: 'application/javascript' }),
        );
        try {
          await context.audioWorklet.addModule(url);
        } catch {
          throw new Error('WebView无法加载音频处理模块，实验预览不可用。');
        } finally {
          URL.revokeObjectURL(url);
        }
      }
      if (this.disposed || this.failed) return;
      this.node = this.runtime.audioNode(context);
      this.node.connect(context.destination);
      this.node.port.onmessage = this.audioMessage;
      this.node.onprocessorerror = () => this.fail('音频处理模块已中止，实验预览停止。');
      context.onstatechange = () => {
        if (context.state !== 'running' && this.intentPlaying) {
          this.pause();
          this.emit('paused', '音频设备已暂停，请点击播放恢复。');
        }
      };
      const start = await this.options.api.startPlayback({
        ...(this.options.renderQuality ? { renderQuality: this.options.renderQuality } : {}),
        ...(this.options.proxyProfile
          ? { proxyProfile: this.options.proxyProfile, sourceEpoch: this.options.sourceEpoch }
          : {}),
        projectId: this.options.project.id,
        projectRevision: this.options.project.revision,
        frame: Math.min(this.options.clock.getFrame(), projectEnd(this.options.project)),
      });
      if (this.disposed || this.failed) {
        void this.options.api.stopPlayback(start.status.id).catch(() => {});
        return;
      }
      this.id = start.status.id;
      this.status = start.status;
      this.validateStatus(start.status);
      if (!playbackStreamAllowed(start.streamUrl))
        throw new Error('实时预览未返回受控的本机连接。');
      if (this.options.clock.getFrame() > start.status.durationFrames)
        this.outsideCursor = this.options.clock.getFrame();
      const socket = this.runtime.socket(start.streamUrl);
      this.socket = socket;
      socket.binaryType = 'arraybuffer';
      socket.onmessage = (event) => {
        if (this.socket !== socket || this.disposed || this.failed) return;
        try {
          if (typeof event.data === 'string') this.receiveState(decodePlaybackState(event.data));
          else if (event.data instanceof ArrayBuffer)
            this.receiveFrame(decodePlaybackFrame(event.data));
          else throw new Error('本机预览返回了非二进制媒体。');
        } catch (cause) {
          this.fail(cause instanceof Error ? cause.message : '本机预览数据无效。');
        }
      };
      socket.onerror = () => {
        if (this.socket === socket) this.fail('本机实时预览连接失败，请重新准备或返回工作区预览。');
      };
      socket.onclose = () => {
        if (this.socket === socket && !this.disposed && !this.failed)
          this.fail('实时预览连接已关闭，请重新准备。');
      };
      this.timeout = window.setTimeout(() => {
        if (!this.snapshotReady) this.fail('未能取得实时合成画面，请核对引擎状态后重试。');
      }, 15000);
    } catch (cause) {
      if (!this.disposed) this.fail(cause instanceof Error ? cause.message : '实验预览准备失败。');
    }
  }
  private validateStatus(status: PlaybackStatus) {
    const project = this.options.project;
    if (
      !status.id ||
      status.projectId !== project.id ||
      status.projectRevision !== project.revision ||
      !Number.isSafeInteger(status.durationFrames) ||
      status.durationFrames < 1 ||
      !Number.isSafeInteger(status.width) ||
      !Number.isSafeInteger(status.height) ||
      status.width < 1 ||
      status.height < 1 ||
      status.width > 1920 ||
      status.height > 1080 ||
      status.sampleRate !== 48000 ||
      status.channels !== 2 ||
      status.fpsNum < 1 ||
      status.fpsDen < 1 ||
      status.fpsNum * project.settings.fpsDen !== project.settings.fpsNum * status.fpsDen
    )
      throw new Error('实时预览的工程、尺寸或音频时钟不匹配。');
  }
  private receiveState(status: PlaybackStatus) {
    if (status.id !== this.id || status.epoch < this.epoch) return;
    if (['stale', 'failed', 'closed'].includes(status.status)) {
      this.fail(status.reason || '当前工程的实时会话已失效。');
      return;
    }
    if (status.epoch < this.expectedEpoch) return;
    this.validateStatus(status);
    const fresh = status.epoch > this.epoch;
    if (this.awaitingState && !fresh) return;
    this.status = status;
    if (fresh) {
      this.epoch = status.epoch;
      this.buffer.reset(this.epoch);
      this.mediaSample = null;
      this.ended = false;
      this.lastPaint = '';
      this.awaitingState = false;
      this.intentPlaying = status.status === 'playing';
      this.node?.port.postMessage({
        type: 'reset',
        epoch: this.epoch,
        playing: this.intentPlaying,
      });
    }
    if (status.status === 'paused') {
      this.intentPlaying = false;
      this.options.clock.synchronizeExternal(this.outsideCursor ?? status.frame, false);
      if (!this.ended)
        this.emit(
          this.snapshotReady ? 'paused' : 'preparing',
          this.outsideCursor !== null ? '当前位置在片段范围之外，添加素材后可重新准备。' : '',
        );
    } else if (status.status === 'playing' && fresh) {
      this.options.clock.synchronizeExternal(status.frame, true);
      this.emit('buffering', '正在缓冲声音与画面');
    }
  }
  private receiveFrame(frame: PlaybackFrame) {
    const value = frame.header;
    const status = this.status;
    if (value.epoch < this.epoch || this.awaitingState) return;
    if (
      value.epoch !== this.epoch ||
      !status ||
      value.width !== status.width ||
      value.height !== status.height ||
      value.fpsNum !== status.fpsNum ||
      value.fpsDen !== status.fpsDen ||
      value.position >= status.durationFrames
    )
      throw new Error('实时帧的epoch或工程参数不匹配。');
    if (!this.buffer.push(frame)) return;
    if (!value.playing) {
      if (this.outsideCursor === null) this.paint(frame);
      const wasReady = this.snapshotReady;
      this.buffer.release(value.sequence);
      this.acknowledge();
      this.snapshotReady = true;
      if (this.timeout !== null) window.clearTimeout(this.timeout);
      this.timeout = null;
      if (!this.intentPlaying)
        this.emit(
          this.outsideCursor === null && !wasReady ? 'ready' : 'paused',
          this.outsideCursor === null && !wasReady
            ? '已准备，点击播放启用声音'
            : this.outsideCursor !== null
              ? '当前位置没有片段'
              : '',
        );
      return;
    }
    if (!this.intentPlaying) throw new Error('暂停会话收到播放音频，已停止以免混音。');
    this.node?.port.postMessage({ ...value, type: 'pcm', buffer: frame.pcm.buffer }, [
      frame.pcm.buffer,
    ]);
  }
  private audioMessage = (event: MessageEvent) => {
    const data = event.data;
    if (this.disposed || this.failed || this.awaitingState || !data || data.epoch !== this.epoch)
      return;
    if (data.type === 'error') {
      this.fail(typeof data.reason === 'string' ? data.reason : '预览音频处理失败。');
      return;
    }
    if (typeof data.sample === 'number' && Number.isSafeInteger(data.sample) && data.sample >= 0)
      this.mediaSample = data.sample;
    if (data.type === 'consumed') {
      this.buffer.consumed(data.sequence);
      if (data.end) {
        this.ended = true;
        this.intentPlaying = false;
        this.options.clock.synchronizeExternal(this.status!.durationFrames, false);
        // 声音消费是结尾证据；隐藏/遮挡窗口的rAF可能暂停，状态不能依赖下一次绘制。
        this.emit('ended', '已播放到结尾');
      }
      if (this.mediaSample !== null) this.buffer.candidate(this.mediaFrame());
      this.acknowledge();
      this.schedule();
    } else if (data.type === 'progress') {
      if (this.mediaSample !== null && !this.ended)
        this.options.clock.synchronizeExternal(this.mediaFrame(), this.intentPlaying);
      if (this.intentPlaying)
        this.emit(
          data.buffering ? 'buffering' : 'playing',
          data.buffering
            ? data.bufferPhase === 'startup'
              ? '正在预缓冲声音与画面，播放位置保持'
              : '声音缓冲不足，正在补充；播放位置已保持'
            : '',
        );
      this.schedule();
    }
  };
  private mediaFrame() {
    const status = this.status!;
    return this.ended
      ? status.durationFrames
      : (this.mediaSample! * status.fpsNum) / (48000 * status.fpsDen);
  }
  private schedule() {
    if (!this.raf)
      this.raf = requestAnimationFrame(() => {
        this.raf = 0;
        if (this.disposed || this.failed || this.awaitingState || this.mediaSample === null) return;
        try {
          const frame = this.buffer.candidate(this.mediaFrame());
          if (frame) {
            this.paint(frame);
            this.buffer.release(frame.header.sequence);
          }
          this.acknowledge();
          if (this.ended) this.emit('ended', '已播放到结尾');
        } catch {
          this.fail('实时画面无法呈现，预览已停止。');
        }
      });
  }
  private paint(frame: PlaybackFrame) {
    const key = `${frame.header.epoch}:${frame.header.sequence}`;
    if (key === this.lastPaint) return;
    const canvas = this.options.canvas;
    if (canvas.width !== frame.header.width) canvas.width = frame.header.width;
    if (canvas.height !== frame.header.height) canvas.height = frame.header.height;
    const context = canvas.getContext('2d');
    if (!context) throw new Error('此WebView没有可用的Canvas画面输出。');
    context.putImageData(this.runtime.image(frame), 0, 0);
    this.lastPaint = key;
  }
  private acknowledge() {
    for (const frame of this.buffer.acknowledge())
      this.send({
        type: 'ack',
        epoch: frame.header.epoch,
        sequence: frame.header.sequence,
        position: frame.header.position,
      });
  }
  private send(value: unknown) {
    if (this.socket?.readyState === 1) {
      try {
        this.socket.send(JSON.stringify(value));
      } catch {
        this.fail('本机预览控制未能送达，请重新准备。');
      }
    }
  }
  private control(value: { type: 'play' | 'pause' | 'seek'; frame: number }) {
    if (this.socket?.readyState !== 1) return;
    this.expectedEpoch = Math.max(this.expectedEpoch, this.epoch) + 1;
    this.send(value);
  }
  private flush(clear = false) {
    this.awaitingState = true;
    this.mediaSample = null;
    this.ended = false;
    this.buffer.reset(this.epoch);
    this.node?.port.postMessage({ type: 'flush', epoch: this.epoch });
    cancelAnimationFrame(this.raf);
    this.raf = 0;
    if (clear) {
      this.options.canvas
        .getContext('2d')
        ?.clearRect(0, 0, this.options.canvas.width, this.options.canvas.height);
      this.lastPaint = '';
    }
  }
  play = () => {
    if (this.disposed || this.failed) return;
    if (!this.snapshotReady || !this.context || this.socket?.readyState !== 1) {
      this.emit('preparing', '预览尚在准备，请取得首帧后点击播放。');
      return;
    }
    const version = ++this.intentVersion;
    const status = this.status!;
    const frame =
      this.options.clock.getFrame() >= status.durationFrames ? 0 : this.options.clock.getFrame();
    this.outsideCursor = null;
    this.intentPlaying = true;
    this.options.clock.synchronizeExternal(frame, true);
    this.emit('buffering', '正在启用音频播放');
    // resume必须在用户播放手势内调用，不能排在网络await后。
    void this.context
      .resume()
      .then(() => {
        if (version !== this.intentVersion || this.disposed || this.failed) return;
        this.flush();
        this.control({ type: 'play', frame });
      })
      .catch(() => {
        if (version !== this.intentVersion) return;
        this.intentPlaying = false;
        this.options.clock.synchronizeExternal(frame, false);
        this.emit('paused', '浏览器未允许声音播放，请再次点击播放或返回工作区预览。');
      });
  };
  pause = () => {
    if (this.disposed || this.failed) return;
    this.intentVersion++;
    this.intentPlaying = false;
    const frame = this.options.clock.getFrame();
    this.flush();
    this.options.clock.synchronizeExternal(frame, false);
    this.control({ type: 'pause', frame: Math.min(frame, this.status?.durationFrames ?? frame) });
    this.emit('paused');
  };
  seek = (frame: number) => {
    if (this.disposed) return;
    if (this.failed) {
      // 工程更新到新会话挂载之间仍保留最新定位；失败驱动继续阻止假播放。
      this.options.clock.synchronizeExternal(frame, false);
      return;
    }
    const end = this.status?.durationFrames ?? projectEnd(this.options.project);
    const wasPlaying = this.intentPlaying;
    this.outsideCursor = frame > end ? frame : null;
    this.intentVersion++;
    this.flush(true);
    this.intentPlaying = frame >= end ? false : wasPlaying;
    this.options.clock.synchronizeExternal(frame, this.intentPlaying);
    this.control({ type: frame >= end ? 'pause' : 'seek', frame: Math.min(frame, end) });
    this.emit('buffering', '正在定位合成画面');
  };
  private visibility = () => {
    if (document.hidden && this.intentPlaying) {
      this.pause();
      this.emit('paused', '窗口已隐藏，实时预览暂停。');
    }
  };
  invalidate = () => {
    this.fail('工程已变化，旧实时会话已停止，请重新准备。');
  };
  block(reason: string) {
    this.fail(reason);
  }
  private fail(reason: string) {
    if (this.disposed || this.failed) return;
    this.failed = true;
    this.intentPlaying = false;
    this.options.clock.synchronizeExternal(this.options.clock.getFrame(), false);
    this.release();
    this.emit('failed', reason);
  }
  private release() {
    this.intentVersion++;
    cancelAnimationFrame(this.raf);
    this.raf = 0;
    if (this.timeout !== null) window.clearTimeout(this.timeout);
    this.timeout = null;
    const socket = this.socket;
    this.socket = null;
    socket?.close();
    this.buffer.reset(0);
    if (this.node) {
      this.node.port.onmessage = null;
      this.node.disconnect();
      this.node = null;
    }
    if (this.context) {
      this.context.onstatechange = null;
      void this.context.close().catch(() => {});
      this.context = null;
    }
    if (this.id) {
      const id = this.id;
      this.id = '';
      void this.options.api.stopPlayback(id).catch(() => {});
    }
  }
  dispose() {
    if (this.disposed) return;
    this.disposed = true;
    this.release();
    document.removeEventListener('visibilitychange', this.visibility);
    this.detach();
  }
}
