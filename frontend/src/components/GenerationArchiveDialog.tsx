import { useCallback, useEffect, useRef, useState } from 'react';
import type { GenerationAPI, GenerationArchivePage, GenerationJob } from '../generation-types';
import { generationStatus, modeNames, safeConnectionError } from '../lib/generation';
import { GenerationEstimateSummary } from './GenerationEstimate';
import { Button, Modal } from './ui';

type ArchiveAPI = Pick<
  GenerationAPI,
  'listArchivedGenerationJobs' | 'restoreArchivedGenerationJob'
>;
export function GenerationArchiveDialog({
  api,
  onClose,
  onRestored,
  onReturnFocus,
}: {
  api: ArchiveAPI;
  onClose: () => void;
  onRestored: (job: GenerationJob) => void;
  onReturnFocus: () => void;
}) {
  const [page, setPage] = useState<GenerationArchivePage>({
    items: [],
    total: 0,
    active: 0,
    offset: 0,
    limit: 20,
  });
  const [loading, setLoading] = useState(false);
  const [pending, setPending] = useState('');
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const alive = useRef(false),
    reads = useRef(0),
    mutation = useRef(false);
  const refresh = useRef<HTMLButtonElement>(null);
  const load = useCallback(
    async (offset: number) => {
      const request = ++reads.current;
      setLoading(true);
      setError('');
      try {
        const value = await api.listArchivedGenerationJobs({ offset, limit: 20 });
        if (alive.current && request === reads.current)
          setPage({ ...value, items: value.items || [] });
      } catch (cause) {
        if (alive.current && request === reads.current) setError(safeConnectionError(cause));
      } finally {
        if (alive.current && request === reads.current) setLoading(false);
      }
    },
    [api],
  );
  useEffect(() => {
    alive.current = true;
    void load(0);
    return () => {
      alive.current = false;
      reads.current++;
    };
  }, [load]);
  async function restore(id: string) {
    if (mutation.current || loading) return;
    mutation.current = true;
    setPending(id);
    setError('');
    setNotice('');
    try {
      const job = await api.restoreArchivedGenerationJob(id);
      if (!alive.current) return;
      onRestored(job);
      setNotice('记录已还原，生成内容没有重新提交。');
      await load(page.offset);
    } catch (cause) {
      if (alive.current) setError(safeConnectionError(cause));
    } finally {
      mutation.current = false;
      if (alive.current) {
        setPending('');
        window.setTimeout(() => refresh.current?.focus(), 0);
      }
    }
  }
  return (
    <Modal
      open
      title="生成记录归档"
      description="归档只移出默认任务列表，原请求编号、配方、估价和素材文件仍保留。还原不会重新生成；归档不释放媒体占用空间。"
      busy={Boolean(pending)}
      onOpenChange={(value) => {
        if (!value && !mutation.current) onClose();
      }}
      onCloseAutoFocus={(event) => {
        event.preventDefault();
        onReturnFocus();
      }}
    >
      <div className="generation-refresh">
        <span>
          默认历史 {page.active}/2000 · 归档 {page.total}/20000
        </span>
        <Button
          ref={refresh}
          disabled={Boolean(pending)}
          busy={loading}
          onClick={() => void load(page.offset)}
        >
          刷新归档
        </Button>
      </div>
      {page.active >= 2000 ? (
        <p className="field-hint">默认历史已满，请先关闭归档并归档其他已结束任务，再还原记录。</p>
      ) : null}
      {notice ? (
        <p className="generation-notice" role="status">
          {notice}
        </p>
      ) : null}
      {error ? (
        <p className="form-error" role="alert">
          {error}
        </p>
      ) : null}
      {loading ? (
        <p className="field-hint" role="status">
          正在读取归档记录…
        </p>
      ) : null}
      {!loading && !page.items.length ? (
        <p className="field-hint">尚无归档记录。可以在默认任务列表归档已结束的记录。</p>
      ) : null}
      <div className="generation-jobs" aria-label="归档记录列表" aria-busy={loading}>
        {page.items.map((item) => (
          <article
            className="generation-job"
            key={item.id}
            aria-label={`归档记录 ${item.job?.request.prompt.slice(0, 80) || item.id}`}
          >
            <div className="job-heading">
              <strong>
                {item.job ? modeNames[item.job.request.mode] || '生成记录' : '记录需要核对'}
              </strong>
              <span>{item.job ? generationStatus[item.job.status] : '无法读取'}</span>
            </div>
            <p className="field-hint">
              归档于 {new Date(item.archivedAt).toLocaleString('zh-CN', { hour12: false })}
            </p>
            {item.job ? (
              <>
                <p className="generation-meta">{item.job.request.modelId}</p>
                <p className="generation-prompt-preview">{item.job.request.prompt.slice(0, 200)}</p>
                {item.job.request.prompt.length > 200 ? (
                  <details className="agent-details">
                    <summary>查看完整请求描述</summary>
                    <p>{item.job.request.prompt}</p>
                  </details>
                ) : null}
                <GenerationEstimateSummary estimate={item.job.estimate} submitted />
              </>
            ) : null}
            {item.issue ? <p className="form-error">{item.issue}</p> : null}
            <Button
              disabled={loading || Boolean(pending) || !item.canRestore || page.active >= 2000}
              busy={pending === item.id}
              onClick={() => void restore(item.id)}
            >
              还原记录
            </Button>
          </article>
        ))}
      </div>
      <div className="dialog-actions">
        <span className="field-hint">
          {page.total
            ? `${page.offset + 1}–${Math.min(page.offset + page.items.length, page.total)} / ${page.total}`
            : '0 条记录'}
        </span>
        <Button
          disabled={loading || Boolean(pending) || page.offset === 0}
          onClick={() => void load(Math.max(0, page.offset - page.limit))}
        >
          上一页归档
        </Button>
        <Button
          disabled={loading || Boolean(pending) || page.offset + page.limit >= page.total}
          onClick={() => void load(page.offset + page.limit)}
        >
          下一页归档
        </Button>
        <Button disabled={Boolean(pending)} onClick={onClose}>
          关闭归档
        </Button>
      </div>
    </Modal>
  );
}
