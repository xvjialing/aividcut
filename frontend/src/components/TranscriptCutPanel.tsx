import { useEffect, useMemo, useRef, useState } from 'react';
import type { Bootstrap, Project, Transcript } from '../types';
import type {
  TranscriptCutAPI,
  TranscriptCutInput,
  TranscriptWordSelection,
} from '../transcript-cut-types';
import type { WorkflowPreview } from '../workflow-types';
import { transcriptCutAPI } from '../lib/bridge';
import { formatSourceSeconds } from '../lib/action-review';
import { SourceRangeMedia } from './SourceRangeMedia';
import { Button, Field, Modal, Select } from './ui';

export function TranscriptCutPanel({
  project,
  transcript,
  sourceEpoch,
  busy,
  blockedReason,
  prepareProject,
  onLocate,
  applyToProject,
  api = transcriptCutAPI,
}: {
  project: Project;
  transcript: Transcript;
  sourceEpoch: number;
  busy: boolean;
  blockedReason?: string;
  prepareProject?: () => Promise<Project | null>;
  onLocate?: (value: TranscriptWordSelection) => void;
  applyToProject: (action: () => Promise<Bootstrap>) => Promise<Bootstrap | null>;
  api?: TranscriptCutAPI;
}) {
  const clips = project.clips.filter(
    (clip) =>
      clip.assetId === transcript.assetId && (clip.kind === 'audio' || clip.kind === 'video'),
  );
  const words = useMemo(
    () => [...transcript.acceptedWords].sort((a, b) => a.startUs - b.startUs),
    [transcript.id, transcript.revision],
  );
  const [clipId, setClipId] = useState(clips.length === 1 ? clips[0].id : '');
  const [selection, setSelection] = useState<[number, number] | null>(null);
  const anchor = useRef(0);
  const [limit, setLimit] = useState(80);
  const [mode, setMode] = useState<'close' | 'gap' | 'all'>('close');
  const [located, setLocated] = useState<TranscriptWordSelection | null>(null);
  const [preview, setPreview] = useState<WorkflowPreview | null>(null);
  const [working, setWorking] = useState('');
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const pending = useRef(false);
  const scope = `${project.id}:${project.revision}:${transcript.id}:${transcript.revision}:${sourceEpoch}`;
  const currentScope = useRef(scope);
  currentScope.current = scope;
  const alive = useRef(true);
  useEffect(() => {
    alive.current = true;
    return () => {
      alive.current = false;
    };
  }, []);
  useEffect(() => {
    setPreview(null);
    setLocated(null);
    setError('');
  }, [scope]);
  useEffect(() => {
    setSelection(null);
    setLimit(80);
  }, [transcript.id, transcript.revision, sourceEpoch]);
  const target = clips.find((clip) => clip.id === clipId);
  const disabled = busy || !!working || !!blockedReason || !!transcript.stale;
  const ids = selection ? words.slice(selection[0], selection[1] + 1).map((word) => word.id) : [];
  function invalidate() {
    setPreview(null);
    setLocated(null);
    setError('');
    setNotice('');
  }
  function select(index: number, extend: boolean) {
    const first = extend && selection ? Math.min(anchor.current, index) : index;
    const last = extend && selection ? Math.max(anchor.current, index) : index;
    if (last - first + 1 > 1000) {
      setError('一次最多选择1000个连续词，请缩小范围。');
      return;
    }
    if (!extend || !selection) anchor.current = index;
    invalidate();
    setSelection([first, last]);
  }
  async function inspect(kind: 'locate' | 'preview') {
    if (pending.current || disabled || !target || !ids.length) return;
    const requestScope = scope;
    pending.current = true;
    setWorking(kind);
    setError('');
    setPreview(null);
    setNotice('');
    try {
      const ready = prepareProject ? await prepareProject() : project;
      if (!alive.current || currentScope.current !== requestScope) return;
      if (!ready || ready.id !== project.id || ready.revision !== project.revision)
        throw new Error('工程版本已变化，请核对当前片段并重新预览。');
      const input: TranscriptCutInput = {
        projectId: ready.id,
        projectRevision: ready.revision,
        sourceEpoch,
        transcriptId: transcript.id,
        transcriptRevision: transcript.revision,
        clipId,
        wordIds: ids,
        closeGap: mode === 'close',
        rippleAll: mode === 'all',
      };
      if (kind === 'locate') {
        const value = await api.locate(input);
        if (!alive.current || currentScope.current !== requestScope) return;
        setLocated(value);
        onLocate?.(value);
        setNotice(`已定位工程帧 ${value.startFrame}–${value.endFrame}，工程未修改。`);
      } else {
        const value = await api.preview(input);
        if (!alive.current || currentScope.current !== requestScope) return;
        setPreview(value);
      }
    } catch (cause) {
      if (alive.current && currentScope.current === requestScope)
        setError(cause instanceof Error ? cause.message : '无法读取选词范围，请重试。');
    } finally {
      pending.current = false;
      if (alive.current) setWorking('');
    }
  }
  async function apply() {
    if (
      !preview ||
      disabled ||
      pending.current ||
      preview.projectId !== project.id ||
      preview.projectRevision !== project.revision
    )
      return;
    pending.current = true;
    setWorking('apply');
    setError('');
    try {
      const result = await applyToProject(() => api.apply(preview.id));
      if (alive.current && !result) setError('删减尚未应用。请处理工程草稿或冲突，再重新预览。');
      if (alive.current && result) {
        setPreview(null);
        setLocated(null);
        setNotice('已应用按文字删减，可使用撤销恢复。');
      }
    } catch (cause) {
      if (alive.current)
        setError(cause instanceof Error ? cause.message : '应用失败，工程未改变；请重新预览。');
    } finally {
      pending.current = false;
      if (alive.current) setWorking('');
    }
  }
  const asset = project.assets.find((item) => item.id === transcript.assetId);
  const evidence = preview?.evidence.find((item) => item.kind === 'accepted-word-cut');
  return (
    <section className="transcript-cut" aria-label="按文字剪辑">
      <p className="field-hint">
        只使用已接受文字。点选起词，再按住 Shift 点选末词；键盘可用 Shift + Enter 扩展。最多1000词。
      </p>
      <Field
        label="要剪辑的上轨片段"
        hint={
          mode === 'all' ? '全轨模式也会移动其它实例与未选轨道。' : '同素材的其它实例保持不变。'
        }
      >
        <Select
          value={target ? clipId : ''}
          disabled={disabled}
          onChange={(event) => {
            setClipId(event.target.value);
            invalidate();
          }}
        >
          <option value="">{clips.length ? '请选择具体实例' : '该素材尚未上轨'}</option>
          {clips.map((clip) => (
            <option key={clip.id} value={clip.id}>
              {clip.name || asset?.name || clip.id} ·{' '}
              {project.tracks.find((track) => track.id === clip.trackId)?.name || clip.trackId} · 帧{' '}
              {clip.startFrame}–{clip.startFrame + clip.durationFrames}
            </option>
          ))}
        </Select>
      </Field>
      <div className="transcript-cut-words" role="group" aria-label="接受词选择">
        {words.slice(0, limit).map((word, index) => (
          <Button
            key={word.id}
            disabled={disabled}
            aria-pressed={!!selection && index >= selection[0] && index <= selection[1]}
            title={`源 ${formatSourceSeconds(word.startUs)}–${formatSourceSeconds(word.endUs)}秒`}
            onClick={(event) => select(index, event.shiftKey)}
          >
            {word.text}
          </Button>
        ))}
      </div>
      {!words.length ? <p className="field-hint">暂无接受词，请先接受识别结果。</p> : null}
      {words.length > limit ? (
        <Button disabled={disabled} onClick={() => setLimit((value) => value + 80)}>
          再显示80词（已显示{Math.min(limit, words.length)} / {words.length}）
        </Button>
      ) : null}
      <div className="button-row">
        <span className="field-hint">已选 {ids.length} 词</span>
        <Button
          disabled={disabled || !selection}
          onClick={() => {
            setSelection(null);
            invalidate();
          }}
        >
          清除选词
        </Button>
      </div>
      <Field label="删减后的位置">
        <Select
          value={mode}
          disabled={disabled}
          onChange={(event) => {
            setMode(event.target.value as 'close' | 'gap' | 'all');
            invalidate();
          }}
        >
          <option value="close">仅闭合当前音画组</option>
          <option value="gap">保留空隙</option>
          <option value="all">全轨波纹闭合（含未选轨）</option>
        </Select>
      </Field>
      <p className="field-hint">
        按工程帧覆盖词范围，原话记录保留。
        {mode === 'all' ? '全轨模式统一移动所有后段，请核对未选轨。' : '未选片段不波纹移动。'}
        请试听剪切边界。
      </p>
      {blockedReason || transcript.stale ? (
        <p className="speech-warning">{blockedReason || '源内容已变化，请先重新识别。'}</p>
      ) : null}
      <div className="button-row">
        <Button
          disabled={disabled || !target || !ids.length}
          busy={working === 'locate'}
          onClick={() => void inspect('locate')}
        >
          定位到时间线
        </Button>
        <Button
          variant="primary"
          disabled={disabled || !target || !ids.length}
          busy={working === 'preview'}
          onClick={() => void inspect('preview')}
        >
          预览删减影响
        </Button>
      </div>
      {located ? (
        <p className="field-hint">
          “{located.text}” · 源 {formatSourceSeconds(located.sourceStartUs)}–
          {formatSourceSeconds(located.sourceEndUs)}秒 · 工程帧 {located.startFrame}–
          {located.endFrame}
          {located.warnings.map((value) => (
            <span className="speech-warning" key={value}>
              {value}
            </span>
          ))}
        </p>
      ) : null}
      <p className="form-error" role="status">
        {error}
      </p>
      <p className="field-hint" role="status">
        {notice}
      </p>
      <Modal
        open={!!preview}
        title="核对按文字删减"
        description="确认当前实例和关联音画的变化后应用。可整次撤销。"
        busy={!!working}
        onOpenChange={(open) => {
          if (!open && !working) setPreview(null);
        }}
      >
        {preview ? (
          <>
            <p>{preview.summary}</p>
            {evidence ? (
              <>
                <blockquote>{evidence.description}</blockquote>
                <p className="field-hint">
                  源 {formatSourceSeconds(evidence.sourceStartUs)}–
                  {formatSourceSeconds(evidence.sourceEndUs)}秒 → 工程帧 {evidence.startFrame}–
                  {evidence.endFrame}（终点不含）
                </p>
                <SourceRangeMedia
                  asset={asset}
                  range={{
                    start: evidence.sourceStartUs / 1e6,
                    end: evidence.sourceEndUs / 1e6,
                    speed: target ? target.speedNum / target.speedDen : 1,
                  }}
                  title="所选原话"
                  muted={false}
                />
              </>
            ) : null}
            <p className="field-hint">源区间试听不含工程效果；应用后请用主预览核对音画和字幕。</p>
            <ul>
              {preview.evidence
                .filter((item) => item.kind === 'selected-linked-source')
                .map((item) => (
                  <li key={item.clipId}>
                    {project.clips.find((clip) => clip.id === item.clipId)?.name || item.clipId} ·{' '}
                    {
                      project.tracks.find(
                        (track) =>
                          track.id ===
                          project.clips.find((clip) => clip.id === item.clipId)?.trackId,
                      )?.name
                    }{' '}
                    · 帧 {item.startFrame}–{item.endFrame}
                  </li>
                ))}
            </ul>
            <p>
              受影响片段（含字幕）：
              {preview.rangeSummary?.totalAffectedClips ?? preview.affectedClipIds.length} ·
              工程时长缩短：
              {preview.savedFrames}帧
            </p>
            {preview.warnings.map((value) => (
              <p className="speech-warning" key={value}>
                {value}
              </p>
            ))}
            {error ? (
              <p className="form-error" role="alert">
                {error}
              </p>
            ) : null}
            <div className="dialog-actions">
              <Button disabled={!!working} onClick={() => setPreview(null)} autoFocus>
                返回检查
              </Button>
              <Button
                variant="danger"
                disabled={disabled}
                busy={working === 'apply'}
                onClick={() => void apply()}
              >
                应用删减
              </Button>
            </div>
          </>
        ) : null}
      </Modal>
    </section>
  );
}
