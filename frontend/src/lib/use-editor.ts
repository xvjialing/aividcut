import { useCallback, useEffect, useRef, useState } from 'react';
import { bridge, mcpBridge } from './bridge';
import { hasEditorDraft, hasTimelineGesture } from './close-protection';
import type { MCPStatus } from '../mcp-types';
import { fps, PlaybackClock, projectEnd } from './editor';
import type { Bootstrap, Command, Job } from '../types';

export function useEditor() {
  const [bootstrap, setBootstrap] = useState<Bootstrap | null>(null);
  const stateRef = useRef<Bootstrap | null>(null);
  const getCurrentBootstrap = useCallback(() => stateRef.current, []);
  const [mcpStatus, setMCPStatus] = useState<MCPStatus | null>(null);
  const mcpReading = useRef(false);
  const mcpEpoch = useRef(0);
  const reconciledExecutions = useRef(new Map<string, number>());
  const [externalChange, setExternalChange] = useState<{ revision: number; reason: string } | null>(
    null,
  );
  const externalRef = useRef<typeof externalChange>(null);
  const [draftEpoch, setDraftEpoch] = useState(0);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState('');
  const pending = useRef(false);
  const pendingTask = useRef<Promise<Bootstrap | null> | null>(null);
  const failedDraft = useRef(false);
  const [draftFailure, setDraftFailure] = useState(false);
  const [notice, setNotice] = useState('');
  const [jobs, setJobs] = useState<Job[]>([]);
  const clockRef = useRef(new PlaybackClock());

  const accept = useCallback((value: Bootstrap) => {
    const sequenceChanged =
      stateRef.current &&
      (stateRef.current.project.sequenceId || 'sequence-main') !==
        (value.project.sequenceId || 'sequence-main');
    if (
      stateRef.current &&
      (stateRef.current.project.id !== value.project.id ||
        stateRef.current.project.revision !== value.project.revision)
    )
      clockRef.current.invalidateExternal();
    value.project.assets ??= [];
    value.project.clips ??= [];
    value.project.tracks ??= [];
    stateRef.current = value;
    externalRef.current = null;
    failedDraft.current = false;
    setDraftFailure(false);
    setExternalChange(null);
    setBootstrap(value);
    clockRef.current.configure(projectEnd(value.project), fps(value.project.settings));
    if (sequenceChanged) clockRef.current.seek(0);
    document.title = `${value.project.name}${value.dirty ? ' •' : ''} · AIVidCut`;
  }, []);
  const deferExternal = useCallback(
    (revision: number, reason = '外部工具已更改工程，本地属性草稿已保留。') => {
      const value = { revision, reason };
      externalRef.current = value;
      setExternalChange(value);
    },
    [],
  );

  const run = useCallback(
    (label: string, action: () => Promise<Bootstrap>) => {
      if (pending.current) return Promise.resolve(null);
      if (externalRef.current) {
        setError('请先处理外部工程更改，再继续提交或保存。当前属性草稿保留。');
        return Promise.resolve(null);
      }
      const preserveDraft = hasEditorDraft();
      const task = (async () => {
        pending.current = true;
        setBusy(label);
        setError('');
        try {
          const value = await action();
          accept(value);
          setNotice(label);
          return value;
        } catch (cause) {
          failedDraft.current = preserveDraft || hasEditorDraft();
          setDraftFailure(failedDraft.current);
          setError(cause instanceof Error ? cause.message : '操作未完成，请重试。');
          try {
            const latest = await bridge.bootstrap();
            if (preserveDraft || hasEditorDraft()) {
              if (
                latest.project.id !== stateRef.current?.project.id ||
                latest.project.revision !== stateRef.current?.project.revision
              )
                deferExternal(latest.project.revision);
            } else accept(latest);
          } catch {
            /* 保留已有工程，错误继续显示。 */
          }
          return null;
        } finally {
          pending.current = false;
          setBusy('');
        }
      })();
      pendingTask.current = task;
      return task;
    },
    [accept, deferExternal],
  );
  const waitForIdle = useCallback(async () => {
    const success = !pending.current || (await pendingTask.current) !== null;
    if (failedDraft.current && !hasEditorDraft()) {
      failedDraft.current = false;
      setDraftFailure(false);
    }
    return (
      success &&
      !externalRef.current &&
      !(failedDraft.current && hasEditorDraft()) &&
      !hasTimelineGesture()
    );
  }, []);
  const protectDraft = useCallback(() => {
    failedDraft.current = true;
    setDraftFailure(true);
  }, []);
  const canBeginInteraction = useCallback(
    () => !externalRef.current && !pending.current && !hasEditorDraft() && !hasTimelineGesture(),
    [],
  );

  const refresh = useCallback(() => run('读取工程', bridge.bootstrap), [run]);
  useEffect(() => {
    void refresh();
    return () => clockRef.current.destroy();
  }, [refresh]);
  const syncExternalRevision = useCallback(
    async (projectId: string, revision: number, force = false) => {
      const current = stateRef.current;
      if (
        !current ||
        projectId !== current.project.id ||
        (!force && revision === current.project.revision)
      )
        return false;
      if (externalRef.current) {
        deferExternal(revision, externalRef.current.reason);
        return false;
      }
      if (hasTimelineGesture()) {
        deferExternal(
          revision,
          '外部工具在拖动过程中修改了工程。请结束拖动，重新读取后重试移动或修剪。',
        );
        return false;
      }
      if (pending.current || hasEditorDraft()) {
        deferExternal(revision);
        return false;
      }
      const latest = await bridge.bootstrap();
      if (
        stateRef.current?.project.id !== current.project.id ||
        stateRef.current?.project.revision !== current.project.revision
      )
        return false;
      if (externalRef.current) return false;
      if (hasTimelineGesture()) {
        deferExternal(
          latest.project.revision,
          '外部工具在拖动过程中修改了工程。请结束拖动，重新读取后重试移动或修剪。',
        );
        return false;
      }
      if (pending.current || hasEditorDraft()) {
        deferExternal(latest.project.revision);
        return false;
      }
      accept(latest);
      return true;
    },
    [accept, deferExternal],
  );
  const acceptMCPStatus = useCallback(
    async (status: MCPStatus) => {
      mcpEpoch.current++;
      setMCPStatus(status);
      if (!status.projectId) return;
      const ended = Boolean(
        status.executionId &&
        !status.running &&
        reconciledExecutions.current.get(status.executionId) !== status.projectRevision,
      );
      if (ended && status.executionId)
        reconciledExecutions.current.set(status.executionId, status.projectRevision);
      if ((status.running && status.scopeValid) || status.executionId)
        await syncExternalRevision(status.projectId, status.projectRevision, ended);
    },
    [syncExternalRevision],
  );
  const readMCPStatus = useCallback(async () => {
    if (mcpReading.current) return;
    mcpReading.current = true;
    const epoch = mcpEpoch.current;
    try {
      const status = await mcpBridge.getMCPStatus();
      if (epoch === mcpEpoch.current) await acceptMCPStatus(status);
    } finally {
      mcpReading.current = false;
    }
  }, [acceptMCPStatus]);
  useEffect(() => {
    if (!bootstrap) return;
    // 旧桌面桥缺少能力时由MCP面板解释，不让编辑器启动被可选能力阻断。
    void readMCPStatus().catch(() => {});
  }, [Boolean(bootstrap), readMCPStatus]);
  useEffect(() => {
    if (!mcpStatus?.running) return;
    const interval = window.setInterval(() => void readMCPStatus().catch(() => {}), 1500);
    return () => clearInterval(interval);
  }, [mcpStatus?.running, readMCPStatus]);
  const reloadExternal = useCallback(async () => {
    if (pending.current) await pendingTask.current;
    try {
      const latest = await bridge.bootstrap();
      accept(latest);
      setDraftEpoch((value) => value + 1);
      setError('');
      return true;
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : '重新读取失败，当前草稿保留。');
      return false;
    }
  }, [accept]);
  useEffect(() => {
    if (!bootstrap) return;
    let mounted = true;
    const loadJobs = () =>
      bridge
        .jobs()
        .then((list) => {
          if (mounted) setJobs(list || []);
        })
        .catch(() => {});
    void loadJobs();
    const interval = window.setInterval(loadJobs, 1500);
    return () => {
      mounted = false;
      clearInterval(interval);
    };
  }, [Boolean(bootstrap)]);
  useEffect(() => {
    if (!bootstrap?.dirty) return;
    const handler = (event: BeforeUnloadEvent) => {
      event.preventDefault();
      event.returnValue = '';
    };
    window.addEventListener('beforeunload', handler);
    return () => window.removeEventListener('beforeunload', handler);
  }, [bootstrap?.dirty]);

  const edit = useCallback(
    (label: string, commands: Command[]) => {
      const current = stateRef.current;
      if (!current) return Promise.resolve(null);
      return run(label, () =>
        bridge.apply({
          baseRevision: current.project.revision,
          idempotencyKey: crypto.randomUUID(),
          label,
          commands,
        }),
      );
    },
    [run],
  );

  return {
    bootstrap,
    getCurrentBootstrap,
    error,
    setError,
    busy,
    notice,
    setNotice,
    jobs,
    setJobs,
    clock: clockRef.current,
    run,
    refresh,
    edit,
    waitForIdle,
    acceptBootstrap: accept,
    mcpStatus,
    acceptMCPStatus,
    externalChange,
    draftEpoch,
    reloadExternal,
    draftFailure,
    protectDraft,
    canBeginInteraction,
    syncExternalRevision,
  };
}
