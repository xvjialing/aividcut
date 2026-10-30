import { lazy, Suspense, useCallback, useEffect, useMemo, useRef, useState } from 'react';
import {
  Captions,
  Copy,
  ClipboardCopy,
  ClipboardPaste,
  ScissorsLineDashed,
  Settings2,
  Eye,
  EyeOff,
  Film,
  LockKeyhole,
  LocateFixed,
  Minus,
  Music2,
  Plus,
  Scissors,
  Trash2,
  Type,
  UnlockKeyhole,
  Volume2,
  VolumeX,
} from 'lucide-react';
import type { Bootstrap, Clip, Project, Track } from '../types';
import type { ClipboardStatus } from '../clipboard-types';
import { ClipWaveform } from './ClipWaveform';
import {
  linkedSelection,
  groupMoveDraft,
  groupTrimDraft,
  trimSelectionClips,
} from '../lib/clip-selection';
import {
  canPlace,
  fps,
  PlaybackClock,
  projectEnd,
  timecode,
  trackNames,
  trimClip,
} from '../lib/editor';
import { IconButton } from './ui';
import {
  TimelineIndex,
  visibleTicks,
  visibleTrackIndices,
  TRACK_ROW_HEIGHT,
  TIMELINE_RULER_HEIGHT,
  type TimelineViewport,
} from '../lib/timeline-index';

const ROW_HEIGHT = TRACK_ROW_HEIGHT;
const trackIcon = (kind: string) =>
  kind === 'audio' ? (
    <Music2 size={15} />
  ) : kind === 'text' ? (
    <Type size={15} />
  ) : kind === 'caption' ? (
    <Captions size={15} />
  ) : (
    <Film size={15} />
  );

function Playhead({
  clock,
  pixelsPerFrame,
  verticalOffset,
}: {
  clock: PlaybackClock;
  pixelsPerFrame: number;
  verticalOffset: number;
}) {
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const sync = () => {
      if (ref.current)
        ref.current.style.transform = `translateX(${clock.getFrame() * pixelsPerFrame}px)`;
    };
    sync();
    return clock.subscribe(sync);
  }, [clock, pixelsPerFrame]);
  return (
    <div ref={ref} className="playhead" aria-hidden="true">
      <span style={{ transform: `translateY(${verticalOffset}px)` }} />
    </div>
  );
}

