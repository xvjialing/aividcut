import { useEffect, useRef, useState } from 'react';
import type { ProjectScope } from '../types';
import type {
  SnapshotStorageAPI,
  StorageAction,
  StorageItem,
  StorageKind,
  StoragePage,
  StorageView,
} from '../snapshot-storage-types';
import { snapshotStorageAPI } from '../lib/bridge';
import { collectionSize } from '../lib/project-collect';
import { useSettingsFormState, type SettingsFormEvents } from '../lib/settings';
import { Button, SettingsSurface, PanelTabs } from './ui';
import { ProjectSnapshotList } from './ProjectSnapshotList';

export function SnapshotStorageDialog({
  scope,
  initialKind,
  onClose,
  onReturnFocus,
  inline = false,
  onBusyChange,
  onDirtyChange,
  api = snapshotStorageAPI,
}: {
  scope: ProjectScope;
  initialKind: StorageKind;
  onClose: () => void;
  onReturnFocus?: () => void;
  api?: SnapshotStorageAPI;
} & SettingsFormEvents & { inline?: boolean }) {
  const [kind, setKind] = useState<StorageKind>(initialKind),
    [view, setView] = useState<StorageView>('active'),
    [offset, setOffset] = useState(0),
    [page, setPage] = useState<StoragePage | null>(null),
    [selected, setSelected] = useState<StorageItem | null>(null),
    [confirmPurge, setConfirmPurge] = useState(false),
    [busy, setBusy] = useState('读取'),
    [error, setError] = useState(''),
    [notice, setNotice] = useState(''),
    [reload, setReload] = useState(0);
  const epoch = useRef(0),
    pending = useRef(false),
    content = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const version = ++epoch.current;
    setBusy('读取');
    setPage(null);
    setSelected(null);
    setConfirmPurge(false);
    setError('');
    void api
      .list({ ...scope, kind, view, offset, limit: 20 })
      .then((value) => {
        if (epoch.current === version) setPage(value);
      })
      .catch((cause) => {
        if (epoch.current === version)
          setError(cause instanceof Error ? cause.message : '无法读取资料，请刷新重试。');
      })
      .finally(() => {
        if (epoch.current === version) setBusy('');
      });
    return () => {
      epoch.current++;
    };
  }, [api, scope.projectId, scope.projectRevision, scope.sourceEpoch, kind, view, offset, reload]);
  useEffect(() => {
    if (selected)
      content.current?.querySelector<HTMLButtonElement>('[data-safe-storage-action]')?.focus();
  }, [selected, confirmPurge]);
  async function change(action: StorageAction) {
    if (!selected || pending.current || busy || (action === 'purge' && !confirmPurge)) return;
    pending.current = true;
    setBusy(action);
    setError('');
    const version = epoch.current;
    try {
      const result = await api.change({
        ...scope,
        token: selected.token,
        action,
        confirmPermanent: action === 'purge' && confirmPurge,
      });
      if (epoch.current !== version) return;
      setSelected(null);
      setConfirmPurge(false);
      setNotice(
        result.alreadyApplied
          ? '该操作此前已处理，列表已重新核对。'
          : action === 'trash'
            ? '已移至回收站，可还原到活动列表。'
            : action === 'restore'
              ? '已还原到活动资料列表，没有替换当前工程。'
              : '此资料已永久删除，原素材和已保存工程保留。',
      );
      setReload((v) => v + 1);
    } catch (cause) {
      if (epoch.current === version)
        setError(
          cause instanceof Error ? cause.message : '资料操作失败，未完成的内容已保留，请刷新核对。',
        );
    } finally {
      pending.current = false;
      if (epoch.current === version) setBusy('');
    }
  }
  useSettingsFormState(Boolean(busy) && busy !== '读取', false, { onBusyChange, onDirtyChange });
  return (
    <SettingsSurface
      inline={inline}
      open
      title="资料管理"
      className="replacement-dialog snapshot-storage-dialog"
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
      description="整理版本快照和恢复副本，当前工程及原素材保留。回收站不会自动清空。"
    >
      <div ref={content} className="snapshot-storage-layout">
        <div className="replacement-dialog-body">
          {selected ? (
            <section aria-label={confirmPurge ? '永久删除核对' : '资料操作核对'}>
              <h3>
                {confirmPurge ? '永久删除' : '整理'}“{selected.name}”{confirmPurge ? '？' : ''}
              </h3>
              <p>
                {new Date(selected.time).toLocaleString('zh-CN')} · {collectionSize(selected.bytes)}{' '}
                · {kind === 'version' ? '工程版本' : '恢复副本'} ·{' '}
                {view === 'active' ? '活动列表' : '回收站'}
              </p>
              {confirmPurge ? (
                <p>
                  永久删除后不能通过资料管理找回此快照。已保存的工程文件和原素材不会删除；此操作不能撤销。请确认不再需要这份资料。
                </p>
              ) : (
                <p>
                  {view === 'active'
                    ? '移至回收站后仍可还原，不再计入活动资料预算；磁盘空间在永久删除后才释放。'
                    : '还原只回到活动资料列表，不立即替换时间线。永久删除需要下一步单独核对。'}
                </p>
              )}
              {!selected.readable ? (
                <p className="field-hint">
                  摘要不可读，按文件名和修改时间列出；未将其标为可恢复工程。
                </p>
              ) : null}
              {selected.reason ? <p className="field-hint">{selected.reason}</p> : null}
            </section>
          ) : (
            <>
              <fieldset className="storage-tabs" disabled={Boolean(busy)}>
                <legend className="sr-only">资料类型和位置</legend>
                <PanelTabs
                  id="storage-kind"
                  label="资料类型"
                  value={kind}
                  items={[
                    { value: 'version', label: '工程版本' },
                    { value: 'recovery', label: '恢复副本' },
                  ]}
                  onChange={(value) => {
                    if (!busy) {
                      setKind(value);
                      setOffset(0);
                      setNotice('');
                    }
                  }}
                >
                  <PanelTabs
                    id="storage-view"
                    label="资料位置"
                    value={view}
                    items={[
                      { value: 'active', label: '活动列表' },
                      { value: 'trash', label: '回收站' },
                    ]}
                    onChange={(value) => {
                      if (!busy) {
                        setView(value);
                        setOffset(0);
                        setNotice('');
                      }
                    }}
                  />
                </PanelTabs>
              </fieldset>
              <p className="field-hint">
                {kind === 'version' ? '当前工程的版本资料。' : '本机资料目录中各工程的恢复副本。'}
                当前正在保护工程的点不可清理。额外文件和链接不自动永久删除。
              </p>
              {page ? (
                <p>
                  {page.total} 条资料 · 已知文件大小约 {collectionSize(page.totalBytes)}
                  。回收站仍占磁盘空间。
                </p>
              ) : busy === '读取' ? (
                <p role="status">正在读取资料…</p>
              ) : null}
              <div className="speech-actions">
                <Button
                  disabled={Boolean(busy)}
                  onClick={() => {
                    setNotice('');
                    setReload((v) => v + 1);
                  }}
                >
                  刷新资料
                </Button>
              </div>
              {page?.total === 0 ? <p>这里没有资料。</p> : null}
              <ProjectSnapshotList
                label="可管理资料"
                actionLabel="查看操作"
                busy={Boolean(busy)}
                rows={(page?.items || []).map((v) => ({
                  id: v.id,
                  name: v.name,
                  time: v.time,
                  revision: v.readable ? v.revision : undefined,
                  details: `${collectionSize(v.bytes)} · ${v.readable ? '摘要可读' : '摘要不可读'}${v.reason ? ' · ' + v.reason : ''}`,
                }))}
                onSelect={(id) => {
                  setSelected(page?.items.find((v) => v.id === id) || null);
                  setConfirmPurge(false);
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
              <Button
                data-safe-storage-action
                disabled={Boolean(busy)}
                onClick={() => {
                  if (confirmPurge) setConfirmPurge(false);
                  else setSelected(null);
                }}
              >
                取消{confirmPurge ? '永久删除' : '整理'}
              </Button>
              {confirmPurge ? (
                <Button
                  variant="danger"
                  busy={busy === 'purge'}
                  disabled={Boolean(busy) || selected.locked || !selected.canPurge}
                  onClick={() => void change('purge')}
                >
                  永久删除此资料
                </Button>
              ) : view === 'active' ? (
                <Button
                  busy={busy === 'trash'}
                  disabled={Boolean(busy) || selected.locked}
                  onClick={() => void change('trash')}
                >
                  移至回收站
                </Button>
              ) : (
                <>
                  <Button
                    busy={busy === 'restore'}
                    disabled={Boolean(busy) || selected.locked}
                    onClick={() => void change('restore')}
                  >
                    还原到活动列表
                  </Button>
                  <Button
                    variant="ghost"
                    disabled={Boolean(busy) || selected.locked || !selected.canPurge}
                    onClick={() => setConfirmPurge(true)}
                  >
                    永久删除…
                  </Button>
                </>
              )}
            </>
          ) : (
            <Button disabled={Boolean(busy)} onClick={onClose}>
              关闭资料管理
            </Button>
          )}
        </div>
      </div>
    </SettingsSurface>
  );
}
