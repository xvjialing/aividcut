import { useMemo, useState } from 'react';
import type { Project } from '../types';
import type {
  SpeechAudioImportOptions,
  SpeechCharacterAlignment,
  SpeechJob,
  SpeechSummary,
} from '../speech-types';
import { captionLayoutDraft, parseCaptionLayout } from '../lib/caption-layout';
import {
  alignmentDisplayState,
  alignmentRows,
  displayTimingSeconds,
  speechAudioPlacements,
} from '../lib/speech-timing';
import { formatSourceSeconds, sourceSecondsToMicroseconds } from '../lib/action-review';
import { framesToSeconds, secondsToFrames } from '../lib/editor';
import { CaptionLayoutEditor } from './CaptionLayoutEditor';
import { Button, Field, Input, Select } from './ui';

const sourceName = (source: string) => (source === 'normalized' ? '朗读文本' : '原文');
function AlignmentInspector({
  value,
  label,
  raw,
}: {
  value: SpeechCharacterAlignment | null | undefined;
  label: string;
  raw?: unknown;
}) {
  const [open, setOpen] = useState(false);
  const [showRaw, setShowRaw] = useState(false);
  const [offset, setOffset] = useState(0);
  const state = useMemo(() => alignmentDisplayState(value), [value]);
  const characters = value?.characters || [];
  return (
    <details
      className="speech-details alignment-inspector"
      onToggle={(event) => setOpen(event.currentTarget.open)}
    >
      <summary>
        {label}字符时间 ·{' '}
        {
          { missing: '未返回', incomplete: '不完整', complete: '结构齐全', invalid: '数据待核对' }[
            state
          ]
        }
      </summary>
      {open ? (
        <>
          {value ? (
            <>
              <p className="speech-transcript-text">{characters.join('')}</p>
              <p className="field-hint">
                这是供应商返回的{label}时间；能否用于字幕，以音频核查结果为准。
              </p>
              {alignmentRows(value, offset, 40).map((row) => (
                <p className="speech-word-result" key={row.index}>
                  <strong>{row.character === ' ' ? '空格' : row.character}</strong>
                  <span>
                    {displayTimingSeconds(row.startSeconds)} →{' '}
                    {displayTimingSeconds(row.endSeconds)}
                  </span>
                </p>
              ))}
              <div className="speech-actions">
                <span className="field-hint">
                  {characters.length ? offset + 1 : 0}–{Math.min(offset + 40, characters.length)} /{' '}
                  {characters.length} 项
                </span>
                {offset > 0 ? (
                  <Button
                    variant="ghost"
                    onClick={() => setOffset((value) => Math.max(0, value - 40))}
                  >
                    上一批时间
                  </Button>
                ) : null}
                {offset + 40 < characters.length ? (
                  <Button variant="ghost" onClick={() => setOffset((value) => value + 40)}>
                    下一批时间
                  </Button>
                ) : null}
              </div>
            </>
          ) : (
            <p className="field-hint">没有返回这套时间，不能用另一套冒充或补零。</p>
          )}
          {raw !== undefined ? (
            <details
              className="speech-details"
              onToggle={(event) => setShowRaw(event.currentTarget.open)}
            >
              <summary>供应商原始时间数据</summary>
              {showRaw ? (
                <pre>{typeof raw === 'string' ? raw : JSON.stringify(raw, null, 2)}</pre>
              ) : null}
            </details>
          ) : null}
        </>
      ) : null}
    </details>
  );
}
export function SpeechAudioResult({
  job,
  detail,
  project,
  busy,
  onImport,
  onOpenCaptions,
  prepareProject,
}: {
  job: SpeechSummary;
  detail: SpeechJob;
  project: Project;
  busy: boolean;
  onImport: (options: SpeechAudioImportOptions) => Promise<boolean>;
  onOpenCaptions?: (transcriptId: string) => void;
  prepareProject?: () => Promise<Project | null>;
}) {
  const [addToTimeline, setAddToTimeline] = useState(false);
  const [trackId, setTrackId] = useState('');
  const [startSeconds, setStartSeconds] = useState('0.000');
  const [generateCaptions, setGenerateCaptions] = useState(false);
  const [captionTrackId, setCaptionTrackId] = useState('');
  const [alignmentSource, setAlignmentSource] = useState<'original' | 'normalized'>('original');
  const [layout, setLayout] = useState(() => captionLayoutDraft());
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [error, setError] = useState('');
  const [placementLimit, setPlacementLimit] = useState(5);
  const audio = job.audio!;
  const targetOpen = job.projectId === project.id;
  const placements = useMemo(
    () => speechAudioPlacements(project, audio.id),
    [project.clips, project.tracks, audio.id],
  );
  const existing = placements.length > 0 || job.audioOnTimeline;
  const fixedSource = job.captionTimingSource;
  const chosenSource = fixedSource || alignmentSource;
  const choices = detail.timingChoices || [];
  const choice = choices.find((item) => item.source === chosenSource);
  const withCaptions = generateCaptions && !fixedSource;
  const activeTranscript =
    project.transcripts?.find((value) => value.id === job.acceptedTranscriptId) ||
    project.transcripts?.find((value) => value.assetId === audio.id && value.origin === 'tts');
  const allowed =
    job.status === 'succeeded' &&
    targetOpen &&
    (!withCaptions ||
      (choice?.available && (existing || addToTimeline) && (!existing || placements.length > 0)));
  async function apply() {
    if (!allowed || busy) return;
    setError('');
    const startUs = sourceSecondsToMicroseconds(startSeconds);
    if (!existing && addToTimeline && startUs === null) {
      setError('配音起点需为非负秒数，最多6位小数。');
      return;
    }
    const options: SpeechAudioImportOptions = {
      trackId: !existing && addToTimeline ? trackId : '',
      startFrame:
        !existing && addToTimeline ? secondsToFrames(startUs! / 1e6, project.settings) : 0,
      addToTimeline: !existing && addToTimeline,
    };
    if (withCaptions) {
      const parsed = parseCaptionLayout(layout);
      setErrors(parsed.errors);
      if (!parsed.value) return;
      options.generateCaptions = true;
      options.captionTrackId = captionTrackId;
      options.captionLayout = parsed.value;
      options.alignmentSource = chosenSource;
    }
    await onImport(options);
  }
  const disabled =
    busy || !allowed || (!withCaptions && (existing || !addToTimeline) && job.audioImported);
  return (
    <section className="speech-audio-result">
      <h4>已保存的配音</h4>
      {audio.url ? (
        <audio src={audio.url} controls preload="none" aria-label={`试听${audio.name}`} />
      ) : (
        <p className="field-hint">本地试听地址暂不可用，请重新读取任务。</p>
      )}
      <p className="field-hint">实测时长 {formatSourceSeconds(audio.durationUs)}秒</p>
      {job.mode === 'synthesize' ? (
        <>
          <p className="field-hint">
            {!job.timingRequested
              ? '本次未请求配音时间，仍可单独导入音频。'
              : `原文时间${!job.timingAvailable ? '未提供' : job.timingComplete ? '结构齐全' : '不完整'}；朗读文本时间${!job.normalizedTimingAvailable ? '未提供' : job.normalizedTimingComplete ? '结构齐全' : '不完整'}。`}
          </p>
          {job.timingReason ? <p className="speech-warning">{job.timingReason}</p> : null}
          {job.normalizedTimingReason ? (
            <p className="field-hint">朗读文本：{job.normalizedTimingReason}</p>
          ) : null}
          {detail.timing ? (
            <>
              <p className="field-hint">
                原文与朗读文本分开保留；数字、缩写等可能读成不同文字，不自动替换。
              </p>
              <AlignmentInspector
                label="原文"
                value={detail.timing.alignment}
                raw={detail.timing.rawAlignment}
              />
              <AlignmentInspector
                label="朗读文本（规范化后）"
                value={detail.timing.normalizedAlignment}
                raw={detail.timing.rawNormalizedAlignment}
              />
              {(detail.timing.warnings || []).map((warning, index) => (
                <p className="speech-warning" key={index}>
                  {warning}
                </p>
              ))}
            </>
          ) : null}
        </>
      ) : null}
      {job.status === 'succeeded' ? (
        <>
          {existing ? (
            <div className="speech-placement-scope">
              <p className="speech-warning">
                {placements.length
                  ? `该配音已在时间线上使用 ${placements.length} 段，位置保持不变，不重复添加配音。`
                  : '该配音已上轨，请先核对当前工程范围。'}{' '}
                字幕会跟随此配音的全部上轨片段，包括分割与重复使用。
              </p>
              {placements.slice(0, placementLimit).map((placement) => (
                <p className="field-hint" key={placement.clipId}>
                  {project.tracks.find((track) => track.id === placement.trackId)?.name || '音频轨'}{' '}
                  · {framesToSeconds(placement.startFrame, project.settings).toFixed(3)}–
                  {framesToSeconds(
                    placement.startFrame + placement.durationFrames,
                    project.settings,
                  ).toFixed(3)}
                  秒{placement.locked ? ' · 已锁定' : ''}
                </p>
              ))}
              {placements.length > placementLimit ? (
                <Button variant="ghost" onClick={() => setPlacementLimit((value) => value + 20)}>
                  显示更多配音范围
                </Button>
              ) : null}
              {!placements.length && prepareProject ? (
                <Button
                  onClick={() =>
                    void prepareProject().catch((cause) =>
                      setError(cause instanceof Error ? cause.message : '工程范围读取失败。'),
                    )
                  }
                >
                  核对当前配音范围
                </Button>
              ) : null}
            </div>
          ) : (
            <label className="checkbox-field">
              <input
                type="checkbox"
                checked={addToTimeline}
                disabled={busy || !targetOpen}
                onChange={(event) => setAddToTimeline(event.target.checked)}
              />
              同时加入时间线
            </label>
          )}
          {!existing && addToTimeline ? (
            <>
              <Field label="音频轨">
                <Select
                  value={trackId}
                  disabled={busy}
                  onChange={(event) => setTrackId(event.target.value)}
                >
                  <option value="">新建音频轨</option>
                  {project.tracks
                    .filter((track) => track.kind === 'audio')
                    .map((track) => (
                      <option key={track.id} value={track.id} disabled={track.locked}>
                        {track.name}
                        {track.locked ? '（已锁定）' : ''}
                      </option>
                    ))}
                </Select>
              </Field>
              <Field label="时间线起点 · 秒">
                <Input
                  type="text"
                  inputMode="decimal"
                  value={startSeconds}
                  disabled={busy}
                  onChange={(event) => setStartSeconds(event.target.value)}
                />
              </Field>
              <p className="field-hint">起点会对齐到影片的最近一帧。</p>
            </>
          ) : null}
          {fixedSource ? (
            <>
              <p className="speech-notice">
                字幕已使用{sourceName(fixedSource)}
                时间。来源已固定，重复导入不会重排或覆盖人工修改。
              </p>
              {activeTranscript && onOpenCaptions ? (
                <Button onClick={() => onOpenCaptions(activeTranscript.id)}>
                  查看或预览字幕分句
                </Button>
              ) : null}
            </>
          ) : job.mode === 'synthesize' ? (
            <>
              <label className="checkbox-field">
                <input
                  type="checkbox"
                  checked={withCaptions}
                  disabled={busy || !targetOpen || !choices.some((item) => item.available)}
                  onChange={(event) => {
                    setGenerateCaptions(event.target.checked);
                    if (event.target.checked && !existing) setAddToTimeline(true);
                    setError('');
                  }}
                />
                一并添加真实时间字幕
              </label>
              {!choices.some((item) => item.available) ? (
                <p className="field-hint">
                  当前没有可用字幕时间，可单独导入音频；不会自动识别或再次生成。
                </p>
              ) : null}
            </>
          ) : null}
          {withCaptions ? (
            <div className="speech-caption-options">
              <Field label="字幕文字来源">
                <Select
                  value={chosenSource}
                  disabled={busy || Boolean(fixedSource)}
                  onChange={(event) =>
                    setAlignmentSource(event.target.value as 'original' | 'normalized')
                  }
                >
                  <option value="original">原文时间对应文字</option>
                  <option value="normalized">朗读文本（规范化后）</option>
                </Select>
              </Field>
              {choice?.available ? (
                <p className="speech-transcript-text">{choice.text}</p>
              ) : (
                <p className="speech-warning">
                  {choice?.reason || '请明确选择有真实时间的文本来源。'}
                </p>
              )}
              <p className="field-hint">
                不会拿另一套时间冒充所选文字。添加字幕会作用于这份配音的全部已上轨片段。
              </p>
              <Field label="目标字幕轨">
                <Select
                  value={captionTrackId}
                  disabled={busy}
                  onChange={(event) => setCaptionTrackId(event.target.value)}
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
              <CaptionLayoutEditor
                value={layout}
                errors={errors}
                disabled={busy}
                onChange={(value) => {
                  setLayout(value);
                  setErrors({});
                }}
              />
            </div>
          ) : null}
          {error ? (
            <p className="form-error" role="alert">
              {error}
            </p>
          ) : null}
          <Button variant="primary" disabled={disabled} onClick={() => void apply()}>
            {withCaptions
              ? existing
                ? '为已有配音添加字幕'
                : '添加配音与字幕'
              : !existing && addToTimeline
                ? '导入配音并加入时间线'
                : job.audioImported
                  ? '配音已在素材库'
                  : '导入配音到素材库'}
          </Button>
        </>
      ) : null}
    </section>
  );
}
