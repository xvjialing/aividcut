import { useEffect, useRef, useState } from 'react';
import type { Bootstrap, ProjectScope } from '../types';
import type {
  DraftPreview,
  DraftRef,
  InspectorDraft,
  InspectorDraftAPI,
} from '../inspector-draft-types';
import { inspectorDraftAPI } from '../lib/bridge';
import { Button, Field, Modal } from './ui';
import { ProjectSnapshotList } from './ProjectSnapshotList';

const labels: Record<string, string> = {
  projectName: '工程名称',
  name: '片段名称',
  text: '显示内容',
  startFrame: '起点 · 帧',
  durationFrames: '长度 · 帧',
  sourceInUs: '素材入点 · 秒',
  fontSize: '字号 · px',
  x: '水平位移 · px',
  y: '垂直位移 · px',
  scale: '缩放',
  rotation: '旋转 · °',
  volume: '音量 · %',
  fadeInUs: '淡入 · 秒',
  fadeOutUs: '淡出 · 秒',
  lutIntensity: 'LUT 强度 · %',
};
function shown(key: string, value: string | number) {
  return typeof value === 'number'
    ? String(
        key === 'sourceInUs' || key === 'fadeInUs' || key === 'fadeOutUs'
          ? value / 1e6
          : key === 'volume' || key === 'lutIntensity'
            ? value * 100
            : value,
      )
    : value;
}
export function InspectorDraftsDialog({
  scope,
  dirty,
  onChange,
  onClose,
  onReturnFocus,
  api = inspectorDraftAPI,
}: {
  scope: ProjectScope;
  dirty: boolean;
  onChange: (action: () => Promise<Bootstrap>) => Promise<void>;
  onClose: () => void;
  onReturnFocus: () => void;
  api?: InspectorDraftAPI;
}) {
  const [page, setPage] = useState<{ records: InspectorDraft[]; unreadable: number } | null>(null),
    [selected, setSelected] = useState<InspectorDraft | null>(null),
    [values, setValues] = useState<Record<string, string>>({}),
    [preview, setPreview] = useState<DraftPreview | null>(null),
    [busy, setBusy] = useState('读取'),
    [error, setError] = useState(''),
    [notice, setNotice] = useState(''),
    [reload, setReload] = useState(0),
    [offset, setOffset] = useState(0),
    [confirm, setConfirm] = useState<'base' | 'delete' | null>(null);
  const epoch = useRef(0),
    pending = useRef(false);
  useEffect(() => {
    const key = ++epoch.current;
    setPreview(null);
    setConfirm(null);
    setBusy('读取');
    setError('');
    void api
      .list()
      .then((p) => {
        if (epoch.current === key) setPage(p);
      })
      .catch((e) => {
        if (epoch.current === key) setError(String(e));
      })
      .finally(() => {
        if (epoch.current === key) setBusy('');
      });
    return () => {
      epoch.current++;
    };
  }, [api, scope.projectId, scope.projectRevision, scope.sourceEpoch, reload]);
  useEffect(() => {
    if (confirm) document.getElementById('inspector-draft-cancel')?.focus();
  }, [confirm]);
  useEffect(() => {
    if (!preview) return;
    const invalid = preview.rows.find((row) => row.error);
    const node = document.getElementById(
      invalid
        ? `draft-input-${invalid.key}`
        : preview.error
          ? 'draft-preview-error'
          : 'inspector-draft-review',
    );
    node?.focus();
    node?.scrollIntoView?.({ block: 'nearest' });
  }, [preview]);
  async function perform(label: string, action: () => Promise<void>) {
    if (pending.current || busy) return;
    pending.current = true;
    setBusy(label);
    setError('');
    const key = epoch.current;
    try {
      await action();
    } catch (e) {
      if (key === epoch.current)
        setError(e instanceof Error ? e.message : '操作失败，草稿与工程保留。');
    } finally {
      pending.current = false;
      if (key === epoch.current) setBusy('');
    }
  }
  function ref(): DraftRef {
    if (!selected) throw Error('请先选择草稿');
    return { ...scope, id: selected.id, sha256: selected.sha256 };
  }
  function choose(v: InspectorDraft) {
    setSelected(v);
    setValues(Object.fromEntries(v.fields.map((f) => [f.key, f.raw])));
    setPreview(null);
    setConfirm(null);
    setError('');
    setNotice('');
  }
  async function review(recheckCurrent = false) {
    await perform('核对', async () => {
      const key = epoch.current;
      const result = await api.preview({ ...ref(), values, recheckCurrent });
      if (key === epoch.current) setPreview(result);
    });
  }
  const conflicts = preview?.rows.some((row) => row.conflict);
  return (
    <Modal
      open
      title="属性输入草稿"
      description="找回属性面板尚未提交的输入。先核对当前属性，再应用到工程；原文件不会自动覆盖。"
      className="replacement-dialog recovery-center-dialog"
      busy={Boolean(busy)}
      onOpenChange={(open) => {
        if (!open && !busy && !pending.current) onClose();
      }}

      onCloseAutoFocus={(event) => {
        event.preventDefault();
        onReturnFocus();
      }}
    >
      <div className="replacement-dialog-body">
        {selected ? (
          <>
            <h3>
              {selected.name} · {selected.target ? '片段属性' : '工程名称'}
            </h3>
            <p className="field-hint file-path">
              {selected.originalPath || '未保存工程'} · 基础修订 {selected.baseRevision}
            </p>
            <p>输入稳定后约1秒保护。数字仍为原始输入，可先修正后核对；素材仍使用原位置。</p>
            {selected.fields.map((field) => {
              const row = preview?.rows.find((r) => r.key === field.key);
              return (
                <Field key={field.key} label={labels[field.key] || field.key}>
                  <p className="field-hint inspector-draft-value">
                    记录基值：{shown(field.key, field.base)}
                    {row ? `\n当前属性：${shown(field.key, row.current)}` : ''}
                  </p>
                  <textarea
                    rows={field.key === 'text' ? 4 : 2}
                    id={`draft-input-${field.key}`}
                    aria-label={labels[field.key] || field.key}
                    className="input resize-none"
                    value={values[field.key] ?? field.raw}
                    disabled={Boolean(busy) || Boolean(confirm)}
                    aria-invalid={Boolean(row?.error)}
                    aria-describedby={row?.error ? `draft-error-${field.key}` : undefined}
                    onChange={(e) => {
                      setValues({ ...values, [field.key]: e.target.value });
                      setPreview(null);
                      setError('');
                    }}
                  />
                  {row?.error ? (
                    <p id={`draft-error-${field.key}`} className="form-error">
                      {row.error}
                    </p>
                  ) : null}
                </Field>
              );
            })}
            {preview?.error ? (
              <p id="draft-preview-error" tabIndex={-1} role="alert" className="form-error">
                {preview.error}
              </p>
            ) : null}
            {selected.projectId !== scope.projectId ? (
              <p>当前打开的工程与草稿不同。请打开原工程，或核对后恢复记录中的基础工程。</p>
            ) : null}
            {confirm === 'base' ? (
              <section aria-label="基础工程恢复核对">
                <h3>恢复基础工程？</h3>
                <p>
                  {dirty
                    ? '当前未保存更改会先写入另一份工程恢复副本；保护失败会停止切换。'
                    : '当前没有未保存的工程更改。'}
                  基础工程来自这份草稿首次保护时的状态，可能早于当前编辑。恢复为未保存工程，撤销历史重新开始；输入值仍需另外核对并应用。
                </p>
              </section>
            ) : null}
            {confirm === 'delete' ? (
              <section aria-label="草稿删除核对">
                <h3>删除这份输入草稿？</h3>
                <p>
                  将永久删除这份输入记录及其基础工程副本。当前工程与原文件保留，删除后不能撤销。
                </p>
              </section>
            ) : null}
          </>
        ) : (
          <>
            <p>
              这里仅保护工程名、片段名、字幕/文字和数值属性，不包含连接密钥或生成表单。最近1秒与未完成的输入法组合不保证已保存。
            </p>
            <Button disabled={Boolean(busy)} onClick={() => setReload((v) => v + 1)}>
              刷新输入草稿
            </Button>
            {busy === '读取' ? (
              <p role="status">正在读取输入草稿…</p>
            ) : page?.records.length === 0 ? (
              <p>没有已保护的属性输入草稿。正常提交后会清理本会话已匹配的输入。</p>
            ) : null}
            {page?.unreadable ? (
              <p role="status">
                {page.unreadable} 份资料无法读取，原资料保留，请保留数据目录后检查磁盘。
              </p>
            ) : null}
            <ProjectSnapshotList
              label="已保护输入草稿"
              busy={Boolean(busy)}
              rows={(page?.records || []).slice(offset, offset + 20).map((v) => ({
                id: v.id,
                name: v.name,
                time: v.updatedAt,
                revision: v.baseRevision,
                details: `${v.fields.length} 项属性 · ${v.target ? '片段' : '工程名称'} · ${v.originalPath || '未保存工程'}`,
              }))}
              onSelect={(id) => {
                const v = page?.records.find((r) => r.id === id);
                if (v) choose(v);
              }}
            />
            {page && page.records.length > 20 ? (
              <div className="speech-actions">
                <Button
                  disabled={Boolean(busy) || offset === 0}
                  onClick={() => setOffset(Math.max(0, offset - 20))}
                >
                  上一页
                </Button>
                <span>
                  {offset + 1}–{Math.min(offset + 20, page.records.length)} / {page.records.length}
                </span>
                <Button
                  disabled={Boolean(busy) || offset + 20 >= page.records.length}
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
        <Button
          id="inspector-draft-cancel"
          disabled={Boolean(busy)}
          onClick={() => {
            if (confirm) {
              setConfirm(null);
            } else if (selected) {
              setSelected(null);
              setPreview(null);
            } else onClose();
          }}
        >
          {confirm ? '取消操作' : selected ? '返回草稿列表' : '暂不恢复'}
        </Button>
        {selected && !confirm ? (
          <>
            <Button variant="ghost" disabled={Boolean(busy)} onClick={() => setConfirm('delete')}>
              删除草稿
            </Button>
            <Button disabled={Boolean(busy)} onClick={() => setConfirm('base')}>
              恢复基础工程…
            </Button>
            <Button
              disabled={Boolean(busy) || selected.projectId !== scope.projectId}
              id="inspector-draft-review"
              onClick={() => void review(Boolean(conflicts))}
            >
              {conflicts ? '以当前属性重新核对' : '核对当前属性'}
            </Button>
            <Button
              variant="primary"
              disabled={Boolean(busy) || !preview?.canApply}
              busy={busy === '应用'}
              onClick={() =>
                void perform('应用', async () => {
                  if (!preview?.id) return;
                  await onChange(() => api.apply(preview.id));
                  onClose();
                })
              }
            >
              应用草稿
            </Button>
          </>
        ) : null}
        {confirm === 'base' ? (
          <Button
            variant="primary"
            disabled={Boolean(busy)}
            onClick={() =>
              void perform('恢复', async () => {
                await onChange(() => api.restoreBase(ref()));
                setConfirm(null);
                setNotice('基础工程已打开，输入值尚未应用，请重新核对。');
              })
            }
          >
            保留当前状态并恢复
          </Button>
        ) : null}
        {confirm === 'delete' ? (
          <Button
            variant="danger"
            disabled={Boolean(busy)}
            onClick={() =>
              void perform('删除', async () => {
                await api.remove(ref());
                setSelected(null);
                setConfirm(null);
                setOffset(0);
                setReload((v) => v + 1);
                setNotice('输入草稿已删除。');
              })
            }
          >
            永久删除输入草稿
          </Button>
        ) : null}
      </div>
    </Modal>
  );
}
