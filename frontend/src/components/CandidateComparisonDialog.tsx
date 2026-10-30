import { useRef } from 'react';
import { candidateIsImported, type CandidateComparisonItem } from '../lib/candidate-comparison';
import { modeNames, providerNames } from '../lib/generation';
import type { GenerationJobActions } from './GenerationJobs';
import { Button, Modal } from './ui';
import { SavedCandidateMedia } from './SavedCandidateMedia';

export function CandidateComparisonDialog({
  items,
  pendingID,
  errors,
  onClose,
  onReturnFocus,
  onImport,
  onReplace,
  replacementIssue,
  onPlace,
}: {
  items: CandidateComparisonItem[];
  pendingID: string;
  errors: Record<string, string>;
  onClose: () => void;
  onReturnFocus: () => void;
} & Pick<GenerationJobActions, 'onImport' | 'onReplace' | 'replacementIssue' | 'onPlace'>) {
  const body = useRef<HTMLDivElement | null>(null);
  const busy = items.some((item) => item.job.id === pendingID);
  function pauseOther(current: HTMLVideoElement) {
    body.current?.querySelectorAll('video').forEach((node) => {
      if (node !== current) node.pause();
    });
  }
  function muteOther(current: HTMLVideoElement) {
    body.current?.querySelectorAll('video').forEach((node) => {
      if (node !== current) node.muted = true;
    });
  }
  return (
    <Modal
      open
      title="并排对比候选"
      description="比较已保存的本机原始结果。视频默认静音、逐个播放；工程裁切、LUT和字幕请在替换后的工程预览中核对。"
      className="replacement-dialog candidate-compare-dialog"
      busy={busy}
      onCloseAutoFocus={(event) => {
        event.preventDefault();
        onReturnFocus();
      }}
      onOpenChange={(open) => {
        if (!open && !busy) onClose();
      }}
    >
      <div className="replacement-dialog-body" ref={body}>
        <div className="replacement-comparison candidate-compare-grid">
          {items.map((item, index) => {
            const { job, asset } = item;
            const imported = candidateIsImported(item);
            const issue = replacementIssue?.(job, asset);
            return (
              <section
                className="candidate-compare-pane"
                aria-label={`候选${index === 0 ? 'A' : 'B'}`}
                key={item.key}
              >
                <h3>
                  {index === 0 ? 'A' : 'B'} · {asset.name}
                </h3>
                <SavedCandidateMedia
                  asset={asset}
                  showDetails
                  onPlay={pauseOther}
                  onAudible={muteOther}
                />
                <p className="generation-meta">
                  {providerNames[job.request.providerId] || job.request.providerId} ·{' '}
                  {job.request.modelId}
                </p>
                <details className="agent-details">
                  <summary>生成参数与描述</summary>
                  <p>
                    {modeNames[job.request.mode] || job.request.mode} ·{' '}
                    {[
                      job.request.size,
                      job.request.aspectRatio,
                      job.request.quality,
                      job.request.outputFormat,
                    ]
                      .filter(Boolean)
                      .join(' · ') || '默认参数'}{' '}
                    ·{' '}
                    {job.request.durationSeconds ? `${job.request.durationSeconds}秒` : '静态图片'}
                  </p>
                  {job.request.generateAudio !== undefined ? (
                    <p>请求声音：{job.request.generateAudio ? '有声' : '无声'}</p>
                  ) : null}
                  <p>{job.request.prompt}</p>
                  <p className="field-hint">
                    任务 {job.id} · 配方 {asset.recipeId}
                  </p>
                </details>
                <div className="candidate-compare-actions">
                  {onPlace ? (
                    <Button disabled={Boolean(pendingID)} onClick={() => onPlace(job, asset)}>
                      添加到时间线…
                    </Button>
                  ) : null}
                  <Button
                    disabled={Boolean(pendingID) || imported}
                    busy={job.id === pendingID}
                    onClick={() => void onImport(job, [asset.id])}
                  >
                    {imported ? '已加入素材库' : '加入素材库'}
                  </Button>
                  {onReplace ? (
                    <Button
                      disabled={Boolean(pendingID) || !!issue}
                      onClick={() => onReplace(job, asset)}
                    >
                      替换当前镜头…
                    </Button>
                  ) : null}
                </div>
                {issue ? <p className="field-hint">{issue}</p> : null}
                {errors[job.id] ? (
                  <p className="form-error" role="alert">
                    {errors[job.id]}
                  </p>
                ) : null}
              </section>
            );
          })}
        </div>
      </div>
      <div className="dialog-actions replacement-dialog-actions">
        <Button disabled={busy} onClick={onClose}>
          返回生成任务
        </Button>
      </div>
    </Modal>
  );
}
