import { useEffect, useRef, useState } from 'react';
import type { GenerationOutputAsset } from '../generation-types';
import { Button } from './ui';

// 列表与对比共用成品读取和释放行为，不额外探测、上传或请求模型。
export function SavedCandidateMedia({
  asset,
  active = true,
  showDetails = false,
  onPlay,
  onAudible,
}: {
  asset: GenerationOutputAsset;
  active?: boolean;
  showDetails?: boolean;
  onPlay?: (node: HTMLVideoElement) => void;
  onAudible?: (node: HTMLVideoElement) => void;
}) {
  const video = useRef<HTMLVideoElement | null>(null);
  const [attempt, setAttempt] = useState(0);
  const [error, setError] = useState('');
  const [details, setDetails] = useState('');
  useEffect(() => {
    setError('');
    setDetails('');
  }, [asset.url, asset.sha256, active, attempt]);
  useEffect(() => {
    const node = video.current;
    return () => node?.pause();
  }, [asset.url, asset.sha256, active, attempt, error]);
  if (!active) return <p className="field-hint">列表预览已暂停，正在处理当前候选。</p>;
  if (!asset.url) return <p className="field-hint">本机预览地址不可用，请刷新任务。</p>;
  return (
    <div className="saved-candidate-media">
      {!error ? (
        asset.kind === 'video' ? (
          <video
            key={`${asset.url}:${attempt}`}
            ref={video}
            src={asset.url}
            controls
            muted
            preload="metadata"
            aria-label={asset.name}
            onLoadedMetadata={(event) => {
              const node = event.currentTarget;
              setDetails(
                `${node.videoWidth}×${node.videoHeight} · ${Number.isFinite(node.duration) ? node.duration.toFixed(3) + '秒' : '时长未知'}`,
              );
            }}
            onPlay={(event) => onPlay?.(event.currentTarget)}
            onVolumeChange={(event) => {
              if (!event.currentTarget.muted && event.currentTarget.volume > 0)
                onAudible?.(event.currentTarget);
            }}
            onError={() => setError('视频暂时无法读取或解码，请重试；文件缺失时请恢复原文件。')}
          />
        ) : (
          <img
            key={`${asset.url}:${attempt}`}
            src={asset.url}
            alt={asset.name}
            loading="lazy"
            onLoad={(event) =>
              setDetails(`${event.currentTarget.naturalWidth}×${event.currentTarget.naturalHeight}`)
            }
            onError={() => setError('图片暂时无法读取，请重试；文件缺失时请恢复原文件。')}
          />
        )
      ) : (
        <div className="candidate-no-preview">
          <p className="form-error" role="status">
            {error}
          </p>
          <Button onClick={() => setAttempt((value) => value + 1)}>重试读取</Button>
        </div>
      )}
      {showDetails && details ? <p className="field-hint">{details}</p> : null}
    </div>
  );
}
