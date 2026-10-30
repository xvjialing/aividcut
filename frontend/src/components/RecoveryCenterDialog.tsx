import { useEffect, useRef, useState } from 'react';
import type { ProjectScope } from '../types';
import type {
  RecoveryAPI,
  RecoveryPage,
  RecoveryRecord,
  RecoveryStatus,
  RestoreRecoveryInput,
} from '../recovery-types';
import { recoveryAPI } from '../lib/bridge';
import { Button, Modal } from './ui';
import { ProjectSnapshotList } from './ProjectSnapshotList';

export function RecoveryCenterDialog({
  scope,
  dirty,
  onRestore,
  onClose,
  onReturnFocus,
  onManage,
  onDrafts,
  api = recoveryAPI,
}: {
  scope: ProjectScope;
  dirty: boolean;
  onRestore: (input: RestoreRecoveryInput) => Promise<void>;
  onClose: () => void;
  onReturnFocus?: () => void;
  onManage?: () => void;
  onDrafts?: () => void;
  api?: RecoveryAPI;
}) {
  const [page, setPage] = useState<RecoveryPage | null>(null),
    [status, setStatus] = useState<RecoveryStatus | null>(null),
    [offset, setOffset] = useState(0),
    [reload, setReload] = useState(0),
    [selected, setSelected] = useState<RecoveryRecord | null>(null),
    [busy, setBusy] = useState('读取'),
    [error, setError] = useState(''),
    [notice, setNotice] = useState('');
  const epoch = useRef(0),
    pending = useRef(false);
  useEffect(() => {
    const version = ++epoch.current;
    setBusy('读取');
    setError('');
    setSelected(null);
    setPage(null);
    void Promise.all([api.list({ offset, limit: 20 }), api.status()])
      .then(([p, s]) => {
        if (epoch.current === version) {
          setPage(p);
          setStatus(s);
        }
      })
      .catch((cause) => {
        if (epoch.current === version)
          setError(cause instanceof Error ? cause.message : '无法读取恢复资料，请刷新重试。');
      })
      .finally(() => {
        if (epoch.current === version) setBusy('');
      });
    return () => {
      epoch.current++;
    };
  }, [api, scope.projectId, scope.sourceEpoch, scope.projectRevision, offset, reload]);
  async function checkpoint() {
    if (pending.current || busy) return;
    pending.current = true;
    setBusy('保护');
    setError('');
    const version = epoch.current;
    try {
      const value = await api.checkpoint();
      if (epoch.current === version) {
        setStatus(value);
        setNotice(
          value.protectedAt
            ? '当前已提交的工程更改已写入恢复副本。'
            : '当前工程与已保存状态一致，无需创建恢复副本。',
        );
        setReload((v) => v + 1);
      }
    } catch (cause) {
      if (epoch.current === version)
        setError(cause instanceof Error ? cause.message : '保护失败，请手动保存工程。');
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
      setError(cause instanceof Error ? cause.message : '恢复失败，当前工程保留。');
    } finally {
      pending.current = false;
      setBusy('');
    }
  }
  const currentStatus =
    status?.scope.projectId === scope.projectId && status.scope.sourceEpoch === scope.sourceEpoch;
  return (
    <Modal
      open
      title="恢复中心"
      className="replacement-dialog recovery-center-dialog"
      busy={Boolean(busy)}
      onOpenChange={(open) => {
        if (!open && !busy && !pending.current) onClose();
      }}
      onCloseAutoFocus={(event) => {
        if (onReturnFocus) {
          event.preventDefault();
          onReturnFocus();
        }
      }}
      description="恢复本机保留的工程更改。恢复后作为未保存工程打开，保存时另选位置，原文件不自动覆盖。"
    >
      <div className="replacement-dialog-body">
        {selected ? (
          <section aria-label="恢复副本核对">
            <h3>恢复“{selected.name}”？</h3>
            <p>
              {selected.sequences || 1} 个序列 · 保存时序列：{selected.sequenceName || '主时间线'} ·{' '}
              {selected.clips} 个片段 · {selected.assets} 个素材 · 修订 {selected.revision}
            </p>
            <p className="file-path">{selected.projectPath || '原工程尚未保存到文件'}</p>
            <p>
              {dirty
                ? '当前工程有未保存的更改，将先写入另一份恢复副本；写入失败会停止切换。'
                : '当前没有未保存的工程更改，恢复不会自动覆盖工程文件。'}
            </p>
            <p>
              恢复后撤销历史重新开始，素材仍在原位置。已保留的当前状态可从恢复中心找回，模型任务不会重新提交。
            </p>
          </section>
        ) : (
          <>
            <p role="status">
              {status?.enabled
                ? `每 ${status.intervalSeconds} 秒保护一次已提交的更改。`
                : '自动恢复未连接，请重启客户端或刷新重试。'}
              {currentStatus && status?.protectedAt
                ? `当前工程已保护到修订 ${status.protectedRevision}，${new Date(status.protectedAt).toLocaleString('zh-CN')}。`
                : null}
            </p>
            {status?.error ? (
              <p role="alert" className="form-error">
                {status.error}
              </p>
            ) : null}
            <p className="field-hint">
              工程副本只包含已提交的编辑；尚未失焦的属性输入请查看“属性输入草稿”。上次保护后的更改和原素材文件仍需正常保存。
            </p>
            <div className="speech-actions">
              <Button
                disabled={Boolean(busy) || !status?.enabled}
                busy={busy === '保护'}
                onClick={() => void checkpoint()}
              >
                立即保护当前工程
              </Button>
              <Button
                disabled={Boolean(busy)}
                onClick={() => {
                  setNotice('');
                  setReload((v) => v + 1);
                }}
              >
                刷新恢复列表
              </Button>
            </div>
            {busy === '读取' ? (
              <p role="status">正在读取恢复资料…</p>
            ) : page?.total === 0 ? (
              <p>没有待恢复的工程。当前工程的自动保护点不会在这里重复列出。</p>
            ) : null}
            {page?.unreadable ? (
              <p role="status">
                {page.unreadable}{' '}
                份恢复摘要无法读取，原文件已保留。请保留资料后检查磁盘，不会自动丢弃它们。
              </p>
            ) : null}
            <ProjectSnapshotList
              label="可恢复工程"
              busy={Boolean(busy)}
              rows={(page?.records || []).map((v) => ({
                id: v.id,
                name: v.name,
                time: v.updatedAt,
                revision: v.revision,
                details: `${v.sequences || 1} 个序列 · 保存时序列：${v.sequenceName || '主时间线'} · ${v.clips} 个片段 · ${v.assets} 个素材 · ${v.projectPath || '未保存工程'}`,
                beforeRestore: v.beforeRestore,
              }))}
              onSelect={(id) => {
                setSelected(page?.records.find((v) => v.id === id) || null);
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
          <p role="alert" className="form-error">
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
              保留当前状态并恢复
            </Button>
          </>
        ) : (
          <>
            {onDrafts ? (
              <Button disabled={Boolean(busy)} onClick={onDrafts}>
                属性输入草稿（{status?.inputDrafts || 0}）
              </Button>
            ) : null}
            {onManage ? (
              <Button variant="ghost" disabled={Boolean(busy)} onClick={onManage}>
                管理恢复资料
              </Button>
            ) : null}
            <Button disabled={Boolean(busy)} onClick={onClose}>
              暂不恢复
            </Button>
          </>
        )}
      </div>
    </Modal>
  );
}
