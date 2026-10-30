import { lazy, Suspense, useEffect, useMemo, useRef, useState } from 'react';
import { Download, Image as ImageIcon, RefreshCw, Square, Video } from 'lucide-react';
import type { GenerationJob, GenerationOutputAsset } from '../generation-types';
import {
  generationStatus,
  jobCanResume,
  jobCanArchive,
  modeNames,
  providerNames,
} from '../lib/generation';
import { Button, Modal } from './ui';
import {
  comparisonItems,
  comparisonKey,
  type CandidateComparisonItem,
} from '../lib/candidate-comparison';
import { SavedCandidateMedia } from './SavedCandidateMedia';
import { GenerationEstimateSummary } from './GenerationEstimate';
const CandidateComparisonDialog = lazy(() =>
  import('./CandidateComparisonDialog').then((module) => ({
    default: module.CandidateComparisonDialog,
  })),
);

export interface GenerationJobActions {
  onPlace?: (job: GenerationJob, candidate: GenerationOutputAsset, trigger?: HTMLElement) => void;
  onReuse?: (job: GenerationJob, candidate?: GenerationOutputAsset, trigger?: HTMLElement) => void;
  onStop: (job: GenerationJob) => Promise<boolean>;
  onResume: (job: GenerationJob) => Promise<boolean>;
  onAcknowledge: (job: GenerationJob) => Promise<boolean>;
  onImport: (job: GenerationJob, assetIDs?: string[]) => Promise<boolean>;
  onArchive?: (job: GenerationJob) => Promise<boolean>;
  onReplace?: (job: GenerationJob, candidate: GenerationOutputAsset) => void;
  replacementIssue?: (job: GenerationJob, candidate: GenerationOutputAsset) => string;
}

