import { useEffect, useRef, useState } from 'react';
import type { Bootstrap, Project } from '../types';
import type { ConnectionProfile, GenerationCapability } from '../generation-types';
import type {
  GenerationReuseAPI,
  GenerationReuseInput,
  GenerationReusePlan,
} from '../generation-reuse-types';
import { generationReuseAPI } from '../lib/bridge';
import {
  canGenerate,
  draftFromGenerationRequest,
  findCapability,
  modeNames,
  providerNames,
  referenceRoleName,
  validateGenerationDraft,
} from '../lib/generation';
import { Button, Field, Modal, Select } from './ui';

export function GenerationReuseDialog({
  input,
  project,
  sourceEpoch,
  connections,
  catalog,
  importError,
  prepareProject,
  importCandidate,
  onLoaded,
  onClose,
  onReturnFocus,
  api = generationReuseAPI,
}: {
  input: GenerationReuseInput;
  project: Project;
  sourceEpoch: number;
  connections: ConnectionProfile[];
  catalog: GenerationCapability[];
  importError?: string;
  prepareProject?: () => Promise<Project | null>;
  importCandidate: () => Promise<Bootstrap | null>;
  onLoaded: (connectionID: string, plan: GenerationReusePlan) => void;
  onClose: () => void;
  onReturnFocus?: () => void;
  api?: GenerationReuseAPI;
}) {
  const [plan, setPlan] = useState<GenerationReusePlan | null>(null),
    [connectionID, setConnectionID] = useState(''),
    [busy, setBusy] = useState(false),
    [error, setError] = useState(''),
    [read, setRead] = useState(0);
  const pending = useRef(false),
    alive = useRef(false),
    scope = `${project.id}:${sourceEpoch}`,
    latest = useRef(scope);
  latest.current = scope;
  useEffect(() => {
    alive.current = true;
    return () => {
      alive.current = false;
    };
  }, []);
  useEffect(() => {
    let active = true;
    setPlan(null);
    setError('');
    void api
      .prepare(input)
      .then((value) => {
        if (active && alive.current && latest.current === scope) {
          setPlan(value);
          setConnectionID(
            connections.some((c) => c.id === value.connectionId) ? value.connectionId : '',
          );
        }
      })
      .catch((cause) => {
        if (active) setError(cause instanceof Error ? cause.message : '准备失败，请重试');
      });
    return () => {
      active = false;
    };
  }, [api, input, read]);
  const matches = plan
    ? connections.filter(
        (c) => c.providerId === plan.request.providerId && c.modelId === plan.request.modelId,
      )
    : [];
  const profile = matches.find((c) => c.id === connectionID),
    capability = findCapability(profile, catalog);
  const validation = plan
    ? validateGenerationDraft(
        profile,
        capability,
        draftFromGenerationRequest(plan.request, plan.referenceFingerprints),
        project.assets,
      )
    : {};
  const parameterErrors = Object.entries(validation)
    .filter(([key]) => key !== 'references')
    .map(([, value]) => value);
  if (plan?.request.generateAudio !== undefined && !capability?.supportsAudioGeneration)
    parameterErrors.push('当前能力不能保留原请求的声音设置。');
  async function load() {
    if (!plan || !profile || parameterErrors.length || pending.current) return;
    pending.current = true;
    setBusy(true);
    setError('');
    try {
      const ready = prepareProject ? await prepareProject() : project;
      if (!ready || ready.id !== input.projectId || latest.current !== scope)
        throw Error('工程已切换或草稿未处理，请重新准备');
      const current = await api.prepare(input);
      if (
        JSON.stringify(current.request) !== JSON.stringify(plan.request) ||
        JSON.stringify(current.referenceFingerprints) !==
          JSON.stringify(plan.referenceFingerprints) ||
        JSON.stringify(current.warnings) !== JSON.stringify(plan.warnings) ||
        current.needsImport !== plan.needsImport ||
        current.candidate?.fingerprint !== plan.candidate?.fingerprint
      ) {
        setPlan(current);
        throw Error('来源已变化，请重新核对后加载');
      }
      if (current.needsImport) {
        const imported = await importCandidate();
        if (
          !imported ||
          !current.candidate ||
          !imported.project.assets.some(
            (a) =>
              a.id === current.candidate!.id && a.fingerprint === current.candidate!.fingerprint,
          )
        )
          throw Error('加载尚未完成，请核对素材库和任务错误后重试');
      }
      if (!alive.current || latest.current !== scope) return;
      onLoaded(profile.id, current);
    } catch (cause) {
      if (alive.current && latest.current === scope)
        setError(cause instanceof Error ? cause.message : '加载未完成，请重试');
    } finally {
      pending.current = false;
      if (alive.current) setBusy(false);
    }
  }
  return (
    <Modal
      open
      title={input.candidateId ? '以此图片继续修改' : '沿用生成参数'}
      description="只准备本机创作草稿。加载会替换当前生成表单；之后仍需单独核对并确认生成。"
      className="replacement-dialog generation-reuse-dialog"
      busy={busy}
      onCloseAutoFocus={(event) => {
        event.preventDefault();
        onReturnFocus?.();
      }}
      onOpenChange={(open) => {
        if (!open && !busy) onClose();
      }}
    >
      <div className="replacement-dialog-body">
        {!plan ? (
          <>
            <p className="field-hint">{error ? '未读取到可复用参数' : '正在核对已保存配方…'}</p>
            <Button disabled={busy} onClick={() => setRead((v) => v + 1)}>
              重新读取
            </Button>
          </>
        ) : (
          <>
            <p className="generation-meta">
              {providerNames[plan.request.providerId] || plan.request.providerId} ·{' '}
              {plan.request.modelId}
            </p>
            <Field label="复用所用连接">
              <Select
                value={connectionID}
                disabled={busy}
                onChange={(e) => setConnectionID(e.target.value)}
              >
                <option value="">请选择同一型号的连接</option>
                {matches.map((c) => (
                  <option key={c.id} value={c.id}>
                    {c.name} · {c.configured ? '已配置，未验证' : '未配置凭据'}
                  </option>
                ))}
              </Select>
            </Field>
            {!matches.length ? (
              <p className="form-error">没有相同服务商和型号的连接，请返回配置连接后重试。</p>
            ) : null}
            <dl className="generation-review">
              <div>
                <dt>任务 / 数量</dt>
                <dd>
                  {modeNames[plan.request.mode] || plan.request.mode} / {plan.request.count || 1}
                </dd>
              </div>
              <div>
                <dt>参数</dt>
                <dd>
                  {[
                    plan.request.size,
                    plan.request.aspectRatio,
                    plan.request.quality,
                    plan.request.outputFormat,
                    plan.request.durationSeconds ? `${plan.request.durationSeconds}秒` : '',
                    plan.request.generateAudio === undefined
                      ? ''
                      : plan.request.generateAudio
                        ? '有声'
                        : '无声',
                    plan.request.background ? `背景 ${plan.request.background}` : '',
                  ]
                    .filter(Boolean)
                    .join(' · ') || '默认参数'}
                </dd>
              </div>
              <div>
                <dt>参考</dt>
                <dd>
                  {plan.request.references?.length
                    ? plan.request.references
                        .map(
                          (r) =>
                            `${referenceRoleName(r.role)}：${plan.candidate?.id === r.assetId ? plan.candidate.name : project.assets.find((a) => a.id === r.assetId)?.name || '已缺失图片'}`,
                        )
                        .join('；')
                    : '无'}
                </dd>
              </div>
              <div>
                <dt>描述</dt>
                <dd>{plan.request.prompt}</dd>
              </div>
            </dl>
            {plan.needsImport ? (
              <p className="generation-warning">
                加载时将这张图片加入素材库，可一次撤销；不自动放入时间线。继续修改初始生成数量为1。
              </p>
            ) : null}
            {plan.warnings.map((warning, index) => (
              <p className="generation-warning" key={index}>
                {warning}
              </p>
            ))}
            {plan.candidate && !plan.needsImport ? (
              <p className="field-hint">图片已在素材库，直接使用现有素材。继续修改初始数量为1。</p>
            ) : null}
            {parameterErrors.map((message, index) => (
              <p className="form-error" key={index}>
                {message}
              </p>
            ))}
            {!canGenerate(capability) && !parameterErrors.length ? (
              <p className="form-error">当前连接没有可用的模型能力。</p>
            ) : null}
          </>
        )}
      </div>
      {importError || error ? (
        <p className="form-error replacement-dialog-status" role="alert">
          {importError || error}
        </p>
      ) : null}
      <div className="dialog-actions replacement-dialog-actions">
        <Button disabled={busy} onClick={onClose}>
          保留当前草稿
        </Button>
        <Button
          variant="primary"
          busy={busy}
          disabled={
            !plan || !!parameterErrors.length || !profile?.configured || !canGenerate(capability)
          }
          onClick={() => void load()}
        >
          {plan?.needsImport ? '加载并加入图片' : '加载到创作草稿'}
        </Button>
      </div>
    </Modal>
  );
}
