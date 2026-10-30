import { useEffect, useRef, useState } from 'react';
import type { Asset } from '../types';
import { formatSourceSeconds } from '../lib/action-review';

export interface SourceRange {
  start: number;
  end: number;
  speed: number;
}
export function SourceRangeMedia({
  asset,
  range,
  title,
  muted = true,
}: {
  asset?: Asset;
  range?: SourceRange | null;
  title: string;
  muted?: boolean;
}) {
  const video = useRef<HTMLMediaElement | null>(null);
  const [error, setError] = useState('');
  const Media = asset?.kind === 'audio' ? 'audio' : 'video';
  function position() {
    if (!video.current || !range) return;
    try {
      video.current.playbackRate = range.speed;
      video.current.currentTime = range.start;
      setError('');
    } catch {
      video.current.pause();
      setError('浏览器无法按该源入点或速度预览，请在工程合成预览中核对。');
    }
  }
  useEffect(() => {
    setError('');
    if (video.current && video.current.readyState >= 1) position();
  }, [asset?.url, range?.start, range?.end, range?.speed]);
  useEffect(() => {
    const node = video.current;
    return () => {
      node?.pause();
    };
  }, [asset?.url]);
  function constrain() {
    const node = video.current;
    if (!node || !range) return;
    if (node.currentTime < range.start) node.currentTime = range.start;
    if (node.currentTime >= range.end) {
      node.pause();
      node.currentTime = Math.max(range.start, range.end - 0.001);
    }
    if (node.playbackRate !== range.speed) {
      try {
        node.playbackRate = range.speed;
      } catch {
        node.pause();
        setError('浏览器不支持工程指定速度。');
      }
    }
  }
  return (
    <section className="replacement-source">
      <h3>{title}</h3>
      {asset?.url && !asset.missing && range ? (
        asset.kind === 'image' ? (
          <img src={asset.url} alt={asset.name} />
        ) : (
          <Media
            ref={(node: HTMLVideoElement | HTMLAudioElement | null) => {
              video.current = node;
            }}
            src={asset.url}
            controls={!error}
            muted={muted}
            preload="metadata"
            aria-label={`${title}源区间预览`}
            onLoadedMetadata={position}
            onTimeUpdate={constrain}
            onSeeking={constrain}
            onRateChange={constrain}
            onPlay={() => {
              const node = video.current;
              if (node && range && node.currentTime >= range.end - 0.002)
                node.currentTime = range.start;
            }}
            onError={() => setError('此源素材暂时无法在浏览器中预览。')}
          />
        )
      ) : (
        <div className="replacement-media-empty">
          {asset?.missing
            ? '原素材缺失，不能展示画面。'
            : title === '原镜头' || range
              ? '当前源素材预览地址不可用。'
              : '先预览变化，探测候选源区间与实际余量。'}
        </div>
      )}
      {asset ? (
        <p className="field-hint replacement-source-name">
          <strong title={asset.name}>{asset.name}</strong>
          <span>
            {asset.kind !== 'image'
              ? `${formatSourceSeconds(asset.durationUs)}秒 · ${asset.hasAudio ? '有音轨' : '无音轨'}`
              : `${asset.width}×${asset.height}`}
          </span>
        </p>
      ) : null}
      {range ? (
        <p className="field-hint">
          {asset?.kind !== 'image'
            ? `源区间 ${formatSourceSeconds(Math.round(range.start * 1e6))}–${formatSourceSeconds(Math.round(range.end * 1e6))}秒 · ${range.speed}x`
            : '静态画面 · 1x'}
        </p>
      ) : null}
      {error ? (
        <p className="form-error" role="status">
          {error}
        </p>
      ) : null}
    </section>
  );
}
