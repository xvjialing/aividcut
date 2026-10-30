import { useEffect, useId, useRef, useState } from 'react';
import type { Bootstrap } from '../types';
import type { SequenceAPI, SequenceChangeInput } from '../sequence-types';
import { sequenceAPI } from '../lib/bridge';
import {
  currentSequenceID,
  sequenceChoices,
  sequenceDraft,
  uniqueSequenceName,
} from '../lib/sequences';
import { parseNewProject, type NewProjectErrors } from '../lib/new-project';
import { NewProjectSettings } from './NewProjectSettings';
import { Button, Field, Input, Modal, Select } from './ui';

export function SequenceManagerDialog({
  snapshot,
  onApply,
  onClose,
  onReturnFocus,
  api = sequenceAPI,
}: {
  snapshot: Bootstrap;
  onApply: (operation: () => Promise<Bootstrap>, label: string) => Promise<Bootstrap | null>;
  onClose: () => void;
  onReturnFocus: () => void;
  api?: SequenceAPI;
}) {
  const choices = sequenceChoices(snapshot);
  const [selected, setSelected] = useState(currentSequenceID(snapshot)),
    [mode, setMode] = useState<'list' | 'create' | 'duplicate' | 'rename' | 'delete'>('list');
  const [name, setName] = useState(''),
    [draft, setDraft] = useState(sequenceDraft(snapshot.project.settings)),
    [errors, setErrors] = useState<NewProjectErrors>({});
  const [error, setError] = useState(''),
    [busy, setBusy] = useState(false);
  const pending = useRef(false),
    composing = useRef(false),
    nameRef = useRef<HTMLInputElement>(null),
    cancelRef = useRef<HTMLButtonElement>(null),
    errorID = useId();
  const target = choices.find((s) => s.id === selected) || choices[0];
  useEffect(() => {
    if (mode === 'delete') cancelRef.current?.focus();
    else if (mode !== 'list') nameRef.current?.focus();
  }, [mode]);
  function form(next: typeof mode) {
    setMode(next);
    setError('');
    setErrors({});
    setName(
      next === 'create'
        ? uniqueSequenceName(snapshot, '新序列')
        : next === 'duplicate'
          ? uniqueSequenceName(snapshot, `${target.name} 副本`)
          : target.name,
    );
    setDraft(sequenceDraft(snapshot.project.settings));
  }
  async function send(action: SequenceChangeInput['action']) {
    if (pending.current || composing.current) return;
    let settings: SequenceChangeInput['settings'];
    if (action === 'create' || action === 'duplicate' || action === 'rename') {
      const clean = name.trim();
      if (!clean || Array.from(clean).length > 80 || /[\u0000-\u001f\u007f]/.test(clean)) {
        setErrors({ name: '序列名称需为1到80个字符，不能包含控制字符。' });
        nameRef.current?.focus();
        return;
      }
      if (
        choices.some(
          (s) =>
            s.id !== (action === 'rename' ? target.id : '') &&
            s.name.toLocaleLowerCase() === clean.toLocaleLowerCase(),
        )
      ) {
        setErrors({ name: '已有同名序列，请使用不同名称。' });
        nameRef.current?.focus();
        return;
      }
      if (action === 'create') {
        const parsed = parseNewProject(clean, draft);
        setErrors(parsed.errors);
        if (!parsed.settings) {
          document
            .querySelector<HTMLInputElement>('[data-sequence-fields] [aria-invalid="true"]')
            ?.focus();
          return;
        }
        settings = { ...parsed.settings, sampleRate: snapshot.project.settings.sampleRate };
      }
    }
    pending.current = true;
    setBusy(true);
    setError('');
    const input: SequenceChangeInput = {
      projectId: snapshot.project.id,
      projectRevision: snapshot.project.revision,
      sourceEpoch: snapshot.sourceEpoch || 0,
      action,
      sequenceId: action === 'create' ? '' : target.id,
      idempotencyKey: crypto.randomUUID(),
      ...(settings ? { settings } : {}),
      ...(action === 'create' || action === 'duplicate' || action === 'rename'
        ? { name: name.trim() }
        : {}),
      ...(action === 'delete' ? { confirmDelete: true } : {}),
    };
    let failure = '未取得序列操作回执，请关闭窗口核对工程后重试，不会自动重复提交。';
    const label = {
      switch: '切换序列',
      create: '新建序列',
      duplicate: '复制序列',
      rename: '重命名序列',
      delete: '删除序列',
    }[action];
    try {
      const result = await onApply(async () => {
        try {
          return await api.change(input);
        } catch (cause) {
          failure = cause instanceof Error ? cause.message : failure;
          throw cause;
        }
      }, label);
      if (result) onClose();
      else setError(failure);
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : failure);
    } finally {
      pending.current = false;
      setBusy(false);
    }
  }
  const deleteReason =
    choices.length < 2
      ? '工程至少保留一个序列。'
      : target.locked
        ? '此序列有锁定轨道，请先进入并解锁后再删除。'
        : '';
  return (
    <Modal
      open
      title="序列管理"
      className="replacement-dialog sequence-manager-dialog"
      busy={busy}
      description="同一工程共享素材库，各序列独立剪辑。切换先暂停并回到开头；保存时记录当前序列。"
      onOpenChange={(open) => {
        if (!open && !pending.current) onClose();
      }}
      onCloseAutoFocus={(event) => {
        event.preventDefault();
        onReturnFocus();
      }}
    >
      <div className="replacement-dialog-body" data-sequence-fields>
        {mode === 'list' ? (
          <>
            <Field label="要管理的序列">
              <Select
                value={selected}
                disabled={busy}
                onChange={(event) => {
                  setSelected(event.target.value);
                  setError('');
                }}
              >
                {choices.map((s) => (
                  <option key={s.id} value={s.id}>
                    {s.name}
                    {s.current ? '（当前）' : ''}
                  </option>
                ))}
              </Select>
            </Field>
            <p className="sequence-details">
              {target.settings.width} × {target.settings.height} ·{' '}
              {(target.settings.fpsNum / target.settings.fpsDen).toFixed(2)} fps ·{' '}
              {target.trackCount} 条轨道 · {target.clipCount} 个片段
            </p>
            <div className="sequence-actions">
              <Button disabled={busy || target.current} onClick={() => void send('switch')}>
                进入此序列
              </Button>
              <Button disabled={busy || choices.length >= 32} onClick={() => form('duplicate')}>
                复制此序列
              </Button>
              <Button disabled={busy} onClick={() => form('rename')}>
                重命名
              </Button>
              <Button disabled={busy || Boolean(deleteReason)} onClick={() => form('delete')}>
                删除序列
              </Button>
            </div>
            {deleteReason ? <p className="muted">{deleteReason}</p> : null}
            <p className="muted">
              {choices.length} / 32
              个序列。预览和导出使用当前序列；工程保存、版本和收集包含全部序列。
            </p>
            <Button
              variant="primary"
              disabled={busy || choices.length >= 32}
              onClick={() => form('create')}
            >
              新建空序列
            </Button>
          </>
        ) : mode === 'delete' ? (
          <>
            <p>
              删除序列“{target.name}”及其中的 {target.clipCount} 个片段？
            </p>
            <p className="muted">
              可一次撤销恢复。共享素材文件保留；删除当前序列后进入第一个剩余序列。
            </p>
          </>
        ) : (
          <form
            id="sequence-edit-form"
            noValidate
            onCompositionStart={() => {
              composing.current = true;
            }}
            onCompositionEnd={() => {
              composing.current = false;
            }}
            onSubmit={(event) => {
              event.preventDefault();
              void send(mode);
            }}
          >
            <Field label="序列名称">
              <Input
                ref={nameRef}
                value={name}
                disabled={busy}
                aria-invalid={Boolean(errors.name)}
                aria-describedby={errors.name ? errorID : undefined}
                onChange={(event) => {
                  setName(event.target.value);
                  setErrors((old) => ({ ...old, name: undefined }));
                }}
              />
              {errors.name ? (
                <p id={errorID} className="form-error">
                  {errors.name}
                </p>
              ) : null}
            </Field>
            {mode === 'create' ? (
              <NewProjectSettings
                context="sequence"
                value={draft}
                onChange={setDraft}
                errors={errors}
                disabled={busy}
              />
            ) : null}
            {mode === 'duplicate' ? (
              <p className="muted">
                完整复制“{target.name}
                ”的片段、字幕、时基及锁轨，复制后进入新序列，后续编辑相互独立。
              </p>
            ) : null}
          </form>
        )}
        {error ? (
          <p role="alert" className="inline-error">
            {error}
          </p>
        ) : null}
      </div>
      <div className="dialog-actions replacement-dialog-actions">
        <Button
          ref={cancelRef}
          disabled={busy}
          onClick={() => {
            if (mode === 'list') onClose();
            else {
              setMode('list');
              setErrors({});
              setError('');
            }
          }}
        >
          {mode === 'list' ? '关闭' : '返回'}
        </Button>
        {mode === 'delete' ? (
          <Button variant="danger" disabled={busy} onClick={() => void send('delete')}>
            删除此序列
          </Button>
        ) : mode !== 'list' ? (
          <Button variant="primary" type="submit" form="sequence-edit-form" disabled={busy}>
            {mode === 'create' ? '新建并进入' : mode === 'duplicate' ? '复制并进入' : '保存名称'}
          </Button>
        ) : null}
      </div>
    </Modal>
  );
}
