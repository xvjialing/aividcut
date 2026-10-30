import type { Clip, Project } from '../types';
import { canPlace } from './editor';

export interface ClipSelection {
  projectId: string;
  ids: string[];
  primary: string | null;
}
export const emptySelection: ClipSelection = { projectId: '', ids: [], primary: null };
// 清理可能晚于下一次编辑结果执行；旧读模型不能删除新修订刚选中的副本。
export function pruneClipSelection(
  current: ClipSelection,
  captured: Project,
  authoritative?: Project,
): ClipSelection {
  if (captured !== authoritative || current.projectId !== captured.id) return current;
  const known = new Set(captured.clips.map((clip) => clip.id));
  const ids = current.ids.filter((id) => known.has(id));
  return ids.length === current.ids.length
    ? current
    : {
        ...current,
        ids,
        primary: ids.includes(current.primary || '') ? current.primary : ids.at(-1) || null,
      };
}
export function selectClip(
  current: ClipSelection,
  projectId: string,
  id: string,
  additive = false,
): ClipSelection {
  const ids = current.projectId === projectId ? current.ids : [];
  if (!additive) return { projectId, ids: [id], primary: id };
  const next = ids.includes(id) ? ids.filter((v) => v !== id) : [...ids, id];
  return { projectId, ids: next, primary: next.includes(id) ? id : next.at(-1) || null };
}
export function linkedSelection(project: Project, ids: readonly string[]): Clip[] {
  const selected = new Set(ids),
    groups = new Set(
      project.clips.filter((c) => selected.has(c.id) && c.linkGroup).map((c) => c.linkGroup),
    );
  return project.clips.filter(
    (c) => selected.has(c.id) || (!!c.linkGroup && groups.has(c.linkGroup)),
  );
}
export function groupMoveDraft(
  project: Project,
  ids: readonly string[],
  frameDelta: number,
  trackDelta: number,
) {
  const clips = linkedSelection(project, ids),
    selected = new Set(clips.map((c) => c.id));
  const tracks = new Map(project.tracks.map((t, i) => [t.id, { track: t, index: i }]));
  let valid = Number.isSafeInteger(frameDelta) && Number.isSafeInteger(trackDelta);
  const drafts = clips.map((c) => {
    const origin = tracks.get(c.trackId)!;
    if (origin.track.locked) valid = false;
    if (c.captionSource) {
      if (!selected.has(c.captionSource.sourceClipId)) valid = false;
      return { ...c, startFrame: c.startFrame + frameDelta };
    }
    const target = project.tracks[origin.index + trackDelta];
    if (
      origin.track.locked ||
      !target ||
      target.locked ||
      !canPlace(target, c.kind) ||
      c.startFrame + frameDelta < 0 ||
      !Number.isSafeInteger(c.startFrame + frameDelta + c.durationFrames)
    )
      valid = false;
    return { ...c, startFrame: c.startFrame + frameDelta, trackId: target?.id || c.trackId };
  });
  return { drafts, valid };
}

export function trimSelectionClips(project: Project, ids: readonly string[]): Clip[] {
  const direct = linkedSelection(project, ids);
  const selected = new Set(direct.map((c) => c.id));
  return [
    ...direct,
    ...project.clips.filter(
      (c) => c.captionSource && selected.has(c.captionSource.sourceClipId) && !selected.has(c.id),
    ),
  ];
}

export function groupTrimDraft(
  project: Project,
  ids: readonly string[],
  edge: 'start' | 'end',
  delta: number,
) {
  const clips = trimSelectionClips(project, ids),
    tracks = new Map(project.tracks.map((t) => [t.id, t]));
  const sources = new Map<string, Clip>(),
    hidden: string[] = [];
  let valid = Number.isSafeInteger(delta);
  for (const c of clips) {
    if (tracks.get(c.trackId)?.locked) valid = false;
    if (c.captionSource) continue;
    const after = { ...c };
    if (edge === 'start') {
      after.startFrame += delta;
      after.durationFrames -= delta;
      if (c.kind === 'video' || c.kind === 'audio') {
        const change = Math.floor(
          (Math.abs(delta) * project.settings.fpsDen * 1e6 * c.speedNum) /
            (project.settings.fpsNum * c.speedDen),
        );
        after.sourceInUs += Math.sign(delta) * change;
      }
    } else after.durationFrames += delta;
    if (
      after.startFrame < 0 ||
      after.durationFrames < 1 ||
      after.sourceInUs < 0 ||
      !Number.isSafeInteger(after.startFrame + after.durationFrames) ||
      !Number.isSafeInteger(after.sourceInUs)
    )
      valid = false;
    sources.set(c.id, after);
  }
  const drafts = clips.map((c) => {
    if (!c.captionSource) return sources.get(c.id)!;
    const source = sources.get(c.captionSource.sourceClipId);
    if (!source) {
      valid = false;
      return c;
    }
    const startFrame = Math.max(c.startFrame, source.startFrame),
      end = Math.min(c.startFrame + c.durationFrames, source.startFrame + source.durationFrames);
    if (end <= startFrame) hidden.push(c.id);
    return { ...c, startFrame, durationFrames: Math.max(0, end - startFrame) };
  });
  return { drafts, hidden, valid };
}
