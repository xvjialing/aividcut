import type { CaptionLayout } from '../types';
import { formatSourceSeconds, sourceSecondsToMicroseconds } from './action-review';
export interface CaptionLayoutDraft {
  mode: CaptionLayout['mode'];
  maxCharsPerLine: string;
  maxLines: string;
  maxDurationSeconds: string;
  pauseThresholdSeconds: string;
}
export function captionLayoutDraft(value?: CaptionLayout): CaptionLayoutDraft {
  return {
    mode: value?.mode || 'word',
    maxCharsPerLine: String(value?.maxCharsPerLine || 18),
    maxLines: String(value?.maxLines || 2),
    maxDurationSeconds: formatSourceSeconds(value?.maxDurationUs || 4000000),
    pauseThresholdSeconds: formatSourceSeconds(value?.pauseThresholdUs || 700000),
  };
}
export function parseCaptionLayout(draft: CaptionLayoutDraft): {
  value: CaptionLayout | null;
  errors: Record<string, string>;
} {
  if (draft.mode === 'word') return { value: { mode: 'word' }, errors: {} };
  const errors: Record<string, string> = {};
  const chars = Number(draft.maxCharsPerLine);
  const lines = Number(draft.maxLines);
  const duration = sourceSecondsToMicroseconds(draft.maxDurationSeconds);
  const pause = sourceSecondsToMicroseconds(draft.pauseThresholdSeconds);
  if (!draft.maxCharsPerLine.trim() || !Number.isSafeInteger(chars) || chars < 2 || chars > 120)
    errors.maxCharsPerLine = '每行字数需为 2–120 的整数。';
  if (!draft.maxLines.trim() || !Number.isSafeInteger(lines) || lines < 1 || lines > 4)
    errors.maxLines = '每条行数需为 1–4 的整数。';
  if (duration === null || duration < 100000 || duration > 60000000)
    errors.maxDurationSeconds = '单条时长需为 0.100–60.000 秒，最多6位小数。';
  if (pause === null || pause <= 0 || pause > 10000000)
    errors.pauseThresholdSeconds = '停顿分界需大于0且不超过10秒；默认0.700秒。';
  return {
    value: Object.keys(errors).length
      ? null
      : {
          mode: 'readable',
          maxCharsPerLine: chars,
          maxLines: lines,
          maxDurationUs: duration!,
          pauseThresholdUs: pause!,
        },
    errors,
  };
}
