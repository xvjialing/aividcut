import { lazy, Suspense, useCallback, useEffect, useRef, useState } from 'react';
import { useProjectFonts, projectFontFamily } from '../lib/use-project-fonts';
import { AudioLines, Film, Image as ImageIcon, Maximize2 } from 'lucide-react';
import type { Asset, Clip, PreviewFrame, Project } from '../types';
import { clipAt, PlaybackClock } from '../lib/editor';
import { browserAudioWindow, LegacyMediaBoundary } from '../lib/legacy-media';
import { playbackBridge } from '../lib/bridge';
import type { PlaybackAPI, PlaybackCapabilities, RenderQuality } from '../playback-types';
import type { ProxyProfile } from '../proxy-types';
import { Button, IconButton, Select } from './ui';
const RealtimePreviewSurface = lazy(() =>
  import('./RealtimePreviewSurface').then((module) => ({ default: module.RealtimePreviewSurface })),
);
export function realtimeClientReason(): string {
  if (typeof AudioContext !== 'function' || typeof AudioWorkletNode !== 'function')
    return '当前WebView没有AudioWorklet音频输出，不能启用实验预览。';
  if (typeof ImageData !== 'function') return '当前WebView没有实时Canvas图像支持。';
  return '';
}

export function Preview({
  project,
  sourceEpoch = 0,
  clock,
  asset,
  composed,
  onImport,
  onUseCurrentFrame,
  prepareProject,
  playbackAPI = playbackBridge,
  clientReason = realtimeClientReason,
}: {
  project: Project;
  sourceEpoch?: number;
  clock: PlaybackClock;
  asset?: Asset;
  composed: PreviewFrame | null;
  onImport: () => void;
  onUseCurrentFrame?: (
    intent: Omit<
      import('../frame-capture-types').CaptureIntent,
      'id' | 'projectId' | 'sourceEpoch'
    >,
  ) => void;
  prepareProject?: () => Promise<Project | null>;
  playbackAPI?: PlaybackAPI;
  clientReason?: () => string;
}) {
  const wrapper = useRef<HTMLDivElement>(null);
  const stage = useRef<HTMLDivElement>(null);
  const snapshot = useRef<HTMLImageElement>(null);
  const empty = useRef<HTMLDivElement>(null);
  const [mediaError, setMediaError] = useState('');
  const elements = useRef(new Map<string, HTMLElement>());
  const boundaries = useRef(new Map<string, LegacyMediaBoundary>());
  const syncCurrent = useRef<() => void>(() => {});
  const [audioNotice, setAudioNotice] = useState('');
  const [scale, setScale] = useState(1);
  const [activeClips, setActiveClips] = useState<Clip[]>([]);
  const activeKey = useRef('');
  const [mode, setMode] = useState<'workspace' | 'realtime'>('workspace');
  const [qualityScope, setQualityScope] = useState<{ projectId: string; quality: RenderQuality }>({
    projectId: project.id,
    quality: 'full',
  });
  const [proxyScope, setProxyScope] = useState({
    scope: project.id + ':' + sourceEpoch,
    profile: '' as ProxyProfile,
  });
  const proxyProfile =
    proxyScope.scope === project.id + ':' + sourceEpoch ? proxyScope.profile : '';
  const renderQuality = qualityScope.projectId === project.id ? qualityScope.quality : 'full';
  const [capabilities, setCapabilities] = useState<{
    key: string;
    value: PlaybackCapabilities;
  } | null>(null);
  const [modeReason, setModeReason] = useState('正在核对实时预览能力');
  const [switching, setSwitching] = useState(false);
  const [retry, setRetry] = useState(0);
  const projectFonts = useProjectFonts(project, sourceEpoch ?? 0, mode === 'workspace' && !asset);
  const guard = useRef<(() => void) | null>(null);
  const sourceMedia = useRef<HTMLMediaElement | null>(null);
  const sourceMediaRef = useCallback((element: HTMLMediaElement | null) => {
    if (sourceMedia.current && sourceMedia.current !== element) sourceMedia.current.pause();
    sourceMedia.current = element;
  }, []);
  const capabilityKey = `${project.id}:${sourceEpoch}:${project.revision}`;
  const currentCapabilities = capabilities?.key === capabilityKey ? capabilities.value : null;
  const unavailable =
    clientReason() ||
    (!currentCapabilities
      ? modeReason || '正在核对实时预览能力'
      : !currentCapabilities.available
        ? currentCapabilities.reason || '本机合成引擎不可用'
        : !currentCapabilities.projectSupported
          ? currentCapabilities.projectReason || '当前工程包含未支持的合成内容'
          : currentCapabilities.protocol !== 'AIV1' ||
              currentCapabilities.display !== 'webview_binary_canvas_audio'
            ? '本机预览协议与此客户端不匹配'
            : project.clips.length === 0
              ? '请先向时间线添加素材'
              : '');
  function ensureGuard() {
    if (clock.hasExternalDriver()) return;
    guard.current = clock.attachExternal({
      play: () => setModeReason('实时预览尚未准备，请稍后重试。'),
      pause: () => clock.synchronizeExternal(clock.getFrame(), false),
      seek: (frame) => clock.synchronizeExternal(frame, false),
      invalidate: () => {},
    });
  }
  useEffect(() => {
    let alive = true;
    setCapabilities(null);
    setModeReason('正在核对实时预览能力');
    void playbackAPI
      .getPlaybackCapabilities()
      .then((value) => {
        if (alive) {
          setCapabilities({ key: capabilityKey, value });
          setModeReason('');
        }
      })
      .catch((cause) => {
        if (alive) setModeReason(cause instanceof Error ? cause.message : '无法核对本机合成能力。');
      });
    return () => {
      alive = false;
    };
  }, [capabilityKey, playbackAPI, retry]);
  useEffect(() => {
    if (mode === 'realtime' && !asset) ensureGuard();
    return () => {
      guard.current?.();
      guard.current = null;
    };
  }, [mode, asset?.id, capabilityKey]);
  useEffect(() => () => sourceMedia.current?.pause(), [asset?.id]);
  async function chooseMode(value: 'workspace' | 'realtime', profile: ProxyProfile = '') {
    if (value === 'workspace') {
      clock.pause();
      setMode(value);
      return;
    }
    if (unavailable || switching) return;
    setSwitching(true);
    clock.pause();
    try {
      const current = prepareProject ? await prepareProject() : project;
      if (!current) {
        setModeReason('请先处理属性草稿、工程冲突或时间线手势。');
        return;
      }
      if (current.id !== project.id || current.revision !== project.revision) {
        setModeReason('工程已变化，请重新选择预览模式。');
        return;
      }
      ensureGuard();
      setProxyScope({ scope: project.id + ':' + sourceEpoch, profile });
      setMode('realtime');
      setRetry((value) => value + 1);
    } catch (cause) {
      setModeReason(cause instanceof Error ? cause.message : '无法核对当前工程。');
    } finally {
      setSwitching(false);
    }
  }
  async function chooseQuality(quality: RenderQuality) {
    if (switching || quality === renderQuality) return;
    setSwitching(true);
    try {
      if (prepareProject && !(await prepareProject())) return;
      clock.pause();
      setQualityScope({ projectId: project.id, quality });
    } finally {
      setSwitching(false);
    }
  }

  useEffect(() => {
    setMediaError('');
  }, [asset?.id, asset?.url, asset?.path, asset?.missing, project.id, project.revision]);
  useEffect(() => {
    if (!wrapper.current) return;
    const resize = () => {
      const size = wrapper.current!.getBoundingClientRect();
      setScale(
        Math.min(
          (size.width - 48) / project.settings.width,
          (size.height - 36) / project.settings.height,
        ),
      );
    };
    const observer = new ResizeObserver(resize);
    observer.observe(wrapper.current);
    resize();
    return () => observer.disconnect();
  }, [project.settings.width, project.settings.height]);

  useEffect(() => {
    if (asset || mode === 'realtime') return;
    const sync = () => {
      const frame = clock.getFrame();
      const timelineSeconds = clock.getTimeSeconds();
      let visible = false;
      const active = clipAt(project, frame);
      const key = `${project.revision}:${active.map((clip) => clip.id).join(',')}`;
      if (key !== activeKey.current) {
        activeKey.current = key;
        setActiveClips(active);
      }
      for (const clip of activeClips) {
        const element = elements.current.get(clip.id);
        if (!element) continue;
        const track = project.tracks.find((t) => t.id === clip.trackId);
        const active = frame >= clip.startFrame && frame < clip.startFrame + clip.durationFrames;
        const visualActive = active && !track?.hidden;
        element.style.visibility = visualActive ? 'visible' : 'hidden';
        if (visualActive && clip.kind !== 'audio') visible = true;
        if (element instanceof HTMLMediaElement) {
          const source = project.assets.find((asset) => asset.id === clip.assetId);
          if (source)
            boundaries.current
              .get(clip.id)
              ?.sync(
                element,
                clip,
                source,
                project.settings,
                timelineSeconds,
                clock.isPlaying(),
                active,
                clip.muted || Boolean(track?.muted),
                `${project.revision}:${clock.getSeekSerial()}`,
              );
        }
      }
      if (empty.current) empty.current.hidden = visible;
      if (snapshot.current)
        snapshot.current.hidden =
          !composed ||
          composed.frame !== frame ||
          clock.isPlaying() ||
          composed.revision !== project.revision;
    };
    sync();
    syncCurrent.current = sync;
    const unsubscribe = clock.subscribe(sync);
    return unsubscribe;
  }, [project, clock, asset, composed, activeClips, mode]);
  useEffect(
    () => () => {
      for (const element of elements.current.values())
        if (element instanceof HTMLMediaElement) element.pause();
      boundaries.current.forEach((boundary) => boundary.destroy());
    },
    [],
  );
  useEffect(() => {
    const audible = activeClips
      .filter((clip) => clip.kind === 'audio' || clip.kind === 'video')
      .map((clip) => {
        const source = project.assets.find((asset) => asset.id === clip.assetId);
        if (!source?.hasAudio) return '';
        return (
          browserAudioWindow(clip, source, project.settings)?.reason ||
          '音轨范围无法核对，该音轨在工作区已静音。'
        );
      })
      .filter(Boolean);
    setAudioNotice(audible[0] || '');
  }, [activeClips, project]);

  const renderClip = (clip: Clip) => {
    const source = project.assets.find((a) => a.id === clip.assetId);
    const trackIndex = project.tracks.findIndex((t) => t.id === clip.trackId);
    const transform = clip.transform || { x: 0, y: 0, scale: 1, rotation: 0 };
    const style = {
      zIndex: project.tracks.length - trackIndex,
      transform: `translate(${transform.x}px, ${transform.y}px) scale(${transform.scale}) rotate(${transform.rotation}deg)`,
    };
    const ref = (element: HTMLElement | null) => {
      if (element) {
        elements.current.set(clip.id, element);
        if (element instanceof HTMLMediaElement)
          boundaries.current.set(clip.id, new LegacyMediaBoundary());
      } else {
        const previous = elements.current.get(clip.id);
        if (previous instanceof HTMLMediaElement) previous.pause();
        boundaries.current.get(clip.id)?.destroy();
        boundaries.current.delete(clip.id);
        elements.current.delete(clip.id);
      }
    };
    if (clip.kind === 'text' || clip.kind === 'caption')
      return (
        <div
          key={clip.id}
          ref={ref}
          title={
            clip.textStyle.fontAssetId && !projectFonts.ready.has(clip.textStyle.fontAssetId)
              ? projectFonts.error || '正在加载工程字体…'
              : undefined
          }
          className={`stage-layer text-layer ${clip.kind}`}
          style={{
            ...style,
            justifyContent:
              clip.textStyle.align === 'left'
                ? 'flex-start'
                : clip.textStyle.align === 'right'
                  ? 'flex-end'
                  : 'center',
            paddingLeft: 32,
            paddingRight: 32,
            paddingBottom: clip.kind === 'caption' ? 48 : 0,
          }}
        >
          <span
            style={{
              color: clip.textStyle.color || '#fff',
              fontSize: clip.textStyle.fontSize || 48,
              fontFamily: clip.textStyle.fontAssetId
                ? projectFontFamily(
                    project.assets.find((a) => a.id === clip.textStyle.fontAssetId)?.fingerprint ||
                      '',
                  )
                : undefined,
              fontWeight: clip.textStyle.fontAssetId
                ? project.assets.find((a) => a.id === clip.textStyle.fontAssetId)?.font?.weight
                : undefined,
              fontStyle:
                clip.textStyle.fontAssetId &&
                project.assets.find((a) => a.id === clip.textStyle.fontAssetId)?.font?.italic
                  ? 'italic'
                  : undefined,
              visibility:
                clip.textStyle.fontAssetId && !projectFonts.ready.has(clip.textStyle.fontAssetId)
                  ? 'hidden'
                  : undefined,
              backgroundColor: clip.textStyle.background || undefined,
              textAlign: clip.textStyle.align as 'center' | 'left' | 'right',
            }}
          >
            {clip.text}
          </span>
        </div>
      );
    if (!source?.url || source.missing) return null;
    if (clip.kind === 'image')
      return (
        <img
          key={clip.id}
          ref={ref}
          src={source.url}
          alt={source.name}
          className="stage-layer media-layer"
          style={style}
          onError={() => setMediaError(`无法显示 ${source.name}，请检查源素材。`)}
        />
      );
    if (clip.kind === 'video')
      return (
        <video
          key={clip.id}
          ref={ref}
          src={source.url}
          className="stage-layer media-layer"
          style={style}
          preload="auto"
          loop={false}
          onLoadedMetadata={() => syncCurrent.current()}
          onTimeUpdate={(event) => boundaries.current.get(clip.id)?.checkEnd(event.currentTarget)}
          onPlaying={(event) => boundaries.current.get(clip.id)?.mediaPlaying(event.currentTarget)}
          onEnded={(event) => boundaries.current.get(clip.id)?.mediaEnded(event.currentTarget)}
          playsInline
          onError={() => setMediaError(`无法播放 ${source.name}。此编码可能需要代理或转换。`)}
        />
      );
    if (clip.kind === 'audio')
      return (
        <audio
          key={clip.id}
          ref={ref}
          src={source.url}
          preload="auto"
          loop={false}
          onLoadedMetadata={() => syncCurrent.current()}
          onTimeUpdate={(event) => boundaries.current.get(clip.id)?.checkEnd(event.currentTarget)}
          onPlaying={(event) => boundaries.current.get(clip.id)?.mediaPlaying(event.currentTarget)}
          onEnded={(event) => boundaries.current.get(clip.id)?.mediaEnded(event.currentTarget)}
          onError={() => setMediaError(`无法播放 ${source.name}，请检查源素材。`)}
        />
      );
    return null;
  };

  return (
    <section className="viewer" aria-label="剪辑预览">
      <div className="panel-heading viewer-heading">
        {asset ? (
          <span>素材预览</span>
        ) : (
          <Select
            aria-label="预览模式"
            value={
              mode === 'workspace'
                ? 'workspace'
                : proxyProfile
                  ? `proxy-${proxyProfile}`
                  : 'realtime'
            }
            className="input select preview-mode-select"
            disabled={switching}
            onChange={(event) => {
              const value = event.target.value;
              void chooseMode(
                value === 'workspace' ? 'workspace' : 'realtime',
                value.startsWith('proxy-') ? (value.slice(6) as ProxyProfile) : '',
              );
            }}
          >
            <option value="workspace">工作区预览</option>
            <option value="realtime" disabled={Boolean(unavailable)}>
              实时合成·原片
            </option>
            <option
              value="proxy-720p"
              disabled={Boolean(unavailable) || !currentCapabilities?.sourceProxies?.supported}
            >
              实时合成·720p代理
            </option>
            <option
              value="proxy-540p"
              disabled={Boolean(unavailable) || !currentCapabilities?.sourceProxies?.supported}
            >
              实时合成·540p代理
            </option>
          </Select>
        )}
        <div className="viewer-heading-right">
          {onUseCurrentFrame ? (
            <Button
              variant="ghost"
              disabled={!!asset && asset.kind !== 'image' && asset.kind !== 'video'}
              title={
                asset
                  ? '准备源取样参考，不含时间线效果'
                  : '准备时间线原片全精度合成参考，含字幕/图层/LUT'
              }
              onClick={() => {
                clock.pause();
                onUseCurrentFrame(
                  asset
                    ? {
                        kind: 'source',
                        frame: 0,
                        assetId: asset.id,
                        fingerprint: asset.fingerprint,
                        positionUs:
                          asset.kind === 'image'
                            ? 0
                            : Math.min(
                                Math.max(0, asset.durationUs - 1),
                                Math.round((sourceMedia.current?.currentTime || 0) * 1e6),
                              ),
                      }
                    : { kind: 'timeline', frame: clock.getFrame() },
                );
              }}
            >
              使用当前画面
            </Button>
          ) : null}
          {!asset && mode === 'realtime' ? (
            <>
              {' '}
              <Select
                className="input select preview-quality-select"
                aria-label="预览合成精度"
                title="较低精度可减少合成负担；文字与色彩细节请选原片画面及全精度核对，导出始终使用原片和原画幅。"
                value={renderQuality}
                disabled={switching}
                onChange={(e) => void chooseQuality(e.target.value as RenderQuality)}
              >
                <option value="full">全精度</option>
                <option value="half">半精度</option>
                <option value="quarter">四分之一精度</option>
              </Select>
            </>
          ) : null}
          <span className="quiet-badge">
            {project.settings.width} × {project.settings.height}
          </span>
          <IconButton label="全屏预览" onClick={() => void wrapper.current?.requestFullscreen?.()}>
            <Maximize2 size={14} />
          </IconButton>
        </div>
      </div>
      <div className="viewer-canvas" ref={wrapper}>
        {asset ? (
          <div className="source-preview">
            {asset.missing || !asset.url ? (
              <div className="source-missing">
                <Film size={32} />
                <p>源素材不可用</p>
                <span>{asset.name}</span>
              </div>
            ) : asset.kind === 'video' ? (
              <video
                key={asset.id}
                ref={sourceMediaRef}
                src={asset.url}
                controls
                playsInline
                preload="metadata"
                onError={() => setMediaError('此素材暂时无法播放，请检查编码与文件位置。')}
              />
            ) : asset.kind === 'image' ? (
              <img src={asset.url} alt={asset.name} />
            ) : (
              <div className="audio-preview">
                <AudioLines size={40} />
                <span>{asset.name}</span>
                <audio ref={sourceMediaRef} src={asset.url} controls preload="metadata" />
              </div>
            )}
          </div>
        ) : mode === 'realtime' ? (
          <Suspense
            fallback={
              <div className="realtime-preview-status" role="status">
                正在加载实时合成预览…
              </div>
            }
          >
            <RealtimePreviewSurface
              key={`${sourceEpoch}:${capabilityKey}:${renderQuality}:${proxyProfile}:${retry}`}
              sourceEpoch={sourceEpoch}
              proxyProfile={proxyProfile}
              renderQuality={renderQuality}
              project={project}
              clock={clock}
              api={playbackAPI}
              allowed={!unavailable}
              reason={unavailable}
              onRetry={() => setRetry((value) => value + 1)}
              onFallback={() => void chooseMode('workspace')}
            />
          </Suspense>
        ) : (
          <div
            ref={stage}
            className="preview-stage"
            style={{
              width: project.settings.width,
              height: project.settings.height,
              transform: `translate(-50%, -50%) scale(${Math.max(0.05, scale)})`,
            }}
          >
            {activeClips.map(renderClip)}
            {composed ? (
              <img
                className="composed-frame"
                ref={snapshot}
                src={composed.url}
                alt={`第 ${composed.frame} 帧的后端合成检查结果`}
              />
            ) : null}
          </div>
        )}
        {!asset && mode === 'workspace' ? (
          <div className="viewer-empty" ref={empty}>
            {project.assets.length === 0 ? (
              <>
                <div className="empty-frame-icon">
                  <ImageIcon size={23} />
                  <span />
                </div>
                <h2>从第一个镜头开始</h2>
                <p>
                  导入视频、音频或图片，
                  <br />
                  在时间线上编排你的故事。
                </p>
                <Button onClick={onImport} variant="primary">
                  导入素材
                </Button>
              </>
            ) : (
              <>
                <Film size={27} />
                <p>{project.clips.length ? '当前时刻没有可见画面' : '将素材添加到时间线'}</p>
              </>
            )}
          </div>
        ) : null}
        {mediaError ? (
          <div role="alert" className="viewer-error">
            {mediaError}
          </div>
        ) : null}
      </div>
      <div className="viewer-footnote">
        <span className="status-dot" />
        <span>
          {asset
            ? asset.name
            : mode === 'realtime'
              ? '实时合成预览（实验）'
              : projectFonts.error ||
                (project.clips.some(
                  (c) =>
                    c.textStyle.fontAssetId && !projectFonts.ready.has(c.textStyle.fontAssetId),
                )
                  ? '正在加载工程字体 · 源素材与文字工作区预览'
                  : '源素材与文字工作区预览')}
        </span>
        {projectFonts.error ? (
          <Button variant="ghost" onClick={projectFonts.retry}>
            重试字体
          </Button>
        ) : null}
        <span className="viewer-note">
          {!asset && mode === 'realtime'
            ? '音频主时钟 · 当前工程单独校验'
            : 'LUT 与输出效果请用合成帧核对'}
        </span>
        {!asset && mode === 'workspace' && audioNotice ? (
          <span className="viewer-note" title={audioNotice}>
            工作区声音近似预览
          </span>
        ) : null}
        {!asset && mode === 'workspace' && unavailable ? (
          <span className="viewer-note" title={unavailable}>
            实验预览不可用：{unavailable}
          </span>
        ) : null}
      </div>
    </section>
  );
}
