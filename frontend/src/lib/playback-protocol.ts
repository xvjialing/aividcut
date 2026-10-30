import type { PlaybackFrame, PlaybackFrameHeader, PlaybackStatus } from '../playback-types';
const MAX_HEADER = 128 * 1024;
const MAX_IMAGE = 1920 * 1080 * 4;
const MAX_AUDIO = 1024 * 1024;
const unsigned = (value: unknown, minimum = 0): value is number =>
  typeof value === 'number' && Number.isSafeInteger(value) && value >= minimum;
function failure(): never {
  throw new Error('实时预览数据格式无效，已停止接收。');
}
export function decodePlaybackFrame(buffer: ArrayBuffer): PlaybackFrame {
  if (buffer.byteLength < 16 || buffer.byteLength > 16 + MAX_HEADER + MAX_IMAGE + MAX_AUDIO)
    return failure();
  const prefix = new DataView(buffer);
  if (prefix.getUint32(0, false) !== 0x41495631) return failure();
  const headerBytes = prefix.getUint32(4, false);
  const imageBytes = prefix.getUint32(8, false);
  const audioBytes = prefix.getUint32(12, false);
  if (
    !headerBytes ||
    headerBytes > MAX_HEADER ||
    imageBytes > MAX_IMAGE ||
    audioBytes > MAX_AUDIO ||
    16 + headerBytes + imageBytes + audioBytes !== buffer.byteLength
  )
    return failure();
  let value: unknown;
  try {
    value = JSON.parse(
      new TextDecoder('utf-8', { fatal: true }).decode(new Uint8Array(buffer, 16, headerBytes)),
    );
  } catch {
    return failure();
  }
  if (!value || typeof value !== 'object' || Array.isArray(value)) return failure();
  const header = value as PlaybackFrameHeader;
  if (
    header.type !== 'frame' ||
    !unsigned(header.epoch, 1) ||
    !unsigned(header.sequence) ||
    !unsigned(header.position) ||
    !unsigned(header.width, 1) ||
    !unsigned(header.height, 1) ||
    header.width > 1920 ||
    header.height > 1080 ||
    header.width * header.height * 4 !== imageBytes ||
    !unsigned(header.fpsNum, 1) ||
    !unsigned(header.fpsDen, 1) ||
    header.sampleRate !== 48000 ||
    header.channels !== 2 ||
    !unsigned(header.samples) ||
    header.samples * 8 !== audioBytes ||
    !unsigned(header.audioStartSample) ||
    typeof header.end !== 'boolean' ||
    typeof header.playing !== 'boolean' ||
    (header.playing && header.samples === 0)
  )
    return failure();
  const rgba = new Uint8ClampedArray(buffer, 16 + headerBytes, imageBytes);
  const pcm = new Float32Array(header.samples * 2);
  const audio = new DataView(buffer, 16 + headerBytes + imageBytes, audioBytes);
  for (let index = 0; index < pcm.length; index++) {
    const sample = audio.getFloat32(index * 4, true);
    if (!Number.isFinite(sample)) return failure();
    pcm[index] = sample;
  }
  return { header, rgba, pcm };
}
export function decodePlaybackState(text: string): PlaybackStatus {
  if (text.length > MAX_HEADER) return failure();
  let value: unknown;
  try {
    value = JSON.parse(text);
  } catch {
    return failure();
  }
  if (
    !value ||
    typeof value !== 'object' ||
    Array.isArray(value) ||
    !('type' in value) ||
    value.type !== 'state' ||
    !('status' in value) ||
    !value.status ||
    typeof value.status !== 'object'
  )
    return failure();
  const status = value.status as PlaybackStatus;
  if (
    typeof status.id !== 'string' ||
    typeof status.projectId !== 'string' ||
    !unsigned(status.projectRevision) ||
    !['idle', 'paused', 'playing', 'stale', 'failed', 'closed'].includes(status.status) ||
    !unsigned(status.frame) ||
    !unsigned(status.durationFrames) ||
    !unsigned(status.epoch) ||
    !unsigned(status.inFlight) ||
    typeof status.connected !== 'boolean' ||
    typeof status.reason !== 'string'
  )
    return failure();
  return status;
}
export function playbackStreamAllowed(url: string): boolean {
  try {
    const value = new URL(url);
    return (
      ['ws:', 'wss:'].includes(value.protocol) &&
      ['127.0.0.1', 'localhost', '[::1]'].includes(value.hostname) &&
      !value.username &&
      !value.password
    );
  } catch {
    return false;
  }
}
