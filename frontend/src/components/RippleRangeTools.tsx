import { useEffect, useId, useRef, useState } from 'react';
import type { Bootstrap, Project } from '../types';
import type { RippleAPI, TimelineRange } from '../ripple-types';
import type { WorkflowPreview } from '../workflow-types';
import { rippleAPI } from '../lib/bridge';
import { PlaybackClock, projectEnd, timecode } from '../lib/editor';
import { Button, Field, Input, Modal } from './ui';
export function RippleRangeTools({
  project,
  sourceEpoch,
  clock,
  range,
  onRange,
  busy,
  prepareProject,
  applyToProject,
  api = rippleAPI,
}: {
  project: Project;
  sourceEpoch: number;
  clock: PlaybackClock;
  range: TimelineRange;
  onRange: (range: TimelineRange) => void;
  busy: boolean;
  prepareProject?: () => Promise<Project | null>;
  applyToProject: (action: () => Promise<Bootstrap>) => Promise<Bootstrap | null>;
  api?: RippleAPI;
}) {
  const end = projectEnd(project);
  const errorID = useId();
  const form = useRef<HTMLFormElement>(null);
  const [invalid, setInvalid] = useState<{ from?: boolean; to?: boolean }>({});
  const [open, setOpen] = useState(false);
  const [from, setFrom] = useState('0');
  const [to, setTo] = useState('1');
  const [error, setError] = useState('');
  const [working, setWorking] = useState('');
  const [preview, setPreview] = useState<WorkflowPreview | null>(null);
  const pending = useRef(false);
  const [limit, setLimit] = useState(20);
  const base = `${project.id}:${sourceEpoch}`;
  const scope = `${base}:${project.revision}`;
  const latest = useRef(scope);
  latest.current = scope;
  const latestBase = useRef(base);
  latestBase.current = base;
  const alive = useRef(true);
  useEffect(() => {
    alive.current = true;
    return () => {
      alive.current = false;
    };
  }, []);
  useEffect(() => {
    setPreview(null);
    setError('');
    setLimit(20);
  }, [scope]);
  useEffect(() => {
    setOpen(false);
  }, [base]);
  const valid = (a: number, b: number) =>
    Number.isSafeInteger(a) && Number.isSafeInteger(b) && a >= 0 && a < b && b <= end;
  const marked = range.from !== null && range.to !== null && valid(range.from, range.to);
  function mark(edge: 'from' | 'to') {
    if (busy || pending.current || end < 1) return;
    clock.pause();
    const frame = Math.max(0, Math.min(clock.getFrame(), end));
    onRange({ ...range, [edge]: edge === 'to' ? Math.min(end, frame + 1) : frame });
  }
  useEffect(() => {
    const key = (event: KeyboardEvent) => {
      const target = event.target instanceof Element ? event.target : null;
      if (
        event.defaultPrevented ||
        event.isComposing ||
        event.ctrlKey ||
        event.metaKey ||
        event.altKey ||
        event.shiftKey ||
        busy ||
        open ||
        target?.closest(
          'input,textarea,select,[contenteditable="true"],[role="dialog"],[role="alertdialog"]',
        )
      )
        return;
      if (event.key.toLowerCase() === 'i' || event.key.toLowerCase() === 'o') {
        event.preventDefault();
        mark(event.key.toLowerCase() === 'i' ? 'from' : 'to');
      }
    };
    window.addEventListener('keydown', key);
    return () => window.removeEventListener('keydown', key);
  }, [scope, range, busy, open]);
  function start() {
    if (busy || end < 1) return;
    clock.pause();
    const first = range.from ?? Math.min(clock.getFrame(), Math.max(0, end - 1));
    setFrom(String(first));
    setTo(String(range.to ?? Math.min(end, first + 1)));
    setPreview(null);
    setError('');
    setOpen(true);
  }
  function change(edge: 'from' | 'to', text: string) {
    if (edge === 'from') setFrom(text);
    else setTo(text);
    setPreview(null);
    setError('');
    setInvalid({});
  }
  async function inspect(event: React.FormEvent) {
    event.preventDefault();
    if (pending.current || busy) return;
    const a = Number(from),
      b = Number(to);
    if (!from.trim() || !to.trim() || !valid(a, b)) {
      setError('请输入工程内的整数帧，终点不含且必须大于起点。');
      setInvalid({
        from: !from.trim() || !Number.isSafeInteger(a) || a < 0 || a >= end,
        to: !to.trim() || !Number.isSafeInteger(b) || b <= a || b > end,
      });
      window.setTimeout(
        () => form.current?.querySelector<HTMLElement>('[aria-invalid="true"]')?.focus(),
        0,
      );
      return;
    }
    pending.current = true;
    setWorking('preview');
    setError('');
    setPreview(null);
    const context = scope;
    try {
      const ready = prepareProject ? await prepareProject() : project;
      if (!alive.current || latest.current !== context) return;
      if (!ready || ready.id !== project.id || ready.revision !== project.revision)
        throw Error('工程版本已变化，请重新核对后预览。');
      onRange({ from: a, to: b });
      const value = await api.preview({
        projectId: project.id,
        projectRevision: project.revision,
        sourceEpoch,
        fromFrame: a,
        toFrame: b,
      });
      if (alive.current && latest.current === context) {
        setPreview(value);
        setLimit(20);
      }
    } catch (cause) {
      if (alive.current && latest.current === context)
        setError(cause instanceof Error ? cause.message : '区间预览失败，工程保留。');
    } finally {
      pending.current = false;
      if (alive.current) setWorking('');
    }
  }
  async function apply() {
    if (!preview || busy || pending.current || preview.projectRevision !== project.revision) return;
    pending.current = true;
    setWorking('apply');
    setError('');
    const context = base;
    const first = Number(from);
    try {
      const value = await applyToProject(() => api.apply(preview.id));
      if (alive.current && latestBase.current === context) {
        if (value) {
          onRange({ from: null, to: null });
          clock.pause();
          clock.seek(Math.min(first, projectEnd(value.project)));
          setPreview(null);
          setOpen(false);
        } else setError('尚未应用，请先处理工程草稿或冲突，再重新预览。');
      }
    } catch (cause) {
      if (alive.current && latestBase.current === context)
        setError(cause instanceof Error ? cause.message : '应用失败，请重新预览。');
    } finally {
      pending.current = false;
      if (alive.current) setWorking('');
    }
  }
  const summary = preview?.rangeSummary;
  const disabled = busy || !!working;
  return (
    <>
      {range.from !== null || range.to !== null ? (
        <span className="quiet-badge" aria-label="时间区间标记">
          I {range.from ?? '—'} · O {range.to ?? '—'}
          {marked ? '' : ' · 待补全'}
        </span>
      ) : null}
      <Button
        variant="ghost"
        disabled={busy || end < 1}
        onClick={start}
        title="I设置入点，O设置包含当前帧的出点；全轨波纹删除前会核对影响。"
      >
        时间区间
      </Button>
      <Modal
        open={open}
        title="时间区间与全轨波纹删除"
        description="删除明确区间，所有轨道的后段统一前移。含未选、隐藏和静音轨，原片与原话保留，可一次撤销。"
        busy={!!working}
        onOpenChange={(value) => {
          if (!working) setOpen(value);
        }}
      >
        <form ref={form} aria-busy={!!working} noValidate onSubmit={(event) => void inspect(event)}>
          <div className="field-pair">
            <Field label="入点 · 帧">
              <Input
                autoFocus
                type="number"
                min={0}
                max={end}
                step={1}
                value={from}
                aria-invalid={invalid.from || undefined}
                aria-describedby={invalid.from ? errorID : undefined}
                disabled={disabled}
                onChange={(event) => change('from', event.target.value)}
              />
            </Field>
            <Field label="出点 · 帧（不含）">
              <Input
                type="number"
                min={1}
                max={end}
                step={1}
                value={to}
                aria-invalid={invalid.to || undefined}
                aria-describedby={invalid.to ? errorID : undefined}
                disabled={disabled}
                onChange={(event) => change('to', event.target.value)}
              />
            </Field>
          </div>
          <div className="button-row">
            <Button
              disabled={disabled}
              onClick={() => change('from', String(Math.min(clock.getFrame(), end)))}
            >
              播放头设入点
            </Button>
            <Button
              disabled={disabled}
              onClick={() => change('to', String(Math.min(clock.getFrame() + 1, end)))}
            >
              播放头设出点
            </Button>
            <Button
              disabled={disabled}
              onClick={() => {
                onRange({ from: null, to: null });
                setPreview(null);
              }}
            >
              清除标记
            </Button>
          </div>
          <p className="field-hint">
            O包含当前播放头帧，保存为终点+1。工程末端 {end}帧 · {timecode(end, project.settings)}
            。独立字幕/文字保留原文，人工整句不可安全拆分时拒绝。
          </p>
          <Button type="submit" disabled={disabled} busy={working === 'preview'}>
            预览全轨影响
          </Button>
          {preview ? (
            <section className="ripple-review" aria-label="全轨影响预览">
              <p>{preview.summary}</p>
              {summary ? (
                <p>
                  删除{summary.deleted} · 分割{summary.split} · 修剪{summary.trimmed} · 前移
                  {summary.moved} · 字幕跟随{summary.captionChanges} · 含新增共影响
                  {summary.totalAffectedClips}个片段
                </p>
              ) : null}
              {preview.warnings.map((value) => (
                <p key={value} className="speech-warning">
                  {value}
                </p>
              ))}
              <ul>
                {preview.effects.slice(0, limit).map((effect, index) => (
                  <li key={index}>
                    {project.tracks.find((track) => track.id === effect.trackId)?.name ||
                      effect.trackId}{' '}
                    · {effect.description} · 原帧 {effect.startFrame}–{effect.endFrame}
                  </li>
                ))}
              </ul>
              {preview.effects.length > limit ? (
                <Button disabled={disabled} onClick={() => setLimit((value) => value + 20)}>
                  再显示20项
                </Button>
              ) : null}
            </section>
          ) : null}
          <p id={errorID} className="form-error" role="alert">
            {error}
          </p>
          <div className="dialog-actions">
            <Button disabled={!!working} onClick={() => setOpen(false)}>
              保留时间线
            </Button>
            <Button
              variant="danger"
              disabled={disabled || !preview || preview.projectRevision !== project.revision}
              busy={working === 'apply'}
              onClick={() => void apply()}
            >
              应用全轨删除
            </Button>
          </div>
        </form>
      </Modal>
    </>
  );
}
