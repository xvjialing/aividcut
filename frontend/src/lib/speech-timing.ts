import type { Project } from '../types';
import type { SpeechCharacterAlignment } from '../speech-types';
import type { SpeechSummary } from '../speech-types';
import { formatSourceSeconds } from './action-review';
export function speechTimingSummary(job: SpeechSummary): string {
  if (!job.timingRequested) return '配音时间：未请求';
  if (['queued', 'preparing', 'submitting', 'running', 'saving', 'cancelling'].includes(job.status))
    return '配音时间：已请求，等待结果';
  const original = !job.timingAvailable
    ? '原文时间未提供'
    : job.timingComplete
      ? '原文可核对'
      : '原文时间有缺失';
  const normalized = !job.normalizedTimingAvailable
    ? '朗读时间未提供'
    : job.normalizedTimingComplete
      ? '朗读文本可核对'
      : '朗读时间有缺失';
  return `配音时间：${original} / ${normalized}`;
}

export function alignmentDisplayState(
  value: SpeechCharacterAlignment | null | undefined,
): 'missing' | 'incomplete' | 'complete' | 'invalid' {
  if (!value) return 'missing';
  const characters = value.characters || [];
  const starts = value.startTimesSeconds || [];
  const ends = value.endTimesSeconds || [];
  if (characters.length !== starts.length || characters.length !== ends.length) return 'invalid';
  let complete = characters.length > 0;
  let lastStart = -1;
  let lastEnd = -1;
  for (let index = 0; index < characters.length; index++) {
    if (!characters[index]) return 'invalid';
    const start = starts[index];
    const end = ends[index];
    if (start === null || end === null) complete = false;
    if (start !== null && (!Number.isFinite(start) || start < 0 || start < lastStart))
      return 'invalid';
    if (
      end !== null &&
      (!Number.isFinite(end) || end < 0 || end < lastEnd || (start !== null && end < start))
    )
      return 'invalid';
    if (start !== null) lastStart = start;
    if (end !== null) lastEnd = end;
  }
  return complete ? 'complete' : 'incomplete';
}

export function alignmentRows(value: SpeechCharacterAlignment, offset = 0, limit = 40) {
  const start = Math.max(0, Math.trunc(offset));
  const count = Math.max(0, Math.min(200, Math.trunc(limit)));
  return (value.characters || []).slice(start, start + count).map((character, position) => ({
    index: start + position,
    character,
    startSeconds: value.startTimesSeconds?.[start + position] ?? null,
    endSeconds: value.endTimesSeconds?.[start + position] ?? null,
  }));
}
export function displayTimingSeconds(value: number | null | undefined): string {
  return value == null || !Number.isFinite(value)
    ? '未提供'
    : `${formatSourceSeconds(Math.round(value * 1e6))}秒`;
}

export function speechAudioPlacements(project: Project, audioId: string) {
  return project.clips
    .filter((clip) => clip.kind === 'audio' && clip.assetId === audioId)
    .map((clip) => ({
      clipId: clip.id,
      trackId: clip.trackId,
      startFrame: clip.startFrame,
      durationFrames: clip.durationFrames,
      sourceInUs: clip.sourceInUs,
      locked: Boolean(project.tracks.find((track) => track.id === clip.trackId)?.locked),
    }));
}
export function sourceCaptionOverrides(project: Project, transcriptId: string) {
  const captions = project.clips.filter(
    (clip) => clip.captionSource?.transcriptId === transcriptId,
  );
  return {
    text: captions.filter((clip) => clip.captionSource?.textOverride).map((clip) => clip.id),
    style: captions.filter((clip) => clip.captionSource?.styleOverride).map((clip) => clip.id),
  };
}
