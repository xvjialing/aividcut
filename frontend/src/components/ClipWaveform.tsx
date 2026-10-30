import { useEffect, useMemo, useRef, useState } from 'react';
import type { Asset, Clip, Settings } from '../types';
import {
  addWaveformTile,
  waveformQueue,
  waveformWindow,
  type WaveformQueue,
} from '../lib/waveform';

export function ClipWaveform({
  projectId,
  asset,
  clip,
  settings,
  pixelsPerFrame,
  viewLeft,
  viewWidth,
  queue = waveformQueue,
}: {
  projectId: string;
  asset: Asset;
  clip: Clip;
  settings: Settings;
  pixelsPerFrame: number;
  viewLeft: number;
  viewWidth: number;
  queue?: WaveformQueue;
}) {
  const canvas = useRef<HTMLCanvasElement>(null);
  const [retry, setRetry] = useState(0);
  const [status, setStatus] = useState<'loading' | 'ready' | 'empty' | 'error'>('loading');
  const [error, setError] = useState('');
  const [onscreen, setOnscreen] = useState(() => typeof IntersectionObserver === 'undefined');
  const window = useMemo(
    () => waveformWindow(clip, asset, settings, pixelsPerFrame, viewLeft, viewWidth),
    [clip, asset, settings, pixelsPerFrame, viewLeft, viewWidth],
  );
  useEffect(() => {
    if (!canvas.current || typeof IntersectionObserver === 'undefined') return;
    const observer = new IntersectionObserver(([entry]) => setOnscreen(entry.isIntersecting));
    observer.observe(canvas.current);
    return () => observer.disconnect();
  }, [!!window]);
  useEffect(() => {
    if (!window || !canvas.current || !onscreen) return;
    const element = canvas.current;
    const context = element.getContext('2d');
    if (!context) {
      setStatus('error');
      setError('当前窗口无法绘制波形，请重启客户端后重试。');
      return;
    }
    const controller = new AbortController();
    // 只分配可见宽度，设备像素比限制为2，避免长片段分配超宽画布。
    const ratio = Math.min(2, globalThis.devicePixelRatio || 1);
    const width = Math.max(1, Math.ceil(window.width * ratio));
    const height = 22;
    element.width = width;
    element.height = height * ratio;
    const mins = new Float32Array(width),
      maxs = new Float32Array(width),
      coverage = new Uint8Array(width);
    setStatus('loading');
    setError('');
    let paint = 0;
    const draw = () => {
      paint = 0;
      context.clearRect(0, 0, width, height * ratio);
      context.strokeStyle = getComputedStyle(element).color;
      context.lineWidth = 1;
      context.beginPath();
      const middle = (height * ratio) / 2;
      for (let x = 0; x < width; x++)
        if (coverage[x]) {
          const top = middle - Math.max(-1, Math.min(1, maxs[x])) * (middle - ratio);
          const bottom = middle - Math.max(-1, Math.min(1, mins[x])) * (middle - ratio);
          context.moveTo(x + 0.5, top);
          context.lineTo(x + 0.5, Math.max(top + 1, bottom));
        }
      context.stroke();
    };
    // 已读波形只重绘颜色，不因界面外观变化重新请求音频。
    const themeObserver = new MutationObserver(() => {
      if (!paint) paint = requestAnimationFrame(draw);
    });
    themeObserver.observe(document.documentElement, {
      attributes: true,
      attributeFilter: ['data-theme'],
    });
    void (async () => {
      for (let index = window.first; index <= window.last; index++) {
        const tile = await queue.read(projectId, asset, index, controller.signal);
        if (controller.signal.aborted) return;
        addWaveformTile(tile, window.sourceStartUs, window.sourceEndUs, mins, maxs, coverage);
        if (!paint) paint = requestAnimationFrame(draw);
      }
      if (!controller.signal.aborted) setStatus(coverage.some(Boolean) ? 'ready' : 'empty');
    })().catch((reason: unknown) => {
      if (!controller.signal.aborted) {
        setError(reason instanceof Error ? reason.message : '无法读取源音频。');
        setStatus('error');
      }
    });
    return () => {
      controller.abort();
      themeObserver.disconnect();
      if (paint) cancelAnimationFrame(paint);
      element.width = 0;
      element.height = 0;
    };
  }, [projectId, asset, window, queue, retry, onscreen]);
  if (!window) return null;
  return (
    <div
      className="clip-waveform"
      style={{ left: window.left, width: window.width }}
      data-waveform-state={status}
    >
      <canvas ref={canvas} aria-hidden="true" />
      {status === 'loading' ? <span className="waveform-state">读取波形…</span> : null}
      {status === 'empty' ? <span className="waveform-state">此区间无音频样本</span> : null}
      {status === 'error' ? (
        <button
          type="button"
          className="waveform-retry"
          title={error}
          aria-label={`重试读取 ${clip.name} 的波形：${error}`}
          onPointerDown={(e) => e.stopPropagation()}
          onClick={(e) => {
            e.stopPropagation();
            setRetry((v) => v + 1);
          }}
        >
          波形未读取 · 重试
        </button>
      ) : null}
      <span className="sr-only">
        {status === 'ready'
          ? '源音频峰值波形'
          : status === 'loading'
            ? '正在读取源音频波形'
            : status === 'error'
              ? error
              : '此区间无音频样本'}
      </span>
    </div>
  );
}
