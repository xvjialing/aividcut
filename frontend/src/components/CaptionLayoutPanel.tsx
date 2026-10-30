import { useEffect, useRef, useState } from 'react';
import type { Bootstrap, Project, Transcript } from '../types';
import type { CaptionLayoutInput, CaptionLayoutPreview } from '../speech-types';
import { captionLayoutDraft, parseCaptionLayout } from '../lib/caption-layout';
import { sourceCaptionOverrides } from '../lib/speech-timing';
import { framesToSeconds } from '../lib/editor';
import { CaptionLayoutEditor } from './CaptionLayoutEditor';
import { Button, Field, Select } from './ui';
export function CaptionLayoutPanel({
  project,
  transcript,
  busy,
  blockedReason,
  prepareProject,
  onPreview,
  onApply,
}: {
  project: Project;
  transcript: Transcript;
  busy: boolean;
  blockedReason?: string;
  prepareProject?: () => Promise<Project | null>;
  onPreview: (input: CaptionLayoutInput) => Promise<CaptionLayoutPreview>;
  onApply: (id: string) => Promise<Bootstrap | null>;
}) {
  const [draft, setDraft] = useState(() => captionLayoutDraft(transcript.captionLayout));
  const [trackId, setTrackId] = useState(transcript.captionTrackId || '');
  const [targetNotice, setTargetNotice] = useState('');
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [error, setError] = useState('');
  const [preview, setPreview] = useState<CaptionLayoutPreview | null>(null);
  const [working, setWorking] = useState('');
  const pending = useRef(false);
  const targetContext = useRef(`${project.id}:${transcript.id}`);
  const form = useRef<HTMLFormElement>(null);
  const [limit, setLimit] = useState(20);
  const protectedCaptions = sourceCaptionOverrides(project, transcript.id);
  const targetUnavailable =
    trackId !== '' &&
    !project.tracks.some((track) => track.id === trackId && track.kind === 'caption');
  const fallbackTrack = project.tracks.find(
    (track) => track.id === transcript.captionTrackId && track.kind === 'caption',
  );
  const effectiveTrackId = targetUnavailable ? fallbackTrack?.id || '' : trackId;
  useEffect(() => {
    setDraft(captionLayoutDraft(transcript.captionLayout));
    setTrackId(transcript.captionTrackId || '');
    setPreview(null);
    setErrors({});
    setTargetNotice('');
  }, [project.id, transcript.id]);
  useEffect(() => {
    const context = `${project.id}:${transcript.id}`;
    if (targetContext.current !== context) {
      targetContext.current = context;
      return;
    }
    if (!targetUnavailable) return;
    setTrackId(effectiveTrackId);
    setPreview(null);
    setError('');
    setTargetNotice(
      effectiveTrackId
        ? `原目标轨已不可用，已改用“${fallbackTrack?.name || '当前字幕轨'}”。排版参数保留，请重新预览。`
        : '原目标轨已不可用，已切换为新建字幕轨。排版参数保留，请重新预览。',
    );
  }, [project.id, transcript.id, targetUnavailable, effectiveTrackId, fallbackTrack?.name]);
  const stale = Boolean(
    preview &&
    (preview.applied ||
      preview.projectId !== project.id ||
      preview.projectRevision !== project.revision ||
      preview.transcriptId !== transcript.id ||
      preview.transcriptRevision !== transcript.revision),
  );
  const disabled = busy || Boolean(working);
  async function inspect(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (pending.current || busy || blockedReason || transcript.stale) return;
    const parsed = parseCaptionLayout(draft);
    setErrors(parsed.errors);
    if (!parsed.value) {
      setTimeout(
        () => form.current?.querySelector<HTMLElement>('[aria-invalid="true"]')?.focus(),
        0,
      );
      return;
    }
    setPreview(null);
    pending.current = true;
    setWorking('读取分句预览');
    setError('');
    try {
      const current = prepareProject ? await prepareProject() : project;
      if (!current) {
        setError('请先处理未提交的属性或工程更改。');
        return;
      }
      if (
        current.id !== project.id ||
        current.revision !== project.revision ||
        current.transcripts?.find((value) => value.id === transcript.id)?.revision !==
          transcript.revision
      ) {
        setError('字幕来源或工程已变化，请重新核对。');
        return;
      }
      const value = await onPreview({
        projectId: current.id,
        projectRevision: current.revision,
        transcriptId: transcript.id,
        transcriptRevision: transcript.revision,
        trackId: effectiveTrackId,
        ...(effectiveTrackId === '' ? { createNewTrack: true } : {}),
        layout: parsed.value,
      });
      setPreview(value);
      setLimit(20);
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : '字幕分句预览未完成，现有字幕保留。');
    } finally {
      pending.current = false;
      setWorking('');
    }
  }
  async function apply() {
    if (!preview || stale || disabled || pending.current || blockedReason) return;
    pending.current = true;
    setWorking('应用字幕分句');
    setError('');
    try {
      const value = await onApply(preview.id);
      if (value) {
        setPreview(null);
        const accepted = value.project.transcripts?.find((source) => source.id === transcript.id);
        setDraft(captionLayoutDraft(accepted?.captionLayout));
        setTrackId(accepted?.captionTrackId || '');
      } else setError('字幕排版未提交，请核对工程错误后重试。');
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : '字幕排版未提交，现有字幕保留。');
    } finally {
      pending.current = false;
      setWorking('');
    }
  }
  return (
    <section className="caption-layout-panel">
      <p className="field-hint">
        先预览分句和保护范围，再应用；不会改动原始时间数据。人工文字 {protectedCaptions.text.length}{' '}
        段，人工样式 {protectedCaptions.style.length} 段。
      </p>
      {blockedReason || transcript.stale ? (
        <p className="speech-warning">{blockedReason || '此来源已陈旧，请先核对素材。'}</p>
      ) : null}
      <form ref={form} noValidate onSubmit={(event) => void inspect(event)}>
        <CaptionLayoutEditor
          value={draft}
          errors={errors}
          disabled={disabled}
          onChange={(value) => {
            setDraft(value);
            setErrors({});
            setPreview(null);
          }}
        />
        <Field label="目标字幕轨">
          <Select
            value={effectiveTrackId}
            disabled={disabled}
            onChange={(event) => {
              setTrackId(event.target.value);
              setTargetNotice('');
              setPreview(null);
            }}
          >
            <option value="">新建字幕轨</option>
            {project.tracks
              .filter((track) => track.kind === 'caption')
              .map((track) => (
                <option key={track.id} value={track.id} disabled={track.locked}>
                  {track.name}
                  {track.locked ? '（已锁定）' : ''}
                </option>
              ))}
          </Select>
        </Field>
        {targetNotice ? (
          <p className="field-hint" role="status">
            {targetNotice}
          </p>
        ) : null}
        <Button type="submit" disabled={disabled || Boolean(blockedReason) || transcript.stale}>
          预览字幕分句
        </Button>
      </form>
      {preview ? (
        <div className="caption-layout-preview">
          <h4>{stale ? '上次分句预览（已过期）' : '分句预览'}</h4>
          <p>{preview.summary}</p>
          <p className="field-hint">
            原来 {preview.beforeCount} 条 → 预览 {preview.afterCount} 条；人工修改保护{' '}
            {preview.protectedCount} 条。
          </p>
          {preview.warnings.map((warning, index) => (
            <p className="speech-warning" key={index}>
              {warning}
            </p>
          ))}
          {preview.captions.slice(0, limit).map((caption) => (
            <div className="caption-preview-row" key={caption.id}>
              <span>
                {framesToSeconds(caption.startFrame, project.settings).toFixed(3)}–
                {framesToSeconds(
                  caption.startFrame + caption.durationFrames,
                  project.settings,
                ).toFixed(3)}
                秒
              </span>
              <p>{caption.text}</p>
              {caption.captionSource?.textOverride || caption.captionSource?.styleOverride ? (
                <small>保留人工修改</small>
              ) : null}
            </div>
          ))}
          {preview.captions.length > limit ? (
            <Button onClick={() => setLimit((value) => value + 20)}>显示更多字幕预览</Button>
          ) : null}
          {preview.truncated ? (
            <p className="field-hint">本次仅展示前200条；完整变更由工程统一处理。</p>
          ) : null}
          {stale ? <p className="speech-warning">工程或字幕接受版本已变化，请重新预览。</p> : null}
          <Button
            variant="primary"
            disabled={disabled || stale || Boolean(blockedReason)}
            onClick={() => void apply()}
          >
            应用字幕分句并保留撤销
          </Button>
        </div>
      ) : null}
      {error ? (
        <p className="form-error" role="alert">
          {error}
        </p>
      ) : null}
    </section>
  );
}
