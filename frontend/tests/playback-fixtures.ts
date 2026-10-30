import { vi } from 'vitest';
import type {
  PlaybackAPI,
  PlaybackCapabilities,
  PlaybackFrameHeader,
  PlaybackStatus,
} from '../src/playback-types';
import { originalAsset, originalClip, replacementProject } from './execution-replacement-fixtures';
export const playbackProject = {
  ...replacementProject,
  clips: [{ ...originalClip, startFrame: 0, durationFrames: 3 }],
  assets: [originalAsset],
};
export const playbackStatus: PlaybackStatus = {
  id: 'playback-test',
  projectId: playbackProject.id,
  projectRevision: playbackProject.revision,
  status: 'paused',
  frame: 0,
  durationFrames: 3,
  width: 2,
  height: 1,
  fpsNum: 30,
  fpsDen: 1,
  sampleRate: 48000,
  channels: 2,
  epoch: 1,
  reason: '',
  connected: true,
  inFlight: 0,
};
const supported = { supported: true, reason: '' };
export const playbackCapabilities: PlaybackCapabilities = {
  available: true,
  reason: '',
  protocol: 'AIV1',
  mltVersion: 'fixture',
  display: 'webview_binary_canvas_audio',
  video: supported,
  image: supported,
  audio: supported,
  text: supported,
  transform: supported,
  speed: supported,
  lut: supported,
  producers: [],
  filters: [],
  transitions: [],
  projectSupported: true,
  projectReason: '',
  maxWidth: 1280,
  maxHeight: 720,
};
export function playbackPacket(override: Partial<PlaybackFrameHeader> = {}, values?: number[]) {
  const header: PlaybackFrameHeader = {
    type: 'frame',
    epoch: 1,
    sequence: 1,
    position: 0,
    width: 2,
    height: 1,
    fpsNum: 30,
    fpsDen: 1,
    sampleRate: 48000,
    channels: 2,
    samples: 1600,
    audioStartSample: 0,
    end: false,
    playing: false,
    ...override,
  };
  const metadata = new TextEncoder().encode(JSON.stringify(header));
  const imageLength = header.width * header.height * 4;
  const buffer = new ArrayBuffer(16 + metadata.length + imageLength + header.samples * 8);
  const view = new DataView(buffer);
  view.setUint32(0, 0x41495631, false);
  view.setUint32(4, metadata.length, false);
  view.setUint32(8, imageLength, false);
  view.setUint32(12, header.samples * 8, false);
  new Uint8Array(buffer, 16, metadata.length).set(metadata);
  new Uint8Array(buffer, 16 + metadata.length, imageLength).fill(header.position + 1);
  for (let index = 0; index < header.samples * 2; index++)
    view.setFloat32(16 + metadata.length + imageLength + index * 4, values?.[index] ?? 0, true);
  return buffer;
}
export function fakePlaybackAPI() {
  return {
    getPlaybackCapabilities: vi
      .fn<PlaybackAPI['getPlaybackCapabilities']>()
      .mockResolvedValue(playbackCapabilities),
    startPlayback: vi.fn<PlaybackAPI['startPlayback']>().mockResolvedValue({
      status: playbackStatus,
      streamUrl: 'ws://127.0.0.1:43111/playback/test?token=fixture-only-secret',
    }),
    controlPlayback: vi.fn<PlaybackAPI['controlPlayback']>().mockResolvedValue(playbackStatus),
    getPlaybackStatus: vi.fn<PlaybackAPI['getPlaybackStatus']>().mockResolvedValue(playbackStatus),
    stopPlayback: vi
      .fn<PlaybackAPI['stopPlayback']>()
      .mockResolvedValue({ ...playbackStatus, status: 'closed' }),
  };
}
