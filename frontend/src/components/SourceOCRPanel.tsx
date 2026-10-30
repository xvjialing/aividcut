import { useEffect, useId, useRef, useState } from 'react';
import type { Asset, Bootstrap } from '../types';
import type { SourceOCRAPI, SourceOCRDraft } from '../source-ocr-types';
import { analysisProducer } from '../visual-analysis-types';
import { sourceOCRBridge } from '../lib/bridge';
import { Button, Field, Modal } from './ui';

export function SourceOCRPanel({
  projectId,
  projectRevision,
  sourceEpoch,
  asset,
  positionUs,
  applyAnalysis,
  api = sourceOCRBridge,
}: {
  projectId: string;
  projectRevision: number;
  sourceEpoch: number;
  asset: Asset;
  positionUs: number;
  applyAnalysis?: (action: () => Promise<Bootstrap>) => Promise<Bootstrap | null>;
  api?: SourceOCRAPI;
}) {
  const [capability, setCapability] = useState<Awaited<
    ReturnType<SourceOCRAPI['capability']>
  > | null>(null);
  const [draft, setDraft] = useState<SourceOCRDraft | null>(null);
  const [text, setText] = useState('');
  const [busy, setBusy] = useState(false);
  const [accepting, setAccepting] = useState(false);
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const [invalid, setInvalid] = useState(false);
  const errorID = useId();
  const textField = useRef<HTMLTextAreaElement>(null);
  const operation = useRef('');
  const sequence = useRef(0);
  const trigger = useRef<HTMLButtonElement>(null);
  const scope = JSON.stringify([
    projectId,
    projectRevision,
    sourceEpoch,
    asset.id,
    asset.path,
    asset.fingerprint,
    positionUs,
  ]);
  const currentScope = useRef(scope);
  currentScope.current = scope;
  const draftScope = useRef('');
  const visibleDraft = draftScope.current === scope ? draft : null;
  useEffect(() => {
    let alive = true;
    setCapability(null);
    void api
      .capability()
      .then((value) => {
        if (alive) setCapability(value);
      })
      .catch(() => {
        if (alive)
          setCapability({ available: false, engine: '', reason: '无法读取本地文字识别能力' });
      });
    return () => {
      alive = false;
    };
  }, [api]);
  useEffect(() => {
    sequence.current++;
    setBusy(false);
    setAccepting(false);
    setDraft(null);
    setError('');
    setText('');
    setInvalid(false);
    return () => {
      sequence.current++;
      if (operation.current) void api.cancel(operation.current).catch(() => {});
      operation.current = '';
    };
  }, [scope, api]);
  async function recognize() {
    if (operation.current || !capability?.available) return;
    const id = crypto.randomUUID();
    operation.current = id;
    const token = ++sequence.current,
      captured = scope;
    setBusy(true);
    setError('');
    setNotice('');
    setInvalid(false);
    try {
      const result = await api.prepare({
        requestId: id,
        projectId,
        projectRevision,
        sourceEpoch,
        assetId: asset.id,
        fingerprint: asset.fingerprint || '',
        positionUs,
      });
      if (token !== sequence.current || captured !== currentScope.current) return;
      if (
        result.id !== id ||
        result.projectId !== projectId ||
        result.projectRevision !== projectRevision ||
        result.sourceEpoch !== sourceEpoch ||
        result.frame.evidence.assetId !== asset.id ||
        result.frame.evidence.sourceFingerprint !== asset.fingerprint ||
        result.frame.evidence.positionUs !== positionUs ||
        result.result.engine !== capability.engine
      )
        throw Error('识别回复不属于当前源画面，请重试。');
      draftScope.current = captured;
      setDraft(result);
      setText(result.result.lines.map((line) => line.text).join('\n'));
    } catch (cause) {
      if (token === sequence.current) {
        operation.current = '';
        void api.cancel(id).catch(() => {});
        setError(cause instanceof Error ? cause.message : '文字识别失败，请重试');
      }
    } finally {
      if (token === sequence.current) setBusy(false);
    }
  }
  async function discard() {
    if (accepting) return;
    sequence.current++;
    const id = operation.current;
    operation.current = '';
    setBusy(false);
    setDraft(null);
    setError('');
    if (id) await api.cancel(id).catch(() => {});
  }
  async function accept() {
    if (!visibleDraft || accepting || !applyAnalysis) return;
    const lines = text
      .split('\n')
      .map((line) => line.trim())
      .filter(Boolean);
    if (
      lines.length < 1 ||
      lines.length > 50 ||
      lines.some((line) => Array.from(line).length > 1000)
    ) {
      setInvalid(true);
      setError('请保留1–50行文字，每行不超过1000字。');
      textField.current?.focus();
      return;
    }
    setAccepting(true);
    setInvalid(false);
    setError('');
    const token = sequence.current;
    try {
      const value = await applyAnalysis(() =>
        api.accept({
          id: visibleDraft.id,
          projectId: visibleDraft.projectId,
          projectRevision: visibleDraft.projectRevision,
          sourceEpoch: visibleDraft.sourceEpoch,
          lines,
        }),
      );
      if (!value) throw Error('尚未接纳，请处理工程草稿或冲突后重试。');
      if (token === sequence.current) {
        operation.current = '';
        setDraft(null);
        setNotice('画面文字已保存到内容记录，可检索、回看和撤销。');
      }
    } catch (cause) {
      if (token === sequence.current)
        setError(cause instanceof Error ? cause.message : '未接纳识别结果');
    } finally {
      if (token === sequence.current) setAccepting(false);
    }
  }
  return (
    <section className="inspector-section" aria-label="源画面文字识别">
      <span className="inspector-caption">画面文字</span>
      <p className="field-hint">仅识别当前源画面，不生成字幕。请核对取样位置与识别结果。</p>
      <Button
        ref={trigger}
        disabled={
          !capability?.available || busy || accepting || !applyAnalysis || !asset.fingerprint
        }
        onClick={() => void recognize()}
        busy={busy}
      >
        识别当前画面文字
      </Button>
      {busy ? <Button onClick={() => void discard()}>取消识别</Button> : null}
      {!capability?.available ? (
        <p className="field-hint">{capability?.reason || '读取本机识别能力…'}</p>
      ) : null}
      {error && !visibleDraft ? (
        <p role="alert" className="form-error">
          {error}
        </p>
      ) : null}
      {notice ? (
        <p role="status" className="field-hint">
          {notice}
        </p>
      ) : null}
      <Modal
        open={Boolean(visibleDraft)}
        title="核对画面文字"
        description="本机识别可能有误；修改后明确接纳，保存为可检索的内容记录，不改变时间线。"
        busy={accepting}
        onOpenChange={(open) => {
          if (!open) void discard();
        }}
        onCloseAutoFocus={(event) => {
          event.preventDefault();
          trigger.current?.focus();
        }}
      >
        {visibleDraft ? (
          <>
            <p className="field-hint">
              {asset.name} · 源位置 {(visibleDraft.frame.evidence.positionUs / 1e6).toFixed(3)} 秒 ·
              {visibleDraft.result.engine === 'windows-media-ocr'
                ? analysisProducer['windows-media-ocr']
                : analysisProducer['apple-vision']}
              {' · '}
              {visibleDraft.result.languages.join('、')}
            </p>
            <img
              className="source-frame ocr-review-frame"
              src={visibleDraft.frame.url}
              alt="本次文字识别的源画面"
            />
            {visibleDraft.result.truncated ? (
              <p role="status" className="field-hint">
                结果已截断，最多保留50条、每条1000字；未覆盖整段素材。
              </p>
            ) : null}
            {visibleDraft.result.lines.length ? (
              <>
                <Field label="核对识别文字（每行一条）">
                  <textarea
                    className="input resize-none"
                    ref={textField}
                    aria-invalid={invalid}
                    aria-describedby={error ? errorID : undefined}
                    aria-label="核对识别文字（每行一条）"
                    value={text}
                    onChange={(event) => setText(event.target.value)}
                    rows={8}
                    disabled={accepting}
                  />
                </Field>
                {visibleDraft.result.lines.every((line) => line.confidence === null) ? (
                  <p className="field-hint">系统未提供逐行置信度，请核对源画面。</p>
                ) : (
                  <details>
                    <summary>查看原始识别置信度</summary>
                    {visibleDraft.result.lines.map((line, index) => (
                      <p key={index} className="field-hint">
                        {line.confidence === null
                          ? '系统未提供置信度'
                          : `${Math.round(line.confidence * 100)}%`}{' '}
                        · {line.text}
                      </p>
                    ))}
                  </details>
                )}
              </>
            ) : (
              <p role="status">当前取样画面没有识别到文字。可以换一个源位置重试。</p>
            )}
            {error ? (
              <p id={errorID} role="alert" className="form-error">
                {error}
              </p>
            ) : null}
            <div className="dialog-actions">
              <Button disabled={accepting} onClick={() => void discard()}>
                放弃结果
              </Button>
              <Button
                variant="primary"
                busy={accepting}
                disabled={!visibleDraft.result.lines.length || !text.trim()}
                onClick={() => void accept()}
              >
                接纳画面文字
              </Button>
            </div>
          </>
        ) : null}
      </Modal>
    </section>
  );
}
