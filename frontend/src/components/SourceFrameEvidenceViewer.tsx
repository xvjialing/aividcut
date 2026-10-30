import { useEffect, useRef, useState } from 'react';
import type { Project } from '../types';
import type { SourceFrameAPI, SourceFrameEvidence } from '../source-frame-types';
import { sourceFrameAPI } from '../lib/bridge';
import { formatSourceSeconds } from '../lib/action-review';
import { Button, Modal } from './ui';

export function SourceFrameEvidenceViewer({
  project,
  sourceEpoch = 0,
  runProjectID,
  frames,
  api = sourceFrameAPI,
  inline = false,
  purpose = 'agent',
}: {
  project: Project;
  sourceEpoch?: number;
  runProjectID: string;
  frames: SourceFrameEvidence[];
  api?: SourceFrameAPI;
  inline?: boolean;
  purpose?: 'agent' | 'ocr';
}) {
  const [selected, setSelected] = useState<SourceFrameEvidence | null>(null);
  const [url, setURL] = useState('');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const read = useRef(0);
  const pending = useRef('');
  const scope = `${sourceEpoch}:${project.id}:${project.revision}`;
  const scopeRef = useRef(scope);
  scopeRef.current = scope;
  const selectedScope = useRef('');
  useEffect(() => {
    read.current++;
    setSelected(null);
    setURL('');
    setBusy(false);
    setError('');
    return () => {
      read.current++;
      if (pending.current) void api.cancel(pending.current).catch(() => {});
      pending.current = '';
    };
  }, [scope, api]);
  function issue(frame: SourceFrameEvidence) {
    if (runProjectID !== project.id) return '请先打开记录所属工程。';
    const asset = project.assets.find((value) => value.id === frame.assetId);
    if (!asset || asset.missing || asset.fingerprint !== frame.sourceFingerprint)
      return '源素材缺失或指纹已变化，不能冒充原证据。';
    return '';
  }
  async function open(frame: SourceFrameEvidence) {
    const reason = issue(frame);
    if (reason) {
      setError(reason);
      return;
    }
    if (pending.current) void api.cancel(pending.current).catch(() => {});
    const id = crypto.randomUUID(),
      current = ++read.current,
      capturedScope = scope;
    pending.current = id;
    selectedScope.current = scope;
    setSelected(frame);
    setURL('');
    setError('');
    setBusy(true);
    try {
      const value = await api.read({
        sourceEpoch,
        requestId: id,
        projectId: project.id,
        assetId: frame.assetId,
        fingerprint: frame.sourceFingerprint,
        positionUs: frame.requestedPositionUs,
        profile: frame.profile,
      });
      if (current !== read.current || scopeRef.current !== capturedScope) return;
      const actual = value.evidence;
      if (
        actual.id !== frame.id ||
        actual.imageSha256 !== frame.imageSha256 ||
        actual.sourceFingerprint !== frame.sourceFingerprint ||
        actual.assetId !== frame.assetId ||
        actual.positionUs !== frame.positionUs ||
        actual.width !== frame.width ||
        actual.height !== frame.height ||
        actual.recipeVersion !== frame.recipeVersion ||
        actual.profile !== frame.profile
      )
        throw Error('当前画面与历史图像指纹不一致；源文件或提取器版本可能已变化，未展示为原证据。');
      setURL(value.url);
    } catch (cause) {
      if (current === read.current && scopeRef.current === capturedScope)
        setError(cause instanceof Error ? cause.message : '源画面读取失败，可重试。');
    } finally {
      if (current === read.current) {
        setBusy(false);
        pending.current = '';
      }
    }
  }
  const visible = selectedScope.current === scope ? selected : null;
  function closeFrame() {
    read.current++;
    if (pending.current) void api.cancel(pending.current).catch(() => {});
    pending.current = '';
    setSelected(null);
    setURL('');
    setBusy(false);
  }
  const body = visible ? (
    <>
      <p>
        {visible.assetName} · 约 {formatSourceSeconds(visible.positionUs)}秒
      </p>
      {busy ? <p role="status">正在核对源画面…</p> : null}
      {url ? (
        <img
          className="source-evidence-image"
          src={url}
          alt={`${visible.assetName}，约${formatSourceSeconds(visible.positionUs)}秒的源画面`}
          onError={() => {
            setURL('');
            setError('画面暂时无法加载，请关闭后重试。');
          }}
        />
      ) : null}
      {error ? (
        <p className="form-error" role="alert">
          {error}
        </p>
      ) : null}
    </>
  ) : null;
  return (
    <section aria-label="源画面证据">
      <p className="field-hint">
        {purpose === 'ocr'
          ? '与本次文字识别关联的取样画面。回看只读取本机，不重新识别或调用模型。'
          : '本次工具准备的源画面；不证明CLI模型已收到或理解。回看只读取本机，不重新推理。'}
      </p>
      {frames.map((frame) => (
        <div className="source-evidence-item" key={frame.id}>
          <p>
            {frame.assetName} · 约 {formatSourceSeconds(frame.positionUs)}秒 · {frame.width}×
            {frame.height}
          </p>
          <p className="file-path" title={frame.id}>
            证据 {frame.id}
          </p>
          {issue(frame) ? <p className="field-hint">{issue(frame)}</p> : null}
          <Button disabled={Boolean(issue(frame))} onClick={() => void open(frame)}>
            回看证据画面
          </Button>
        </div>
      ))}
      {inline ? (
        visible ? (
          <div className="source-evidence-inline">
            {body}
            <Button onClick={closeFrame}>关闭画面</Button>
          </div>
        ) : null
      ) : (
        <Modal
          open={Boolean(visible)}
          title="回看画面证据"
          description="近似源取样，不含时间线和LUT效果；需匹配历史图像字节指纹。"
          onOpenChange={(value) => {
            if (!value) closeFrame();
          }}
        >
          {body}
        </Modal>
      )}
    </section>
  );
}
