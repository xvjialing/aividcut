import { useCallback, useEffect, useRef, useState } from 'react';
import { bridge } from './bridge';
import type { ClipboardStatus } from '../clipboard-types';

export function useTimelineClipboard(projectId: string | undefined, sourceEpoch = 0) {
  const [status, setStatus] = useState<ClipboardStatus | null>(null);
  const [error, setError] = useState('');
  const key = `${projectId || ''}/${sourceEpoch}`;
  const scope = useRef({ projectId, sourceEpoch, key, active: true });
  scope.current = { projectId, sourceEpoch, key, active: true };
  const latest = useRef<ClipboardStatus | null>(null);
  const pending = useRef<{ key: string; task: Promise<void> } | null>(null);
  const accept = useCallback((next: ClipboardStatus) => {
    const current = scope.current;
    if (
      !current.active ||
      !next ||
      next.projectId !== current.projectId ||
      next.sourceEpoch !== current.sourceEpoch
    )
      return;
    const old = latest.current;
    if (
      old &&
      old.projectId === next.projectId &&
      old.sourceEpoch === next.sourceEpoch &&
      old.version > next.version
    )
      return;
    latest.current = next;
    setStatus(next);
    setError('');
  }, []);
  const refresh = useCallback((): Promise<void> => {
    const current = scope.current;
    if (!current.projectId || !current.active) return Promise.resolve();
    if (pending.current?.key === current.key) return pending.current.task;
    let task: Promise<void>;
    task = (async () => {
      try {
        if (typeof bridge.clipboardStatus !== 'function') throw Error('剪贴板接口尚未就绪');
        const next = await bridge.clipboardStatus();
        accept(next);
      } catch (cause) {
        if (scope.current.active && scope.current.key === current.key)
          setError(cause instanceof Error ? cause.message : '无法读取剪贴板状态');
      }
    })().finally(() => {
      if (pending.current?.task === task) pending.current = null;
    });
    pending.current = { key: current.key, task };
    return task;
  }, [accept]);
  useEffect(() => {
    scope.current.active = true;
    latest.current = null;
    setStatus(null);
    setError('');
    if (!projectId) return;
    void refresh();
    const id = window.setInterval(() => {
      if (!document.hidden) void refresh();
    }, 2500);
    const visible = () => {
      if (!document.hidden) void refresh();
    };
    document.addEventListener('visibilitychange', visible);
    return () => {
      if (scope.current.key === key) scope.current.active = false;
      window.clearInterval(id);
      document.removeEventListener('visibilitychange', visible);
    };
  }, [projectId, sourceEpoch, key, refresh]);
  const current =
    status && status.projectId === projectId && status.sourceEpoch === sourceEpoch ? status : null;
  return { status: current, error, accept, refresh };
}

export type TimelineClipboardAction = 'copy' | 'cut' | 'paste';
export function timelineClipboardShortcut(
  event: KeyboardEvent,
): TimelineClipboardAction | undefined {
  if (
    event.defaultPrevented ||
    event.isComposing ||
    !(event.metaKey || event.ctrlKey) ||
    event.altKey ||
    event.shiftKey
  )
    return;
  const target = event.target;
  if (
    !(target instanceof Element) ||
    !target.closest('.timeline-panel') ||
    target.closest(
      'input,textarea,select,[contenteditable="true"],[role="dialog"],[role="alertdialog"]',
    )
  )
    return;
  return ({ c: 'copy', x: 'cut', v: 'paste' } as const)[event.key.toLowerCase() as 'c' | 'x' | 'v'];
}
