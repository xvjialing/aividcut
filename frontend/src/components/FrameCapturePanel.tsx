import { useEffect, useRef, useState } from 'react';
import type { Bootstrap, Project } from '../types';
import type {
  CaptureIntent,
  FrameCaptureAPI,
  FrameCaptureInput,
  FrameCaptureResult,
} from '../frame-capture-types';
import { frameCaptureAPI } from '../lib/bridge';
import { Button, Field, Input, Select } from './ui';
import { SecondsInput } from './SecondsInput';
export function FrameCapturePanel({
  project,
  sourceEpoch,
  currentFrame,
  pausePlayback,
  intent,
  prepareProject,
  applyToProject,
  onCaptured,
  api = frameCaptureAPI,
}: {
  project: Project;
  sourceEpoch: number;
  currentFrame: () => number;
  pausePlayback?: () => void;
  intent?: CaptureIntent | null;
  prepareProject?: () => Promise<Project | null>;
  applyToProject: (action: () => Promise<Bootstrap>) => Promise<Bootstrap | null>;
  onCaptured: (result: FrameCaptureResult) => void;
  api?: FrameCaptureAPI;
}) {
  const [open, setOpen] = useState(!!intent);
  const [kind, setKind] = useState<'timeline' | 'source'>(intent?.kind || 'timeline');
  const [assetId, setAssetId] = useState(intent?.assetId || '');
  const [position, setPosition] = useState(intent?.positionUs || 0);
  const [frame, setFrame] = useState(String(intent?.frame || currentFrame()));
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const [captured, setCaptured] = useState<
    (Pick<FrameCaptureResult, 'assetId' | 'origin' | 'imported'> & { imageURL?: string }) | null
  >(null);
  const pending = useRef(false);
  const request = useRef<FrameCaptureInput | null>(null);
  const alive = useRef(false);
  const base = `${project.id}:${sourceEpoch}`;
  const latest = useRef(base);
  latest.current = base;
  useEffect(() => {
    alive.current = true;
    return () => {
      alive.current = false;
    };
  }, []);
  useEffect(() => {
    setError('');
    setNotice('');
    setCaptured(null);
    request.current = null;
  }, [base]);
  useEffect(() => {
    if (intent && intent.projectId === project.id && intent.sourceEpoch === sourceEpoch) {
      setOpen(true);
      setKind(intent.kind);
      setAssetId(intent.assetId || '');
      setPosition(intent.positionUs || 0);
      setFrame(String(intent.frame));
      setError('');
      request.current = null;
    }
  }, [intent?.id, base]);
  function edit() {
    setError('');
    setCaptured(null);
    setNotice('');
    request.current = null;
  }
  async function capture() {
    if (pending.current) return;
    pending.current = true;
    pausePlayback?.();
    setBusy(true);
    setError('');
    setNotice('');
    const context = base;
    try {
      const ready = prepareProject ? await prepareProject() : project;
      if (!alive.current || latest.current !== context) return;
      if (!ready || ready.id !== project.id) throw Error('工程已切换，请重新选择画面');
      const source = ready.assets.find((a) => a.id === assetId);
      const value = Number(frame);
      if (
        kind === 'timeline' &&
        (!frame.trim() ||
          !Number.isSafeInteger(value) ||
          value < 0 ||
          value >= Math.max(0, ...ready.clips.map((c) => c.startFrame + c.durationFrames)))
      )
        throw Error('请输入有效的时间线整数帧，不能取片尾之外');
      if (kind === 'source' && (!source || source.missing || !source.fingerprint))
        throw Error('请选择当前可用且已核对的源图片或视频');
      const input: FrameCaptureInput = request.current || {
        id: crypto.randomUUID(),
        projectId: ready.id,
        projectRevision: ready.revision,
        sourceEpoch,
        kind,
        frame: kind === 'timeline' ? value : 0,
        ...(kind === 'source'
          ? { assetId: source!.id, fingerprint: source!.fingerprint, positionUs: position }
          : {}),
      };
      request.current = input;
      let result: FrameCaptureResult | undefined;
      let failed: unknown;
      const imported = await applyToProject(async () => {
        try {
          result = await api.capture(input);
          return result.bootstrap;
        } catch (cause) {
          failed = cause;
          throw cause;
        }
      });
      if (alive.current && latest.current === context) {
        if (!imported || !result) throw failed || Error('尚未保存取图，请处理工程草稿或冲突后重试');
        const finished = result;
        setCaptured({
          assetId: finished.assetId,
          origin: result.origin,
          imported: result.imported,
          imageURL: finished.bootstrap.project.assets.find((a) => a.id === finished.assetId)?.url,
        });
        setNotice(
          result.imported
            ? '图片已保存到素材库，尚未上传或生成。'
            : '这个操作已完成过，撤销后未重新导入；如需新图片，请改变参数或开始新操作。',
        );
        if (result.imported) onCaptured(result);
      }
    } catch (cause) {
      if (alive.current && latest.current === context)
        setError(cause instanceof Error ? cause.message : '取图失败，请重试');
    } finally {
      pending.current = false;
      if (alive.current) setBusy(false);
    }
  }
  return (
    <details className="speech-details" open={open} onToggle={(e) => setOpen(e.currentTarget.open)}>
      <summary>从当前画面准备参考</summary>
      {open ? (
        <section className="frame-capture-panel" aria-label="准备参考画面">
          <p className="field-hint">
            只在本机保存一张图片，可撤销。选择参考或首尾帧后，生成仍需单独核对和提交。
          </p>
          <Field label="取图来源">
            <Select
              value={kind}
              disabled={busy}
              onChange={(e) => {
                setKind(e.target.value as 'timeline' | 'source');
                edit();
              }}
            >
              <option value="timeline">时间线合成 · 含图层/字幕/LUT</option>
              <option value="source">源素材取样 · 无时间线效果</option>
            </Select>
          </Field>
          {kind === 'timeline' ? (
            <>
              <Field label="时间线帧">
                <Input
                  type="number"
                  min={0}
                  step={1}
                  value={frame}
                  disabled={busy}
                  onChange={(e) => {
                    setFrame(e.target.value);
                    edit();
                  }}
                />
              </Field>
              <Button
                disabled={busy}
                onClick={() => {
                  setFrame(String(currentFrame()));
                  edit();
                }}
              >
                读取当前播放头
              </Button>
              <p className="field-hint">
                读取原片及全精度合成，不使用低精度/代理画面；包含当前可见文字、字幕和LUT。
              </p>
            </>
          ) : (
            <>
              <Field label="取样源素材">
                <Select
                  value={assetId}
                  disabled={busy}
                  onChange={(e) => {
                    setAssetId(e.target.value);
                    setPosition(0);
                    edit();
                  }}
                >
                  <option value="">请选择图片或视频</option>
                  {project.assets
                    .filter((a) => (a.kind === 'video' || a.kind === 'image') && !a.missing)
                    .map((a) => (
                      <option value={a.id} key={a.id}>
                        {a.name}
                      </option>
                    ))}
                </Select>
              </Field>
              <Field label="源位置 · 秒">
                <SecondsInput
                  microseconds={position}
                  disabled={busy || project.assets.find((a) => a.id === assetId)?.kind === 'image'}
                  onChange={(value) => {
                    setPosition(value);
                    edit();
                  }}
                />
              </Field>
              <p className="field-hint">
                沿用详细源取样（最高960×540 JPEG），位置近似；不含字幕、图层或时间线调色。
              </p>
            </>
          )}
          <div className="button-row">
            <Button variant="primary" busy={busy} onClick={() => void capture()}>
              保存为参考图片
            </Button>
            {busy ? (
              <Button
                onClick={() => {
                  if (request.current)
                    void api
                      .cancel({
                        id: request.current.id,
                        projectId: request.current.projectId,
                        sourceEpoch: request.current.sourceEpoch,
                      })
                      .catch((cause) =>
                        setError(cause instanceof Error ? cause.message : '取消失败'),
                      );
                }}
              >
                取消取图
              </Button>
            ) : null}
          </div>
          {captured?.imported && captured.imageURL ? (
            <img
              className="frame-capture-result-image"
              src={captured.imageURL}
              alt="已保存的本机参考画面"
              style={{
                aspectRatio: `${captured.origin.asset.width}/${captured.origin.asset.height}`,
              }}
            />
          ) : null}
          {captured?.imported ? (
            <p className="field-hint">
              {captured.origin.asset.name} · {captured.origin.asset.width}×
              {captured.origin.asset.height} ·{' '}
              {captured.origin.approximate ? '近似源取样' : '原片全精度合成'}
            </p>
          ) : null}
          <p className="form-error" role="alert">
            {error}
          </p>
          <p className="field-hint" role="status">
            {notice}
          </p>
        </section>
      ) : null}
    </details>
  );
}