function GenerationJobCard({
  job,
  projectID,
  pendingID,
  error,
  onStop,
  onResume,
  onAcknowledge,
  onImport,
  onArchive,
  onReplace,
  replacementIssue,
  onReuse,
  onPlace,
  comparedKeys,
  comparisonFull,
  comparing,
  onCompare,
}: {
  job: GenerationJob;
  projectID: string;
  pendingID: string;
  error?: string;
  comparedKeys: string[];
  comparisonFull: boolean;
  comparing: boolean;
  onCompare: (job: GenerationJob, asset: GenerationOutputAsset) => void;
} & GenerationJobActions) {
  const [selection, setSelection] = useState<string[]>([]);
  const [confirmation, setConfirmation] = useState<'stop' | 'ack' | 'archive' | null>(null);
  const [recordChecked, setRecordChecked] = useState(false);
  const imported = new Set(
    job.imported ? job.assets.map((asset) => asset.id) : job.importedAssetIds || [],
  );
  const canStop = ['queued', 'submitting', 'running', 'waiting', 'importing'].includes(job.status);
  const unknownWithoutID = job.status === 'unknown' && !job.remoteId;
  const targetOpen = job.projectId === projectID;
  const hasSavedResults = job.status === 'succeeded' && job.assets.length > 0;
  const pending = pendingID === job.id;
  const selected = selection.filter((id) => !imported.has(id));
  useEffect(() => {
    setSelection((current) => current.filter((id) => !job.importedAssetIds?.includes(id)));
  }, [job.importedAssetIds?.join(',')]);

  async function importSelected() {
    if (selected.length && (await onImport(job, selected))) setSelection([]);
  }
  const date = new Date(job.createdAt);
  const created = Number.isNaN(date.valueOf())
    ? job.createdAt
    : date.toLocaleString('zh-CN', {
        month: '2-digit',
        day: '2-digit',
        hour: '2-digit',
        minute: '2-digit',
        hour12: false,
      });

  return (
    <article
      className={`generation-job generation-job-${job.status}`}
      aria-label={`${modeNames[job.request.mode] || '生成任务'} ${generationStatus[job.status]} · ${job.request.prompt.slice(0, 80)}`}
    >
      <div className="job-heading">
        <strong>{modeNames[job.request.mode] || '生成任务'}</strong>
        <span>{generationStatus[job.status]}</span>
      </div>
      <p className="generation-meta">
        {providerNames[job.request.providerId] || job.request.providerId} · {job.request.modelId}
      </p>
      <p className="generation-prompt-preview">{job.request.prompt}</p>
      <p className="field-hint">
        {created} · 数量 {job.request.count || 1}
      </p>
      <p className="generation-job-message">{job.message}</p>
      <GenerationEstimateSummary estimate={job.estimate} submitted />
      {unknownWithoutID ? (
        <p className="generation-warning">
          服务商可能已经受理并计费，但没有可恢复的远端标识。请核对服务记录；这里不会重交。
        </p>
      ) : null}
      {job.acknowledged ? <p className="field-hint">已记录人工核对，远端状态仍可能未知。</p> : null}
      {job.imported ? (
        <p className="generation-notice">全部结果已加入素材库</p>
      ) : imported.size ? (
        <p className="generation-notice">{imported.size} 个候选已加入素材库</p>
      ) : null}
      {hasSavedResults ? (
        <div className="generation-candidates">
          {job.assets.map((asset) => (
            <div className="generation-candidate" key={asset.id}>
              {asset.url ? (
                <SavedCandidateMedia asset={asset} active={!comparing} />
              ) : (
                <div className="candidate-no-preview">
                  {asset.kind === 'video' ? <Video size={23} /> : <ImageIcon size={23} />}
                  <span>已保存，加入素材库后可继续查看</span>
                </div>
              )}
              <label className="candidate-select">
                <input
                  type="checkbox"
                  checked={imported.has(asset.id) || selection.includes(asset.id)}
                  disabled={Boolean(pendingID) || imported.has(asset.id) || !targetOpen}
                  onChange={(event) =>
                    setSelection((current) =>
                      event.target.checked
                        ? [...current, asset.id]
                        : current.filter((id) => id !== asset.id),
                    )
                  }
                />
                <span>
                  {asset.name}
                  {imported.has(asset.id) ? ' · 已导入' : ''}
                </span>
              </label>
              {onPlace ? (
                <Button
                  disabled={Boolean(pendingID) || !targetOpen || !asset.url}
                  onClick={(event) => onPlace(job, asset, event.currentTarget)}
                >
                  添加到时间线…
                </Button>
              ) : null}
              {onReuse && asset.kind === 'image' ? (
                <Button
                  disabled={
                    Boolean(pendingID) ||
                    !targetOpen ||
                    !['image/png', 'image/jpeg'].includes(asset.mime)
                  }
                  onClick={(event) => onReuse(job, asset, event.currentTarget)}
                >
                  以此图片继续修改…
                </Button>
              ) : null}
              <Button
                aria-pressed={comparedKeys.includes(comparisonKey(job, asset))}
                aria-label={`${asset.name} · ${comparedKeys.includes(comparisonKey(job, asset)) ? '移出对比' : '加入对比'}`}
                disabled={
                  Boolean(pendingID) ||
                  !targetOpen ||
                  !asset.url ||
                  (comparisonFull && !comparedKeys.includes(comparisonKey(job, asset)))
                }
                onClick={() => onCompare(job, asset)}
              >
                {comparedKeys.includes(comparisonKey(job, asset)) ? '移出对比' : '加入对比'}
              </Button>
              {onReplace ? (
                <>
                  <Button
                    disabled={
                      Boolean(pendingID) || !targetOpen || Boolean(replacementIssue?.(job, asset))
                    }
                    onClick={() => onReplace(job, asset)}
                  >
                    替换当前镜头…
                  </Button>
                  {replacementIssue?.(job, asset) ? (
                    <p className="field-hint">{replacementIssue(job, asset)}</p>
                  ) : null}
                </>
              ) : null}
            </div>
          ))}
        </div>
      ) : null}
      {!targetOpen ? (
        <p className="field-hint">请打开任务所属工程后加入结果；不会添加到当前其他工程。</p>
      ) : null}
      <div className="generation-job-actions">
        {onArchive && jobCanArchive(job) ? (
          <Button
            variant="ghost"
            disabled={Boolean(pendingID)}
            onClick={() => setConfirmation('archive')}
          >
            归档记录…
          </Button>
        ) : null}
        {onReuse && hasSavedResults ? (
          <Button
            disabled={Boolean(pendingID) || !targetOpen}
            onClick={(event) => onReuse(job, undefined, event.currentTarget)}
          >
            沿用参数…
          </Button>
        ) : null}
        {canStop ? (
          <Button
            variant="ghost"
            disabled={Boolean(pendingID)}
            onClick={() => setConfirmation('stop')}
          >
            <Square size={12} />
            停止本地等待
          </Button>
        ) : null}
        {jobCanResume(job) ? (
          <Button
            variant="ghost"
            busy={pending}
            disabled={Boolean(pendingID)}
            onClick={() => void onResume(job)}
          >
            <RefreshCw size={12} />
            {job.remoteId ? '恢复查询' : '恢复获取结果'}
          </Button>
        ) : null}
        {unknownWithoutID && !job.acknowledged ? (
          <Button
            variant="ghost"
            disabled={Boolean(pendingID)}
            onClick={() => {
              setRecordChecked(false);
              setConfirmation('ack');
            }}
          >
            已核对服务记录…
          </Button>
        ) : null}
        {hasSavedResults && !job.imported ? (
          <>
            <Button
              disabled={Boolean(pendingID) || !targetOpen || !selected.length}
              onClick={() => void importSelected()}
            >
              <Download size={12} />
              加入所选候选
            </Button>
            <Button
              variant="ghost"
              disabled={Boolean(pendingID) || !targetOpen}
              onClick={() => void onImport(job)}
            >
              加入全部结果
            </Button>
          </>
        ) : null}
      </div>
      {error ? (
        <p className="form-error" role="alert">
          {error}
        </p>
      ) : null}
      <Modal
        open={Boolean(confirmation)}
        title={
          confirmation === 'archive'
            ? '归档生成记录'
            : confirmation === 'ack'
              ? '记录已核对服务记录'
              : '停止本地等待？'
        }
        description={
          confirmation === 'archive'
            ? '仅将这条已结束任务移出默认列表。原请求编号、配方、费用参考和媒体文件保留，可在归档管理中还原；不改变远端状态。'
            : confirmation === 'ack'
              ? '请先到服务商核对本次请求的受理和费用。此操作只记录核对，不重新提交、不取消远端，也不保证退款。'
              : '仅停止这台电脑的等待。服务商可能继续生成并计费，后续是否可恢复取决于已有任务信息。'
        }
        busy={pending}
        onOpenChange={(value) => {
          if (!value && !pending) setConfirmation(null);
        }}
      >
        {confirmation === 'archive' ? (
          <>
            <p className="generation-meta">{job.request.modelId}</p>
            <p className="generation-prompt-preview">{job.request.prompt}</p>
            <GenerationEstimateSummary estimate={job.estimate} submitted />
          </>
        ) : null}
        {confirmation === 'ack' ? (
          <label className="checkbox-field">
            <input
              type="checkbox"
              checked={recordChecked}
              onChange={(event) => setRecordChecked(event.target.checked)}
            />
            我已在服务商核对本次请求
          </label>
        ) : null}
        <div className="dialog-actions">
          <Button disabled={pending} onClick={() => setConfirmation(null)}>
            返回任务
          </Button>
          <Button
            variant="primary"
            busy={pending}
            disabled={
              confirmation === 'archive'
                ? !onArchive || !jobCanArchive(job)
                : confirmation === 'ack'
                  ? !recordChecked || !unknownWithoutID
                  : !canStop
            }
            onClick={() => {
              void (
                confirmation === 'archive'
                  ? onArchive?.(job) || Promise.resolve(false)
                  : confirmation === 'ack'
                    ? onAcknowledge(job)
                    : onStop(job)
              ).then((success) => {
                if (success) setConfirmation(null);
              });
            }}
          >
            {confirmation === 'archive'
              ? '移入归档'
              : confirmation === 'ack'
                ? '记录已核对'
                : '停止本地等待'}
          </Button>
        </div>
        {error ? (
          <p className="form-error" role="alert">
            {error}
          </p>
        ) : null}
      </Modal>
    </article>
  );
}

