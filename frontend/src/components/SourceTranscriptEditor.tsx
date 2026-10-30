import { lazy, Suspense, useEffect, useId, useRef, useState } from 'react';
import type { Bootstrap, Project, SourceWord, Transcript } from '../types';
import type {
  CaptionLayoutInput,
  CaptionLayoutPreview,
  SpeechTranscriptCorrection,
} from '../speech-types';
import { Button, Field, Input, Modal, Select } from './ui';
import { SecondsInput } from './SecondsInput';
import type { TranscriptWordSelection } from '../transcript-cut-types';
const TranscriptCutPanel = lazy(() =>
  import('./TranscriptCutPanel').then((module) => ({ default: module.TranscriptCutPanel })),
);
const CaptionLayoutPanel = lazy(() =>
  import('./CaptionLayoutPanel').then((module) => ({ default: module.CaptionLayoutPanel })),
);

export function SourceTranscriptEditor({
  project,
  busy,
  onCorrect,
  onDetach,
  prepareProject,
  onLayoutPreview,
  onLayoutApply,
  focusTranscriptId,
  sourceEpoch,
  onLocateWords,
  applyWordCut,
}: {
  project: Project;
  busy: boolean;
  onCorrect: (input: SpeechTranscriptCorrection) => Promise<Transcript | null>;
  onDetach: (clipId: string) => Promise<boolean>;
  prepareProject?: () => Promise<Project | null>;
  onLayoutPreview?: (input: CaptionLayoutInput) => Promise<CaptionLayoutPreview>;
  onLayoutApply?: (id: string) => Promise<Bootstrap | null>;
  focusTranscriptId?: string;
  sourceEpoch?: number;
  onLocateWords?: (value: TranscriptWordSelection) => void;
  applyWordCut?: (action: () => Promise<Bootstrap>) => Promise<Bootstrap | null>;
}) {
  const transcripts = project.transcripts || [];
  const [transcriptID, setTranscriptID] = useState(focusTranscriptId || transcripts[0]?.id || '');
  const transcript = transcripts.find((value) => value.id === transcriptID);
  const [words, setWords] = useState<SourceWord[]>([]);
  const [baseRevision, setBaseRevision] = useState(0);
  const [limit, setLimit] = useState(40);
  const [boundLimit, setBoundLimit] = useState(40);
  const [error, setError] = useState('');
  const [changed, setChanged] = useState(false);
  const [detaching, setDetaching] = useState('');
  const [showLayout, setShowLayout] = useState(false);
  const [showCut, setShowCut] = useState(false);
  const layoutDetails = useRef<HTMLDetailsElement>(null);
  const errorID = useId();
  const reset = () => {
    setWords((transcript?.acceptedWords || []).map((word) => ({ ...word })));
    setBaseRevision(transcript?.revision || 0);
    setLimit(40);
    setError('');
    setChanged(false);
    setDetaching('');
    setBoundLimit(40);
  };
  useEffect(() => {
    if (!transcripts.some((value) => value.id === transcriptID))
      setTranscriptID(transcripts[0]?.id || '');
  }, [project.id, transcripts.map((value) => value.id).join(',')]);
  useEffect(reset, [project.id, transcriptID]);
  useEffect(() => {
    if (focusTranscriptId && transcripts.some((value) => value.id === focusTranscriptId)) {
      if (changed) {
        setError('请先保存或重新读取当前纠错，再转到字幕分句。');
        return;
      }
      setTranscriptID(focusTranscriptId);
      setShowLayout(true);
    }
  }, [focusTranscriptId]);
  useEffect(() => {
    if (!showLayout || !focusTranscriptId || focusTranscriptId !== transcriptID) return;
    const timer = window.setTimeout(
      () => layoutDetails.current?.scrollIntoView?.({ block: 'start', behavior: 'auto' }),
      0,
    );
    return () => clearTimeout(timer);
  }, [showLayout, transcriptID, focusTranscriptId]);
  useEffect(() => {
    if (transcript && !changed && baseRevision !== transcript.revision) {
      setWords(transcript.acceptedWords.map((word) => ({ ...word })));
      setBaseRevision(transcript.revision);
    }
  }, [transcript?.revision]);
  const stale = Boolean(transcript?.stale || (transcript && baseRevision !== transcript.revision));
  const bound = project.clips.filter((clip) => clip.captionSource?.transcriptId === transcriptID);
  function update(index: number, patch: Partial<SourceWord>) {
    setWords((current) =>
      current.map((word, position) => (position === index ? { ...word, ...patch } : word)),
    );
    setError('');
    setChanged(true);
  }
  async function save(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!transcript || stale || busy) return;
    if (
      words.some(
        (word, index) =>
          !word.text.trim() ||
          !Number.isSafeInteger(word.startUs) ||
          !Number.isSafeInteger(word.endUs) ||
          word.startUs < 0 ||
          word.endUs <= word.startUs ||
          word.endUs > transcript.sourceDurationUs ||
          (index > 0 && word.startUs < words[index - 1].startUs),
      )
    ) {
      setError('文字不能为空；源时间需完整、有序，终点大于起点且不超过素材时长，最多6位小数秒。');
      return;
    }
    const accepted = await onCorrect({
      projectId: project.id,
      transcriptId: transcript.id,
      transcriptRevision: baseRevision,
      words,
    });
    if (accepted) {
      setWords((accepted.acceptedWords || []).map((word) => ({ ...word })));
      setBaseRevision(accepted.revision);
      setChanged(false);
      setError('');
    }
  }
  return (
    <section className="source-transcript-editor" aria-label="字幕文字与来源">
      <h3>字幕文字与来源</h3>
      {!transcripts.length ? (
        <p className="field-hint">暂无字幕来源。可接受识别结果，或使用带时间的配音添加字幕。</p>
      ) : (
        <>
          <Field label="字幕来源">
            <Select
              value={transcriptID}
              disabled={busy}
              onChange={(event) => setTranscriptID(event.target.value)}
            >
              {transcripts.map((value) => (
                <option key={value.id} value={value.id}>
                  {project.assets.find((asset) => asset.id === value.assetId)?.name ||
                    value.assetId}{' '}
                  ·{' '}
                  {value.origin === 'tts'
                    ? `配音字幕源／${value.timingSource === 'normalized' ? '朗读文本' : value.timingSource === 'original' ? '原文' : '来源待核对'}`
                    : '识别字幕源'}{' '}
                  · 版本 {value.revision}
                </option>
              ))}
            </Select>
          </Field>
          {transcript && sourceEpoch !== undefined && applyWordCut ? (
            <details
              className="speech-details"
              open={showCut}
              onToggle={(event) => setShowCut(event.currentTarget.open)}
            >
              <summary>按文字定位与删减</summary>
              {showCut ? (
                <Suspense fallback={<p className="field-hint">读取按文字剪辑…</p>}>
                  <TranscriptCutPanel
                    key={`${sourceEpoch}:${transcript.id}`}
                    project={project}
                    transcript={transcript}
                    sourceEpoch={sourceEpoch}
                    busy={busy}
                    blockedReason={
                      changed
                        ? '请先保存或重新读取文字纠错，再按文字剪辑。'
                        : stale
                          ? '字幕接受版本已变化，请先重新读取。'
                          : undefined
                    }
                    prepareProject={prepareProject}
                    onLocate={onLocateWords}
                    applyToProject={applyWordCut}
                  />
                </Suspense>
              ) : null}
            </details>
          ) : null}
          {transcript && onLayoutPreview && onLayoutApply ? (
            <details
              className="speech-details source-caption-layout"
              ref={layoutDetails}
              open={showLayout}
              onToggle={(event) => setShowLayout(event.currentTarget.open)}
            >
              <summary>字幕分句与排版</summary>
              {showLayout ? (
                <Suspense fallback={<p className="field-hint">读取字幕排版设置…</p>}>
                  <CaptionLayoutPanel
                    key={transcript.id}
                    project={project}
                    transcript={transcript}
                    busy={busy}
                    blockedReason={
                      changed
                        ? '请先保存或重新读取文字纠错，再调整分句。'
                        : stale
                          ? '字幕接受版本已变化，请先重新读取。'
                          : undefined
                    }
                    prepareProject={prepareProject}
                    onPreview={onLayoutPreview}
                    onApply={async (id) => {
                      const result = await onLayoutApply(id);
                      const accepted = result?.project.transcripts?.find(
                        (value) => value.id === transcript.id,
                      );
                      if (accepted && !changed) {
                        setWords(accepted.acceptedWords.map((word) => ({ ...word })));
                        setBaseRevision(accepted.revision);
                      }
                      return result;
                    }}
                  />
                </Suspense>
              ) : null}
            </details>
          ) : null}
          {transcript ? (
            <form noValidate onSubmit={(event) => void save(event)}>
              <p className="field-hint">
                {transcript.origin === 'tts'
                  ? '这是配音返回的字幕时间，不是语音识别；原文与朗读文本不可互换。'
                  : '这是已接受的识别结果。'}{' '}
                修改文字与源时间不会覆盖原始记录。
              </p>
              {stale ? (
                <p className="speech-warning">
                  {transcript.stale
                    ? '源内容已变化，此转写已陈旧，请重新识别。'
                    : '接受版本已变化，请先重新读取；当前纠错草稿保留。'}
                </p>
              ) : null}
              {words.slice(0, limit).map((word, index) => (
                <fieldset className="source-word" key={word.id} disabled={busy || stale}>
                  <legend>
                    词 {index + 1}
                    {word.speakerId ? ` · ${word.speakerId}` : ''}
                  </legend>
                  <Field label="文本">
                    <Input
                      value={word.text}
                      onChange={(event) => update(index, { text: event.target.value })}
                    />
                  </Field>
                  <div className="field-pair">
                    <Field label="起点 · 秒">
                      <SecondsInput
                        microseconds={word.startUs}
                        onChange={(value) => update(index, { startUs: value })}
                      />
                    </Field>
                    <Field label="终点 · 秒">
                      <SecondsInput
                        microseconds={word.endUs}
                        onChange={(value) => update(index, { endUs: value })}
                      />
                    </Field>
                  </div>
                </fieldset>
              ))}
              {words.length > limit ? (
                <Button variant="ghost" onClick={() => setLimit((value) => value + 40)}>
                  再显示 40 个词
                </Button>
              ) : null}
              {error ? (
                <p className="form-error" id={errorID} role="alert">
                  {error}
                </p>
              ) : null}
              <div className="speech-actions">
                <Button variant="primary" type="submit" disabled={busy || stale || !changed}>
                  保存源词纠错
                </Button>
                <Button disabled={busy} onClick={reset}>
                  重新读取接受版本
                </Button>
              </div>
            </form>
          ) : null}
          {bound.length ? (
            <details className="speech-details">
              <summary>绑定字幕 · {bound.length} 条</summary>
              <p className="field-hint">解绑后作为静态字幕保留，不再跟随源片段派生；可撤销。</p>
              {bound.slice(0, boundLimit).map((clip) => (
                <div className="speech-bound-row" key={clip.id}>
                  <span>
                    {clip.text || clip.name}
                    {clip.captionSource?.textOverride ? ' · 人工文字' : ''}
                    {clip.captionSource?.styleOverride ? ' · 人工样式' : ''}
                  </span>
                  <Button variant="ghost" disabled={busy} onClick={() => setDetaching(clip.id)}>
                    解绑此字幕
                  </Button>
                </div>
              ))}
              {bound.length > boundLimit ? (
                <Button variant="ghost" onClick={() => setBoundLimit((value) => value + 40)}>
                  再显示 40 条绑定字幕
                </Button>
              ) : null}
            </details>
          ) : null}
          <Modal
            open={Boolean(detaching)}
            title="解绑此字幕？"
            description="保留当前文本、样式与时序，之后不再跟随源片段派生。此操作可撤销。"
            busy={busy}
            onOpenChange={(value) => {
              if (!value && !busy) setDetaching('');
            }}
          >
            <div className="dialog-actions">
              <Button disabled={busy} onClick={() => setDetaching('')}>
                保持绑定
              </Button>
              <Button
                variant="primary"
                disabled={busy}
                onClick={() =>
                  void onDetach(detaching).then((success) => {
                    if (success) setDetaching('');
                    else setError('解绑未提交，请核对锁轨、源状态或工程错误后重试。');
                  })
                }
              >
                确认解绑字幕
              </Button>
            </div>
            {error ? (
              <p className="form-error" role="alert">
                {error}
              </p>
            ) : null}
          </Modal>
        </>
      )}
    </section>
  );
}