function TimelineClip({
  clip,
  project,
  selected,
  pixelsPerFrame,
  clock,
  onSelect,
  onUpdate,
  disabled,
  canBeginGesture,
  viewLeft,
  viewWidth,
  index,
  scroll,
  onGesture,
  selectionSize = 1,
  group,
  trim,
}: {
  clip: Clip;
  project: Project;
  selected: boolean;
  pixelsPerFrame: number;
  clock: PlaybackClock;
  onSelect: (additive?: boolean) => void;
  onUpdate: (clip: Clip) => void;
  disabled: boolean;
  canBeginGesture?: () => boolean;
  viewLeft: number;
  viewWidth: number;
  index: TimelineIndex;
  scroll: () => { left: number; top: number };
  onGesture: (id: string | null, refresh?: () => void) => void;
  selectionSize?: number;
  group?: {
    ids: string[];
    preview: (delta: number, trackDelta: number) => void;
    finish: (delta: number, trackDelta: number) => void;
    clear: () => void;
  };
  trim?: {
    locked: boolean;
    preview: (edge: 'start' | 'end', delta: number) => void;
    finish: (edge: 'start' | 'end', delta: number) => void;
    clear: () => void;
  };
}) {
  const ref = useRef<HTMLDivElement>(null);
  const gesture = useRef<{
    pointer: number;
    x: number;
    y: number;
    edge?: 'start' | 'end';
    draft: Clip;
    scrollLeft: number;
    scrollTop: number;
    lastX: number;
    lastY: number;
    delta: number;
    trackDelta: number;
  } | null>(null);
  const [dragging, setDragging] = useState(false);
  const handledClick = useRef(false);
  useEffect(
    () => () => {
      if (gesture.current) {
        gesture.current = null;
        onGesture(null);
      }
    },
    [onGesture],
  );
  const track = index.tracksById.get(clip.trackId)!.track;
  const asset = index.assets.get(clip.assetId || '');
  const hasWaveform =
    !!asset?.hasAudio && !asset.missing && (clip.kind === 'audio' || clip.kind === 'video');
  const select = (additive = false) => {
    if (additive) {
      onSelect(true);
      return;
    }
    if (!selected) onSelect();
  };

  function start(event: React.PointerEvent<HTMLButtonElement>, edge?: 'start' | 'end') {
    if (event.button !== 0) return;
    if (canBeginGesture && !canBeginGesture()) return;
    handledClick.current = true;
    if (!edge && (event.shiftKey || event.metaKey || event.ctrlKey)) {
      event.preventDefault();
      select(true);
      return;
    }
    if (edge && ((selected && selectionSize > 1 && !trim) || trim?.locked)) return;
    select();
    if (disabled || track.locked) {
      if (!edge && selected && selectionSize > 1) onSelect();
      return;
    }
    event.preventDefault();
    event.currentTarget.focus({ preventScroll: true });
    event.currentTarget.setPointerCapture(event.pointerId);
    gesture.current = {
      pointer: event.pointerId,
      x: event.clientX,
      y: event.clientY,
      edge,
      draft: clip,
      scrollLeft: scroll().left,
      scrollTop: scroll().top,
      lastX: event.clientX,
      lastY: event.clientY,
      delta: 0,
      trackDelta: 0,
    };
    setDragging(true);
    onGesture(clip.id, () => {
      const current = gesture.current;
      if (current) position(current.lastX, current.lastY);
    });
  }
  function move(event: React.PointerEvent<HTMLButtonElement>) {
    position(event.clientX, event.clientY);
  }
  function position(x: number, y: number) {
    const current = gesture.current;
    if (!current) return;
    current.lastX = x;
    current.lastY = y;
    const offset = scroll();
    const delta = Math.round((x - current.x + offset.left - current.scrollLeft) / pixelsPerFrame);
    let draft: Clip;
    if (current.edge && trim) {
      current.delta = delta;
      trim.preview(current.edge, delta);
      return;
    }
    if (current.edge) draft = trimClip(clip, current.edge, delta, project.settings);
    else {
      const minimum = group
        ? linkedSelection(project, group.ids).reduce(
            (value, c) => Math.min(value, c.startFrame),
            Infinity,
          )
        : clip.startFrame;
      let startFrame = clip.startFrame + Math.max(-minimum, delta);
      const excluded = group
        ? new Set(linkedSelection(project, group.ids).map((c) => c.id))
        : clip.id;
      const snap = index.snap(startFrame, 6 / pixelsPerFrame, excluded, clock.getFrame());
      if (snap !== undefined && (!group || snap - clip.startFrame >= -minimum)) startFrame = snap;
      const startIndex = index.tracksById.get(clip.trackId)!.index;
      const nextIndex = Math.max(
        0,
        Math.min(
          project.tracks.length - 1,
          startIndex + Math.round((y - current.y + offset.top - current.scrollTop) / ROW_HEIGHT),
        ),
      );
      const target = project.tracks[nextIndex];
      draft = {
        ...clip,
        startFrame,
        trackId: target && canPlace(target, clip.kind) && !target.locked ? target.id : clip.trackId,
      };
      current.delta = draft.startFrame - clip.startFrame;
      current.trackDelta = nextIndex - startIndex;
      if (group) {
        group.preview(current.delta, current.trackDelta);
        current.draft = draft;
        return;
      }
    }
    current.draft = draft;
    if (ref.current) {
      ref.current.style.left = `${draft.startFrame * pixelsPerFrame}px`;
      ref.current.style.width = `${Math.max(18, draft.durationFrames * pixelsPerFrame)}px`;
      const startTrack = index.tracksById.get(clip.trackId)!.index;
      const endTrack = index.tracksById.get(draft.trackId)!.index;
      ref.current.style.transform = `translateY(${(endTrack - startTrack) * ROW_HEIGHT}px)`;
      const wave = ref.current.querySelector<HTMLElement>('.clip-waveform');
      if (wave)
        wave.style.transform =
          current.edge === 'start'
            ? `translateX(${(clip.startFrame - draft.startFrame) * pixelsPerFrame}px)`
            : '';
    }
  }
  function finish(event: React.PointerEvent<HTMLButtonElement>) {
    const current = gesture.current;
    if (!current) return;
    gesture.current = null;
    setDragging(false);
    onGesture(null);
    if (current.edge && trim) {
      trim.clear();
      ref.current?.removeAttribute('data-editor-gesture');
      if (event.currentTarget.hasPointerCapture(event.pointerId))
        event.currentTarget.releasePointerCapture(event.pointerId);
      if (current.delta) trim.finish(current.edge, current.delta);
      return;
    }
    if (group && !current.edge) {
      group.clear();
      if (event.currentTarget.hasPointerCapture(event.pointerId))
        event.currentTarget.releasePointerCapture(event.pointerId);
      if (current.delta || current.trackDelta) group.finish(current.delta, current.trackDelta);
      // click 在手势标记卸载后处理选择，避免属性导航被正在结束的手势守卫拒绝。
      else handledClick.current = false;
      return;
    }
    if (event.currentTarget.hasPointerCapture(event.pointerId))
      event.currentTarget.releasePointerCapture(event.pointerId);
    if (ref.current) {
      ref.current.style.left = `${clip.startFrame * pixelsPerFrame}px`;
      ref.current.style.width = `${Math.max(18, clip.durationFrames * pixelsPerFrame)}px`;
      ref.current.style.transform = '';
      const wave = ref.current.querySelector<HTMLElement>('.clip-waveform');
      if (wave) wave.style.transform = '';
    }
    if (
      current.draft.startFrame !== clip.startFrame ||
      current.draft.durationFrames !== clip.durationFrames ||
      current.draft.sourceInUs !== clip.sourceInUs ||
      current.draft.trackId !== clip.trackId
    )
      onUpdate(current.draft);
  }
  function cancel() {
    group?.clear();
    trim?.clear();
    gesture.current = null;
    ref.current?.removeAttribute('data-editor-gesture');
    setDragging(false);
    onGesture(null);
    if (ref.current) {
      ref.current.style.left = `${clip.startFrame * pixelsPerFrame}px`;
      ref.current.style.width = `${Math.max(18, clip.durationFrames * pixelsPerFrame)}px`;
      ref.current.style.transform = '';
      const wave = ref.current.querySelector<HTMLElement>('.clip-waveform');
      if (wave) wave.style.transform = '';
    }
  }
  function cancelFromKey(event: React.KeyboardEvent<HTMLButtonElement>) {
    if (event.key !== 'Escape' || !gesture.current) return false;
    event.preventDefault();
    event.stopPropagation();
    const pointer = gesture.current.pointer;
    cancel();
    if (event.currentTarget.hasPointerCapture(pointer))
      event.currentTarget.releasePointerCapture(pointer);
    return true;
  }
  const events = (edge?: 'start' | 'end') => ({
    onPointerDown: (e: React.PointerEvent<HTMLButtonElement>) => start(e, edge),
    onPointerMove: move,
    onPointerUp: finish,
    onPointerCancel: cancel,
    onLostPointerCapture: cancel,
  });
  return (
    <div
      ref={ref}
      className={`timeline-clip clip-${clip.kind} ${clip.durationFrames * pixelsPerFrame < 40 ? 'clip-narrow' : ''} ${selected ? 'selected' : ''} ${dragging ? 'dragging' : ''} ${track.locked ? 'locked' : ''}`}
      data-editor-gesture={dragging ? 'true' : undefined}
      style={{
        left: clip.startFrame * pixelsPerFrame,
        width: Math.max(18, clip.durationFrames * pixelsPerFrame),
      }}
      data-clip-id={clip.id}
    >
      <button
        type="button"
        className="clip-face"
        aria-label={`${trackNames[clip.kind]}片段 ${clip.name}，${timecode(clip.startFrame, project.settings)}，${clip.durationFrames} 帧`}
        aria-pressed={selected}
        onClick={(event) => {
          if (handledClick.current) {
            handledClick.current = false;
            return;
          }
          onSelect(event.shiftKey || event.metaKey || event.ctrlKey);
        }}
        onKeyDown={(event) => {
          cancelFromKey(event);
        }}
        {...events()}
      >
        <span className="clip-name">
          {trackIcon(clip.kind)}
          <span>{clip.name}</span>
          {track.locked ? <LockKeyhole size={11} /> : null}
        </span>
        <span className={`clip-detail ${hasWaveform ? 'sr-only' : ''}`}>
          {clip.kind === 'text' || clip.kind === 'caption'
            ? clip.text
            : `${(clip.durationFrames / fps(project.settings)).toFixed(1)} 秒`}
        </span>
      </button>
      {hasWaveform && asset ? (
        <ClipWaveform
          projectId={project.id}
          asset={asset}
          clip={clip}
          settings={project.settings}
          pixelsPerFrame={pixelsPerFrame}
          viewLeft={viewLeft}
          viewWidth={viewWidth}
        />
      ) : null}
      {!track.locked ? (
        <>
          <button
            type="button"
            className="clip-trim clip-trim-start"
            disabled={disabled || trim?.locked || (selected && selectionSize > 1 && !trim)}
            aria-label={`修剪 ${clip.name} 的起点`}
            onClick={() => select()}
            {...events('start')}
            onKeyDown={(e) => {
              if (cancelFromKey(e)) return;
              if (e.key === 'ArrowRight' || e.key === 'ArrowLeft') {
                if (canBeginGesture && !canBeginGesture()) return;
                e.preventDefault();
                e.stopPropagation();
                const delta = (e.key === 'ArrowRight' ? 1 : -1) * (e.shiftKey ? 10 : 1);
                if (trim) trim.finish('start', delta);
                else onUpdate(trimClip(clip, 'start', delta, project.settings));
              }
            }}
          />
          <button
            type="button"
            className="clip-trim clip-trim-end"
            disabled={disabled || trim?.locked || (selected && selectionSize > 1 && !trim)}
            aria-label={`修剪 ${clip.name} 的终点`}
            onClick={() => select()}
            {...events('end')}
            onKeyDown={(e) => {
              if (cancelFromKey(e)) return;
              if (e.key === 'ArrowRight' || e.key === 'ArrowLeft') {
                if (canBeginGesture && !canBeginGesture()) return;
                e.preventDefault();
                e.stopPropagation();
                const delta = (e.key === 'ArrowRight' ? 1 : -1) * (e.shiftKey ? 10 : 1);
                if (trim) trim.finish('end', delta);
                else onUpdate(trimClip(clip, 'end', delta, project.settings));
              }
            }}
          />
        </>
      ) : null}
    </div>
  );
}

