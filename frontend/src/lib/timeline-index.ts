import type { Asset, Clip, Project, Track } from '../types';

interface ClipNode {
  clip: Clip;
  order: number;
  maxEnd: number;
  maxStart: number;
  left: ClipNode | null;
  right: ClipNode | null;
}
export interface IndexedTrack {
  track: Track;
  index: number;
  ordinal: number;
  root: ClipNode | null;
}
export interface TimelineViewport {
  left: number;
  width: number;
  top: number;
  height: number;
}
export const TRACK_ROW_HEIGHT = 66;
export const TIMELINE_RULER_HEIGHT = 32;

export class TimelineIndex {
  readonly tracks: IndexedTrack[];
  readonly tracksById = new Map<string, IndexedTrack>();
  readonly assets = new Map<string, Asset>();
  readonly clips = new Map<string, Clip>();
  private points: { frame: number; owners: { id: string; order: number }[] }[];
  constructor(project: Pick<Project, 'tracks' | 'clips' | 'assets'>) {
    const ordinals = new Map<string, number>();
    this.tracks = project.tracks.map((track, index) => {
      const ordinal = (ordinals.get(track.kind) || 0) + 1;
      ordinals.set(track.kind, ordinal);
      const item = { track, index, ordinal, root: null as ClipNode | null };
      this.tracksById.set(track.id, item);
      return item;
    });
    for (const asset of project.assets) this.assets.set(asset.id, asset);
    const groups = new Map<string, { clip: Clip; order: number }[]>();
    const points = new Map<number, { id: string; order: number }[]>();
    project.clips.forEach((clip, order) => {
      this.clips.set(clip.id, clip);
      const group = groups.get(clip.trackId) || [];
      group.push({ clip, order });
      groups.set(clip.trackId, group);
      [clip.startFrame, clip.startFrame + clip.durationFrames].forEach((frame, edge) => {
        const owners = points.get(frame) || [];
        owners.push({ id: clip.id, order: order * 2 + edge });
        points.set(frame, owners);
      });
    });
    const build = (
      items: { clip: Clip; order: number }[],
      lo: number,
      hi: number,
    ): ClipNode | null => {
      if (lo >= hi) return null;
      const mid = (lo + hi) >>> 1,
        value = items[mid];
      const left = build(items, lo, mid),
        right = build(items, mid + 1, hi);
      return {
        ...value,
        left,
        right,
        maxEnd: Math.max(
          value.clip.startFrame + value.clip.durationFrames,
          left?.maxEnd || 0,
          right?.maxEnd || 0,
        ),
        maxStart: right?.maxStart ?? value.clip.startFrame,
      };
    };
    for (const track of this.tracks) {
      const items = groups.get(track.track.id) || [];
      items.sort((a, b) => a.clip.startFrame - b.clip.startFrame || a.order - b.order);
      track.root = build(items, 0, items.length);
    }
    this.points = [...points]
      .map(([frame, owners]) => ({ frame, owners }))
      .sort((a, b) => a.frame - b.frame);
  }
  query(
    trackId: string,
    from: number,
    to: number,
    minFrames: number,
    diagnostics?: { visited: number },
  ): Clip[] {
    const found: { clip: Clip; order: number }[] = [];
    const visit = (node: ClipNode | null) => {
      if (!node || Math.max(node.maxEnd, node.maxStart + minFrames) <= from) return;
      if (diagnostics) diagnostics.visited++;
      visit(node.left);
      if (node.clip.startFrame >= to) return;
      if (
        Math.max(
          node.clip.startFrame + node.clip.durationFrames,
          node.clip.startFrame + minFrames,
        ) > from
      )
        found.push(node);
      visit(node.right);
    };
    if (to > from) visit(this.tracksById.get(trackId)?.root || null);
    found.sort((a, b) => a.order - b.order);
    return found.map((item) => item.clip);
  }
  snap(
    start: number,
    tolerance: number,
    exclude: string | ReadonlySet<string>,
    playhead: number,
  ): number | undefined {
    if (Math.abs(playhead - start) <= tolerance) return playhead;
    let lo = 0,
      hi = this.points.length;
    while (lo < hi) {
      const mid = (lo + hi) >>> 1;
      if (this.points[mid].frame < start - tolerance) lo = mid + 1;
      else hi = mid;
    }
    let best: { frame: number; order: number } | undefined;
    for (let i = lo; i < this.points.length && this.points[i].frame <= start + tolerance; i++) {
      const point = this.points[i],
        owner = point.owners.find((item) =>
          typeof exclude === 'string' ? item.id !== exclude : !exclude.has(item.id),
        );
      if (owner && (!best || owner.order < best.order))
        best = { frame: point.frame, order: owner.order };
    }
    return best?.frame;
  }
}

export function visibleTrackIndices(
  count: number,
  top: number,
  height: number,
  pinned: number[] = [],
): number[] {
  const first = Math.max(
    0,
    Math.min(count, Math.floor((top - TIMELINE_RULER_HEIGHT) / TRACK_ROW_HEIGHT) - 2),
  );
  const last = Math.max(
    first,
    Math.min(count, Math.ceil((top + height - TIMELINE_RULER_HEIGHT) / TRACK_ROW_HEIGHT) + 2),
  );
  const indices = new Set<number>();
  for (let i = first; i < last; i++) indices.add(i);
  for (const index of pinned) if (index >= 0 && index < count) indices.add(index);
  return [...indices].sort((a, b) => a - b);
}
export function visibleTicks(
  seconds: number,
  zoom: number,
  step: number,
  left: number,
  width: number,
): number[] {
  const first = Math.max(0, Math.ceil((left - 100) / (zoom * step)));
  const last = Math.min(
    Math.ceil(seconds / step),
    Math.floor((left + width + 100) / (zoom * step)),
  );
  return Array.from({ length: Math.max(0, last - first + 1) }, (_, i) => (first + i) * step);
}
