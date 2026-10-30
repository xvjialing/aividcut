import { useEffect, useRef, useState } from 'react';
import type { FormEvent } from 'react';
import type { DraftField, InspectorDraftAPI } from '../inspector-draft-types';
import type { ProjectScope } from '../types';
import { inspectorDraftAPI } from './bridge';

// 仅收集显式登记的属性控件；不读任意输入或浏览器持久存储。
export function useInspectorDrafts(
  scope: ProjectScope,
  target: string,
  api: InspectorDraftAPI = inspectorDraftAPI,
) {
  const latest = useRef(scope);
  latest.current = scope;
  const [status, setStatus] = useState('');
  const state = useRef({
    fields: new Map<string, DraftField>(),
    sequence: 0,
    written: 0,
    timer: undefined as ReturnType<typeof setTimeout> | undefined,
    statusTimer: undefined as ReturnType<typeof setTimeout> | undefined,
    composing: false,
    inflight: false,
    queued: false,
    mounted: true,
    scope,
  });
  async function flush() {
    const s = state.current;
    if (s.composing || (!s.fields.size && s.sequence === s.written)) return;
    if (s.inflight) {
      s.queued = true;
      return;
    }
    s.inflight = true;
    s.queued = false;
    const sequence = s.sequence;
    const input = {
      ...s.scope,
      target,
      fields: Array.from(s.fields.values()).map((f) => ({ ...f })),
    };
    try {
      const record = await api.save(input);
      s.written = sequence;
      const kept = new Set(record?.fields.map((f) => f.key) || []);
      for (const f of input.fields) {
        if (!kept.has(f.key) && s.fields.get(f.key)?.raw === f.raw) s.fields.delete(f.key);
      }
      if (s.mounted)
        setStatus(
          s.sequence === sequence
            ? record
              ? '输入草稿已保护'
              : '当前属性无未提交草稿'
            : '输入草稿等待保护…',
        );
    } catch (cause) {
      if (s.mounted)
        setStatus(
          `草稿未保护：${cause instanceof Error ? cause.message : '请及时提交并保存工程。'}`,
        );
    } finally {
      s.inflight = false;
      if (s.queued) {
        s.queued = false;
        void flush();
      }
    }
  }
  useEffect(() => {
    if (state.current.fields.size && !state.current.composing) void flush();
  }, [scope.projectRevision]);
  function capture(event: FormEvent<HTMLDivElement>) {
    const input = event.target;
    if (!(input instanceof HTMLInputElement || input instanceof HTMLTextAreaElement)) return;
    const key = input.dataset.inspectorKey,
      base = input.dataset.inspectorBase;
    if (!key || base === undefined || state.current.composing) return;
    const s = state.current;
    const old = s.fields.get(key);
    s.sequence++;
    if (
      input.dataset.inspectorDisplay !== undefined &&
      (input.value === input.dataset.inspectorDisplay ||
        (input.type === 'number' &&
          input.value.trim() !== '' &&
          input.dataset.inspectorDisplay !== undefined &&
          Number(input.value) === Number(input.dataset.inspectorDisplay)))
    )
      s.fields.delete(key);
    else
      s.fields.set(key, {
        key,
        raw: input.value,
        base: old?.base ?? (JSON.parse(base) as string | number),
      });
    s.scope = latest.current;
    if (s.timer) clearTimeout(s.timer);
    // 捕获阶段先保存原始输入，提示延后刷新，避免覆盖冒泡阶段尚未更新的受控值。
    if (s.statusTimer) clearTimeout(s.statusTimer);
    s.statusTimer = setTimeout(() => {
      s.statusTimer = undefined;
      if (s.mounted && !s.composing && s.sequence !== s.written) setStatus('输入草稿等待保护…');
    }, 0);
    s.timer = setTimeout(() => {
      s.timer = undefined;
      void flush();
    }, 1000);
  }
  useEffect(() => {
    const s = state.current;
    s.mounted = true;
    return () => {
      s.mounted = false;
      if (s.timer) clearTimeout(s.timer);
      if (s.statusTimer) clearTimeout(s.statusTimer);
      void flush();
    };
  }, []);
  return {
    status,
    events: {
      onInputCapture: capture,
      onCompositionStartCapture: () => {
        const s = state.current;
        s.composing = true;
        if (s.timer) clearTimeout(s.timer);
        if (s.statusTimer) clearTimeout(s.statusTimer);
        s.statusTimer = undefined;
      },
      onCompositionEndCapture: (event: FormEvent<HTMLDivElement>) => {
        state.current.composing = false;
        capture(event);
      },
    },
  };
}
