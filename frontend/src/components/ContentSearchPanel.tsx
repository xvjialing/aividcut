import { useEffect, useRef, useState, type Ref } from 'react';
import type { Bootstrap, Project } from '../types';
import type {
  ContentAPI,
  SourceContentHit,
  SourceContentQuery,
  SourceContentResult,
} from '../content-types';
import { contentBridge } from '../lib/bridge';
import { formatSourceSeconds } from '../lib/action-review';
import { Button, Field, Input, Modal, Select } from './ui';
import { SourceRangeMedia } from './SourceRangeMedia';
import { SourceFrameEvidenceViewer } from './SourceFrameEvidenceViewer';
import { visualNoteKind, analysisProducer } from '../visual-analysis-types';

const originName = (origin: string) =>
  origin === 'visual'
    ? '画面分析记录'
    : origin === 'tts'
      ? '生成配音'
      : origin === 'asr'
        ? '原话转写'
        : '旧版转写（来源未标注）';
export function ContentSearchPanel({
  project,
  sourceEpoch = 0,
  api = contentBridge,
  searchRef,
  applyAnalysis,
}: {
  project: Project;
  sourceEpoch?: number;
  api?: ContentAPI;
  searchRef?: Ref<HTMLInputElement>;
  applyAnalysis?: (action: () => Promise<Bootstrap>) => Promise<Bootstrap | null>;
}) {
  const [text, setText] = useState('');
  const [assetID, setAssetID] = useState('');
  const [origin, setOrigin] = useState('');
  const [data, setData] = useState<SourceContentResult | null>(null);
  const [applied, setApplied] = useState<SourceContentQuery | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const [analysisLimit, setAnalysisLimit] = useState(20);
  const [preview, setPreview] = useState<SourceContentHit | null>(null);
  const token = useRef(0);
  const scope = `${sourceEpoch}:${project.id}:${project.revision}`;
  const resultScope = useRef('');
  const activeScope = useRef(scope);
  activeScope.current = scope;
  useEffect(() => {
    token.current++;
    setData(null);
    setAnalysisLimit(20);
    setApplied(null);
    setPreview(null);
    setError('');
    setBusy(false);
    if (!project.assets.some((asset) => asset.id === assetID)) setAssetID('');
    return () => {
      token.current++;
    };
  }, [scope]);
  useEffect(() => setNotice(''), [sourceEpoch, project.id]);
  async function search(more = false) {
    const capturedScope = scope;
    const input: SourceContentQuery =
      more && applied && data
        ? { ...applied, offset: data.hits.length }
        : {
            sourceEpoch,
            projectId: project.id,
            projectRevision: project.revision,
            text: text.trim(),
            assetId: assetID,
            origin,
            offset: 0,
            limit: 40,
          };
    if (!input.text || Array.from(input.text).length > 100) {
      setError('请输入1–100个字符作为检索内容。');
      return;
    }
    const read = ++token.current;
    setBusy(true);
    setError('');
    setPreview(null);
    if (!more) {
      setData(null);
      setApplied(input);
    }
    try {
      const result = await api.search(input);
      if (read !== token.current || capturedScope !== activeScope.current) return;
      if (
        result.sourceEpoch !== input.sourceEpoch ||
        result.projectId !== input.projectId ||
        result.projectRevision !== input.projectRevision ||
        result.query !== input.text ||
        result.offset !== input.offset ||
        result.origin !== (input.origin || '') ||
        (result.assetId || '') !== (input.assetId || '')
      )
        throw new Error('检索结果条件已变化，请重新检索。');
      resultScope.current = capturedScope;
      setData(more && data ? { ...result, hits: [...data.hits, ...result.hits] } : result);
    } catch (cause) {
      if (read === token.current && capturedScope === activeScope.current)
        setError(cause instanceof Error ? cause.message : '检索未完成，请重试。');
    } finally {
      if (read === token.current && capturedScope === activeScope.current) setBusy(false);
    }
  }
  async function removeAnalysis(id: string) {
    if (!api.removeAnalysis || !id || busy) return;
    setBusy(true);
    setError('');
    try {
      const action = () =>
        api.removeAnalysis!({
          sourceEpoch,
          projectId: project.id,
          projectRevision: project.revision,
          id,
          operationId: crypto.randomUUID(),
        });
      const value = applyAnalysis ? await applyAnalysis(action) : await action();
      if (!value) return;
      setData(null);
      setPreview(null);
      setNotice('已移除该份画面分析记录，可撤销。');
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : '移除未完成，请核对工程。');
    } finally {
      setBusy(false);
    }
  }
  const shown = resultScope.current === scope ? data : null;
  const visiblePreview = resultScope.current === scope ? preview : null;
  const asset =
    visiblePreview && project.assets.find((value) => value.id === visiblePreview.assetId);
  return (
    <section className="content-search-panel" aria-label="素材内容检索" aria-busy={busy}>
      <form
        noValidate
        onSubmit={(event) => {
          event.preventDefault();
          void search();
        }}
      >
        <div className="content-search-entry">
          <Field label="检索内容">
            <Input
              ref={searchRef}
              value={text}
              maxLength={200}
              onChange={(event) => setText(event.target.value)}
              placeholder="搜索已接受的转写短语"
              onKeyDown={(event) => {
                if (event.key === 'Enter' && event.nativeEvent.isComposing) event.preventDefault();
              }}
            />
          </Field>
          <Button type="submit" busy={busy} disabled={!text.trim()}>
            搜索内容
          </Button>
        </div>
        <div className="field-pair">
          <Field label="检索素材">
            <Select value={assetID} onChange={(event) => setAssetID(event.target.value)}>
              <option value="">全部素材</option>
              {project.assets
                .filter((asset) => asset.kind === 'video' || asset.kind === 'audio')
                .map((asset) => (
                  <option key={asset.id} value={asset.id}>
                    {asset.name}
                  </option>
                ))}
            </Select>
          </Field>
          <Field label="内容来源">
            <Select value={origin} onChange={(event) => setOrigin(event.target.value)}>
              <option value="">全部来源</option>
              <option value="asr">原话转写</option>
              <option value="tts">生成配音</option>
              <option value="visual">画面分析记录</option>
            </Select>
          </Field>
        </div>
      </form>
      <p className="field-hint">
        检索已接受的转写和画面分析记录，不自动识别或上传素材；画面记录不等于已证实事实。
      </p>
      {project.visualAnalyses?.length ? (
        <details className="agent-details">
          <summary>已接纳画面记录 · {project.visualAnalyses.length}</summary>
          <p className="field-hint">
            包含来源已变化或缺失的记录。移除整份记录可撤销，不修改时间线。
          </p>
          {project.visualAnalyses.slice(0, analysisLimit).map((value) => (
            <div className="source-evidence-item" key={value.id}>
              <strong>
                {project.assets.find((asset) => asset.id === value.assetId)?.name ||
                  value.frames[0]?.assetName ||
                  value.assetId}
              </strong>
              <span>
                {value.stale
                  ? '来源已变化'
                  : project.assets.find((asset) => asset.id === value.assetId)?.missing
                    ? '源缺失'
                    : '来源匹配'}{' '}
                · {value.notes.length}条条目
              </span>
              {api.removeAnalysis ? (
                <Button disabled={busy} onClick={() => void removeAnalysis(value.id)}>
                  移除整份记录
                </Button>
              ) : null}
            </div>
          ))}
          {analysisLimit < project.visualAnalyses.length ? (
            <Button onClick={() => setAnalysisLimit((value) => value + 20)}>
              显示更多分析记录
            </Button>
          ) : null}
        </details>
      ) : null}
      {notice ? <p role="status">{notice}</p> : null}
      {error ? (
        <p className="form-error" role="alert">
          {error}
        </p>
      ) : null}
      {busy ? <p role="status">正在检索“{applied?.text}”…</p> : null}
      {shown ? (
        <>
          <p className="content-search-summary" role="status">
            “{shown.query}” ·{' '}
            {shown.assetId
              ? project.assets.find((asset) => asset.id === shown.assetId)?.name || '指定素材'
              : '全部素材'}{' '}
            · {shown.origin ? originName(shown.origin) : '全部来源'} · {shown.total} 处匹配
          </p>
          <p className="field-hint">
            已索引 {shown.stats.transcripts} 份转写、{shown.stats.words} 个词、
            {shown.stats.analyses || 0} 份画面分析。
            {shown.stats.skippedStale || shown.stats.skippedMissing
              ? `跳过 ${shown.stats.skippedStale} 份已失效、${shown.stats.skippedMissing} 份源缺失的内容记录。`
              : ''}
          </p>
          {!shown.hits.length ? (
            <p className="field-hint">未找到匹配内容。可调整关键词，或到语音页先接纳素材转写。</p>
          ) : (
            <ul className="content-search-results">
              {shown.hits.map((hit) => (
                <li key={hit.id}>
                  <strong>{hit.assetName}</strong>
                  <span>
                    {hit.kind === 'visual' ? (
                      `${visualNoteKind[hit.noteKind || 'inference']} · 取样约 ${(hit.frames || []).map((frame) => formatSourceSeconds(frame.positionUs)).join('、')}秒`
                    ) : (
                      <>
                        {originName(hit.origin)} · {formatSourceSeconds(hit.startUs)}–
                        {formatSourceSeconds(hit.endUs)}秒
                      </>
                    )}
                  </span>
                  <p>{hit.text}</p>
                  {hit.kind === 'visual' ? (
                    <>
                      <span>
                        {analysisProducer[hit.producer || 'external-mcp']}
                        {hit.producer === 'windows-media-ocr'
                          ? ' · 系统语言单帧识别'
                          : hit.producer === 'apple-vision'
                            ? ' · 中英文单帧识别'
                            : ' · 具体模型未记录'}
                      </span>
                      {hit.tags?.length ? <p>标签：{hit.tags.join('、')}</p> : null}
                    </>
                  ) : null}
                  <Button disabled={busy} onClick={() => setPreview(hit)}>
                    {hit.kind === 'visual' ? '查看分析与证据' : '回看源区间'}
                  </Button>
                  {hit.kind === 'visual' && api.removeAnalysis ? (
                    <Button disabled={busy} onClick={() => void removeAnalysis(hit.analysisId!)}>
                      移除这份分析记录
                    </Button>
                  ) : null}
                </li>
              ))}
            </ul>
          )}
          {shown.hits.length < shown.total ? (
            <Button busy={busy} onClick={() => void search(true)}>
              加载更多匹配
            </Button>
          ) : null}
        </>
      ) : !busy && !error ? (
        <p className="field-hint">输入关键词，查找原素材里说过的内容；不会修改时间线。</p>
      ) : null}
      <Modal
        open={Boolean(visiblePreview)}
        title={visiblePreview?.kind === 'visual' ? '回看画面分析' : '回看素材内容'}
        description={
          visiblePreview?.kind === 'visual'
            ? '已接纳分析及离散源帧取样，不代表连续事件；不会调用模型或修改时间线。'
            : '原素材区间预览，保留接受版本的源词时间；不会修改时间线。播放器定位精度取决于当前格式。'
        }
        onOpenChange={(open) => {
          if (!open) setPreview(null);
        }}
      >
        {visiblePreview ? (
          <>
            <p>
              {visiblePreview.kind === 'visual' ? (
                `${visualNoteKind[visiblePreview.noteKind || 'inference']} · ${analysisProducer[visiblePreview.producer || 'external-mcp']}`
              ) : (
                <>
                  {originName(visiblePreview.origin)} · 转写版本 {visiblePreview.transcriptRevision}
                </>
              )}
            </p>
            <p className="content-evidence-text">
              {visiblePreview.kind === 'visual'
                ? project.visualAnalyses
                    ?.find((value) => value.id === visiblePreview.analysisId)
                    ?.notes.find((note) => note.id === visiblePreview.noteId)?.text ||
                  visiblePreview.text
                : visiblePreview.text}
            </p>
            {visiblePreview.kind === 'visual' ? (
              <SourceFrameEvidenceViewer
                inline
                purpose={
                  visiblePreview.producer === 'apple-vision' ||
                  visiblePreview.producer === 'windows-media-ocr'
                    ? 'ocr'
                    : 'agent'
                }
                project={project}
                sourceEpoch={sourceEpoch}
                runProjectID={project.id}
                frames={visiblePreview.frames || []}
              />
            ) : (
              <>
                <SourceRangeMedia
                  key={`${scope}:${visiblePreview.id}`}
                  asset={asset || undefined}
                  range={{
                    start: visiblePreview.startUs / 1e6,
                    end: visiblePreview.endUs / 1e6,
                    speed: 1,
                  }}
                  title="素材内容"
                  muted={false}
                />
                <p className="field-hint">
                  词证据 {visiblePreview.wordIds.length} 项 · {visiblePreview.transcriptId}
                </p>
              </>
            )}
          </>
        ) : null}
      </Modal>
    </section>
  );
}
