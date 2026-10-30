import type { Clip, Settings } from '../types';
import { framesToSeconds } from './editor';

// 数值编辑明确按当前片段重设，另一端超过剩余范围时缩短到毫秒精度。
export function editedAudioFade(
  clip: Clip,
  settings: Settings,
  side: 'in' | 'out',
  seconds: number,
): Clip['audioFade'] {
  const length = Math.floor(framesToSeconds(clip.durationFrames, settings) * 1e6);
  const value = Math.max(0, Math.min(length, Math.round(seconds * 1e6)));
  const remaining = Math.floor((length - value) / 1000) * 1000;
  const inUs = side === 'in' ? value : Math.min(clip.audioFade?.inUs || 0, remaining);
  const outUs = side === 'out' ? value : Math.min(clip.audioFade?.outUs || 0, remaining);
  return inUs || outUs
    ? { inUs, outUs, spanFrames: clip.durationFrames, offsetFrames: 0 }
    : undefined;
}
