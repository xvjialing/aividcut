import { vi } from 'vitest';
import processorSource from '../src/playback/audio-processor.js?raw';

export interface PreviewAudioProcessor {
  receive: (message: unknown) => void;
  process: (inputs: unknown[], outputs: Float32Array[][]) => boolean;
  port: { onmessage: unknown; postMessage: ReturnType<typeof vi.fn> };
}
export function audioProcessor() {
  let Constructor!: new () => PreviewAudioProcessor;
  class Base {
    port = { onmessage: null, postMessage: vi.fn() };
  }
  // 执行本仓库实际静态模块；只替换设备全局，不创建真实AudioContext。
  new Function('AudioWorkletProcessor', 'registerProcessor', processorSource)(
    Base,
    (_name: string, value: new () => PreviewAudioProcessor) => {
      Constructor = value;
    },
  );
  return new Constructor();
}
export function pcmBlock(options: {
  samples: number;
  sequence?: number;
  position?: number;
  audioStartSample?: number;
  epoch?: number;
  end?: boolean;
  value?: number;
}) {
  return {
    type: 'pcm',
    epoch: options.epoch ?? 1,
    sequence: options.sequence ?? 1,
    position: options.position ?? 0,
    audioStartSample: options.audioStartSample ?? 0,
    end: options.end ?? false,
    samples: options.samples,
    buffer: new Float32Array(options.samples * 2).fill(options.value ?? 0.25).buffer,
  };
}
export function renderQuantum(processor: PreviewAudioProcessor, length = 128) {
  const channels = [new Float32Array(length), new Float32Array(length)];
  processor.process([], [channels]);
  return channels;
}
export function audioMessages(processor: PreviewAudioProcessor, type: string) {
  return processor.port.postMessage.mock.calls
    .map(([message]) => message)
    .filter((message) => message.type === type);
}
export function audioProgress(processor: PreviewAudioProcessor) {
  return audioMessages(processor, 'progress').at(-1);
}
