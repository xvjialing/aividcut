// 仅消费真实PCM；预缓冲/欠载等待输出0，但不推进内容样本位置。
const PREBUFFER_SAMPLES = 2400;
const MAX_BLOCKS = 3;
class AIVidCutPreviewAudio extends AudioWorkletProcessor {
  constructor() {
    super();
    this.epoch = 0;
    this.running = false;
    this.active = false;
    this.phase = 'idle';
    this.blocks = [];
    this.offset = 0;
    this.sample = null;
    this.position = null;
    this.consumed = 0;
    this.underrun = 0;
    this.underrunEvents = 0;
    this.startupWaitFrames = 0;
    this.lastSequence = null;
    this.nextSample = null;
    this.nextPosition = null;
    this.receivedEnd = false;
    this.reportFrames = 0;
    this.port.onmessage = (event) => this.receive(event.data);
  }
  receive(message) {
    if (message.type === 'reset' || message.type === 'flush') {
      this.epoch = message.epoch;
      this.active = message.type === 'reset' && message.playing === true;
      this.running = false;
      this.phase = this.active ? 'startup' : 'idle';
      this.blocks = [];
      this.offset = 0;
      this.sample = null;
      this.position = null;
      this.consumed = 0;
      this.underrun = 0;
      this.underrunEvents = 0;
      this.startupWaitFrames = 0;
      this.lastSequence = null;
      this.nextSample = null;
      this.nextPosition = null;
      this.receivedEnd = false;
      this.reportFrames = 0;
      return;
    }
    if (message.type !== 'pcm' || message.epoch !== this.epoch || !this.active) return;
    const frames = message.samples;
    if (
      !Number.isSafeInteger(frames) ||
      frames < 1 ||
      frames > 131072 ||
      !(message.buffer instanceof ArrayBuffer) ||
      message.buffer.byteLength !== frames * 8 ||
      this.blocks.length >= MAX_BLOCKS ||
      !Number.isSafeInteger(message.sequence) ||
      message.sequence < 1 ||
      (this.lastSequence !== null && message.sequence !== this.lastSequence + 1) ||
      !Number.isSafeInteger(message.position) ||
      message.position < 0 ||
      (this.nextPosition !== null && message.position !== this.nextPosition) ||
      !Number.isSafeInteger(message.audioStartSample) ||
      message.audioStartSample < 0 ||
      !Number.isSafeInteger(message.audioStartSample + frames) ||
      (this.nextSample !== null && message.audioStartSample !== this.nextSample) ||
      typeof message.end !== 'boolean' ||
      this.receivedEnd
    ) {
      this.active = false;
      this.running = false;
      this.phase = 'failed';
      this.blocks = [];
      this.port.postMessage({
        type: 'error',
        epoch: this.epoch,
        reason: '预览声音数据的大小或顺序不符合要求，已停止。',
      });
      return;
    }
    this.blocks.push({ ...message, data: new Float32Array(message.buffer) });
    this.lastSequence = message.sequence;
    this.nextSample = message.audioStartSample + frames;
    this.nextPosition = message.position + 1;
    this.receivedEnd = message.end;
  }
  bufferedSamples() {
    let frames = -this.offset;
    for (const block of this.blocks) frames += block.samples;
    return frames;
  }
  canStart() {
    return (
      this.blocks.length > 0 &&
      (this.bufferedSamples() >= PREBUFFER_SAMPLES ||
        this.blocks.length === MAX_BLOCKS ||
        this.receivedEnd)
    );
  }
  process(_inputs, outputs) {
    const channels = outputs[0];
    if (!channels?.length) return true;
    for (const channel of channels) channel.fill(0);
    if (!this.active) return true;
    if (channels.length !== 2) {
      this.running = false;
      this.active = false;
      this.phase = 'failed';
      this.port.postMessage({
        type: 'error',
        epoch: this.epoch,
        reason: '浏览器没有提供双声道预览输出。',
      });
      return true;
    }
    let written = 0;
    const length = channels[0].length;
    const phaseBefore = this.phase;
    let completed = false;
    if (!this.running && this.canStart()) {
      this.running = true;
      this.phase = 'playing';
    }
    if (!this.running) {
      if (this.phase === 'startup') this.startupWaitFrames += length;
      else this.underrun += length;
    }
    while (written < length && this.running) {
      const block = this.blocks[0];
      if (!block) {
        this.underrun += length - written;
        this.underrunEvents++;
        this.running = false;
        this.phase = 'rebuffering';
        break;
      }
      const count = Math.min(length - written, block.samples - this.offset);
      for (let index = 0; index < count; index++) {
        channels[0][written + index] = block.data[(this.offset + index) * 2];
        channels[1][written + index] = block.data[(this.offset + index) * 2 + 1];
      }
      this.offset += count;
      written += count;
      this.consumed += count;
      this.sample = block.audioStartSample + this.offset;
      this.position = block.position;
      if (this.offset === block.samples) {
        this.blocks.shift();
        this.offset = 0;
        this.port.postMessage({
          type: 'consumed',
          epoch: this.epoch,
          sequence: block.sequence,
          position: block.position,
          sample: this.sample,
          end: block.end,
        });
        completed = true;
        if (block.end) {
          this.running = false;
          this.active = false;
          this.phase = 'ended';
        }
      }
    }
    this.reportFrames += length;
    if (completed || this.phase !== phaseBefore || this.reportFrames >= 512) {
      this.reportFrames = 0;
      this.port.postMessage({
        type: 'progress',
        epoch: this.epoch,
        sample: this.sample,
        position: this.position,
        consumed: this.consumed,
        underrun: this.underrun,
        underrunEvents: this.underrunEvents,
        startupWaitFrames: this.startupWaitFrames,
        bufferedSamples: this.bufferedSamples(),
        prebufferTargetSamples: PREBUFFER_SAMPLES,
        bufferPhase: this.phase,
        buffering: this.phase === 'startup' || this.phase === 'rebuffering',
        running: this.running,
      });
    }
    return true;
  }
}
registerProcessor('aividcut-preview-audio', AIVidCutPreviewAudio);
