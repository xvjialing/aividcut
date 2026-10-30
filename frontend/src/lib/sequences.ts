import type { Bootstrap, Settings } from '../types';
import type { NewProjectDraft } from './new-project';
import { projectFrameRates } from './new-project';
export const currentSequenceID = (b: Bootstrap) => b.project.sequenceId || 'sequence-main';
export const currentSequenceName = (b: Bootstrap) => b.project.sequenceName || '主时间线';
export function sequenceChoices(b: Bootstrap) {
  return (
    b.sequences || [
      {
        id: currentSequenceID(b),
        name: currentSequenceName(b),
        settings: b.project.settings,
        clipCount: b.project.clips.length,
        trackCount: b.project.tracks.length,
        durationFrames: 0,
        locked: b.project.tracks.some((t) => t.locked),
        current: true,
      },
    ]
  );
}
export function uniqueSequenceName(b: Bootstrap, base: string) {
  const names = new Set(sequenceChoices(b).map((s) => s.name.toLocaleLowerCase()));
  const short = Array.from(base).slice(0, 70).join('');
  if (!names.has(short.toLocaleLowerCase())) return short;
  for (let i = 2; i <= 100; i++) {
    const name = `${short} ${i}`;
    if (!names.has(name.toLocaleLowerCase())) return name;
  }
  return `${short} 副本`;
}
export function sequenceDraft(s: Settings): NewProjectDraft {
  const rate = `${s.fpsNum}/${s.fpsDen}`;
  return {
    layout: 'custom',
    resolution: '1080',
    width: String(s.width),
    height: String(s.height),
    frameRate: projectFrameRates.some(([id]) => id === rate) ? rate : 'custom',
    fpsNum: String(s.fpsNum),
    fpsDen: String(s.fpsDen),
  };
}
