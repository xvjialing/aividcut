import { lazy, Suspense, useCallback, useEffect, useId, useRef, useState } from 'react';
import { Image as ImageIcon, Plus, RefreshCw, Settings2, WandSparkles, X } from 'lucide-react';
import type { Bootstrap, Clip, Project } from '../types';
import type { TimelineRange } from '../ripple-types';
import type { GenerationPlacementAPI } from '../generation-placement-types';
const GenerationPlacementDialog = lazy(() =>
  import('./GenerationPlacementDialog').then((module) => ({
    default: module.GenerationPlacementDialog,
  })),
);
import type {
  ConnectionProfile,
  GenerationAPI,
  GenerationCapability,
  GenerationJob,
  GenerationRequest,
  GenerationOutputAsset,
  GenerationEstimateInput,
} from '../generation-types';
import { calculateCostEstimate, estimateInput } from '../lib/generation-estimate';
import { GenerationEstimateFields } from './GenerationEstimate';
import { generationBridge } from '../lib/bridge';
import {
  allowedAspects,
  canGenerate,
  defaultGenerationDraft,
  draftFromGenerationRequest,
  generationContentSignature,
  eligibleReferenceImages,
  findCapability,
  generationConnections,
  makeGenerationRequest,
  modeNames,
  providerNames,
  referenceRoleName,
  requestSignature,
  safeConnectionError,
  sizeAspect,
  validateGenerationDraft,
} from '../lib/generation';
import { Button, Field, Input, Modal, Select } from './ui';
import { ConnectionsDialog } from './ConnectionsDialog';
import { GenerationJobs } from './GenerationJobs';
const GenerationArchiveDialog = lazy(() =>
  import('./GenerationArchiveDialog').then((module) => ({
    default: module.GenerationArchiveDialog,
  })),
);
const GenerationReplacementDialog = lazy(() =>
  import('./GenerationReplacementDialog').then((module) => ({
    default: module.GenerationReplacementDialog,
  })),
);
import { replacementTargetIssue } from '../lib/action-review';
import type { ReplacementAPI } from '../replacement-types';

import type { CaptureIntent } from '../frame-capture-types';
import type {
  GenerationReuseAPI,
  GenerationReuseInput,
  GenerationReusePlan,
} from '../generation-reuse-types';
const GenerationReuseDialog = lazy(() =>
  import('./GenerationReuseDialog').then((module) => ({ default: module.GenerationReuseDialog })),
);
const FrameCapturePanel = lazy(() =>
  import('./FrameCapturePanel').then((module) => ({ default: module.FrameCapturePanel })),
);

type Review = {
  connectionId: string;
  connectionName: string;
  projectId: string;
  projectRevision: number;
  sourceEpoch?: number;
  request: GenerationRequest;
  signature: string;
  estimate: GenerationEstimateInput;
  estimateLocked: boolean;
  estimateError?: string;
};

