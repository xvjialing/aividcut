import { useEffect, useId, useRef, useState } from 'react';
import type {
  ProjectVersion,
  ProjectVersionPage,
  ProjectVersionScope,
  ProjectVersionsAPI,
  RestoreProjectVersionInput,
} from '../project-version-types';
import { projectVersionsAPI } from '../lib/bridge';
import { Button, Field, Input, Modal } from './ui';
import { ProjectSnapshotList } from './ProjectSnapshotList';

export function ProjectVersionsDialog({
  scope,
  dirty,
  onRestore,
  onClose,
  onReturnFocus,
  onManage,
  api = projectVersionsAPI,
}: {
  scope: ProjectVersionScope;
  dirty: boolean;
  onRestore: (input: RestoreProjectVersionInput) => Promise<void>;
  onClose: () => void;
  onReturnFocus?: () => void;
  onManage?: () => void;
  api?: ProjectVersionsAPI;
}) {
  const [name, setName] = useState('');
  const [page, setPage] = useState<ProjectVersionPage | null>(null);
  const [offset, setOffset] = useState(0);
  const [selected, setSelected] = useState<ProjectVersion | null>(null);
  const [busy, setBusy] = useState('读取');
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const [invalid, setInvalid] = useState(false);
  const [reload, setReload] = useState(0);
  const pending = useRef(false),
    epoch = useRef(0);
  const field = useRef<HTMLInputElement>(null);
  const errorID = useId();
  useEffect(() => {
    const version = ++epoch.current;
    setBusy('读取');
    setError('');
    setSelected(null);
    setPage(null);
    void api
      .list({ ...scope, offset, limit: 20 })
      .then((value) => {
        if (epoch.current === version) setPage(value);
      })
      .catch((cause) => {
        if (epoch.current === version)
          setError(cause instanceof Error ? cause.message : '无法读取工程版本，请刷新重试。');
      })
      .finally(() => {
        if (epoch.current === version) setBusy('');
      });
    return () => {
      epoch.current++;
    };
  }, [api, scope.projectId, scope.projectRevision, scope.sourceEpoch, offset, reload]);
  async function create(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (pending.current || busy) return;
    const value = name.trim();
    if (!value || Array.from(value).length > 80) {
      setInvalid(true);
      setError('版本名称需为1到80个字符。');
      field.current?.focus();
      return;
    }
    pending.current = true;
    setBusy('保存');
    setError('');
    setInvalid(false);
    const version = epoch.current;
    try {
      const saved = await api.create({ ...scope, name: value });
      if (epoch.current !== version) return;
      setName('');
      setNotice(`已保存版本“${saved.name}”，工程文件未覆盖。`);
      setOffset(0);
      setReload((v) => v + 1);
    } catch (cause) {
      if (epoch.current === version)
        setError(cause instanceof Error ? cause.message : '保存版本失败，名称和当前工程已保留。');
    } finally {
      pending.current = false;
      if (epoch.current === version) setBusy('');
    }
  }
  async function restore() {
    if (!selected || pending.current || busy) return;
    pending.current = true;
    setBusy('恢复');
    setError('');
    try {
      await onRestore({ ...scope, id: selected.id, sha256: selected.sha256 });
      onClose();
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : '恢复失败，当前工程已保留。');
    } finally {
      pending.current = false;
      setBusy('');
    }
  }
  return (
    <Modal
      open
      title="工程版本"
      className="replacement-dialog project-versions-dialog"
      busy={Boolean(busy)}
      onCloseAutoFocus={(event) => {
        if (onReturnFocus) {
          event.preventDefault();
          onReturnFocus();
        }
      }}
      onOpenChange={(open) => {
        if (!open && !busy && !pending.current) onClose();
      }}
      description="版本保存在本机资料目录，重开客户端后仍可恢复；源媒体保留在原位置。"
    >
      <div className="replacement-dialog-body">
        {selected ? (
          <section aria-label="恢复版本核对">
            <h3>恢复“{selected.name}”？</h3>
            <p>
              {selected.sequences || 1} 个序列 · 保存时序列：{selected.sequenceName || '主时间线'} ·{' '}
              {selected.clips} 个片段 · {selected.assets} 个素材 · 修订 {selected.revision}
            </p>
            <p>
              {dirty ? '当前工程有未保存的更改。' : ''}
              恢复前会先保留当前完整状态为另一个版本；保存失败将停止恢复。原工程文件不会自动覆盖。
            </p>
            <p className="field-hint">
              恢复后撤销历史从此处重新开始；要返回当前状态，可再次打开工程版本，选择“恢复前”版本。模型任务不会重新提交。
            </p>
          </section>
        ) : (
          <>
            <form noValidate onSubmit={(event) => void create(event)}>
              <Field
                label="版本名称"
                hint="例如：粗剪完成、字幕校对后。不会改变当前工程的保存状态。"
              >
                <Input
                  ref={field}
                  value={name}
                  disabled={Boolean(busy)}
                  aria-invalid={invalid || undefined}
                  aria-describedby={invalid ? errorID : undefined}
                  onChange={(event) => {
                    setName(event.target.value);
                    setInvalid(false);
                  }}
                  placeholder="为当前状态取个名字"
                />
              </Field>
              <div className="speech-actions">
                <Button type="submit" busy={busy === '保存'} disabled={Boolean(busy)}>
                  保存当前版本
                </Button>
                <Button
                  disabled={Boolean(busy)}
                  onClick={() => {
                    setNotice('');
                    setReload((v) => v + 1);
                  }}
                >
                  刷新版本
                </Button>
              </div>
            </form>
            <p className="field-hint">
              需要保留当前剪辑时，请点击保存当前版本。每个工程最多保留200个版本，原媒体请一并备份。
            </p>
            {busy === '读取' ? (
              <p role="status">正在读取工程版本…</p>
            ) : page && page.total === 0 ? (
              <p>还没有保存版本。保存一次后，可在这里回到此刻的剪辑。</p>
            ) : null}
            {page?.unreadable ? (
              <p role="status">
                {page.unreadable} 个版本摘要无法读取，已保留原文件；没有将它们列为可恢复版本。
              </p>
            ) : null}
            <ProjectSnapshotList
              label="已保存工程版本"
              busy={Boolean(busy)}
              rows={(page?.versions || []).map((v) => ({
                id: v.id,
                name: v.name,
                time: v.createdAt,
                revision: v.revision,
                details: `${v.sequences || 1} 个序列 · 保存时序列：${v.sequenceName || '主时间线'} · ${v.clips} 个片段 · ${v.assets} 个素材 · ${v.tracks} 条轨道`,
                beforeRestore: v.beforeRestore,
              }))}
              onSelect={(id) => {
                setSelected(page?.versions.find((v) => v.id === id) || null);
                setError('');
                setNotice('');
              }}
            />
            {page && page.total > 20 ? (
              <div className="speech-actions">
                <Button
                  disabled={Boolean(busy) || offset === 0}
                  onClick={() => setOffset(Math.max(0, offset - 20))}
                >
                  上一页
                </Button>
                <span>
                  {offset + 1}–{Math.min(offset + 20, page.total)} / {page.total}
                </span>
                <Button
                  disabled={Boolean(busy) || offset + 20 >= page.total}
                  onClick={() => setOffset(offset + 20)}
                >
                  下一页
                </Button>
              </div>
            ) : null}
          </>
        )}
      </div>
      <div className="replacement-dialog-status">
        {error ? (
          <p id={errorID} role="alert" className="form-error">
            {error}
          </p>
        ) : null}
        {notice ? <p role="status">{notice}</p> : null}
      </div>
      <div className="dialog-actions replacement-dialog-actions">
        {selected ? (
          <>
            <Button disabled={Boolean(busy)} onClick={() => setSelected(null)}>
              取消恢复
            </Button>
            <Button
              variant="primary"
              busy={busy === '恢复'}
              disabled={Boolean(busy)}
              onClick={() => void restore()}
            >
              保留当前状态为版本并恢复
            </Button>
          </>
        ) : (
          <>
            {onManage ? (
              <Button variant="ghost" disabled={Boolean(busy)} onClick={onManage}>
                管理版本资料
              </Button>
            ) : null}
            <Button disabled={Boolean(busy)} onClick={onClose}>
              关闭工程版本
            </Button>
          </>
        )}
      </div>
    </Modal>
  );
}
