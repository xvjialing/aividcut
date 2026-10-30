import { Captions, Download, Plus, Search, Upload } from 'lucide-react';
import type { Clip, Project } from '../types';
import { filterCaptions, timecode } from '../lib/editor';
import { Button, IconButton } from './ui';

export function SubtitleLibrary({
  project,
  query,
  limit,
  busy,
  exportPath,
  onMore,
  onSelect,
  onImport,
  onExport,
  onAdd,
}: {
  project: Project;
  query: string;
  limit: number;
  busy: boolean;
  exportPath?: string;
  onMore: () => void;
  onSelect: (clip: Clip) => void;
  onImport: () => void;
  onExport: () => void;
  onAdd: () => void;
}) {
  const captions = filterCaptions(project, query);
  const hasCaptions = project.clips.some((clip) => clip.kind === 'caption');
  return (
    <>
      <div className="subtitle-actions">
        <Button onClick={onImport} disabled={busy}>
          <Upload size={13} />
          导入 SRT
        </Button>
        <Button
          variant="ghost"
          disabled={busy || !hasCaptions}
          onClick={onExport}
          title="将全部字幕轨导出为一个 SRT 文件"
        >
          <Download size={13} />
          导出 SRT
        </Button>
        <IconButton label="添加一条静态字幕" disabled={busy} onClick={onAdd}>
          <Plus size={14} />
        </IconButton>
      </div>
      <p className="subtitle-note">
        静态字幕按工程时间定位，修改视频后请核对时序。绑定字幕跟随源片段；源词可在右侧“语音”面板核对。
      </p>
      {exportPath ? (
        <p className="subtitle-export-path" role="status">
          SRT 已导出<span>{exportPath}</span>
        </p>
      ) : null}
      {captions.slice(0, limit).map((clip, index) => (
        <button
          type="button"
          className="subtitle-card"
          key={clip.id}
          aria-label={`选择字幕 ${clip.name}：${clip.text || '空字幕'}`}
          onClick={() => onSelect(clip)}
          disabled={busy}
          data-caption-id={clip.id}
        >
          <span className="subtitle-card-heading">
            <span className="subtitle-index">{String(index + 1).padStart(2, '0')}</span>
            <span>{timecode(clip.startFrame, project.settings)}</span>
            <span>→</span>
            <span>{timecode(clip.startFrame + clip.durationFrames, project.settings)}</span>
          </span>
          <span className="subtitle-card-text" title={clip.text}>
            {clip.text || '空字幕'}
          </span>
          {clip.captionSource ? (
            <span className="field-hint">
              源转写绑定{clip.captionSource.textOverride ? ' · 人工文字' : ''}
              {clip.captionSource.styleOverride ? ' · 人工样式' : ''}
              {clip.captionSource.stale ? ' · 来源陈旧' : ''}
            </span>
          ) : null}
        </button>
      ))}
      {captions.length > limit ? (
        <div className="load-more">
          <span>
            已显示 {limit} / {captions.length} 条
          </span>
          <Button variant="ghost" onClick={onMore}>
            再显示 40 条
          </Button>
        </div>
      ) : null}
      {!captions.length ? (
        <div className="asset-empty">
          {query ? (
            <>
              <Search size={25} />
              <p>没有找到匹配字幕</p>
              <span>清空搜索可查看全部字幕。</span>
            </>
          ) : (
            <>
              <Captions size={27} />
              <p>把已有字幕放进影片</p>
              <span>导入 SRT，或先手动添加一条字幕。文字和时长都能继续精修。</span>
              <Button onClick={onImport} disabled={busy}>
                <Upload size={14} />
                导入 SRT 字幕
              </Button>
            </>
          )}
        </div>
      ) : null}
    </>
  );
}
