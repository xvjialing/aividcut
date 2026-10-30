import { useEffect, useId, useRef, useState } from 'react';
import type { Bootstrap, Clip, Project } from '../types';
import type { GenerationJob, GenerationOutputAsset } from '../generation-types';
import type {
  GenerationPlacementAPI,
  GenerationPlacementInput,
  GenerationPlacementPreview,
} from '../generation-placement-types';
import type { TimelineRange } from '../ripple-types';
import { generationPlacementAPI } from '../lib/bridge';
import { framesToSeconds, projectEnd, timecode } from '../lib/editor';
import { sourceSecondsToMicroseconds } from '../lib/action-review';
import { Button, Field, Input, Modal, Select } from './ui';
import { SourceRangeMedia } from './SourceRangeMedia';

export function GenerationPlacementDialog({
  project,
  sourceEpoch,
  job,
  candidate,
  currentFrame,
  timeRange,
  pausePlayback,
  prepareProject,
  applyToProject,
  onApplied,
  onClose,
  onReturnFocus,
  api = generationPlacementAPI,
}: {
  project: Project;
  sourceEpoch: number;
  job: GenerationJob;
  candidate: GenerationOutputAsset;
  currentFrame: () => number;
  timeRange?: TimelineRange;
  pausePlayback?: () => void;
  prepareProject?: () => Promise<Project | null>;
  applyToProject: (action: () => Promise<Bootstrap>) => Promise<Bootstrap | null>;
  onApplied: (projectId: string, clip: Clip) => void;
  onClose: () => void;
  onReturnFocus?: () => void;
  api?: GenerationPlacementAPI;
}) {
  const [start, setStart] = useState(String(currentFrame())),
    [duration, setDuration] = useState(
      candidate.kind === 'video'
        ? ''
        : String(Math.max(1, Math.round((3 * project.settings.fpsNum) / project.settings.fpsDen))),
    ),
    [source, setSource] = useState('0.000'),
    [track, setTrack] = useState(''),
    [audio, setAudio] = useState<'muted' | 'embedded'>('muted');
  const [preview, setPreview] = useState<GenerationPlacementPreview | null>(null),
    [busy, setBusy] = useState(''),
    [error, setError] = useState(''),
    [invalid, setInvalid] = useState<Record<string, string>>({});
  const id = useId(),
    form = useRef<HTMLFormElement>(null),
    pending = useRef(false),
    alive = useRef(false),
    projectRef = useRef(project);
  projectRef.current = project;
  const base = `${project.id}:${sourceEpoch}`,
    latest = useRef(base);
  latest.current = base;
  useEffect(() => {
    alive.current = true;
    return () => {
      alive.current = false;
    };
  }, []);
  useEffect(() => {
    setPreview(null);
  }, [project.id, project.revision, sourceEpoch]);
  const marked =
    !!timeRange &&
    timeRange.from !== null &&
    timeRange.to !== null &&
    Number.isSafeInteger(timeRange.from) &&
    Number.isSafeInteger(timeRange.to) &&
    timeRange.from >= 0 &&
    timeRange.from < timeRange.to &&
    timeRange.to <= projectEnd(project);
  function edit() {
    setPreview(null);
    setError('');
    setInvalid({});
  }
  const fieldError = (key: string) =>
    invalid[key] ? (
      <span className="form-error" id={`${id}-${key}`}>
        {invalid[key]}
      </span>
    ) : null;
  const invalidProps = (key: string) => ({
    'aria-invalid': !!invalid[key],
    'aria-describedby': invalid[key] ? `${id}-${key}` : undefined,
  });
  async function inspect(event: React.FormEvent) {
    event.preventDefault();
    if (pending.current) return;
    pausePlayback?.();
    setError('');
    const a = Number(start),
      b = Number(duration),
      us = candidate.kind === 'image' ? 0 : sourceSecondsToMicroseconds(source),
      auto = candidate.kind === 'video' && !duration.trim();
    const problems: Record<string, string> = {};
    if (!start.trim() || !Number.isSafeInteger(a) || a < 0 || a > 1e9)
      problems.start = '开始帧需要是0–1000000000的整数。';
    if (!auto && (!duration.trim() || !Number.isSafeInteger(b) || b < 1 || b > 1e9))
      problems.duration = '长度需要是1–1000000000的整数帧。';
    if (us === null || us > 1e12) problems.source = '源入点需要非负秒数，最多六位小数。';
    if (
      track &&
      !project.tracks.some(
        (t) =>
          t.id === track &&
          t.kind === 'video' &&
          !t.locked &&
          !t.hidden &&
          (audio === 'muted' || !t.muted),
      )
    )
      problems.track = '目标轨不可用，请重新选择可见未锁视频轨；保留声音时轨道不能静音。';
    setInvalid(problems);
    if (Object.keys(problems).length) {
      window.setTimeout(
        () => form.current?.querySelector<HTMLElement>('[aria-invalid="true"]')?.focus(),
        0,
      );
      return;
    }
    pending.current = true;
    setBusy('预览');
    setPreview(null);
    const scope = base;
    try {
      const ready = prepareProject ? await prepareProject() : projectRef.current;
      if (!ready || ready.id !== job.projectId || latest.current !== scope)
        throw Error('工程已切换或草稿尚未处理，请重新核对');
      const input: GenerationPlacementInput = {
        jobId: job.id,
        candidateId: candidate.id,
        projectId: ready.id,
        projectRevision: ready.revision,
        sourceEpoch,
        startFrame: a,
        durationFrames: auto ? 0 : b,
        autoDuration: auto,
        sourceInUs: us!,
        trackId: track,
        audioMode: audio,
      };
      const value = await api.preview(input);
      if (!alive.current || latest.current !== scope) return;
      if (projectRef.current.revision !== value.projectRevision)
        throw Error('工程已变化，请重新预览');
      setPreview(value);
    } catch (cause) {
      if (alive.current && latest.current === scope)
        setError(cause instanceof Error ? cause.message : '预览失败，请重试');
    } finally {
      pending.current = false;
      if (alive.current) setBusy('');
    }
  }
  async function apply() {
    if (
      !preview ||
      pending.current ||
      preview.projectRevision !== project.revision ||
      preview.sourceEpoch !== sourceEpoch
    )
      return;
    pending.current = true;
    setBusy('应用');
    setError('');
    const scope = base;
    try {
      let failed: unknown;
      const result = await applyToProject(async () => {
        try {
          return await api.apply(preview.id);
        } catch (cause) {
          failed = cause;
          throw cause;
        }
      });
      if (!alive.current || latest.current !== scope) return;
      if (!result) throw failed || Error('尚未提交，请处理工程草稿或冲突后重试');
      const placed = result.project.clips.find((c) => c.id === preview.clip.id);
      if (!placed) {
        setPreview(null);
        throw Error('这个添加操作已完成过，当前片段可能已撤销；如需再次添加请重新预览。');
      }
      onApplied(result.project.id, placed);
      onClose();
    } catch (cause) {
      if (alive.current && latest.current === scope)
        setError(cause instanceof Error ? cause.message : '添加失败，请重试');
    } finally {
      pending.current = false;
      if (alive.current) setBusy('');
    }
  }
  return (
    <Modal
      open
      title="添加生成候选到时间线"
      description="在指定位置新增画面，原片段不移动或删除。先预览后添加，可一次撤销，不发起模型生成。"
      className="replacement-dialog candidate-placement-dialog"
      busy={!!busy}
      onOpenChange={(open) => {
        if (!open && !busy) onClose();
      }}
      onCloseAutoFocus={(event) => {
        event.preventDefault();
        onReturnFocus?.();
      }}
    >
      <div className="replacement-dialog-body">
        <form ref={form} noValidate onSubmit={(event) => void inspect(event)}>
          <div className="candidate-compare-actions">
            <Button
              disabled={!!busy}
              onClick={() => {
                setStart(String(currentFrame()));
                edit();
              }}
            >
              读取当前播放头
            </Button>
            <Button
              disabled={!!busy || !marked}
              onClick={() => {
                if (marked) {
                  setStart(String(timeRange!.from));
                  setDuration(String(timeRange!.to! - timeRange!.from!));
                  edit();
                }
              }}
            >
              使用I/O标记区间
            </Button>
          </div>
          <div className="field-pair">
            <Field label="开始帧">
              <Input
                type="number"
                min={0}
                max={1e9}
                step={1}
                value={start}
                disabled={!!busy}
                onChange={(e) => {
                  setStart(e.target.value);
                  edit();
                }}
                {...invalidProps('start')}
              />
              {fieldError('start')}
            </Field>
            <Field label={candidate.kind === 'video' ? '长度 · 帧（留空用可用视频）' : '长度 · 帧'}>
              <Input
                type="number"
                min={1}
                max={1e9}
                step={1}
                value={duration}
                disabled={!!busy}
                onChange={(e) => {
                  setDuration(e.target.value);
                  edit();
                }}
                {...invalidProps('duration')}
              />
              {fieldError('duration')}
            </Field>
          </div>
          <Field label="目标视频轨">
            <Select
              value={track}
              {...invalidProps('track')}
              disabled={!!busy}
              onChange={(e) => {
                setTrack(e.target.value);
                edit();
              }}
            >
              <option value="">新建上层视频轨</option>
              {track &&
              !project.tracks.some(
                (t) => t.id === track && t.kind === 'video' && !t.locked && !t.hidden,
              ) ? (
                <option value={track} disabled>
                  原目标轨不可用，请重新选择
                </option>
              ) : null}
              {project.tracks
                .filter((t) => t.kind === 'video' && !t.locked && !t.hidden)
                .map((t) => (
                  <option value={t.id} key={t.id} disabled={audio === 'embedded' && t.muted}>
                    {t.name}
                    {t.muted ? ' · 轨道静音' : ''}
                  </option>
                ))}
            </Select>
            {fieldError('track')}
          </Field>
          <div className="field-pair">
            <Field label="源入点 · 秒">
              <Input
                type="text"
                inputMode="decimal"
                value={candidate.kind === 'image' ? '0.000' : source}
                disabled={!!busy || candidate.kind === 'image'}
                onChange={(e) => {
                  setSource(e.target.value);
                  edit();
                }}
                {...invalidProps('source')}
              />
              {fieldError('source')}
            </Field>
            <Field label="候选声音">
              <Select
                value={audio}
                disabled={!!busy || candidate.kind !== 'video'}
                onChange={(e) => {
                  setAudio(e.target.value as typeof audio);
                  edit();
                }}
              >
                <option value="muted">不用候选声音</option>
                <option value="embedded">保留内嵌声音（需实际音轨）</option>
              </Select>
            </Field>
          </div>
          <p className="field-hint">
            新增到已有轨时必须有空位；新上层画面可能遮住下层。声音按所选画面源窗取用，保留时会与原音轨混合。
          </p>
          <Button type="submit" busy={busy === '预览'} disabled={!!busy}>
            预览添加变化
          </Button>
        </form>
        {preview ? (
          <section className="replacement-effects">
            <h3>预览变化</h3>
            <p>{preview.summary}</p>
            <p>
              {preview.importRequired ? '将导入候选素材' : '复用已导入素材'} ·{' '}
              {preview.newTrack ? '新建视频轨' : '使用已有视频轨'}：{preview.track.name}
            </p>
            <p>
              区间 {timecode(preview.clip.startFrame, project.settings)}–
              {timecode(preview.clip.startFrame + preview.clip.durationFrames, project.settings)} ·{' '}
              {preview.clip.durationFrames}帧
              {preview.captionCount > 0 ? ` · 新增绑定字幕 ${preview.captionCount} 条` : ''}
            </p>
            <SourceRangeMedia
              title="候选源画面"
              asset={preview.asset}
              range={{
                start: preview.clip.sourceInUs / 1e6,
                end:
                  preview.clip.sourceInUs / 1e6 +
                  framesToSeconds(preview.clip.durationFrames, project.settings),
                speed: 1,
              }}
            />
            <p className="field-hint">
              这里是源区间试看，默认静音，不含工程构图、字幕或LUT；添加后可在属性和工程预览中精修。
            </p>
            {preview.warnings.map((warning, index) => (
              <p className="generation-warning" key={index}>
                {warning}
              </p>
            ))}
          </section>
        ) : (
          <p className="field-hint">预览不会导入或修改工程；参数变化后需重新预览。</p>
        )}
      </div>
      {error ? (
        <p className="form-error replacement-dialog-status" role="alert">
          {error}
        </p>
      ) : null}
      <div className="dialog-actions replacement-dialog-actions">
        <Button disabled={!!busy} onClick={onClose}>
          取消添加
        </Button>
        <Button
          variant="primary"
          disabled={
            !!busy ||
            !preview ||
            preview.projectRevision !== project.revision ||
            preview.sourceEpoch !== sourceEpoch
          }
          busy={busy === '应用'}
          onClick={() => void apply()}
        >
          添加并保留撤销
        </Button>
      </div>
    </Modal>
  );
}
