import type { ExportOptions } from '../types';

export interface ExportDraft {
  resourceMode?: 'standard' | 'lower_memory';
  renderer: 'auto' | 'native' | 'ffmpeg';
  preset: 'project' | '720p' | '1080p' | '4k';
  encoder: string;
  range: 'all' | 'custom';
  from: string;
  to: string;
  overwrite: boolean;
}
export const defaultExportDraft: ExportDraft = {
  renderer: 'auto',
  resourceMode: 'standard',
  preset: '1080p',
  encoder: 'libx264',
  range: 'all',
  from: '0',
  to: '',
  overwrite: false,
};
export class ExportRangeError extends Error {
  constructor(
    message: string,
    public field: 'from' | 'to',
  ) {
    super(message);
  }
}
export function exportOptions(draft: ExportDraft, duration: number): ExportOptions {
  const result: ExportOptions = {
    renderer: draft.renderer,
    preset: draft.preset,
    videoEncoder: draft.encoder,
    overwrite: draft.overwrite,
  };
  if (draft.resourceMode === 'lower_memory') {
    if (draft.encoder !== 'libx264')
      throw new Error('较低内存档位仅支持H.264软件编码，请选择标准档位。');
    result.resourceMode = 'lower_memory';
  }
  if (draft.range === 'custom') {
    const from = Number(draft.from),
      to = Number(draft.to);
    if (
      !draft.from.trim() ||
      !draft.to.trim() ||
      !Number.isSafeInteger(from) ||
      !Number.isSafeInteger(to) ||
      from < 0 ||
      from >= to ||
      to > duration
    )
      throw new ExportRangeError(
        `导出区间需要满足 0 ≤ 起始帧 < 结束帧 ≤ ${duration}。请保留想要的范围并修改帧数。`,
        !draft.from.trim() || !Number.isSafeInteger(from) || from < 0 || from >= duration
          ? 'from'
          : 'to',
      );
    result.fromFrame = from;
    result.toFrame = to;
  }
  return result;
}
export const rendererNames = { auto: '自动选择', native: '原生合成（实验）', ffmpeg: 'FFmpeg合成' };
