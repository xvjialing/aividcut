import { useEffect, useRef, useState } from 'react';
import type { PlaybackAPI, RenderQuality } from '../playback-types';
import type { Project } from '../types';
import type { PlaybackClock } from '../lib/editor';
import { RealtimePlayback, type RealtimeViewState } from '../lib/realtime-playback';
import { Button } from './ui';

const phaseNames: Record<RealtimeViewState['phase'], string> = {
  preparing: '准备中',
  ready: '已准备',
  playing: '播放中',
  buffering: '缓冲中',
  paused: '已暂停',
  ended: '已到结尾',
  failed: '不可用',
  closed: '已关闭',
};
export function RealtimePreviewSurface({
  project,
  clock,
  api,
  allowed,
  reason,
  onRetry,
  onFallback,
  renderQuality = 'full',
  proxyProfile = '',
  sourceEpoch = 0,
}: {
  project: Project;
  clock: PlaybackClock;
  api: PlaybackAPI;
  allowed: boolean;
  reason: string;
  onRetry: () => void;
  onFallback: () => void;
  renderQuality?: RenderQuality;
  proxyProfile?: import('../proxy-types').ProxyProfile;
  sourceEpoch?: number;
}) {
  const canvas = useRef<HTMLCanvasElement>(null);
  const [state, setState] = useState<RealtimeViewState>({
    phase: 'preparing',
    reason: '正在核对当前工程',
  });
  useEffect(() => {
    if (!canvas.current) return;
    let alive = true;
    const playback = new RealtimePlayback({
      project,
      clock,
      canvas: canvas.current,
      api,
      onState: (value) => {
        if (alive) setState(value);
      },
      renderQuality,
      proxyProfile,
      sourceEpoch,
    });
    if (allowed) void playback.open();
    else playback.block(reason || '当前工程尚未通过实时预览校验。');
    return () => {
      alive = false;
      playback.dispose();
    };
  }, [
    project.id,
    project.revision,
    clock,
    api,
    allowed,
    reason,
    renderQuality,
    proxyProfile,
    sourceEpoch,
  ]);
  return (
    <div className="realtime-preview" aria-label="实时合成预览（实验）">
      <div className="realtime-preview-picture">
        <canvas ref={canvas} aria-label="本机引擎实时合成画面" />
      </div>
      <div className="realtime-preview-status" role={state.phase === 'failed' ? 'alert' : 'status'}>
        <span className="quiet-badge">
          {phaseNames[state.phase]}
          {state.width && state.height ? ` · ${state.width}×${state.height}` : ''}
        </span>
        {state.proxySummary ? <span>{state.proxySummary}</span> : null}
        {state.proxySummary && state.phase === 'ready' ? null : (
          <span>{state.reason || '声音由音频主时钟推进，画面跟随已消费的声音。'}</span>
        )}
        <div className="speech-actions">
          <Button onClick={onRetry} disabled={state.phase === 'preparing'}>
            重新准备
          </Button>
        </div>
        {state.phase === 'failed' ? (
          <div className="speech-actions">
            <Button variant="ghost" onClick={onFallback}>
              返回工作区预览
            </Button>
          </div>
        ) : null}
      </div>
    </div>
  );
}
