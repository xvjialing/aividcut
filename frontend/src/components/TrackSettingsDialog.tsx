import { useRef, useState } from 'react';
import type { Bootstrap, Command, Track } from '../types';
import { trackNames } from '../lib/editor';
import { Button, Field, Input, Modal, Select } from './ui';

export function trackDeleteReason(snapshot: Bootstrap, track: Track) {
  if (track.locked) return '先解锁轨道，再删除空轨。';
  if (snapshot.project.tracks.length <= 1) return '工程至少保留一条轨道。';
  if (snapshot.project.clips.some((clip) => clip.trackId === track.id))
    return '轨道仍有片段。请先移走或删除片段。';
  if (snapshot.project.transcripts?.some((t) => t.captionTrackId === track.id))
    return '轨道仍绑定源字幕。请先解除源字幕绑定。';
  return '';
}

export function TrackSettingsDialog({
  snapshot,
  track,
  onApply,
  onClose,
  onReturnFocus,
}: {
  snapshot: Bootstrap;
  track: Track;
  onApply: (commands: Command[], label: string) => Promise<Bootstrap | null>;
  onClose: () => void;
  onReturnFocus: () => void;
}) {
  const [name, setName] = useState(track.name),
    [locked, setLocked] = useState(track.locked),
    [muted, setMuted] = useState(track.muted),
    [hidden, setHidden] = useState(track.hidden),
    [move, setMove] = useState('0'),
    [confirmDelete, setConfirmDelete] = useState(false),
    [busy, setBusy] = useState(false),
    [error, setError] = useState(''),
    [invalid, setInvalid] = useState(false);
  const pending = useRef(false),
    composing = useRef(false),
    nameRef = useRef<HTMLInputElement>(null);
  const tracks = snapshot.project.tracks.filter((t) => t.kind === track.kind),
    index = tracks.findIndex((t) => t.id === track.id),
    previous = tracks[index - 1],
    next = tracks[index + 1],
    deleteReason = trackDeleteReason(snapshot, track);
  const moveDisabled = (target?: Track) => locked || !target || target.locked;
  async function apply(commands: Command[], label: string) {
    if (pending.current) return;
    pending.current = true;
    setBusy(true);
    setError('');
    try {
      if (await onApply(commands, label)) onClose();
      else setError('轨道未更新。工程可能已变化，请关闭窗口核对当前工作后重试。');
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : '轨道未更新，请重试。');
    } finally {
      pending.current = false;
      setBusy(false);
    }
  }
  function save(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (composing.current || pending.current) return;
    const value = name.trim();
    if (
      value !== track.name &&
      (!value || Array.from(value).length > 120 || /[\u0000-\u001f\u007f-\u009f]/.test(value))
    ) {
      setError('名称需为1–120个字符，不能包含控制字符。');
      setInvalid(true);
      nameRef.current?.focus();
      return;
    }
    const commands: Command[] = [];
    if (
      value !== track.name ||
      locked !== track.locked ||
      muted !== track.muted ||
      hidden !== track.hidden
    )
      commands.push({
        type: 'update_track',
        track: { ...track, name: value, locked, muted, hidden },
      });
    if (move !== '0')
      commands.push({ type: 'move_track', trackId: track.id, trackDelta: Number(move) });
    if (!commands.length) {
      onClose();
      return;
    }
    void apply(commands, '保存轨道设置');
  }
  return (
    <Modal
      open
      title="轨道设置"
      description={`${trackNames[track.kind]}轨 · ${track.name} · ${snapshot.project.clips.filter((c) => c.trackId === track.id).length} 个片段`}
      busy={busy}
      onOpenChange={(open) => {
        if (!open && !pending.current) onClose();
      }}
      onCloseAutoFocus={(e) => {
        e.preventDefault();
        onReturnFocus();
      }}
    >
      <form
        noValidate
        onSubmit={save}
        onCompositionStartCapture={() => {
          composing.current = true;
        }}
        onCompositionEndCapture={() => {
          composing.current = false;
        }}
      >
        <Field label="轨道名称">
          <Input
            ref={nameRef}
            value={name}
            maxLength={120}
            disabled={busy || confirmDelete}
            aria-invalid={invalid || undefined}
            aria-describedby={invalid ? 'track-settings-error' : undefined}
            onChange={(e) => {
              setName(e.target.value);
              setInvalid(false);
              setError('');
            }}
          />
        </Field>
        <label className="checkbox-field">
          <input
            type="checkbox"
            checked={locked}
            disabled={busy || confirmDelete}
            onChange={(e) => {
              setLocked(e.target.checked);
              setMove('0');
            }}
          />
          锁定轨道
        </label>
        {track.kind === 'video' || track.kind === 'audio' ? (
          <label className="checkbox-field">
            <input
              type="checkbox"
              checked={muted}
              disabled={busy || confirmDelete}
              onChange={(e) => setMuted(e.target.checked)}
            />
            静音轨道声音
          </label>
        ) : null}
        {track.kind !== 'audio' ? (
          <label className="checkbox-field">
            <input
              type="checkbox"
              checked={hidden}
              disabled={busy || confirmDelete}
              onChange={(e) => setHidden(e.target.checked)}
            />
            隐藏轨道画面
          </label>
        ) : null}
        <Field
          label="同类轨道顺序"
          hint="上方画面轨覆盖下方画面轨。调整顺序不移动片段时间；不能交换锁定轨道。"
        >
          <Select
            value={move}
            disabled={busy || confirmDelete}
            onChange={(e) => setMove(e.target.value)}
          >
            <option value="0">保持当前顺序</option>
            <option value="-1" disabled={moveDisabled(previous)}>
              {previous
                ? `上移至「${previous.name}」上方${previous.locked ? '（已锁定）' : ''}`
                : '上移（已是同类最上轨）'}
            </option>
            <option value="1" disabled={moveDisabled(next)}>
              {next
                ? `下移至「${next.name}」下方${next.locked ? '（已锁定）' : ''}`
                : '下移（已是同类最下轨）'}
            </option>
          </Select>
        </Field>
        {error ? (
          <p id="track-settings-error" className="inline-error" role="alert">
            {error}
          </p>
        ) : null}
        {confirmDelete ? (
          <>
            <p role="status">
              删除空轨「{track.name}」？未保存的轨道设置会放弃，可通过撤销恢复轨道。
            </p>
            <div className="dialog-actions">
              <Button autoFocus disabled={busy} onClick={() => setConfirmDelete(false)}>
                返回设置
              </Button>
              <Button
                variant="danger"
                busy={busy}
                onClick={() =>
                  void apply([{ type: 'delete_track', trackId: track.id }], '删除空轨道')
                }
              >
                确认删除空轨
              </Button>
            </div>
          </>
        ) : (
          <>
            <div className="dialog-actions">
              <Button
                variant="danger"
                disabled={busy || Boolean(deleteReason)}
                onClick={() => {
                  setConfirmDelete(true);
                  setError('');
                }}
              >
                删除空轨…
              </Button>
              <span className="grow" />
              <Button disabled={busy} onClick={onClose}>
                取消
              </Button>
              <Button type="submit" variant="primary" busy={busy}>
                保存设置
              </Button>
            </div>
            {deleteReason ? <p className="field-hint">{deleteReason}</p> : null}
          </>
        )}
      </form>
    </Modal>
  );
}
