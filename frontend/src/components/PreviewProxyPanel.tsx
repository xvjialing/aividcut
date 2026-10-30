import { useEffect, useRef, useState } from 'react';
import type { Asset } from '../types';
import type { PreviewProxyAPI, PreviewProxyInput, PreviewProxyView } from '../proxy-types';
import { previewProxyAPI } from '../lib/bridge';
import { Button, Field, Modal, Select } from './ui';
export function PreviewProxyPanel({
  projectId,
  sourceEpoch,
  asset,
  api = previewProxyAPI,
}: {
  projectId: string;
  sourceEpoch: number;
  asset: Asset;
  api?: PreviewProxyAPI;
}) {
  const [open, setOpen] = useState(false);
  const [profile, setProfile] = useState<'720p' | '540p'>('720p');
  const [view, setView] = useState<PreviewProxyView | null>(null);
  const [working, setWorking] = useState('');
  const [error, setError] = useState('');
  const [confirmAll, setConfirmAll] = useState(false);
  const pending = useRef(false);
  const token = useRef(0);
  const scope = JSON.stringify([projectId, sourceEpoch, asset.id, asset.fingerprint, profile]);
  const current = useRef(scope);
  current.current = scope;
  const input: PreviewProxyInput = {
    projectId,
    sourceEpoch,
    assetId: asset.id,
    fingerprint: asset.fingerprint || '',
    profile,
  };
  async function read() {
    const id = ++token.current;
    const context = scope;
    try {
      const value = await api.get(input);
      if (id === token.current && current.current === context) {
        setView(value);
        setError('');
      }
    } catch (cause) {
      if (id === token.current && current.current === context)
        setError(cause instanceof Error ? cause.message : '无法读取代理状态。');
    }
  }
  useEffect(() => {
    setView(null);
    setError('');
    setConfirmAll(false);
    if (open) void read();
    return () => {
      token.current++;
    };
  }, [scope, open]);
  const active = view?.status === 'queued' || view?.status === 'running';
  useEffect(() => {
    if (!open || !active) return;
    const timer = window.setInterval(() => void read(), 1000);
    return () => clearInterval(timer);
  }, [scope, open, active]);
  async function action(kind: 'start' | 'clear' | 'clear-all' | 'cancel') {
    if (pending.current) return;
    const context = scope;
    pending.current = true;
    setWorking(kind);
    setError('');
    token.current++;
    try {
      if (kind === 'start') {
        const value = await api.start(input);
        if (current.current === context) setView(value);
      } else {
        if (kind === 'clear' || kind === 'clear-all') {
          await api.clear({ ...input, clearAll: kind === 'clear-all' });
          setConfirmAll(false);
        } else if (view?.job) await api.cancel(view.job.id);
        if (current.current === context) await read();
      }
    } catch (cause) {
      if (current.current === context)
        setError(cause instanceof Error ? cause.message : '代理操作失败，原片保留。');
    } finally {
      pending.current = false;
      if (current.current === context) setWorking('');
    }
  }
  return (
    <details
      className="speech-details"
      open={open}
      onToggle={(event) => setOpen(event.currentTarget.open)}
    >
      <summary>视频代理与缓存</summary>
      {open ? (
        <section className="proxy-panel" aria-label="视频代理与缓存">
          <p className="field-hint">
            生成低分辨率画面供实时预览，声音和导出仍读取原片。代理有损，请用原片核对色彩与细节。
          </p>
          <Field label="代理画面档位">
            <Select
              value={profile}
              disabled={!!working}
              onChange={(event) => setProfile(event.target.value as '720p' | '540p')}
            >
              <option value="720p">720p · 长边1280</option>
              <option value="540p">540p · 长边960</option>
            </Select>
          </Field>
          <p className="field-hint" role="status">
            {view?.reason || '正在读取代理状态…'}
          </p>
          {view?.proof ? (
            <p className="field-hint">
              {view.proof.width}×{view.proof.height} · {view.proof.frames}帧 ·{' '}
              {(view.proof.bytes / 1048576).toFixed(1)}MiB · 帧时间已核对
            </p>
          ) : null}
          {active ? (
            <p className="field-hint" role="status">
              {view?.job?.message || '等待本地代理任务'}
            </p>
          ) : null}
          <div className="button-row">
            <Button
              disabled={!!working || active}
              busy={working === 'start'}
              onClick={() => void action('start')}
            >
              {view?.status === 'cached' ? '核对已有代理' : '生成/核对代理'}
            </Button>
            {active ? (
              <Button
                disabled={!!working}
                busy={working === 'cancel'}
                onClick={() => void action('cancel')}
              >
                取消代理任务
              </Button>
            ) : null}
            <Button
              disabled={!!working || active || !view || view.status === 'missing'}
              busy={working === 'clear'}
              onClick={() => void action('clear')}
            >
              清理该档代理
            </Button>
            <Button disabled={!!working} onClick={() => void read()}>
              刷新代理状态
            </Button>
          </div>
          <p className="field-hint">
            本机代理缓存：{view?.cacheVideos || 0}份 ·{' '}
            {((view?.cacheBytes || 0) / 1048576).toFixed(1)}MiB
          </p>
          <Button
            disabled={
              !!working || active || !view || (!view.cacheBytes && view.status !== 'invalid')
            }
            onClick={() => {
              setError('');
              setConfirmAll(true);
            }}
          >
            清理全部代理
          </Button>
          <Modal
            open={confirmAll}
            title="清理全部代理"
            description="清理本机所有工程已生成的视频代理，之后需按需重新生成。原片和工程内容保留；活跃任务、播放进程或当前工程已登记的缓存素材会阻止清理。"
            busy={!!working}
            onOpenChange={(open) => {
              if (!working) setConfirmAll(open);
            }}
          >
            <p>
              将清理本机代理缓存（当前读取约{view?.cacheVideos || 0}
              份）。缓存状态会在执行前重新检查。
            </p>
            {error ? (
              <p className="form-error" role="alert">
                {error}
              </p>
            ) : null}
            <div className="dialog-actions">
              <Button autoFocus disabled={!!working} onClick={() => setConfirmAll(false)}>
                保留代理
              </Button>
              <Button
                variant="danger"
                busy={working === 'clear-all'}
                disabled={!!working}
                onClick={() => void action('clear-all')}
              >
                清理全部代理
              </Button>
            </div>
          </Modal>
          <p className="field-hint">
            生成后在实时预览中选择同一代理档位，再重新准备。尚无就绪代理的素材继续使用原片；缓存最多32份/8GiB，清理前请退出实时预览。
          </p>
          <p className="form-error" role="alert">
            {!confirmAll ? error : ''}
          </p>
        </section>
      ) : null}
    </details>
  );
}
