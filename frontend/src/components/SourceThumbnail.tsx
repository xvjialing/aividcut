import { useEffect, useRef, useState, type ReactNode } from 'react';
import type { Asset } from '../types';
import { thumbnailQueue, type ThumbnailQueue } from '../lib/thumbnail';

// 卡片和源浏览共用可见性、取消与过期结果保护；完成缓存由Go按文件身份维护。
export function SourceThumbnail({
  projectId,
  asset,
  positionUs = 0,
  refresh = 0,
  fallback,
  queue = thumbnailQueue,
  sourceEpoch = 0,
  detailed = false,
}: {
  projectId: string;
  asset: Asset;
  positionUs?: number;
  refresh?: number;
  fallback?: ReactNode;
  queue?: ThumbnailQueue;
  sourceEpoch?: number;
  detailed?: boolean;
}) {
  const element = useRef<HTMLSpanElement>(null);
  const [visible, setVisible] = useState(() => typeof IntersectionObserver === 'undefined');
  const key = JSON.stringify([
    sourceEpoch,
    projectId,
    asset.id,
    asset.path,
    asset.fingerprint,
    asset.missing,
    positionUs,
    refresh,
  ]);
  const [result, setResult] = useState({ key: '', url: '', error: '', loaded: false });
  const supported = !asset.missing && (asset.kind === 'video' || asset.kind === 'image');
  useEffect(() => {
    if (!element.current || typeof IntersectionObserver === 'undefined') return;
    const observer = new IntersectionObserver(([entry]) => setVisible(entry.isIntersecting));
    observer.observe(element.current);
    return () => observer.disconnect();
  }, []);
  useEffect(() => {
    if (!visible || !supported) return;
    const controller = new AbortController();
    setResult({ key, url: '', error: '', loaded: false });
    void queue
      .read(projectId, asset, positionUs, controller.signal, sourceEpoch)
      .then((image) => {
        if (!controller.signal.aborted)
          setResult({ key, url: image.url, error: '', loaded: false });
      })
      .catch((reason: unknown) => {
        if (!controller.signal.aborted)
          setResult({
            key,
            url: '',
            error: reason instanceof Error ? reason.message : '源画面未读取，请重试。',
            loaded: false,
          });
      });
    return () => controller.abort();
  }, [
    key,
    visible,
    supported,
    queue,
    projectId,
    asset.id,
    asset.path,
    asset.fingerprint,
    sourceEpoch,
    positionUs,
  ]);
  const current = result.key === key ? result : { url: '', error: '', loaded: false };
  const state = !supported
    ? 'unavailable'
    : !visible
      ? 'idle'
      : current.error
        ? 'error'
        : current.loaded
          ? 'ready'
          : 'loading';
  return (
    <span
      ref={element}
      className="source-thumbnail"
      data-thumbnail-state={state}
      title={current.error || undefined}
    >
      {current.url && visible ? (
        <img
          key={current.url}
          src={current.url}
          alt=""
          decoding="async"
          onLoad={() =>
            setResult((value) => (value.key === key ? { ...value, loaded: true } : value))
          }
          onError={() =>
            setResult({ key, url: '', error: '源画面访问已失效，请重试。', loaded: false })
          }
        />
      ) : (
        fallback
      )}
      {state === 'loading' ? <span className="thumbnail-status">读取中…</span> : null}
      {state === 'error' ? (
        <span className={`thumbnail-status ${detailed ? 'thumbnail-error-details' : ''}`}>
          {detailed ? current.error : '画面未读取'}
        </span>
      ) : null}
      <span className="sr-only">
        {current.error ||
          (state === 'ready' ? '已读取源画面' : state === 'loading' ? '正在读取源画面' : '')}
      </span>
    </span>
  );
}
