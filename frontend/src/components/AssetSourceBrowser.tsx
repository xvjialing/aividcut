import { lazy, Suspense, useEffect, useRef, useState } from 'react';
import { Film, RotateCw } from 'lucide-react';
import type { Asset, Bootstrap } from '../types';
import { thumbnailEnd, thumbnailPositions } from '../lib/thumbnail';
import { SourceThumbnail } from './SourceThumbnail';
import { Button, Field } from './ui';
import { SourceOCRPanel } from './SourceOCRPanel';
import { ShotIndexPanel } from './ShotIndexPanel';

const PreviewProxyPanel = lazy(() =>
  import('./PreviewProxyPanel').then((module) => ({ default: module.PreviewProxyPanel })),
);

export function AssetSourceBrowser({
  projectId,
  asset,
  sourceEpoch = 0,
  projectRevision = 0,
  applyAnalysis,
}: {
  projectId: string;
  asset: Asset;
  sourceEpoch?: number;
  projectRevision?: number;
  applyAnalysis?: (action: () => Promise<Bootstrap>) => Promise<Bootstrap | null>;
}) {
  const frame = useRef<HTMLDivElement>(null);
  const [position, setPosition] = useState(0);
  const [settled, setSettled] = useState(0);
  const [refresh, setRefresh] = useState(0);
  const source = JSON.stringify([sourceEpoch, projectId, asset.id, asset.path, asset.fingerprint]);
  const [scope, setScope] = useState(source);
  // 在切换源的同一次渲染复位，避免先向新素材请求旧位置。
  if (scope !== source) {
    setScope(source);
    setPosition(0);
    setSettled(0);
  }
  useEffect(() => {
    const timer = setTimeout(() => setSettled(position), 180);
    return () => clearTimeout(timer);
  }, [position, source]);
  if (asset.kind !== 'image' && asset.kind !== 'video') return null;
  if (asset.missing)
    return asset.kind === 'video' ? (
      <Suspense fallback={null}>
        <PreviewProxyPanel
          key={source}
          projectId={projectId}
          sourceEpoch={sourceEpoch}
          asset={asset}
        />
      </Suspense>
    ) : null;
  const fallback = <Film size={22} aria-hidden="true" />;
  return (
    <section className="inspector-section source-browser" aria-label="素材源画面浏览">
      <div className="source-browser-heading">
        <span className="inspector-caption">源画面</span>
        <Button
          variant="ghost"
          aria-label="重新读取源画面"
          title="重新读取源画面"
          onClick={() => setRefresh((v) => v + 1)}
        >
          <RotateCw size={13} />
          重试
        </Button>
      </div>
      <div className="source-frame" ref={frame}>
        <SourceThumbnail
          detailed
          sourceEpoch={sourceEpoch}
          projectId={projectId}
          asset={asset}
          positionUs={settled}
          refresh={refresh}
          fallback={fallback}
        />
      </div>
      {asset.kind === 'video' ? (
        <>
          <Field label={`浏览位置 · 约 ${(position / 1e6).toFixed(2)} 秒`}>
            <input
              type="range"
              min={0}
              max={thumbnailEnd(asset)}
              step={1}
              value={position}
              aria-label="源画面浏览位置"
              aria-valuetext={`约 ${(position / 1e6).toFixed(2)} 秒`}
              onChange={(e) => setPosition(Number(e.target.value))}
            />
          </Field>
          <div className="source-filmstrip" role="group" aria-label="源视频取样">
            {thumbnailPositions(asset).map((value) => (
              <button
                type="button"
                key={value}
                className="source-sample"
                aria-pressed={Math.abs(position - value) < 1}
                aria-label={`浏览源画面，约 ${(value / 1e6).toFixed(2)} 秒`}
                onClick={() => {
                  setPosition(value);
                  setSettled(value);
                }}
              >
                <SourceThumbnail
                  sourceEpoch={sourceEpoch}
                  projectId={projectId}
                  asset={asset}
                  positionUs={value}
                  refresh={refresh}
                  fallback={fallback}
                />
                <span>{(value / 1e6).toFixed(1)}s</span>
              </button>
            ))}
          </div>
        </>
      ) : null}
      {applyAnalysis ? (
        <SourceOCRPanel
          projectId={projectId}
          projectRevision={projectRevision}
          sourceEpoch={sourceEpoch}
          asset={asset}
          positionUs={position}
          applyAnalysis={applyAnalysis}
        />
      ) : null}
      {asset.kind === 'video' ? (
        <Suspense fallback={<p className="field-hint">读取代理工具…</p>}>
          <PreviewProxyPanel
            key={source}
            projectId={projectId}
            sourceEpoch={sourceEpoch}
            asset={asset}
          />
        </Suspense>
      ) : null}
      {asset.kind === 'video' ? (
        <ShotIndexPanel
          projectId={projectId}
          sourceEpoch={sourceEpoch}
          asset={asset}
          onSelect={(value) => {
            setPosition(value);
            setSettled(value);
            frame.current?.scrollIntoView?.({ block: 'nearest' });
          }}
        />
      ) : null}
      <p className="field-hint">
        {asset.kind === 'video' ? '源视频取样，位置为近似时间。' : '源图片缩略图。'}
        添加到时间线后可查看剪辑效果。
      </p>
    </section>
  );
}
