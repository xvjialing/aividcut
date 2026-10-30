import { useId, useState } from 'react';
import type { Clip, Project } from '../types';
import { timecode } from '../lib/editor';
import { linkedSelection, trimSelectionClips, groupTrimDraft } from '../lib/clip-selection';
import { Button, Field, Input, Select } from './ui';

export function SelectionInspector({
  project,
  clips,
  busy,
  onMove,
  onTrim,
  onDelete,
}: {
  project: Project;
  clips: Clip[];
  busy: boolean;
  onMove: (delta: number) => Promise<boolean>;
  onTrim?: (edge: 'start' | 'end', delta: number) => Promise<boolean>;
  onDelete: () => void;
}) {
  const [delta, setDelta] = useState('0');
  const errorId = useId();
  const trimErrorId = useId();
  const [trimEdge, setTrimEdge] = useState<'start' | 'end'>('start');
  const [trimDelta, setTrimDelta] = useState('0');
  const linked = linkedSelection(
    project,
    clips.map((c) => c.id),
  );
  const locked = linked.some((c) => project.tracks.find((t) => t.id === c.trackId)?.locked);
  const start = linked.reduce((value, c) => Math.min(value, c.startFrame), Infinity),
    end = linked.reduce((value, c) => Math.max(value, c.startFrame + c.durationFrames), 0);
  const number = Number(delta),
    valid = delta.trim() !== '' && Number.isSafeInteger(number) && start + number >= 0;
  const trimClips = trimSelectionClips(
    project,
    clips.map((c) => c.id),
  );
  const trimLocked = trimClips.some((c) => project.tracks.find((t) => t.id === c.trackId)?.locked);
  const trimNumber = Number(trimDelta);
  const trimPreview = groupTrimDraft(
    project,
    clips.map((c) => c.id),
    trimEdge,
    trimNumber,
  );
  const trimValid = trimDelta.trim() !== '' && trimPreview.valid;
  const sourceIDs = new Set(trimClips.filter((c) => !c.captionSource).map((c) => c.id));
  const requiresDetach = trimClips.some(
    (c) => c.captionSource && !sourceIDs.has(c.captionSource.sourceClipId),
  );
  return (
    <div className="inspector-content">
      <div className="inspector-section">
        <div className="inspector-caption">多片段选择</div>
        <h3>已选择 {clips.length} 个片段</h3>
        <p className="field-hint">含联动共 {linked.length} 个片段，移动、修剪和删除统一撤销。</p>
      </div>
      <div className="inspector-section metadata-list">
        <div>
          <span>起始</span>
          <strong>{timecode(start, project.settings)}</strong>
        </div>
        <div>
          <span>末端</span>
          <strong>{timecode(end, project.settings)}</strong>
        </div>
      </div>
      <div className="inspector-section">
        <Field label="整组水平移动（帧）">
          <Input
            type="number"
            step={1}
            min={-start}
            value={delta}
            disabled={busy || locked}
            aria-invalid={!valid || undefined}
            aria-describedby={!valid ? errorId : undefined}
            onChange={(e) => setDelta(e.target.value)}
          />
        </Field>
        {!valid ? (
          <p className="form-error" id={errorId}>
            请输入整数帧，移动后起始不能早于第0帧。
          </p>
        ) : null}
        {locked ? <p className="field-hint">选区或联动片段位于锁定轨道，请先解锁。</p> : null}
        <div className="speech-actions">
          <Button
            disabled={busy || locked || !valid || number === 0}
            onClick={() =>
              void onMove(number).then((success) => {
                if (success) setDelta('0');
              })
            }
          >
            移动选中片段
          </Button>
          <Button variant="ghost" disabled={busy || locked} onClick={onDelete}>
            删除选中片段
          </Button>
        </div>
        <p className="field-hint">
          Alt+左右键移动1帧，Alt+Shift移动10帧。C分割跨越播放头的选中片段，⌘/Ctrl
          D复制到播放头。起点/终点把手左右键修剪1帧，Shift修剪10帧。
        </p>
      </div>
      {onTrim ? (
        <div className="inspector-section">
          <Field label="修剪边缘">
            <Select
              value={trimEdge}
              disabled={busy || trimLocked}
              onChange={(e) => setTrimEdge(e.target.value as 'start' | 'end')}
            >
              <option value="start">起点</option>
              <option value="end">终点</option>
            </Select>
          </Field>
          <Field label="整组修剪量（帧）">
            <Input
              type="number"
              step={1}
              value={trimDelta}
              disabled={busy || trimLocked || requiresDetach}
              aria-invalid={!trimValid || undefined}
              aria-describedby={!trimValid ? trimErrorId : undefined}
              onChange={(e) => setTrimDelta(e.target.value)}
            />
          </Field>
          {!trimValid && !trimLocked && !requiresDetach ? (
            <p className="form-error" id={trimErrorId}>
              请输入整数帧；每个片段须至少1帧，时间和素材入点不能为负。
            </p>
          ) : null}
          {trimLocked ? (
            <p className="field-hint">选区、联动成员或派生字幕轨道已锁定，不能部分修剪。</p>
          ) : null}
          {requiresDetach ? (
            <p className="field-hint">绑定字幕需与其源片段一起选择，或先解除绑定。</p>
          ) : null}
          <Button
            disabled={busy || trimLocked || requiresDetach || !trimValid || trimNumber === 0}
            onClick={() =>
              void onTrim(trimEdge, trimNumber).then((success) => {
                if (success) setTrimDelta('0');
              })
            }
          >
            修剪选中片段
          </Button>
          <p className="field-hint">
            正数向右、负数向左；起点向右缩短，终点向右延长，不自动移动其它片段。源末端和重叠由工程核心整体校验，失败保留参数。
          </p>
        </div>
      ) : null}
    </div>
  );
}