export function GenerationPanel({
  project,
  onOpenConnections,
  connectionsVersion = 0,
  sourceEpoch,
  currentFrame,
  pausePlayback,
  captureIntent,
  onBootstrap,
  importToProject,
  api = generationBridge,
  selectedClipId,
  prepareProject,
  applyReplacement,
  replacementAPI,
  reuseAPI,
  timeRange,
  applyPlacement,
  onPlaced,
  placementAPI,
}: {
  project: Project;
  onOpenConnections?: () => void;
  connectionsVersion?: number;
  sourceEpoch?: number;
  currentFrame?: () => number;
  pausePlayback?: () => void;
  captureIntent?: CaptureIntent | null;
  onBootstrap: (value: Bootstrap) => void;
  importToProject?: (action: () => Promise<Bootstrap>) => Promise<Bootstrap | null>;
  api?: GenerationAPI;
  selectedClipId?: string | null;
  prepareProject?: () => Promise<Project | null>;
  applyReplacement?: (action: () => Promise<Bootstrap>) => Promise<Bootstrap | null>;
  replacementAPI?: ReplacementAPI;
  reuseAPI?: GenerationReuseAPI;
  timeRange?: TimelineRange;
  applyPlacement?: (action: () => Promise<Bootstrap>) => Promise<Bootstrap | null>;
  onPlaced?: (projectId: string, clip: Clip) => void;
  placementAPI?: GenerationPlacementAPI;
}) {
  const [connections, setConnections] = useState<ConnectionProfile[]>([]);
  const [catalog, setCatalog] = useState<GenerationCapability[]>([]);
  const [jobs, setJobs] = useState<GenerationJob[]>([]);
  const [connectionID, setConnectionID] = useState('');
  const [draft, setDraft] = useState(() => defaultGenerationDraft(undefined, project));
  const [loaded, setLoaded] = useState(false);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const [syncError, setSyncError] = useState('');
  const [notice, setNotice] = useState('');
  const [freshCapture, setFreshCapture] = useState('');
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [showConnections, setShowConnections] = useState(false);
  const [review, setReview] = useState<Review | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const submittingRef = useRef(false);
  const [pendingJobID, setPendingJobID] = useState('');
  const pendingJob = useRef('');
  const [jobErrors, setJobErrors] = useState<Record<string, string>>({});
  const [referenceLimit, setReferenceLimit] = useState(20);
  const [view, setView] = useState<'create' | 'jobs'>('create');
  const [showArchive, setShowArchive] = useState(false);
  const archiveTrigger = useRef<HTMLButtonElement>(null);
  const [placing, setPlacing] = useState<{
    job: GenerationJob;
    candidate: GenerationOutputAsset;
  } | null>(null);
  const placementTrigger = useRef<HTMLElement | null>(null);
  const [reuse, setReuse] = useState<{ job: GenerationJob; input: GenerationReuseInput } | null>(
    null,
  );
  const prefill = useRef<{
    scope: string;
    connectionID: string;
    draft: ReturnType<typeof draftFromGenerationRequest>;
  } | null>(null);
  const scopeKey = `${project.id}:${sourceEpoch ?? 0}`;
  const reuseTrigger = useRef<HTMLElement | null>(null);
  useEffect(() => {
    if (captureIntent?.projectId === project.id && captureIntent?.sourceEpoch === sourceEpoch)
      setView('create');
  }, [captureIntent?.id, project.id, sourceEpoch]);
  const [replacing, setReplacing] = useState<{
    job: GenerationJob;
    candidate: GenerationOutputAsset;
    clipId: string;
  } | null>(null);
  const alive = useRef(false);
  const reads = useRef(0);
  const requestIDs = useRef(new Map<string, string>());
  const attemptedEstimates = useRef(new Map<string, GenerationEstimateInput | undefined>());
  const failedSubmission = useRef(new Set<string>());
  const projectRef = useRef(project);
  projectRef.current = project;
  const previousProject = useRef(scopeKey);
  const fieldPrefix = useId();
  const form = useRef<HTMLFormElement>(null);
  const focusPrompt = useRef(false);
  const profile = connections.find((value) => value.id === connectionID);
  const capability = findCapability(profile, catalog);
  const capabilityKey = `${profile?.id || ''}:${capability?.providerId || ''}:${capability?.modelId || ''}`;

  const jobsRef = useRef(jobs);
  jobsRef.current = jobs;
  const syncState = useRef<{ api: GenerationAPI; fingerprint: string }>({ api, fingerprint: '' });
  function invalidateGenerationReads() {
    reads.current++;
    syncState.current.fingerprint = '';
  }
  const refreshJobs = useCallback(
    async (force = false) => {
      const token = ++reads.current;
      if (syncState.current.api !== api) syncState.current = { api, fingerprint: '' };
      const requested = force ? '' : syncState.current.fingerprint;
      try {
        const result = await api.syncGenerationJobs({ fingerprint: requested });
        if (!alive.current || token !== reads.current || syncState.current.api !== api)
          return jobsRef.current;
        if (
          !/^[a-f0-9]{64}$/.test(result.fingerprint) ||
          typeof result.changed !== 'boolean' ||
          (!result.changed && (requested === '' || result.fingerprint !== requested)) ||
          (result.changed && !Array.isArray(result.jobs))
        )
          throw new Error('任务同步回复无效，请重新读取完整列表。');
        setSyncError('');
        syncState.current.fingerprint = result.fingerprint;
        if (!result.changed) return jobsRef.current;
        const values = result.jobs.map((job) => ({
          ...job,
          assets: job.assets || [],
          importedAssetIds: job.importedAssetIds || [],
        }));
        jobsRef.current = values;
        setJobs(values);
        return values;
      } catch (cause) {
        if (alive.current && token === reads.current && syncState.current.api === api)
          setSyncError(safeConnectionError(cause));
        throw cause;
      }
    },
    [api],
  );
  const load = useCallback(async () => {
    setLoading(true);
    setError('');
    try {
      const [profileValues, capabilityValues] = await Promise.all([
        api.getConnections(),
        api.getGenerationCatalog(),
      ]);
      const capabilities = capabilityValues || [];
      const profiles = generationConnections(profileValues || [], capabilities);
      if (!alive.current) return;
      setConnections(profiles || []);
      setCatalog(capabilities || []);
      setConnectionID((current) =>
        profiles.some((value) => value.id === current)
          ? current
          : profiles.find((value) => value.configured)?.id || profiles[0]?.id || '',
      );
      await refreshJobs(true);
      if (alive.current) setLoaded(true);
    } catch (cause) {
      if (alive.current) setError(safeConnectionError(cause));
    } finally {
      if (alive.current) setLoading(false);
    }
  }, [api, refreshJobs]);
  useEffect(() => {
    alive.current = true;
    void load();
    return () => {
      alive.current = false;
      invalidateGenerationReads();
    };
  }, [load]);
  useEffect(() => {
    if (!loaded) return;
    const interval = window.setInterval(() => {
      void refreshJobs().catch(() => undefined);
    }, 2500);
    return () => clearInterval(interval);
  }, [loaded, refreshJobs]);
  useEffect(() => {
    if (!loaded) return;
    void refreshJobs().catch(() => undefined);
  }, [loaded, project.id, project.revision, refreshJobs]);
  useEffect(() => {
    const sameProject = previousProject.current === scopeKey;
    const next = prefill.current;
    if (next && next.scope === scopeKey && next.connectionID === connectionID) {
      setDraft(next.draft);
      prefill.current = null;
    } else
      setDraft((current) =>
        defaultGenerationDraft(capability, project, sameProject ? current.prompt : ''),
      );
    setErrors({});
    setReview(null);
    setReferenceLimit(20);
    if (!sameProject) {
      setPlacing(null);
      setReuse(null);
      prefill.current = null;
      focusPrompt.current = false;
    }
    previousProject.current = scopeKey;
  }, [capabilityKey, scopeKey]);
  useEffect(() => {
    if (!focusPrompt.current || view !== 'create') return;
    const timer = window.setTimeout(() => {
      const target = form.current?.querySelector<HTMLTextAreaElement>('textarea');
      if (target?.isConnected) {
        target.focus();
        focusPrompt.current = false;
      }
    }, 0);
    return () => window.clearTimeout(timer);
  }, [view, draft, capabilityKey, scopeKey]);

  const references = eligibleReferenceImages(project.assets);
  const candidate =
    profile && capability ? makeGenerationRequest('draft', profile, capability, draft) : null;
  const signature =
    candidate && profile
      ? JSON.stringify([project.id, requestSignature(profile.id, candidate)])
      : '';
  const blockingJob = jobs.find(
    (job) =>
      job.projectId === project.id &&
      !job.acknowledged &&
      candidate &&
      profile &&
      generationContentSignature(job.connectionId, job.request) ===
        generationContentSignature(profile.id, candidate) &&
      (!['succeeded', 'failed', 'cancelled'].includes(job.status) || job.canResume),
  );
  const enabled =
    loaded && !loading && profile?.configured && canGenerate(capability) && !blockingJob;
  const aspects = capability ? allowedAspects(capability, draft.mode) : [];
  const showReferences = ['image.edit', 'video.image', 'video.reference'].includes(draft.mode);
  const fieldError = (key: string) =>
    errors[key] ? (
      <span id={`${fieldPrefix}-${key}`} className="form-error">
        {errors[key]}
      </span>
    ) : null;
  const invalid = (key: string) => ({
    'aria-invalid': Boolean(errors[key]),
    'aria-describedby': errors[key] ? `${fieldPrefix}-${key}` : undefined,
  });

  function chooseMode(mode: string) {
    setDraft((current) => defaultGenerationDraft(capability, project, current.prompt, mode));
    setErrors({});
  }
  function updateDraft<K extends keyof typeof draft>(key: K, value: (typeof draft)[K]) {
    setDraft((current) => ({
      ...current,
      [key]: value,
      ...(key === 'references'
        ? {
            referenceFingerprints: Object.fromEntries(
              (value as typeof draft.references).map((ref) => [
                ref.assetId,
                (current.references.some(
                  (old) => old.assetId === ref.assetId && old.role === ref.role,
                ) &&
                  current.referenceFingerprints?.[ref.assetId]) ||
                  project.assets.find((asset) => asset.id === ref.assetId)?.fingerprint ||
                  '',
              ]),
            ),
          }
        : {}),
    }));
    setErrors((current) => ({ ...current, [key]: '' }));
  }
  function chooseReference(role: string, assetID: string) {
    updateDraft('references', [
      ...draft.references.filter((reference) => reference.role !== role),
      ...(assetID ? [{ assetId: assetID, role }] : []),
    ]);
  }
  function openReview(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setError('');
    const invalidFields = validateGenerationDraft(profile, capability, draft, project.assets);
    setErrors(invalidFields);
    if (Object.keys(invalidFields).length || !profile || !capability || blockingJob) {
      window.setTimeout(
        () => form.current?.querySelector<HTMLElement>('[aria-invalid="true"]')?.focus(),
        0,
      );
      return;
    }
    const existing = requestIDs.current.get(signature);
    const requestID =
      existing &&
      failedSubmission.current.has(signature) &&
      !jobs.some((job) => job.request.id === existing)
        ? existing
        : crypto.randomUUID();
    setReview({
      connectionId: profile.id,
      connectionName: profile.name,
      projectId: project.id,
      projectRevision: project.revision,
      sourceEpoch,
      request: makeGenerationRequest(requestID, profile, capability, draft),
      signature,
      estimate: attemptedEstimates.current.get(requestID) || {
        currency: 'CNY',
        unit: 'request',
        unitPrice: '',
      },
      estimateLocked: attemptedEstimates.current.has(requestID),
    });
  }
  async function confirmGeneration() {
    if (!review || submittingRef.current) return;
    let estimate: GenerationEstimateInput | undefined;
    try {
      estimate =
        review.estimate.unitPrice === ''
          ? undefined
          : estimateInput(calculateCostEstimate(review.request, review.estimate));
    } catch (cause) {
      setReview({
        ...review,
        estimateError: cause instanceof Error ? cause.message : '请核对费用参考',
      });
      return;
    }
    if (
      projectRef.current.id !== review.projectId ||
      projectRef.current.revision !== review.projectRevision ||
      sourceEpoch !== review.sourceEpoch
    ) {
      setError('工程或参考素材已变化，请重新核对本次生成。');
      setReview(null);
      return;
    }
    submittingRef.current = true;
    setSubmitting(true);
    setError('');
    requestIDs.current.set(review.signature, review.request.id);
    attemptedEstimates.current.set(review.request.id, estimate);
    try {
      // consent=true 只在用户确认这一个带计费提示的请求时产生。
      const job = await api.submitGeneration({
        connectionId: review.connectionId,
        request: review.request,
        consent: true,
        ...(estimate ? { estimate } : {}),
        context: {
          projectId: review.projectId,
          revision: review.projectRevision,
          sourceEpoch: review.sourceEpoch,
        },
      });
      failedSubmission.current.delete(review.signature);
      invalidateGenerationReads();
      if (alive.current) {
        setJobs((current) => [job, ...current.filter((value) => value.id !== job.id)]);
        setReview(null);
        setView('jobs');
        setNotice('请求已交给本地任务服务，后续状态以任务记录为准。');
      }
    } catch (cause) {
      failedSubmission.current.add(review.signature);
      if (alive.current) {
        setReview(null);
        setError(`${safeConnectionError(cause)} 请刷新任务列表核对提交记录；不会自动重交。`);
      }
    } finally {
      submittingRef.current = false;
      if (alive.current) setSubmitting(false);
    }
  }
  async function runJob(
    job: GenerationJob,
    action: () => Promise<GenerationJob>,
  ): Promise<boolean> {
    if (pendingJob.current) return false;
    pendingJob.current = job.id;
    setPendingJobID(job.id);
    setJobErrors((current) => ({ ...current, [job.id]: '' }));
    try {
      const value = await action();
      invalidateGenerationReads();
      if (alive.current)
        setJobs((current) => current.map((item) => (item.id === value.id ? value : item)));
      return true;
    } catch (cause) {
      if (alive.current)
        setJobErrors((current) => ({ ...current, [job.id]: safeConnectionError(cause) }));
      return false;
    } finally {
      pendingJob.current = '';
      if (alive.current) setPendingJobID('');
    }
  }
  async function importResults(job: GenerationJob, assetIDs?: string[]): Promise<Bootstrap | null> {
    if (
      pendingJob.current ||
      job.projectId !== projectRef.current.id ||
      (assetIDs && !assetIDs.length)
    )
      return null;
    pendingJob.current = job.id;
    setPendingJobID(job.id);
    setJobErrors((current) => ({ ...current, [job.id]: '' }));
    try {
      let actionFailure: unknown;
      const action = async () => {
        try {
          return await (assetIDs
            ? api.importGenerationAssets(job.id, assetIDs)
            : api.importGenerationResult(job.id));
        } catch (cause) {
          actionFailure = cause;
          throw cause;
        }
      };
      const value = await (importToProject ? importToProject(action) : action());
      if (!value) throw actionFailure || Error('尚未导入候选，请处理工程草稿或冲突后重试。');
      if (value.project.id === projectRef.current.id) {
        if (!importToProject) onBootstrap(value);
      } else setNotice('结果已处理，当前工程已切换；请在任务所属工程核对素材。');
      await refreshJobs(true);
      return value;
    } catch (cause) {
      if (alive.current)
        setJobErrors((current) => ({ ...current, [job.id]: safeConnectionError(cause) }));
      return null;
    } finally {
      pendingJob.current = '';
      if (alive.current) setPendingJobID('');
    }
  }

  async function archiveJob(job: GenerationJob): Promise<boolean> {
    if (pendingJob.current) return false;
    pendingJob.current = job.id;
    setPendingJobID(job.id);
    setJobErrors((current) => ({ ...current, [job.id]: '' }));
    try {
      await api.archiveGenerationJob(job.id);
      invalidateGenerationReads();
      if (alive.current) {
        setJobs((current) => current.filter((value) => value.id !== job.id));
        setNotice('记录已移入归档，素材文件仍保留；可在归档管理中还原。');
        window.setTimeout(() => archiveTrigger.current?.focus(), 0);
      }
      return true;
    } catch (cause) {
      if (alive.current)
        setJobErrors((current) => ({ ...current, [job.id]: safeConnectionError(cause) }));
      return false;
    } finally {
      pendingJob.current = '';
      if (alive.current) setPendingJobID('');
    }
  }
  async function stopWaiting(job: GenerationJob) {
    const success = await runJob(job, () => api.stopGenerationWaiting(job.id));
    if (success && alive.current)
      setNotice('已请求停止本地等待，后续以任务记录为准；这不代表服务商已取消生成。');
    return success;
  }
  function connectionChanged(value?: ConnectionProfile) {
    if (value) {
      setConnections((current) =>
        generationConnections(
          [...current.filter((profile) => profile.id !== value.id), value],
          catalog,
        ),
      );
      setConnectionID(
        catalog.some((capability) => capability.providerId === value.providerId) ? value.id : '',
      );
    } else
      void api
        .getConnections()
        .then((values) => {
          if (alive.current) {
            const profiles = generationConnections(values || [], catalog);
            setConnections(profiles);
            setConnectionID((current) =>
              profiles.some((profile) => profile.id === current) ? current : profiles[0]?.id || '',
            );
          }
        })
        .catch((cause) => setError(safeConnectionError(cause)));
  }

  useEffect(() => {
    if (connectionsVersion > 0) connectionChanged();
  }, [connectionsVersion]);

  return (
    <div className="generation-panel">
      <div className="generation-toolbar">
        <div className="generation-view-switch" role="group" aria-label="生成面板视图">
          <Button
            variant={view === 'create' ? 'outline' : 'ghost'}
            aria-pressed={view === 'create'}
            onClick={() => setView('create')}
          >
            创作
          </Button>
          <Button
            variant={view === 'jobs' ? 'outline' : 'ghost'}
            aria-pressed={view === 'jobs'}
            onClick={() => setView('jobs')}
          >
            生成任务{jobs.length ? ` ${jobs.length}` : ''}
          </Button>
        </div>
        <Button
          variant="ghost"
          onClick={() => (onOpenConnections ? onOpenConnections() : setShowConnections(true))}
          disabled={!loaded}
        >
          <Settings2 size={13} />
          管理连接
        </Button>
      </div>
      {error ? (
        <p className="form-error generation-panel-message" role="alert">
          {error}
        </p>
      ) : null}
      {syncError ? (
        <p className="form-error generation-panel-message" role="alert">
          {syncError}
        </p>
      ) : null}
      {notice ? (
        <p className="generation-notice generation-panel-message" role="status">
          {notice}
        </p>
      ) : null}
      {!loaded ? (
        <div className="inspector-empty">
          <ImageIcon size={26} />
          <p>{loading ? '正在读取模型连接' : '模型连接暂不可用'}</p>
          <Button busy={loading} onClick={() => void load()}>
            重新读取
          </Button>
        </div>
      ) : view === 'jobs' ? (
        <>
          <div className="generation-refresh">
            <span>读取任务不会重新提交生成</span>
            <Button
              ref={archiveTrigger}
              disabled={Boolean(pendingJobID)}
              onClick={() => {
                pausePlayback?.();
                setShowArchive(true);
              }}
            >
              归档管理
            </Button>
            <Button variant="ghost" onClick={() => void refreshJobs(true).catch(() => undefined)}>
              <RefreshCw size={12} />
              刷新
            </Button>
          </div>
          <GenerationJobs
            suspendMedia={Boolean(placing || reuse || replacing || showArchive)}
            onPlace={
              applyPlacement && currentFrame
                ? (job, candidate, trigger) => {
                    pausePlayback?.();
                    placementTrigger.current = trigger || null;
                    setPlacing({ job, candidate });
                  }
                : undefined
            }
            key={`${project.id}:${sourceEpoch ?? 0}`}
            onBeforeCompare={pausePlayback}
            jobs={jobs}
            projectID={project.id}
            pendingID={pendingJobID}
            errors={jobErrors}
            onStop={stopWaiting}
            onResume={(job) => runJob(job, () => api.resumeGeneration(job.id))}
            onAcknowledge={(job) => runJob(job, () => api.acknowledgeGenerationUnknown(job.id))}
            onImport={async (job, ids) => !!(await importResults(job, ids))}
            onArchive={archiveJob}
            onReuse={(job, candidate, trigger) => {
              reuseTrigger.current = trigger || null;
              setJobErrors((current) => ({ ...current, [job.id]: '' }));
              setReuse({
                job,
                input: {
                  jobId: job.id,
                  candidateId: candidate?.id,
                  projectId: project.id,
                  sourceEpoch: sourceEpoch ?? 0,
                },
              });
            }}
            replacementIssue={(job, candidate) =>
              replacementTargetIssue(project, selectedClipId || null, candidate.kind, job.projectId)
            }
            onReplace={(job, candidate) => {
              if (
                selectedClipId &&
                !replacementTargetIssue(project, selectedClipId, candidate.kind, job.projectId)
              )
                setReplacing({ job, candidate, clipId: selectedClipId });
            }}
          />
        </>
      ) : (
        <div className="generation-form">
          {sourceEpoch !== undefined && currentFrame && importToProject ? (
            <Suspense fallback={<p className="field-hint">读取本机取图工具…</p>}>
              <FrameCapturePanel
                key={project.id + ':' + sourceEpoch}
                project={project}
                sourceEpoch={sourceEpoch}
                currentFrame={currentFrame}
                pausePlayback={pausePlayback}
                intent={captureIntent}
                prepareProject={prepareProject}
                applyToProject={importToProject}
                onCaptured={(result) => {
                  setFreshCapture(result.assetId);
                  setReview(null);
                }}
              />
            </Suspense>
          ) : null}
          <form ref={form} noValidate onSubmit={openReview}>
            {draft.parentRecipeId ? (
              <div className="generation-notice">
                <p>已从已保存任务加载参数；修改后仍需单独核对生成。</p>
                {draft.background ? <p>沿用背景设置：{draft.background}</p> : null}
                <Button
                  onClick={() => {
                    setDraft(defaultGenerationDraft(capability, project, draft.prompt));
                    setReview(null);
                    setErrors({});
                  }}
                >
                  清除继承设置并恢复默认参数
                </Button>
              </div>
            ) : null}
            {!connections.length ? (
              <div className="generation-empty">
                <p>先连接你的生成服务</p>
                <span>保存配置不验证账号，也不会发起收费调用。</span>
                <Button
                  onClick={() =>
                    onOpenConnections ? onOpenConnections() : setShowConnections(true)
                  }
                >
                  <Plus size={13} />
                  配置模型连接
                </Button>
              </div>
            ) : null}
            <Field label="模型连接">
              <Select
                value={connectionID}
                onChange={(event) => setConnectionID(event.target.value)}
                {...invalid('connection')}
              >
                <option value="">选择模型连接</option>
                {connections.map((connection) => (
                  <option key={connection.id} value={connection.id}>
                    {connection.name} · {connection.configured ? '已配置，未验证' : '未配置凭据'}
                  </option>
                ))}
              </Select>
              {fieldError('connection')}
            </Field>
            {profile ? (
              <p className="generation-meta">
                {providerNames[profile.providerId] || profile.providerId} · {profile.modelId}
                <span>已配置不代表账号与模型权限已验证</span>
              </p>
            ) : null}
            {capability && !canGenerate(capability) ? (
              <p className="generation-warning">
                此模型的具体渠道和可用能力尚未验证，当前不能提交生成。
              </p>
            ) : null}
            {profile && !capability ? (
              <p className="generation-warning">
                此连接模型没有已声明的生成能力，当前不能提交生成。请在连接设置中选择目录内模型。
              </p>
            ) : null}
            {canGenerate(capability) && capability ? (
              <>
                <Field label="创作任务">
                  <Select
                    value={draft.mode}
                    onChange={(event) => chooseMode(event.target.value)}
                    {...invalid('mode')}
                  >
                    {capability.modes.map((mode) => (
                      <option key={mode} value={mode}>
                        {modeNames[mode] || mode}
                      </option>
                    ))}
                  </Select>
                  {fieldError('mode')}
                </Field>
                <Field label="内容描述">
                  <textarea
                    className="input resize-none generation-prompt"
                    rows={5}
                    value={draft.prompt}
                    placeholder="描述主体、动作、场景和构图…"
                    onChange={(event) => updateDraft('prompt', event.target.value)}
                    {...invalid('prompt')}
                  />
                  {fieldError('prompt')}
                </Field>
                <div className="field-pair">
                  {capability.sizes?.length ? (
                    <Field label={capability.mediaKind === 'image' ? '图片尺寸' : '输出尺寸'}>
                      <Select
                        value={draft.size}
                        onChange={(event) => updateDraft('size', event.target.value)}
                        {...invalid('size')}
                      >
                        {draft.preserveDefaults ? <option value="">沿用服务商默认</option> : null}
                        {capability.sizes.map((size) => (
                          <option key={size} value={size}>
                            {size}
                            {capability.mediaKind === 'image' && sizeAspect(size)
                              ? ` · ${sizeAspect(size)}`
                              : ''}
                          </option>
                        ))}
                      </Select>
                      {fieldError('size')}
                    </Field>
                  ) : null}
                  {(capability.mediaKind === 'video' || !capability.sizes?.length) &&
                  aspects.length ? (
                    <Field label="画幅">
                      <Select
                        value={draft.aspectRatio}
                        onChange={(event) => updateDraft('aspectRatio', event.target.value)}
                        {...invalid('aspectRatio')}
                      >
                        {draft.preserveDefaults ? <option value="">沿用服务商默认</option> : null}
                        {aspects.map((aspect) => (
                          <option key={aspect} value={aspect}>
                            {aspect === 'adaptive'
                              ? draft.mode === 'video.image'
                                ? '跟随首帧'
                                : '自动适配'
                              : aspect}
                          </option>
                        ))}
                      </Select>
                      {fieldError('aspectRatio')}
                    </Field>
                  ) : null}
                  {capability.qualities?.length ? (
                    <Field label="质量">
                      <Select
                        value={draft.quality}
                        onChange={(event) => updateDraft('quality', event.target.value)}
                        {...invalid('quality')}
                      >
                        {draft.preserveDefaults ? <option value="">沿用服务商默认</option> : null}
                        {capability.qualities.map((quality) => (
                          <option key={quality} value={quality}>
                            {quality}
                          </option>
                        ))}
                      </Select>
                      {fieldError('quality')}
                    </Field>
                  ) : null}
                  {capability.outputFormats?.length ? (
                    <Field label="输出格式">
                      <Select
                        value={draft.outputFormat}
                        onChange={(event) => updateDraft('outputFormat', event.target.value)}
                        {...invalid('outputFormat')}
                      >
                        {draft.preserveDefaults ? <option value="">沿用服务商默认</option> : null}
                        {capability.outputFormats.map((format) => (
                          <option key={format} value={format}>
                            {format.toUpperCase()}
                          </option>
                        ))}
                      </Select>
                      {fieldError('outputFormat')}
                    </Field>
                  ) : null}
                  {capability.mediaKind === 'video' ? (
                    <Field label="时长 · 秒">
                      <Input
                        type="number"
                        min={capability.minDurationSeconds}
                        max={capability.maxDurationSeconds}
                        step={1}
                        required
                        value={draft.durationSeconds}
                        onChange={(event) => updateDraft('durationSeconds', event.target.value)}
                        {...invalid('durationSeconds')}
                      />
                      {fieldError('durationSeconds')}
                    </Field>
                  ) : null}
                  {capability.supportsAudioGeneration ? (
                    <Field label="视频声音">
                      <Select
                        value={draft.generateAudio ? 'on' : 'off'}
                        onChange={(event) =>
                          updateDraft('generateAudio', event.target.value === 'on')
                        }
                      >
                        <option value="on">生成同步声音</option>
                        <option value="off">无声视频</option>
                      </Select>
                    </Field>
                  ) : null}
                  <Field label="生成数量">
                    <Input
                      type="number"
                      min={1}
                      max={Math.max(1, capability.maxCount)}
                      step={1}
                      required
                      value={draft.count}
                      onChange={(event) => updateDraft('count', event.target.value)}
                      {...invalid('count')}
                    />
                    {fieldError('count')}
                  </Field>
                </div>
                {showReferences ? (
                  <fieldset
                    className="generation-reference-fieldset"
                    aria-invalid={Boolean(errors.references)}
                    aria-describedby={errors.references ? `${fieldPrefix}-references` : undefined}
                  >
                    <legend>{draft.mode === 'video.image' ? '首尾帧' : '参考图片'}</legend>
                    {draft.references.length ? (
                      <Button onClick={() => updateDraft('references', [])}>清除当前参考图</Button>
                    ) : null}
                    {freshCapture && references.some((a) => a.id === freshCapture) ? (
                      <Button
                        onClick={() => {
                          if (draft.mode === 'video.image')
                            chooseReference('first_frame', freshCapture);
                          else if (
                            !draft.references.some((ref) => ref.assetId === freshCapture) &&
                            draft.references.length < capability.maxReferences
                          )
                            updateDraft('references', [
                              ...draft.references,
                              { assetId: freshCapture, role: 'reference_image' },
                            ]);
                        }}
                      >
                        选用刚保存的画面
                      </Button>
                    ) : null}
                    {draft.mode === 'video.image' && capability.requiresFirstFrame ? (
                      <p className="field-hint">必须选择一张首帧；尾帧可选，画幅跟随首帧。</p>
                    ) : null}
                    {!references.length ? (
                      <p className="field-hint">请先导入当前工程可用的 PNG/JPEG 图片。</p>
                    ) : draft.mode === 'video.image' ? (
                      <>
                        {[
                          ['first_frame', '首帧'],
                          ['last_frame', '尾帧'],
                        ].map(([role, label]) => (
                          <Field key={role} label={label}>
                            <Select
                              value={
                                draft.references.find((reference) => reference.role === role)
                                  ?.assetId || ''
                              }
                              onChange={(event) => chooseReference(role, event.target.value)}
                            >
                              <option value="">不指定</option>
                              {references.map((asset) => (
                                <option key={asset.id} value={asset.id}>
                                  {asset.name}
                                </option>
                              ))}
                            </Select>
                          </Field>
                        ))}
                      </>
                    ) : (
                      <>
                        <p className="field-hint">
                          最多 {capability.maxReferences} 张，仅使用当前工程的 PNG/JPEG 图片。
                        </p>
                        {references.slice(0, referenceLimit).map((asset) => (
                          <label className="checkbox-field reference-option" key={asset.id}>
                            <input
                              type="checkbox"
                              checked={draft.references.some(
                                (reference) => reference.assetId === asset.id,
                              )}
                              disabled={
                                !draft.references.some(
                                  (reference) => reference.assetId === asset.id,
                                ) && draft.references.length >= capability.maxReferences
                              }
                              onChange={(event) =>
                                updateDraft(
                                  'references',
                                  event.target.checked
                                    ? [
                                        ...draft.references,
                                        { assetId: asset.id, role: 'reference_image' },
                                      ]
                                    : draft.references.filter(
                                        (reference) => reference.assetId !== asset.id,
                                      ),
                                )
                              }
                            />
                            <span>{asset.name}</span>
                          </label>
                        ))}
                        {references.length > referenceLimit ? (
                          <Button
                            variant="ghost"
                            onClick={() => setReferenceLimit((value) => value + 20)}
                          >
                            显示更多参考图片
                          </Button>
                        ) : null}
                      </>
                    )}
                    {fieldError('references')}
                  </fieldset>
                ) : null}
                <p className="generation-billing-note">
                  生成可能按服务商规则计费。提交前可核对模型、数量、参考素材并选填本次费用参考。
                </p>
                {blockingJob ? (
                  <p className="generation-warning">
                    相同请求已有未确认或可恢复任务。请到生成任务核对，当前不会重交。
                  </p>
                ) : null}
                <Button
                  type="submit"
                  variant="primary"
                  disabled={!enabled || submitting || Boolean(pendingJobID)}
                >
                  <WandSparkles size={14} />
                  生成前核对…
                </Button>
              </>
            ) : null}
            {catalog.some((value) => value.providerId === 'seedance' && !canGenerate(value)) &&
            profile?.providerId !== 'seedance' ? (
              <p className="field-hint generation-unavailable">
                Seedance 2.5 的渠道与能力尚未验证，暂未开放提交。
              </p>
            ) : null}
          </form>
        </div>
      )}
      <ConnectionsDialog
        open={showConnections}
        onOpenChange={setShowConnections}
        connections={connections}
        catalog={catalog}
        onChanged={connectionChanged}
        api={api}
      />
      {showArchive ? (
        <Suspense
          fallback={
            <p className="field-hint" role="status">
              读取归档管理…
            </p>
          }
        >
          <GenerationArchiveDialog
            api={api}
            onClose={() => setShowArchive(false)}
            onReturnFocus={() => {
              if (archiveTrigger.current?.isConnected) archiveTrigger.current.focus();
            }}
            onRestored={(job) => {
              invalidateGenerationReads();
              setJobs((current) =>
                [job, ...current.filter((value) => value.id !== job.id)].sort(
                  (a, b) => b.createdAt.localeCompare(a.createdAt) || b.id.localeCompare(a.id),
                ),
              );
              setNotice('归档记录已还原，可继续使用原结果。');
            }}
          />
        </Suspense>
      ) : null}
      {replacing ? (
        <Suspense
          fallback={
            <Modal
              open
              title="用候选替换当前镜头"
              description="正在加载替换预览面板。"
              onOpenChange={(value) => {
                if (!value) setReplacing(null);
              }}
            >
              <p role="status">正在加载…</p>
              <Button onClick={() => setReplacing(null)}>取消替换</Button>
            </Modal>
          }
        >
          <GenerationReplacementDialog
            key={`${replacing.job.id}:${replacing.candidate.id}:${replacing.clipId}`}
            project={project}
            job={replacing.job}
            candidate={replacing.candidate}
            clipId={replacing.clipId}
            onClose={() => setReplacing(null)}
            onBootstrap={onBootstrap}
            prepareProject={prepareProject}
            applyToProject={applyReplacement}
            api={replacementAPI}
          />
        </Suspense>
      ) : null}
      {placing && applyPlacement && currentFrame ? (
        <Suspense
          fallback={
            <p className="field-hint" role="status">
              读取候选上轨工具…
            </p>
          }
        >
          <GenerationPlacementDialog
            key={`${scopeKey}:${placing.job.id}:${placing.candidate.id}`}
            project={project}
            sourceEpoch={sourceEpoch ?? 0}
            job={placing.job}
            candidate={placing.candidate}
            currentFrame={currentFrame}
            timeRange={timeRange}
            pausePlayback={pausePlayback}
            prepareProject={prepareProject}
            applyToProject={applyPlacement}
            onClose={() => setPlacing(null)}
            onReturnFocus={() => {
              if (placementTrigger.current?.isConnected) placementTrigger.current.focus();
            }}
            api={placementAPI}
            onApplied={(projectId, clip) => {
              placementTrigger.current = null;
              onPlaced?.(projectId, clip);
            }}
          />
        </Suspense>
      ) : null}
      {reuse ? (
        <Suspense
          fallback={
            <p className="field-hint" role="status">
              读取本机参数工具…
            </p>
          }
        >
          <GenerationReuseDialog
            key={`${reuse.input.jobId}:${reuse.input.candidateId || ''}:${scopeKey}`}
            input={reuse.input}
            project={project}
            sourceEpoch={sourceEpoch ?? 0}
            connections={connections}
            catalog={catalog}
            prepareProject={prepareProject}
            api={reuseAPI}
            importError={jobErrors[reuse.job.id]}
            importCandidate={() => importResults(reuse.job, [reuse.input.candidateId!])}
            onClose={() => setReuse(null)}
            onReturnFocus={() => {
              if (reuseTrigger.current?.isConnected) reuseTrigger.current.focus();
            }}
            onLoaded={(id, plan: GenerationReusePlan) => {
              reuseTrigger.current = null;
              focusPrompt.current = true;
              const next = draftFromGenerationRequest(plan.request, plan.referenceFingerprints);
              if (id === connectionID) {
                prefill.current = null;
                setDraft(next);
              } else {
                prefill.current = { scope: scopeKey, connectionID: id, draft: next };
                setConnectionID(id);
              }
              setReview(null);
              setErrors({});
              setReferenceLimit(20);
              setView('create');
              setReuse(null);
              setNotice('参数已加载到创作草稿，尚未上传或生成。');
            }}
          />
        </Suspense>
      ) : null}
      <Modal
        open={Boolean(review)}
        title="核对并确认生成"
        description="确认后会向所选服务商提交这一个请求，可能产生费用。保存结果与加入时间线是独立操作。"
        busy={submitting}
        onOpenChange={(value) => {
          if (!value && !submitting) setReview(null);
        }}
      >
        {review ? (
          <>
            <dl className="generation-review">
              <div>
                <dt>连接</dt>
                <dd>{review.connectionName}</dd>
              </div>
              <div>
                <dt>服务商 / 模型</dt>
                <dd>
                  {providerNames[review.request.providerId] || review.request.providerId} /{' '}
                  {review.request.modelId}
                </dd>
              </div>
              <div>
                <dt>任务 / 数量</dt>
                <dd>
                  {modeNames[review.request.mode]} / {review.request.count || 1}
                </dd>
              </div>
              <div>
                <dt>参数</dt>
                <dd>
                  {[
                    review.request.size,
                    review.request.aspectRatio,
                    review.request.quality,
                    review.request.outputFormat,
                    review.request.background ? `背景 ${review.request.background}` : '',
                    review.request.durationSeconds ? `${review.request.durationSeconds} 秒` : '',
                    review.request.generateAudio === undefined
                      ? ''
                      : review.request.generateAudio
                        ? '生成同步声音'
                        : '无声视频',
                  ]
                    .filter(Boolean)
                    .join(' · ') || '服务商默认参数'}
                </dd>
              </div>
              <div>
                <dt>来源配方</dt>
                <dd>{review.request.parentRecipeId || '本次新建'}</dd>
              </div>
              <div>
                <dt>参考素材</dt>
                <dd>
                  {review.request.references?.length
                    ? review.request.references
                        .map(
                          (reference) =>
                            `${referenceRoleName(reference.role)}：${project.assets.find((asset) => asset.id === reference.assetId)?.name || '已登记图片'}`,
                        )
                        .join('；')
                    : '无'}
                </dd>
              </div>
              <div>
                <dt>描述</dt>
                <dd>{review.request.prompt}</dd>
              </div>
            </dl>
            <GenerationEstimateFields
              request={review.request}
              value={review.estimate}
              error={review.estimateError}
              disabled={submitting}
              locked={review.estimateLocked}
              onChange={(estimate) => setReview({ ...review, estimate, estimateError: undefined })}
            />
            <p className="generation-warning">实际费用、处理时间及远端能否取消以服务商规则为准。</p>
            {failedSubmission.current.has(review.signature) ? (
              <p className="generation-warning">
                上次提交结果未明确，本次沿用原请求编号由本地服务去重核对，不自动创建第二个请求。
              </p>
            ) : null}
            <div className="dialog-actions">
              <Button disabled={submitting} onClick={() => setReview(null)}>
                <X size={13} />
                返回修改
              </Button>
              <Button variant="primary" busy={submitting} onClick={() => void confirmGeneration()}>
                确认并生成（可能计费）
              </Button>
            </div>
          </>
        ) : null}
      </Modal>
    </div>
  );
}
