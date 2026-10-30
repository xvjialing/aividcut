import { useEffect, useId, useRef, useState } from 'react';
import type { Bootstrap, ProjectScope } from '../types';
import type {
  BatchRelinkAPI,
  BatchRelinkInventory,
  BatchRelinkPreview,
  BatchRelinkRef,
} from '../batch-relink-types';
import { batchRelinkAPI } from '../lib/bridge';
import { absoluteCollectionParent } from '../lib/project-collect';
import { Button, Field, Input, Modal } from './ui';

const pageSize = 20;
const message = (cause: unknown, fallback: string) =>
  cause instanceof Error ? cause.message : fallback;

export function BatchRelinkDialog({
  scope,
  desktop,
  onApply,
  onClose,
  onReturnFocus,
  api = batchRelinkAPI,
}: {
  scope: ProjectScope;
  desktop: boolean;
  onApply: (operation: () => Promise<Bootstrap>, label: string) => Promise<Bootstrap | null>;
  onClose: () => void;
  onReturnFocus: () => void;
  api?: BatchRelinkAPI;
}) {
  const [oldRoot, setOldRoot] = useState(''),
    [newRoot, setNewRoot] = useState('');
  const [inventory, setInventory] = useState<BatchRelinkInventory | null>(null);
  const [preview, setPreview] = useState<BatchRelinkPreview | null>(null);
  const [busy, setBusy] = useState('读取'),
    [error, setError] = useState('');
  const [invalid, setInvalid] = useState<'old' | 'new' | null>(null);
  const [reload, setReload] = useState(0),
    [page, setPage] = useState(0);
  const epoch = useRef(0),
    composing = useRef(false),
    request = useRef<BatchRelinkRef | null>(null),
    pending = useRef(false);
  const oldInput = useRef<HTMLInputElement>(null),
    newInput = useRef<HTMLInputElement>(null);
  const errorID = useId(),
    hintID = useId();
  const scopeKey = `${scope.projectId}:${scope.sourceEpoch}:${scope.projectRevision}`;
  const previousScope = useRef(scopeKey);
  useEffect(() => {
    const version = ++epoch.current;
    const changed = previousScope.current !== scopeKey;
    previousScope.current = scopeKey;
    pending.current = false;
    setPreview(null);
    setPage(0);
    setError(changed ? '工程或修订已变化，旧核对已清除，请重新核对。' : '');
    setBusy('读取');
    setInventory(null);
    void api
      .inspect(scope)
      .then((value) => {
        if (epoch.current !== version) return;
        setInventory(value);
        setOldRoot((previous) => previous || value.suggestedOldRoot);
      })
      .catch((cause) => {
        if (epoch.current === version) setError(message(cause, '无法读取素材，请重新读取。'));
      })
      .finally(() => {
        if (epoch.current === version) setBusy('');
      });
    return () => {
      epoch.current++;
      if (request.current) void api.cancel(request.current).catch(() => {});
      request.current = null;
    };
  }, [api, scope.projectId, scope.sourceEpoch, scope.projectRevision, scopeKey, reload]);

  function discardPreview() {
    setPreview(null);
    setPage(0);
    if (request.current) void api.cancel(request.current).catch(() => {});
    request.current = null;
  }
  function close() {
    if (busy === '应用') return;
    epoch.current++;
    discardPreview();
    onClose();
  }
  async function choose() {
    if (pending.current || busy) return;
    pending.current = true;
    setBusy('选择');
    setError('');
    const version = epoch.current;
    try {
      const path = await api.chooseDirectory();
      if (epoch.current === version && path) {
        setNewRoot(path);
        discardPreview();
        setInvalid(null);
      }
    } catch (cause) {
      if (epoch.current === version) setError(message(cause, '无法选择文件夹，请直接输入路径。'));
    } finally {
      if (epoch.current === version) {
        pending.current = false;
        setBusy('');
      }
    }
  }
  async function inspect(event: React.FormEvent) {
    event.preventDefault();
    if (pending.current || busy || !inventory || composing.current) return;
    if (!absoluteCollectionParent(oldRoot.trim())) {
      setInvalid('old');
      setError('请输入旧文件夹的完整路径，旧文件夹不需要仍然存在。');
      oldInput.current?.focus();
      return;
    }
    if (!absoluteCollectionParent(newRoot.trim())) {
      setInvalid('new');
      setError('请输入当前文件夹的完整路径。');
      newInput.current?.focus();
      return;
    }
    discardPreview();
    pending.current = true;
    setBusy('核对');
    setError('');
    setInvalid(null);
    const version = ++epoch.current,
      ref = { ...scope, id: crypto.randomUUID() };
    request.current = ref;
    try {
      const result = await api.preview({
        ...ref,
        oldRoot: oldRoot.trim(),
        newRoot: newRoot.trim(),
      });
      if (epoch.current === version) {
        setPreview(result);
        setPage(0);
      }
    } catch (cause) {
      if (epoch.current === version) setError(message(cause, '核对未完成，目录已保留，请重试。'));
    } finally {
      if (epoch.current === version) {
        pending.current = false;
        setBusy('');
      }
    }
  }
  async function stop() {
    if (!request.current || busy !== '核对') return;
    const ref = request.current;
    const version = ++epoch.current;
    request.current = null;
    setPreview(null);
    setBusy('停止');
    try {
      await api.cancel(ref);
      if (epoch.current === version) setError('已取消核对，工程未修改。');
    } catch (cause) {
      if (epoch.current === version) setError(message(cause, '未取得取消回执，请稍后重新核对。'));
    } finally {
      if (epoch.current === version) {
        pending.current = false;
        setBusy('');
      }
    }
  }
  async function apply() {
    if (pending.current || busy || !preview?.matched || !request.current) return;
    pending.current = true;
    setBusy('应用');
    setError('');
    const ref = request.current,
      version = epoch.current;
    let failure = '未取得修复回执，请重新读取工程后核对。不会自动再次应用。';
    try {
      const result = await onApply(async () => {
        try {
          return await api.apply(ref);
        } catch (cause) {
          failure = message(cause, failure);
          throw cause;
        }
      }, `恢复 ${preview.matched} 个素材链接`);
      if (result) onClose();
      else if (epoch.current === version) {
        setError(failure);
        setPreview(null);
      }
    } catch (cause) {
      if (epoch.current === version) {
        setError(message(cause, failure));
        setPreview(null);
      }
    } finally {
      if (epoch.current === version) {
        pending.current = false;
        setBusy('');
      }
    }
  }
  const entries = preview?.entries || [],
    pages = Math.max(1, Math.ceil(entries.length / pageSize));
  return (
    <Modal
      open
      title="批量修复素材链接"
      className="replacement-dialog batch-relink-dialog"
      busy={busy === '应用'}
      description="素材目录整体移动后，按原来的相对目录结构恢复同一内容。核对不会修改工程或移动文件。"
      onOpenChange={(open) => {
        if (!open) close();
      }}
      onCloseAutoFocus={(event) => {
        event.preventDefault();
        onReturnFocus();
      }}
    >
      <div className="replacement-dialog-body">
        <form
          noValidate
          onCompositionStart={() => {
            composing.current = true;
          }}
          onCompositionEnd={() => {
            composing.current = false;
          }}
          onSubmit={(event) => void inspect(event)}
        >
          <div className="batch-relink-fields">
            <Field label="旧文件夹" hint="填工程记录中的原目录；它可以已经不存在。">
              <Input
                ref={oldInput}
                value={oldRoot}
                disabled={Boolean(busy)}
                aria-invalid={invalid === 'old' || undefined}
                aria-describedby={invalid === 'old' ? errorID : undefined}
                onChange={(event) => {
                  setOldRoot(event.target.value);
                  discardPreview();
                  setInvalid(null);
                }}
              />
            </Field>
            <Field
              label="当前文件夹"
              hint="选择与旧文件夹对应的新位置，内部文件名及子目录保持原样。"
            >
              <Input
                ref={newInput}
                value={newRoot}
                disabled={Boolean(busy)}
                aria-invalid={invalid === 'new' || undefined}
                aria-describedby={invalid === 'new' ? errorID : undefined}
                onChange={(event) => {
                  setNewRoot(event.target.value);
                  discardPreview();
                  setInvalid(null);
                }}
              />
            </Field>
          </div>
          <div className="batch-relink-actions">
            {desktop ? (
              <Button disabled={Boolean(busy)} onClick={() => void choose()}>
                选择当前文件夹
              </Button>
            ) : null}
            <Button
              type="submit"
              disabled={Boolean(busy) || !inventory || inventory.missingCount === 0}
            >
              核对候选文件
            </Button>
            {busy === '核对' ? <Button onClick={() => void stop()}>取消核对</Button> : null}
            <Button
              disabled={Boolean(busy)}
              onClick={() => {
                discardPreview();
                setReload((value) => value + 1);
              }}
            >
              重新读取
            </Button>
          </div>
        </form>
        <p role="status" className="muted batch-relink-status">
          {busy === '读取'
            ? '正在检查工程素材位置…'
            : busy === '核对'
              ? '正在逐个核对文件内容，可取消。'
              : busy === '应用'
                ? '正在重新核对并恢复链接…'
                : inventory
                  ? `工程共有 ${inventory.missingCount} 个离线素材，${inventory.availableCount} 个原位置仍存在。`
                  : '未能读取工程素材。'}
        </p>
        <p id={hintID} className="muted">
          仅恢复内容一致的文件，每批最多256个；缺失、内容不同或没有指纹的素材保留，可继续使用单个素材的“重新定位”。
        </p>
        {preview ? (
          <section aria-label="候选核对结果" aria-describedby={hintID}>
            <p>
              此旧目录内 {entries.length} 个离线素材，{preview.matched} 个内容一致；另有{' '}
              {preview.outside} 个离线素材不在此目录。
            </p>
            {entries.length ? (
              <ol className="batch-relink-list" start={page * pageSize + 1}>
                {entries.slice(page * pageSize, (page + 1) * pageSize).map((entry) => (
                  <li key={entry.assetId}>
                    <strong>{entry.name}</strong>
                    <span className={entry.status === 'matched' ? 'status-success-text' : 'muted'}>
                      {entry.reason}
                    </span>
                    <dl>
                      <dt>原位置</dt>
                      <dd>{entry.originalPath}</dd>
                      <dt>候选位置</dt>
                      <dd>{entry.candidatePath}</dd>
                    </dl>
                  </li>
                ))}
              </ol>
            ) : (
              <p className="muted">
                此旧目录下没有需要修复的素材。请检查旧目录，或关闭窗口继续编辑。
              </p>
            )}
            {pages > 1 ? (
              <nav className="batch-relink-actions" aria-label="候选结果分页">
                <Button
                  disabled={page === 0 || Boolean(busy)}
                  onClick={() => setPage((value) => value - 1)}
                >
                  上一页
                </Button>
                <span>
                  {page + 1} / {pages} 页
                </span>
                <Button
                  disabled={page + 1 >= pages || Boolean(busy)}
                  onClick={() => setPage((value) => value + 1)}
                >
                  下一页
                </Button>
              </nav>
            ) : null}
          </section>
        ) : null}
        <p className="muted">一次撤销可恢复本批原路径。片段时间、字幕、LUT、字体与锁定状态保持。</p>
      </div>
      {error ? (
        <p id={errorID} role="alert" className="inline-error">
          {error}
        </p>
      ) : null}
      <div className="dialog-actions replacement-dialog-actions">
        <Button disabled={busy === '应用'} onClick={close}>
          关闭
        </Button>
        <Button
          variant="primary"
          disabled={Boolean(busy) || !preview?.matched}
          onClick={() => void apply()}
        >
          恢复 {preview?.matched || 0} 个链接
        </Button>
      </div>
    </Modal>
  );
}