export function GenerationJobs({
  jobs,
  projectID,
  pendingID,
  errors,
  onBeforeCompare,
  suspendMedia = false,
  ...actions
}: {
  jobs: GenerationJob[];
  projectID: string;
  pendingID: string;
  errors: Record<string, string>;
  onBeforeCompare?: () => void;
  suspendMedia?: boolean;
} & GenerationJobActions) {
  const [comparedKeys, setComparedKeys] = useState<string[]>([]);
  const [compareOpen, setCompareOpen] = useState(false);
  const compareTrigger = useRef<HTMLElement | null>(null);
  const [comparisonNotice, setComparisonNotice] = useState('');
  const [pageIndex, setPageIndex] = useState(0);
  const pageSize = 20;
  const lastPage = Math.max(0, Math.ceil(jobs.length / pageSize) - 1);
  const currentPage = Math.min(pageIndex, lastPage);
  const pageStart = currentPage * pageSize;
  const visibleJobs = jobs.slice(pageStart, pageStart + pageSize);
  const pageBusy = Boolean(pendingID) || compareOpen || suspendMedia;
  useEffect(() => setPageIndex((index) => Math.min(index, lastPage)), [lastPage]);
  const available = useMemo(() => comparisonItems(jobs, projectID), [jobs, projectID]);
  const chosen = comparedKeys
    .map((key) => available.get(key))
    .filter((item): item is CandidateComparisonItem => !!item);
  useEffect(() => {
    if (comparedKeys.some((key) => !available.has(key))) {
      setComparedKeys((keys) => keys.filter((key) => available.has(key)));
      setCompareOpen(false);
      setComparisonNotice('对比结果或所属工程已变化，请重新选择。');
    }
  }, [available, comparedKeys]);
  function toggleCompare(job: GenerationJob, asset: GenerationOutputAsset) {
    const key = comparisonKey(job, asset);
    if (!available.has(key)) return;
    setComparisonNotice('');
    setComparedKeys((keys) =>
      keys.includes(key)
        ? keys.filter((value) => value !== key)
        : keys.length < 2
          ? [...keys, key]
          : keys,
    );
  }
  return (
    <div className="generation-jobs">
      {jobs.length ? (
        <section className="candidate-compare-tools" aria-label="候选对比选择">
          <p className="field-hint">已选 {chosen.length}/2 个对比候选，可来自不同生成任务。</p>
          {chosen.map((item, index) => (
            <div className="candidate-compare-actions" key={item.key}>
              <span className="candidate-compare-name">
                {index === 0 ? 'A' : 'B'} · {item.asset.name}
                <span className="field-hint" title={item.job.request.prompt}>
                  {item.job.request.modelId} · {item.job.request.prompt.slice(0, 80)}
                  {item.job.request.prompt.length > 80 ? '…' : ''}
                </span>
              </span>
              <Button
                aria-label={`移除对比候选${index === 0 ? 'A' : 'B'}`}
                onClick={() => toggleCompare(item.job, item.asset)}
              >
                移除
              </Button>
            </div>
          ))}
          <div className="candidate-compare-actions">
            <Button
              disabled={chosen.length !== 2}
              onClick={(event) => {
                compareTrigger.current = event.currentTarget;
                onBeforeCompare?.();
                setCompareOpen(true);
              }}
            >
              并排对比
            </Button>
            <Button
              disabled={!chosen.length}
              onClick={() => {
                setComparedKeys([]);
                setComparisonNotice('');
              }}
            >
              清空对比
            </Button>
          </div>
        </section>
      ) : null}
      {comparisonNotice ? (
        <p className="field-hint" role="status">
          {comparisonNotice}
        </p>
      ) : null}
      {jobs.length > 0 ? (
        <nav className="generation-refresh" aria-label="生成任务分页">
          <span className="field-hint" role="status">
            {pageStart + 1}–{Math.min(pageStart + pageSize, jobs.length)} / {jobs.length}
          </span>
          <Button
            disabled={pageBusy || currentPage === 0}
            onClick={() => setPageIndex(currentPage - 1)}
          >
            上一页任务
          </Button>
          <Button
            disabled={pageBusy || currentPage === lastPage}
            onClick={() => setPageIndex(currentPage + 1)}
          >
            下一页任务
          </Button>
        </nav>
      ) : null}
      {jobs.length ? (
        visibleJobs.map((job) => (
          <GenerationJobCard
            key={job.id}
            job={job}
            projectID={projectID}
            pendingID={pendingID}
            error={errors[job.id]}
            comparedKeys={comparedKeys}
            comparisonFull={chosen.length >= 2}
            comparing={suspendMedia || (compareOpen && chosen.length === 2)}
            onCompare={toggleCompare}
            {...actions}
          />
        ))
      ) : (
        <div className="inspector-empty">
          <ImageIcon size={26} />
          <p>还没有生成任务</p>
          <span>点击确认生成后，真实任务与保存结果会显示在这里。</span>
        </div>
      )}
      {compareOpen && chosen.length === 2 ? (
        <Suspense
          fallback={
            <p className="field-hint" role="status">
              正在打开候选对比…
            </p>
          }
        >
          <CandidateComparisonDialog
            onReturnFocus={() => {
              if (compareTrigger.current?.isConnected) compareTrigger.current.focus();
            }}
            items={chosen}
            pendingID={pendingID}
            errors={errors}
            onClose={() => setCompareOpen(false)}
            onImport={actions.onImport}
            replacementIssue={actions.replacementIssue}
            onPlace={
              actions.onPlace
                ? (job, asset) => {
                    const trigger = compareTrigger.current;
                    compareTrigger.current = null;
                    setCompareOpen(false);
                    actions.onPlace?.(job, asset, trigger || undefined);
                  }
                : undefined
            }
            onReplace={
              actions.onReplace
                ? (job, asset) => {
                    compareTrigger.current = null;
                    setCompareOpen(false);
                    actions.onReplace?.(job, asset);
                  }
                : undefined
            }
          />
        </Suspense>
      ) : null}
    </div>
  );
}