const RippleRangeTools = lazy(() =>
  import('./RippleRangeTools').then((module) => ({ default: module.RippleRangeTools })),
);
export function Timeline({
  sourceEpoch = 0,
  prepareProject,
  onRippleApply,
  project,
  clock,
  selected,
  busy,
  onSelect,
  onUpdateClip,
  onUpdateTrack,
  onManageTrack,
  onSplit,
  onDuplicate,
  clipboard,
  clipboardError,
  clipboardFocusVersion = 0,
  onCopy,
  onCut,
  onPaste,
  onDelete,
  onText,
  onAddTrack,
  canBeginGesture,
  selectedIds,
  onSelectMany,
  onMoveSelection,
  onTrimSelection,
  markedRange,
  onMarkedRangeChange,
  sequencePanel,
}: {
  project: Project;
  sequencePanel?: { id: string; labelledBy: string };
  sourceEpoch?: number;
  prepareProject?: () => Promise<Project | null>;
  onRippleApply?: (action: () => Promise<Bootstrap>) => Promise<Bootstrap | null>;
  clock: PlaybackClock;
  selected: string | null;
  busy: boolean;
  onSelect: (id: string, additive?: boolean) => void;
  onUpdateClip: (clip: Clip) => void;
  onUpdateTrack: (track: Track) => void;
  onManageTrack?: (track: Track) => void;
  onSplit: () => void;
  onDuplicate?: () => void;
  clipboard?: ClipboardStatus | null;
  clipboardError?: string;
  clipboardFocusVersion?: number;
  onCopy?: () => void;
  onCut?: () => void;
  onPaste?: () => void;
  onDelete: () => void;
  onText: () => void;
  onAddTrack: () => void;
  canBeginGesture?: () => boolean;
  selectedIds?: string[];
  onSelectMany?: (ids: string[]) => void;
  onMoveSelection?: (delta: number, trackDelta: number) => void;
  onTrimSelection?: (ids: string[], edge: 'start' | 'end', delta: number) => void;
  markedRange?: import('../ripple-types').TimelineRange;
  onMarkedRangeChange?: (range: import('../ripple-types').TimelineRange) => void;
}) {
  const rangeKey = project.id + ':' + sourceEpoch;
  const [rangeScope, setRangeScope] = useState<{
    key: string;
    range: import('../ripple-types').TimelineRange;
  }>({ key: rangeKey, range: { from: null, to: null } });
  const timeRange =
    markedRange || (rangeScope.key === rangeKey ? rangeScope.range : { from: null, to: null });
  const [zoom, setZoom] = useState(48);
  const [height, setHeight] = useState<number>();
  const panel = useRef<HTMLElement>(null);
  const ids = selectedIds || (selected ? [selected] : []);
  const [box, setBox] = useState<{
    x: number;
    y: number;
    w: number;
    h: number;
    ids: string[];
  } | null>(null);
  const marquee = useRef<{
    pointer: number;
    x: number;
    y: number;
    ids: string[];
    base: string[];
    lastX: number;
    lastY: number;
  } | null>(null);
  const rowsOwner = useRef<HTMLDivElement>(null);
  const visibleSelection = new Set(box?.ids || ids);
  const resize = useRef<{ y: number; height: number } | null>(null);
  const [viewport, setViewport] = useState<TimelineViewport>({
    left: 0,
    width: 1000,
    top: 0,
    height: 400,
  });
  const [activeClip, setActiveClip] = useState<string | null>(null);
  const [focus, setFocus] = useState<{ track: string; clip?: string; control?: string } | null>(
    null,
  );
  const grid = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!clipboardFocusVersion) return;
    const id = requestAnimationFrame(() => {
      if (
        document.activeElement === document.body ||
        panel.current?.contains(document.activeElement)
      )
        grid.current?.focus({ preventScroll: true });
    });
    return () => cancelAnimationFrame(id);
  }, [clipboardFocusVersion]);
  const clipboardHint = clipboard?.available
    ? `工程内剪贴板：${clipboard.clipCount} 个片段。自动字幕跟随源转写，字幕手改保留。${clipboard.detachedCaptions ? `${clipboard.detachedCaptions} 条单独字幕粘贴为独立字幕。` : ''}`
    : clipboardError || clipboard?.reason || '正在读取工程剪贴板';
  const pendingView = useRef<number | null>(null);
  const gestureRefresh = useRef<(() => void) | null>(null);
  const pendingFocus = useRef<string | null>(null);
  const pendingClipFocus = useRef<{ id: string; control: string } | null>(null);
  const body = useRef<HTMLDivElement>(null);
  const scrubber = useRef<HTMLInputElement>(null);
  const pixelsPerFrame = zoom / fps(project.settings);
  const index = useMemo(
    () => new TimelineIndex(project),
    [project.tracks, project.clips, project.assets],
  );
  const scroll = useCallback(
    () => ({ left: body.current?.scrollLeft || 0, top: grid.current?.scrollTop || 0 }),
    [],
  );
  const gestureChanged = useCallback((id: string | null, refresh?: () => void) => {
    setActiveClip(id);
    gestureRefresh.current = refresh || null;
  }, []);
  const readViewport = useCallback(() => {
    pendingView.current = null;
    if (!body.current || !grid.current) return;
    const next = {
      ...scroll(),
      width: body.current.clientWidth || 1000,
      height: grid.current.clientHeight || 400,
    };
    setViewport((old) =>
      old.left === next.left &&
      old.width === next.width &&
      old.top === next.top &&
      old.height === next.height
        ? old
        : next,
    );
  }, [scroll]);
  const scheduleViewport = useCallback(() => {
    gestureRefresh.current?.();
    const box = marquee.current;
    if (box && rowsOwner.current) refreshBox(box.lastX, box.lastY, rowsOwner.current);
    if (pendingView.current === null) pendingView.current = requestAnimationFrame(readViewport);
  }, [readViewport, index, pixelsPerFrame]);
  const end = projectEnd(project);
  const seconds = Math.max(30, end / fps(project.settings) + 10);
  const width = Math.max(viewport.width, seconds * zoom);
  const tickStep = zoom < 24 ? 10 : zoom < 45 ? 5 : zoom < 100 ? 2 : 1;
  const ticks = useMemo(
    () => visibleTicks(seconds, zoom, tickStep, viewport.left, viewport.width),
    [seconds, tickStep, zoom, viewport],
  );
  const rowIndices = useMemo(
    () =>
      visibleTrackIndices(project.tracks.length, viewport.top, viewport.height, [
        index.tracksById.get(focus?.track || '')?.index ?? -1,
        index.tracksById.get(index.clips.get(activeClip || '')?.trackId || '')?.index ?? -1,
      ]),
    [project.tracks.length, viewport.top, viewport.height, index, focus, activeClip],
  );
  const rows = useMemo(
    () =>
      rowIndices.map((row) => {
        const track = index.tracks[row];
        const clips = index.query(
          track.track.id,
          Math.max(0, viewport.left - 160) / pixelsPerFrame,
          (viewport.left + viewport.width + 160) / pixelsPerFrame,
          18 / pixelsPerFrame,
        );
        const pins = [activeClip, focus?.clip]
          .map((id) => index.clips.get(id || ''))
          .filter((clip): clip is Clip => !!clip && clip.trackId === track.track.id);
        for (const clip of pins) if (!clips.some((item) => item.id === clip.id)) clips.push(clip);
        return { ...track, clips };
      }),
    [rowIndices, index, viewport.left, viewport.width, pixelsPerFrame, activeClip, focus],
  );
  useEffect(() => {
    if (!body.current || !grid.current) return;
    const observer = new ResizeObserver(scheduleViewport);
    observer.observe(body.current);
    observer.observe(grid.current);
    readViewport();
    return () => {
      observer.disconnect();
      if (pendingView.current !== null) cancelAnimationFrame(pendingView.current);
    };
  }, [readViewport, scheduleViewport]);
  useEffect(() => {
    readViewport();
  }, [project.tracks.length, readViewport, height]);
  useEffect(() => {
    gestureRefresh.current = null;
    setActiveClip(null);
    setFocus(null);
    pendingFocus.current = null;
    pendingClipFocus.current = null;
    if (body.current) body.current.scrollLeft = 0;
    if (grid.current) grid.current.scrollTop = 0;
    readViewport();
  }, [project.id, sourceEpoch, readViewport]);
  const revealTrack = useCallback(
    (row: number) => {
      const owner = grid.current;
      if (!owner) return;
      const top = TIMELINE_RULER_HEIGHT + row * ROW_HEIGHT;
      if (top < owner.scrollTop + TIMELINE_RULER_HEIGHT)
        owner.scrollTop = Math.max(0, top - TIMELINE_RULER_HEIGHT);
      else if (top + ROW_HEIGHT > owner.scrollTop + owner.clientHeight)
        owner.scrollTop = Math.max(0, top + ROW_HEIGHT - (owner.clientHeight || 400));
      readViewport();
    },
    [readViewport],
  );
  const revealClip = useCallback(
    (id: string) => {
      const clip = index.clips.get(id),
        owner = body.current;
      if (!clip || !owner) return;
      const track = index.tracksById.get(clip.trackId);
      if (track) revealTrack(track.index);
      const left = clip.startFrame * pixelsPerFrame;
      if (left < owner.scrollLeft || left >= owner.scrollLeft + (owner.clientWidth || 1000))
        owner.scrollLeft = left;
      readViewport();
    },
    [index, pixelsPerFrame, revealTrack, readViewport],
  );
  useEffect(() => {
    if (selected && !activeClip) revealClip(selected);
  }, [
    selected,
    index.clips.get(selected || '')?.trackId,
    revealClip,
    activeClip,
    viewport.width,
    viewport.height,
  ]);
  useEffect(() => {
    if (!pendingFocus.current) return;
    const element = document.getElementById(`timeline-track-${pendingFocus.current}`);
    if (element) {
      element.focus({ preventScroll: true });
      pendingFocus.current = null;
    }
  }, [rows]);
  useEffect(() => {
    gestureRefresh.current?.();
  }, [rows]);
  useEffect(() => {
    if (activeClip && !index.clips.has(activeClip)) {
      gestureRefresh.current = null;
      setActiveClip(null);
    }
    if (!focus) return;
    const clip = focus.clip ? index.clips.get(focus.clip) : undefined;
    if (!index.tracksById.has(focus.track) || (focus.clip && !clip)) {
      setFocus(null);
      grid.current?.focus({ preventScroll: true });
      return;
    }
    if (clip && clip.trackId !== focus.track) {
      pendingClipFocus.current = { id: clip.id, control: focus.control || '.clip-face' };
      setFocus({ ...focus, track: clip.trackId });
    }
  }, [index, activeClip, focus]);
  useEffect(() => {
    const wanted = pendingClipFocus.current;
    if (!wanted) return;
    const clip = [...grid.current!.querySelectorAll<HTMLElement>('[data-clip-id]')].find(
      (item) => item.dataset.clipId === wanted.id,
    );
    const target = clip?.querySelector<HTMLElement>(wanted.control);
    if (target) {
      pendingClipFocus.current = null;
      target.focus({ preventScroll: true });
    }
  }, [rows]);
  useEffect(() => {
    const owner = body.current?.parentElement;
    if (!owner) return;
    // 横向容器会截留纵向滚轮；由轨道共同滚动容器处理，保持标题与内容对齐。
    const wheel = (event: WheelEvent) => {
      if (
        event.ctrlKey ||
        event.metaKey ||
        event.shiftKey ||
        Math.abs(event.deltaX) > Math.abs(event.deltaY)
      )
        return;
      const before = owner.scrollTop;
      const unit = event.deltaMode === 1 ? 16 : event.deltaMode === 2 ? owner.clientHeight : 1;
      owner.scrollTop = Math.max(
        0,
        Math.min(owner.scrollHeight - owner.clientHeight, before + event.deltaY * unit),
      );
      if (owner.scrollTop !== before) event.preventDefault();
    };
    owner.addEventListener('wheel', wheel, { passive: false });
    return () => owner.removeEventListener('wheel', wheel);
  }, []);
  useEffect(() => {
    const sync = () => {
      if (scrubber.current) {
        scrubber.current.value = String(clock.getFrame());
        scrubber.current.setAttribute(
          'aria-valuetext',
          timecode(clock.getFrame(), project.settings),
        );
      }
    };
    sync();
    return clock.subscribe(sync);
  }, [clock, project.settings]);
  const clampHeight = (value: number) => Math.max(175, Math.min(window.innerHeight - 260, value));
  const groupLocked = linkedSelection(project, ids).some(
    (c) => index.tracksById.get(c.trackId)?.track.locked,
  );
  const trimLocked = trimSelectionClips(project, ids).some(
    (c) => index.tracksById.get(c.trackId)?.track.locked,
  );
  const clearGroup = () => {
    for (const element of panel.current?.querySelectorAll<HTMLElement>('[data-clip-id]') || []) {
      const clip = index.clips.get(element.dataset.clipId || '');
      if (!clip) continue;
      element.style.left = `${clip.startFrame * pixelsPerFrame}px`;
      element.style.width = `${Math.max(18, clip.durationFrames * pixelsPerFrame)}px`;
      element.style.transform = '';
      element.style.visibility = '';
      const wave = element.querySelector<HTMLElement>('.clip-waveform');
      if (wave) wave.style.transform = '';
      delete element.dataset.groupInvalid;
    }
  };
  const previewTrim = (selection: string[], edge: 'start' | 'end', delta: number) => {
    const result = groupTrimDraft(project, selection, edge, delta);
    const map = new Map(result.drafts.map((c) => [c.id, c]));
    const hidden = new Set(result.hidden);
    for (const element of panel.current?.querySelectorAll<HTMLElement>('[data-clip-id]') || []) {
      const original = index.clips.get(element.dataset.clipId || ''),
        draft = map.get(element.dataset.clipId || '');
      if (!original || !draft) continue;
      element.style.left = `${draft.startFrame * pixelsPerFrame}px`;
      element.style.width = `${Math.max(18, draft.durationFrames * pixelsPerFrame)}px`;
      element.style.visibility = hidden.has(draft.id) ? 'hidden' : '';
      element.dataset.groupInvalid = String(!result.valid);
      const wave = element.querySelector<HTMLElement>('.clip-waveform');
      if (wave)
        wave.style.transform =
          edge === 'start'
            ? `translateX(${(original.startFrame - draft.startFrame) * pixelsPerFrame}px)`
            : '';
    }
  };
  const previewGroup = (delta: number, trackDelta: number) => {
    const result = groupMoveDraft(project, ids, delta, trackDelta);
    const map = new Map(result.drafts.map((c) => [c.id, c]));
    for (const element of panel.current?.querySelectorAll<HTMLElement>('[data-clip-id]') || []) {
      const clip = index.clips.get(element.dataset.clipId || ''),
        draft = map.get(element.dataset.clipId || '');
      if (!clip || !draft) continue;
      element.style.left = `${draft.startFrame * pixelsPerFrame}px`;
      element.style.transform = `translateY(${(index.tracksById.get(draft.trackId)!.index - index.tracksById.get(clip.trackId)!.index) * ROW_HEIGHT}px)`;
      element.dataset.groupInvalid = String(!result.valid);
    }
  };
  function boxPoint(event: React.PointerEvent<HTMLElement>) {
    const r = event.currentTarget.getBoundingClientRect();
    return { x: Math.max(0, event.clientX - r.left), y: Math.max(0, event.clientY - r.top) };
  }
  function startBox(event: React.PointerEvent<HTMLDivElement>) {
    if (
      !onSelectMany ||
      event.button !== 0 ||
      (event.target as HTMLElement).closest('[data-clip-id]') ||
      busy ||
      (canBeginGesture && !canBeginGesture())
    )
      return;
    event.preventDefault();
    event.currentTarget.focus({ preventScroll: true });
    event.currentTarget.setPointerCapture(event.pointerId);
    const p = boxPoint(event);
    marquee.current = {
      pointer: event.pointerId,
      ...p,
      ids: [],
      base: event.shiftKey || event.ctrlKey || event.metaKey ? ids : [],
      lastX: event.clientX,
      lastY: event.clientY,
    };
    setBox({ ...p, w: 0, h: 0, ids: marquee.current.base });
  }
  function moveBox(event: React.PointerEvent<HTMLDivElement>) {
    if (marquee.current) {
      marquee.current.lastX = event.clientX;
      marquee.current.lastY = event.clientY;
    }
    refreshBox(event.clientX, event.clientY, event.currentTarget);
  }
  function refreshBox(clientX: number, clientY: number, owner: HTMLDivElement) {
    const current = marquee.current;
    if (!current) return;
    const bounds = owner.getBoundingClientRect();
    const p = { x: Math.max(0, clientX - bounds.left), y: Math.max(0, clientY - bounds.top) },
      x = Math.min(current.x, p.x),
      y = Math.min(current.y, p.y),
      w = Math.abs(p.x - current.x),
      h = Math.abs(p.y - current.y);
    const found = new Set(current.base);
    for (
      let row = Math.max(0, Math.floor(y / ROW_HEIGHT));
      row < project.tracks.length && row * ROW_HEIGHT <= y + h;
      row++
    )
      for (const c of index.query(
        project.tracks[row].id,
        x / pixelsPerFrame,
        (x + w) / pixelsPerFrame,
        18 / pixelsPerFrame,
      ))
        found.add(c.id);
    current.ids = [...found];
    setBox({ x, y, w, h, ids: current.ids });
  }
  function endBox(event: React.PointerEvent<HTMLDivElement>, cancel = false) {
    const current = marquee.current;
    if (!current) return;
    marquee.current = null;
    if (event.currentTarget.hasPointerCapture(event.pointerId))
      event.currentTarget.releasePointerCapture(event.pointerId);
    setBox(null);
    event.currentTarget.removeAttribute('data-editor-gesture');
    if (!cancel) onSelectMany?.(current.ids);
  }

  return (
    <section
      ref={panel}
      className="timeline-panel"
      id={sequencePanel?.id}
      role={sequencePanel ? 'tabpanel' : undefined}
      aria-labelledby={sequencePanel?.labelledBy}
      onKeyDown={(event) => {
        if (
          event.defaultPrevented ||
          (event.target as HTMLElement).closest('input,textarea,select,[contenteditable="true"]')
        )
          return;
        if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 'a' && onSelectMany) {
          event.preventDefault();
          onSelectMany(project.clips.map((c) => c.id));
        }
        if (event.key === 'Escape' && onSelectMany) {
          event.preventDefault();
          if (marquee.current) {
            marquee.current = null;
            setBox(null);
          } else onSelectMany([]);
        }
      }}
      aria-label="时间线"
      style={height ? { height } : undefined}
    >
      <div
        className="timeline-resize"
        role="separator"
        tabIndex={0}
        aria-label="调整时间线高度"
        aria-orientation="horizontal"
        aria-valuemin={175}
        aria-valuemax={Math.max(175, window.innerHeight - 260)}
        aria-valuenow={Math.round(height || panel.current?.clientHeight || 285)}
        onPointerDown={(event) => {
          if (event.button !== 0) return;
          event.currentTarget.setPointerCapture(event.pointerId);
          resize.current = { y: event.clientY, height: panel.current?.clientHeight || 285 };
        }}
        onPointerMove={(event) => {
          if (resize.current)
            setHeight(clampHeight(resize.current.height + resize.current.y - event.clientY));
        }}
        onPointerUp={() => {
          resize.current = null;
        }}
        onPointerCancel={() => {
          resize.current = null;
        }}
        onKeyDown={(event) => {
          if (event.key === 'ArrowUp' || event.key === 'ArrowDown') {
            event.preventDefault();
            event.stopPropagation();
            setHeight(
              clampHeight(
                (panel.current?.clientHeight || 285) + (event.key === 'ArrowUp' ? 20 : -20),
              ),
            );
          }
        }}
      >
        <span />
      </div>
      <div className="timeline-toolbar">
        <div className="timeline-title">
          <span className="sequence-dot" />
          {sequencePanel ? '时间线' : project.sequenceName || '主时间线'}
          <span className="quiet-badge">{project.tracks.length} 条轨道</span>
          {onPaste ? (
            <span className="quiet-badge" title={clipboardHint} aria-label={clipboardHint}>
              剪贴板 {clipboard?.available ? clipboard.clipCount : '空'}
            </span>
          ) : null}
        </div>
        <div className="timeline-tools">
          {onRippleApply ? (
            <Suspense fallback={null}>
              <RippleRangeTools
                key={rangeKey}
                project={project}
                sourceEpoch={sourceEpoch}
                clock={clock}
                range={timeRange}
                onRange={(range) =>
                  onMarkedRangeChange
                    ? onMarkedRangeChange(range)
                    : setRangeScope({ key: rangeKey, range })
                }
                busy={busy || !!activeClip || !!box}
                prepareProject={prepareProject}
                applyToProject={onRippleApply}
              />
            </Suspense>
          ) : null}
          {onCopy ? (
            <IconButton
              label="复制选中片段 · ⌘/Ctrl C"
              disabled={busy || !ids.length}
              onPointerDown={(event) => {
                if (event.button === 0) event.preventDefault();
              }}
              onClick={onCopy}
            >
              <ClipboardCopy size={15} />
            </IconButton>
          ) : null}
          {onCut ? (
            <IconButton
              label="剪切选中片段 · ⌘/Ctrl X"
              disabled={busy || !ids.length || groupLocked}
              onPointerDown={(event) => {
                if (event.button === 0) event.preventDefault();
              }}
              onClick={onCut}
            >
              <ScissorsLineDashed size={15} />
            </IconButton>
          ) : null}
          {onPaste ? (
            <IconButton
              label="粘贴到播放头 · ⌘/Ctrl V"
              aria-description={clipboardHint}
              disabled={busy || !clipboard?.available}
              onPointerDown={(event) => {
                if (event.button === 0) event.preventDefault();
              }}
              onClick={onPaste}
            >
              <ClipboardPaste size={15} />
            </IconButton>
          ) : null}
          <IconButton
            label="在播放头分割片段 · C"
            disabled={busy || !ids.length || groupLocked}
            onClick={onSplit}
          >
            <Scissors size={15} />
          </IconButton>
          {onDuplicate ? (
            <IconButton
              label="复制到播放头 · ⌘/Ctrl D"
              disabled={busy || !ids.length || groupLocked}
              onClick={onDuplicate}
            >
              <Copy size={15} />
            </IconButton>
          ) : null}
          <IconButton
            label="删除选中片段 · Delete"
            disabled={busy || !ids.length || groupLocked}
            onClick={onDelete}
          >
            <Trash2 size={15} />
          </IconButton>
          <span className="toolbar-divider" />
          <IconButton label="添加文字" disabled={busy} onClick={onText}>
            <Type size={16} />
          </IconButton>
          <IconButton label="添加轨道" disabled={busy} onClick={onAddTrack}>
            <Plus size={16} />
          </IconButton>
          <IconButton
            label="定位选中片段"
            disabled={!selected || !!activeClip}
            onClick={() => {
              if (selected) revealClip(selected);
            }}
          >
            <LocateFixed size={16} />
          </IconButton>
          <span className="toolbar-divider" />
          <IconButton
            label="缩小时间线"
            disabled={!!activeClip}
            onClick={() => setZoom((z) => Math.max(12, z / 1.3))}
          >
            <Minus size={15} />
          </IconButton>
          <input
            aria-label="时间线缩放"
            className="timeline-zoom"
            type="range"
            min="12"
            max="180"
            value={zoom}
            disabled={!!activeClip}
            onChange={(e) => setZoom(Number(e.target.value))}
          />
          <IconButton
            label="放大时间线"
            disabled={!!activeClip}
            onClick={() => setZoom((z) => Math.min(180, z * 1.3))}
          >
            <Plus size={15} />
          </IconButton>
        </div>
      </div>
      <div
        className="timeline-grid"
        ref={grid}
        tabIndex={0}
        role="region"
        aria-label="时间线滚动区域"
        onScroll={scheduleViewport}
        onFocusCapture={(event) => {
          const target = event.target as HTMLElement;
          const row = target.closest<HTMLElement>('[data-track-id],[data-track-header]');
          if (row)
            setFocus({
              track: row.dataset.trackId || row.dataset.trackHeader || '',
              clip: target.closest<HTMLElement>('[data-clip-id]')?.dataset.clipId,
              control: target.classList.contains('clip-trim-start')
                ? '.clip-trim-start'
                : target.classList.contains('clip-trim-end')
                  ? '.clip-trim-end'
                  : '.clip-face',
            });
          else setFocus(null);
        }}
        onBlurCapture={(event) => {
          if (!event.currentTarget.contains(event.relatedTarget as Node | null)) setFocus(null);
        }}
      >
        <div
          className="track-headers"
          role="list"
          aria-label="时间线轨道"
          style={{ height: TIMELINE_RULER_HEIGHT + project.tracks.length * ROW_HEIGHT }}
        >
          <div
            className="track-header-ruler"
            style={{ transform: `translateY(${viewport.top}px)` }}
          >
            <span>轨道</span>
            <span>可拖动片段边缘修剪</span>
          </div>
          {rows.map(({ track, index: rowIndex, ordinal }) => (
            <div
              className={`track-header ${track.locked ? 'locked' : ''}`}
              key={track.id}
              style={{ height: ROW_HEIGHT, top: TIMELINE_RULER_HEIGHT + rowIndex * ROW_HEIGHT }}
              id={`timeline-track-${track.id}`}
              data-track-header={track.id}
              role="listitem"
              aria-setsize={project.tracks.length}
              aria-posinset={rowIndex + 1}
              tabIndex={0}
              aria-label={`${track.name}，第${rowIndex + 1}条，共${project.tracks.length}条轨道`}
              onKeyDown={(event) => {
                const next =
                  event.key === 'ArrowDown'
                    ? rowIndex + 1
                    : event.key === 'ArrowUp'
                      ? rowIndex - 1
                      : event.key === 'Home'
                        ? 0
                        : event.key === 'End'
                          ? project.tracks.length - 1
                          : event.key === 'PageDown'
                            ? rowIndex + Math.max(1, Math.floor(viewport.height / ROW_HEIGHT))
                            : event.key === 'PageUp'
                              ? rowIndex - Math.max(1, Math.floor(viewport.height / ROW_HEIGHT))
                              : null;
                if (next === null || event.altKey || event.ctrlKey || event.metaKey) return;
                event.preventDefault();
                event.stopPropagation();
                const row = Math.max(0, Math.min(project.tracks.length - 1, next)),
                  id = index.tracks[row]?.track.id;
                if (id) {
                  pendingFocus.current = id;
                  setFocus({ track: id });
                  revealTrack(row);
                }
              }}
            >
              <div className="track-label">
                <span className={`track-index track-${track.kind}`}>
                  {trackIcon(track.kind)}
                  {`${track.kind === 'video' ? 'V' : track.kind === 'audio' ? 'A' : track.kind === 'text' ? 'T' : 'S'}${ordinal}`}
                </span>
                <span>{track.name}</span>
              </div>
              <div className="track-controls">
                {onManageTrack ? (
                  <IconButton
                    label={`设置轨道${track.name}`}
                    disabled={busy}
                    id={`track-settings-${track.id}`}
                    onClick={() => onManageTrack(track)}
                  >
                    <Settings2 size={12} />
                  </IconButton>
                ) : null}
                <IconButton
                  label={track.locked ? `解锁${track.name}` : `锁定${track.name}`}
                  active={track.locked}
                  disabled={busy}
                  onClick={() => onUpdateTrack({ ...track, locked: !track.locked })}
                >
                  {track.locked ? <LockKeyhole size={12} /> : <UnlockKeyhole size={12} />}
                </IconButton>
                {track.kind === 'audio' || track.kind === 'video' ? (
                  <IconButton
                    label={track.muted ? `取消静音${track.name}` : `静音${track.name}`}
                    active={track.muted}
                    disabled={busy}
                    onClick={() => onUpdateTrack({ ...track, muted: !track.muted })}
                  >
                    {track.muted ? <VolumeX size={12} /> : <Volume2 size={12} />}
                  </IconButton>
                ) : (
                  <IconButton
                    label={track.hidden ? `显示${track.name}` : `隐藏${track.name}`}
                    active={track.hidden}
                    disabled={busy}
                    onClick={() => onUpdateTrack({ ...track, hidden: !track.hidden })}
                  >
                    {track.hidden ? <EyeOff size={12} /> : <Eye size={12} />}
                  </IconButton>
                )}
              </div>
            </div>
          ))}
        </div>
        <div ref={body} className="timeline-scroll" onScroll={scheduleViewport}>
          <div
            className="timeline-content"
            style={{ width, minHeight: 32 + project.tracks.length * ROW_HEIGHT }}
          >
            <div className="timeline-ruler" style={{ transform: `translateY(${viewport.top}px)` }}>
              {timeRange.from !== null ? (
                <span
                  className="timeline-range-mark"
                  style={{ left: timeRange.from * pixelsPerFrame }}
                  aria-hidden="true"
                >
                  I
                </span>
              ) : null}
              {timeRange.to !== null ? (
                <span
                  className="timeline-range-mark"
                  style={{ left: timeRange.to * pixelsPerFrame }}
                  aria-hidden="true"
                >
                  O
                </span>
              ) : null}
              {ticks.map((t) => (
                <span className="ruler-tick" style={{ left: t * zoom }} key={t}>
                  <span>{`${String(Math.floor(t / 60)).padStart(2, '0')}:${String(t % 60).padStart(2, '0')}`}</span>
                </span>
              ))}
              <input
                type="range"
                className="ruler-scrubber"
                ref={scrubber}
                aria-label="播放位置（帧）"
                min="0"
                max={Math.max(1, Math.round(width / pixelsPerFrame))}
                step="1"
                defaultValue="0"
                onInput={(e) => clock.seek(Number(e.currentTarget.value))}
              />
            </div>
            <div
              className="timeline-rows"
              ref={rowsOwner}
              tabIndex={-1}
              aria-label="时间线片段区域"
              data-editor-gesture={box ? 'true' : undefined}
              onPointerDown={startBox}
              onPointerMove={moveBox}
              onPointerUp={(event) => endBox(event)}
              onPointerCancel={(event) => endBox(event, true)}
              onLostPointerCapture={(event) => endBox(event, true)}
              style={{ height: project.tracks.length * ROW_HEIGHT }}
            >
              {timeRange.from !== null && timeRange.to !== null && timeRange.from < timeRange.to ? (
                <div
                  className="timeline-time-range"
                  aria-hidden="true"
                  style={{
                    left: Math.max(viewport.left, timeRange.from * pixelsPerFrame),
                    width: Math.max(
                      0,
                      Math.min(viewport.left + viewport.width, timeRange.to * pixelsPerFrame) -
                        Math.max(viewport.left, timeRange.from * pixelsPerFrame),
                    ),
                  }}
                />
              ) : null}
              {box ? (
                <div
                  className="timeline-marquee"
                  aria-hidden="true"
                  style={{ left: box.x, top: box.y, width: box.w, height: box.h }}
                />
              ) : null}
              {rows.map(({ track, index: rowIndex, clips }) => (
                <div
                  className={`timeline-row ${track.locked ? 'locked' : ''}`}
                  data-track-id={track.id}
                  data-parity={rowIndex % 2 === 1 ? 'even' : 'odd'}
                  key={track.id}
                  style={{ height: ROW_HEIGHT, top: rowIndex * ROW_HEIGHT }}
                >
                  {clips.map((clip) => (
                    <TimelineClip
                      key={clip.id}
                      clip={clip}
                      project={project}
                      selected={visibleSelection.has(clip.id)}
                      selectionSize={ids.length}
                      trim={
                        onTrimSelection
                          ? {
                              locked: visibleSelection.has(clip.id) ? trimLocked : false,
                              preview: (edge, delta) =>
                                previewTrim(
                                  visibleSelection.has(clip.id) ? ids : [clip.id],
                                  edge,
                                  delta,
                                ),
                              finish: (edge, delta) =>
                                onTrimSelection(
                                  visibleSelection.has(clip.id) ? ids : [clip.id],
                                  edge,
                                  delta,
                                ),
                              clear: clearGroup,
                            }
                          : undefined
                      }
                      group={
                        ids.length > 1 && visibleSelection.has(clip.id) && onMoveSelection
                          ? {
                              ids,
                              preview: previewGroup,
                              clear: clearGroup,
                              finish: onMoveSelection,
                            }
                          : undefined
                      }
                      clock={clock}
                      pixelsPerFrame={pixelsPerFrame}
                      onSelect={(additive) =>
                        additive ? onSelect(clip.id, true) : onSelect(clip.id)
                      }
                      onUpdate={onUpdateClip}
                      disabled={busy}
                      canBeginGesture={canBeginGesture}
                      viewLeft={viewport.left}
                      viewWidth={viewport.width}
                      index={index}
                      scroll={scroll}
                      onGesture={gestureChanged}
                    />
                  ))}
                </div>
              ))}
              <Playhead
                clock={clock}
                pixelsPerFrame={pixelsPerFrame}
                verticalOffset={viewport.top}
              />
            </div>
            {project.clips.length === 0 ? (
              <div className="timeline-empty">
                选择素材后双击添加，或先添加一段文字<span>所有编辑都可撤销</span>
              </div>
            ) : null}
          </div>
        </div>
      </div>
    </section>
  );
}
