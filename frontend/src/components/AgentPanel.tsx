import { useCallback, useEffect, useId, useRef, useState } from 'react';
import { Check, ClipboardCopy, RefreshCw, Square, Terminal, WandSparkles } from 'lucide-react';
import type {
  AgentAPI,
  AgentInstallation,
  AgentJob,
  AgentLoginCommand,
  AgentPlanInput,
  AgentProvider,
  AgentWorkflow,
} from '../agent-types';
import type { Bootstrap, Project } from '../types';
import { agentBridge } from '../lib/bridge';
import {
  agentApplyBlocked,
  agentJobStatus,
  agentNames,
  agentUnavailable,
  formatAgentLogin,
  summarizeAgentCommand,
} from '../lib/agent';
import { AgentEnvironmentDetails } from './AgentEnvironmentDetails';
import { agentEnvironmentChangedEvent } from './LocalAISettings';
import { Button, Field, Modal, Select } from './ui';

type AgentReview = {
  input: AgentPlanInput;
  projectId: string;
  projectRevision: number;
  workflowName: string;
  selectionNames: string[];
};

function AgentPlanCard({
  job,
  project,
  busy,
  error,
  onApply,
  onCancel,
}: {
  job: AgentJob;
  project: Project;
  busy: boolean;
  error?: string;
  onApply: (job: AgentJob) => Promise<boolean>;
  onCancel: (job: AgentJob) => Promise<boolean>;
}) {
  const blocked = agentApplyBlocked(job, project);
  const commands = job.result?.request.commands || [];
  const [commandLimit, setCommandLimit] = useState(8);
  const sameVersion = job.projectId === project.id && job.projectRevision === project.revision;
  return (
    <article
      className="agent-plan"
      aria-label={`${agentNames[job.provider]} ${agentJobStatus[job.status]}`}
    >
      <div className="job-heading">
        <strong>{agentNames[job.provider]}</strong>
        <span>{agentJobStatus[job.status]}</span>
      </div>
      <p className="field-hint">
        工程版本 {job.projectRevision} ·{' '}
        {job.createdAt
          ? new Date(job.createdAt).toLocaleString('zh-CN', { hour12: false })
          : '时间未提供'}
      </p>
      <p className="agent-job-message">{job.message}</p>
      {job.result ? (
        <>
          <p className="agent-validation">
            {job.result.coreValidated ? <Check size={13} /> : null}
            {job.result.coreValidated ? '已通过工程副本校验' : '未通过编辑核心校验'} ·{' '}
            {commands.length} 项编辑
          </p>
          {commands.length ? (
            <ol className="agent-command-list">
              {commands.slice(0, commandLimit).map((command, index) => {
                const summary = summarizeAgentCommand(command, project, sameVersion);
                return (
                  <li key={index}>
                    <strong>{summary.title}</strong>
                    {summary.details.map((detail, detailIndex) => (
                      <span key={detailIndex}>{detail}</span>
                    ))}
                  </li>
                );
              })}
            </ol>
          ) : null}
          {commands.length > commandLimit ? (
            <Button variant="ghost" onClick={() => setCommandLimit(commands.length)}>
              显示其余 {commands.length - commandLimit} 项编辑
            </Button>
          ) : null}
          {!sameVersion ? (
            <p className="field-hint">当前工程与原快照不同，摘要只显示计划目标值。</p>
          ) : null}
          {(job.result.warnings || []).map((warning, index) => (
            <p className="agent-warning" key={index}>
              {warning}
            </p>
          ))}
          <details className="agent-details">
            <summary>查看结构化编辑计划</summary>
            <pre>{JSON.stringify(job.result.request, null, 2)}</pre>
          </details>
          <Button
            variant="primary"
            disabled={busy || Boolean(blocked)}
            onClick={() => void onApply(job)}
          >
            <Check size={13} />
            {job.applied ? '计划已应用' : '应用计划到时间线'}
          </Button>
          {blocked ? (
            <p className="field-hint">{blocked}</p>
          ) : (
            <p className="field-hint">核对后应用到当前工程，可使用撤销恢复。</p>
          )}
        </>
      ) : null}
      {['queued', 'running', 'cancelling'].includes(job.status) ? (
        <Button
          variant="ghost"
          disabled={busy || job.status === 'cancelling'}
          onClick={() => void onCancel(job)}
        >
          <Square size={12} />
          {job.status === 'cancelling' ? '正在取消本地规划' : '取消本地规划'}
        </Button>
      ) : null}
      {error ? (
        <p className="form-error" role="alert">
          {error}
        </p>
      ) : null}
    </article>
  );
}

export function AgentPanel({
  project,
  selection,
  onBootstrap,
  applyToProject,
  prepareProject,
  onOpenGeneration,
  onOpenSettings,
  api = agentBridge,
}: {
  project: Project;
  selection: string[];
  onBootstrap: (value: Bootstrap) => void;
  applyToProject?: (action: () => Promise<Bootstrap>) => Promise<Bootstrap | null>;
  prepareProject?: () => Promise<Project | null>;
  onOpenGeneration: () => void;
  onOpenSettings?: () => void;
  api?: AgentAPI;
}) {
  const [installations, setInstallations] = useState<AgentInstallation[]>([]);
  const [workflows, setWorkflows] = useState<AgentWorkflow[]>([]);
  const [jobs, setJobs] = useState<AgentJob[]>([]);
  const [provider, setProvider] = useState<AgentProvider>('codex');
  const [workflow, setWorkflow] = useState('manual-plan');
  const [scope, setScope] = useState<'project' | 'selection'>(
    selection.length ? 'selection' : 'project',
  );
  const [instruction, setInstruction] = useState('');
  const [instructionError, setInstructionError] = useState('');
  const [view, setView] = useState<'plan' | 'jobs'>('plan');
  const [recordLimit, setRecordLimit] = useState(12);
  const [loaded, setLoaded] = useState(false);
  const [statusFresh, setStatusFresh] = useState(false);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const [review, setReview] = useState<AgentReview | null>(null);
  const [login, setLogin] = useState<{
    provider: AgentProvider;
    command: AgentLoginCommand;
  } | null>(null);
  const [shell, setShell] = useState<'posix' | 'powershell'>('posix');
  const [busy, setBusy] = useState('');
  const pending = useRef(false);
  const [jobErrors, setJobErrors] = useState<Record<string, string>>({});
  const alive = useRef(false);
  const reads = useRef(0);
  const workflowReads = useRef(0);
  const metadataReads = useRef(0);
  const uncertainReview = useRef<AgentReview | null>(null);
  const projectRef = useRef(project);
  projectRef.current = project;
  const instructionRef = useRef<HTMLTextAreaElement>(null);
  const loginRef = useRef<HTMLTextAreaElement>(null);
  const instructionErrorID = useId();
  const installation = installations.find((value) => value.provider === provider);
  const currentWorkflow = workflows.find((value) => value.id === workflow);
  const currentSelection = selection.filter((id) => project.clips.some((clip) => clip.id === id));
  const activeJob = jobs.some(
    (job) => ['queued', 'running', 'cancelling'].includes(job.status) && job.provider === provider,
  );
  const unavailable = agentUnavailable(installation);
  const formReady =
    loaded &&
    statusFresh &&
    !loading &&
    !busy &&
    !unavailable &&
    currentWorkflow?.available &&
    !activeJob &&
    (scope !== 'selection' || currentSelection.length > 0);
  const loginText = login ? formatAgentLogin(login.command, shell) : '';

  const refreshJobs = useCallback(async () => {
    const token = ++reads.current;
    const values = (await api.listAgentPlans()) || [];
    if (alive.current && token === reads.current) setJobs(values);
    return values;
  }, [api]);
  const refreshWorkflows = useCallback(async () => {
    const token = ++workflowReads.current;
    const values = (await api.getAgentWorkflows()) || [];
    if (alive.current && token === workflowReads.current) {
      setWorkflows(values);
      setWorkflow((current) =>
        values.some((value) => value.id === current)
          ? current
          : values.find((value) => value.available)?.id || '',
      );
    }
  }, [api]);
  const detect = useCallback(async () => {
    const token = ++metadataReads.current;
    setLoading(true);
    setError('');
    try {
      const [values] = await Promise.all([api.getAgentStatus(), refreshWorkflows(), refreshJobs()]);
      if (!alive.current || token !== metadataReads.current) return;
      setInstallations(values || []);
      setProvider((current) =>
        values?.some((value) => value.provider === current)
          ? current
          : values?.[0]?.provider || 'codex',
      );
      setLoaded(true);
      setStatusFresh(true);
    } catch (cause) {
      if (alive.current && token === metadataReads.current) {
        setStatusFresh(false);
        setError(cause instanceof Error ? cause.message : 'CLI 检测未完成，请重新检测。');
      }
    } finally {
      if (alive.current && token === metadataReads.current) setLoading(false);
    }
  }, [api, refreshWorkflows, refreshJobs]);
  useEffect(() => {
    alive.current = true;
    void detect();
    return () => {
      alive.current = false;
      reads.current++;
      workflowReads.current++;
      metadataReads.current++;
    };
  }, [detect]);
  useEffect(() => {
    if (!loaded) return;
    const interval = window.setInterval(() => {
      void refreshJobs().catch((cause) => {
        if (alive.current)
          setError(cause instanceof Error ? cause.message : '无法读取规划记录，请刷新。');
      });
    }, 2000);
    return () => clearInterval(interval);
  }, [loaded, refreshJobs]);
  useEffect(() => {
    if (!loaded) return;
    void Promise.all([refreshWorkflows(), refreshJobs()]).catch((cause) => {
      if (alive.current)
        setError(cause instanceof Error ? cause.message : '无法核对当前工程工作流。');
    });
  }, [loaded, project.id, project.revision, refreshJobs, refreshWorkflows]);
  useEffect(() => {
    setReview(null);
    setInstruction('');
    setInstructionError('');
  }, [project.id]);

  async function openReview(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!instruction.trim() || new TextEncoder().encode(instruction).length > 32768) {
      setInstructionError('请填写不超过 32 KiB 的剪辑要求。');
      instructionRef.current?.focus();
      return;
    }
    if (!formReady || pending.current || !currentWorkflow) return;
    pending.current = true;
    setBusy('核对工程');
    setError('');
    try {
      const current = prepareProject ? await prepareProject() : projectRef.current;
      if (!current) return;
      if (current.id !== project.id || current.revision !== project.revision) {
        setError('工程已变化，请核对当前工程后重新生成计划。');
        return;
      }
      if (!alive.current) return;
      const input: AgentPlanInput = {
        id: crypto.randomUUID(),
        provider,
        instruction: instruction.trim(),
        workflow,
        selection: scope === 'selection' ? currentSelection : [],
        consent: false,
      };
      const previous = uncertainReview.current;
      if (
        previous &&
        previous.projectId === current.id &&
        previous.projectRevision === current.revision &&
        previous.input.provider === input.provider &&
        previous.input.instruction === input.instruction &&
        previous.input.workflow === input.workflow &&
        JSON.stringify(previous.input.selection) === JSON.stringify(input.selection)
      ) {
        if (jobs.some((job) => job.id === previous.input.id)) {
          uncertainReview.current = null;
          setView('jobs');
          setNotice('已找到上次提交的规划记录，请先核对结果；没有重新调用 CLI。');
          return;
        }
        input.id = previous.input.id;
      }
      setReview({
        projectId: current.id,
        projectRevision: current.revision,
        workflowName: currentWorkflow.name,
        selectionNames:
          scope === 'selection'
            ? currentSelection.map((id) => current.clips.find((clip) => clip.id === id)?.name || id)
            : [],
        input,
      });
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : '无法核对工程状态，请重试。');
    } finally {
      pending.current = false;
      if (alive.current) setBusy('');
    }
  }
  async function startPlan() {
    if (!review || pending.current) return;
    if (
      review.projectId !== projectRef.current.id ||
      review.projectRevision !== projectRef.current.revision
    ) {
      setReview(null);
      setError('工程版本已变化，请重新核对规划要求。');
      return;
    }
    pending.current = true;
    setBusy('生成剪辑计划');
    setError('');
    try {
      // 只有该次明确确认才授权本地 CLI 规划；要求始终作为数据传入。
      const job = await api.startAgentPlan({ ...review.input, consent: true });
      uncertainReview.current = null;
      reads.current++;
      if (alive.current) {
        setJobs((current) => [job, ...current.filter((value) => value.id !== job.id)]);
        setReview(null);
        setView('jobs');
        setNotice('规划任务已提交，生成完成后仍需单独应用。');
      }
    } catch (cause) {
      uncertainReview.current = review;
      if (alive.current) {
        setError(
          `${cause instanceof Error ? cause.message : '规划提交结果未明确。'} 请刷新规划记录核对；不会自动重交。`,
        );
        setReview(null);
      }
    } finally {
      pending.current = false;
      if (alive.current) setBusy('');
    }
  }
  async function showLogin() {
    if (pending.current || !installation?.installed) return;
    pending.current = true;
    setBusy('读取登录说明');
    setError('');
    try {
      const command = await api.getAgentLoginInstructions(provider);
      if (alive.current) {
        setLogin({ provider, command });
        setNotice('');
      }
    } catch (cause) {
      if (alive.current) setError(cause instanceof Error ? cause.message : '无法读取登录说明。');
    } finally {
      pending.current = false;
      if (alive.current) setBusy('');
    }
  }
  async function copyLogin() {
    try {
      if (!navigator.clipboard?.writeText) throw new Error('clipboard_unavailable');
      await navigator.clipboard.writeText(loginText);
      setNotice('登录命令已复制；请自行在正常终端检查后执行。');
    } catch {
      loginRef.current?.focus();
      loginRef.current?.select();
      setNotice('无法访问剪贴板，已选中命令，请手动复制。');
    }
  }
  async function actOnJob(job: AgentJob, action: 'cancel' | 'apply'): Promise<boolean> {
    if (pending.current) return false;
    if (action === 'apply' && agentApplyBlocked(job, projectRef.current)) return false;
    pending.current = true;
    setBusy(action === 'apply' ? '应用剪辑计划' : '取消本地规划');
    setJobErrors((current) => ({ ...current, [job.id]: '' }));
    try {
      if (action === 'cancel') {
        const value = await api.cancelAgentPlan(job.id);
        reads.current++;
        if (alive.current)
          setJobs((current) => current.map((item) => (item.id === value.id ? value : item)));
      } else {
        const apply = () => api.applyAgentPlan(job.id);
        const value = await (applyToProject ? applyToProject(apply) : apply());
        if (!value) return false;
        if (value.project.id !== projectRef.current.id)
          throw new Error('工程已切换，请在原工程核对计划结果。');
        if (!applyToProject) onBootstrap(value);
        reads.current++;
        if (alive.current) {
          setJobs((current) =>
            current.map((item) => (item.id === job.id ? { ...item, applied: true } : item)),
          );
          setNotice('剪辑计划已应用，可使用撤销恢复。');
        }
        try {
          await refreshJobs();
        } catch {
          if (alive.current)
            setJobErrors((current) => ({
              ...current,
              [job.id]: '计划已应用，但暂时无法刷新规划记录，请稍后读取。',
            }));
        }
      }
      return true;
    } catch (cause) {
      if (alive.current)
        setJobErrors((current) => ({
          ...current,
          [job.id]: cause instanceof Error ? cause.message : '规划操作未完成，请重试。',
        }));
      return false;
    } finally {
      pending.current = false;
      if (alive.current) setBusy('');
    }
  }

  useEffect(() => {
    const changed = () => {
      void detect();
    };
    window.addEventListener(agentEnvironmentChangedEvent, changed);
    return () => window.removeEventListener(agentEnvironmentChangedEvent, changed);
  }, [api]);
  return (
    <div className="agent-panel">
      <div className="agent-toolbar">
        <div className="agent-view-switch" role="group" aria-label="本地 Agent 视图">
          <Button
            variant={view === 'plan' ? 'outline' : 'ghost'}
            aria-pressed={view === 'plan'}
            onClick={() => setView('plan')}
          >
            剪辑规划
          </Button>
          <Button
            variant={view === 'jobs' ? 'outline' : 'ghost'}
            aria-pressed={view === 'jobs'}
            onClick={() => setView('jobs')}
          >
            规划记录{jobs.length ? ` ${jobs.length}` : ''}
          </Button>
        </div>
        {!onOpenSettings ? (
          <Button
            variant="ghost"
            busy={loading}
            disabled={Boolean(busy)}
            onClick={() => void detect()}
          >
            <RefreshCw size={12} />
            重新检测
          </Button>
        ) : null}
      </div>
      {error ? (
        <p className="form-error" role="alert">
          {error}
        </p>
      ) : null}
      {notice ? (
        <p className="agent-notice" role="status">
          {notice}
        </p>
      ) : null}
      {!loaded ? (
        <div className="inspector-empty">
          <Terminal size={25} />
          <p>{loading ? '正在检测本地 CLI' : '本地 Agent 暂不可用'}</p>
          <span>检测不会启动推理或自动登录。</span>
        </div>
      ) : view === 'jobs' ? (
        <>
          <div className="agent-record-heading">
            <span className="field-hint">规划结果只在显式应用后修改工程</span>
            <Button
              variant="ghost"
              onClick={() =>
                void refreshJobs().catch((cause) =>
                  setError(cause instanceof Error ? cause.message : '无法读取规划记录。'),
                )
              }
            >
              刷新记录
            </Button>
          </div>
          {jobs.length ? (
            jobs
              .slice(0, recordLimit)
              .map((job) => (
                <AgentPlanCard
                  key={job.id}
                  job={job}
                  project={project}
                  busy={Boolean(busy)}
                  error={jobErrors[job.id]}
                  onApply={(value) => actOnJob(value, 'apply')}
                  onCancel={(value) => actOnJob(value, 'cancel')}
                />
              ))
          ) : (
            <div className="inspector-empty">
              <Terminal size={25} />
              <p>还没有剪辑规划</p>
              <span>明确确认后才会启动本地 CLI。</span>
            </div>
          )}
          {jobs.length > recordLimit ? (
            <Button variant="ghost" onClick={() => setRecordLimit((value) => value + 12)}>
              显示更多规划记录
            </Button>
          ) : null}
        </>
      ) : (
        <form className="agent-form" noValidate onSubmit={(event) => void openReview(event)}>
          <Field label="本地 CLI">
            <Select
              value={provider}
              disabled={Boolean(busy) || loading}
              onChange={(event) => {
                setProvider(event.target.value as AgentProvider);
                setLogin(null);
              }}
            >
              {(['codex', 'claude'] as const).map((value) => (
                <option key={value} value={value}>
                  {agentNames[value]}
                </option>
              ))}
            </Select>
          </Field>
          {onOpenSettings ? (
            <div className="agent-setup-summary">
              <p className={unavailable ? 'agent-warning' : 'field-hint'}>
                {unavailable
                  ? `${agentNames[provider]} 暂不可用，请检查本地 AI 设置。`
                  : `${agentNames[provider]} 已就绪`}
              </p>
              <Button variant="ghost" onClick={onOpenSettings}>
                本地 AI 设置
              </Button>
            </div>
          ) : installation ? (
            <>
              <AgentEnvironmentDetails installation={installation} mode="plan" />
              <Button
                variant="ghost"
                disabled={!installation.installed || Boolean(busy)}
                onClick={() => void showLogin()}
              >
                查看登录说明
              </Button>
            </>
          ) : (
            <p className="agent-warning">尚未检测到此 CLI。请安装后重新检测，不会使用模拟规划。</p>
          )}
          {!statusFresh && loaded ? (
            <p className="agent-warning">当前检测未完成，请重新检测后再生成计划。</p>
          ) : null}
          <Field label="剪辑工作流">
            <Select
              value={workflow}
              disabled={Boolean(busy)}
              onChange={(event) => setWorkflow(event.target.value)}
            >
              {workflows.map((value) => (
                <option key={value.id} value={value.id} disabled={!value.available}>
                  {value.name}
                  {value.available ? '' : '（前置条件未满足）'}
                </option>
              ))}
            </Select>
          </Field>
          {currentWorkflow ? (
            <p className="field-hint">
              {currentWorkflow.description}
              {!currentWorkflow.available ? ` ${currentWorkflow.reason}` : ''}
            </p>
          ) : null}
          <details className="agent-details">
            <summary>工作流与前置条件</summary>
            <ul>
              {workflows.map((value) => (
                <li key={value.id}>
                  <strong>
                    {value.name} · {value.available ? '可规划' : '暂不可用'}
                  </strong>
                  <span>{value.description}</span>
                  {value.reason ? <span>{value.reason}</span> : null}
                </li>
              ))}
            </ul>
          </details>
          <Field label="规划范围">
            <Select
              value={scope}
              disabled={Boolean(busy)}
              onChange={(event) => setScope(event.target.value as 'project' | 'selection')}
            >
              <option value="project">整个当前工程</option>
              <option value="selection">仅当前选中片段</option>
            </Select>
          </Field>
          {scope === 'selection' ? (
            <p className="field-hint">
              {currentSelection.length
                ? currentSelection
                    .map((id) => project.clips.find((clip) => clip.id === id)?.name || id)
                    .join('、')
                : '请先在时间线选择片段，或改为整个当前工程。'}
            </p>
          ) : null}
          <Field label="剪辑要求">
            <textarea
              ref={instructionRef}
              className="input resize-none agent-instruction"
              rows={5}
              value={instruction}
              required
              aria-invalid={Boolean(instructionError)}
              aria-describedby={instructionError ? instructionErrorID : undefined}
              placeholder="例如：将选中片段移动到第 90 帧，保留源素材入点。"
              disabled={Boolean(busy)}
              onChange={(event) => {
                setInstruction(event.target.value);
                setInstructionError('');
              }}
            />
            {instructionError ? (
              <span id={instructionErrorID} className="form-error">
                {instructionError}
              </span>
            ) : null}
          </Field>
          <p className="agent-warning">
            生成计划会调用你的本地 CLI
            账号，可能消耗服务额度。只生成候选编辑计划，应用前不会修改时间线。
          </p>
          {activeJob ? (
            <p className="field-hint">此 CLI 已有规划任务，请到规划记录核对或取消。</p>
          ) : null}
          <Button type="submit" variant="primary" disabled={!formReady} busy={Boolean(busy)}>
            <WandSparkles size={14} />
            生成计划前核对…
          </Button>
          <Button variant="ghost" onClick={onOpenGeneration}>
            打开图片与视频生成
          </Button>
        </form>
      )}
      <Modal
        open={Boolean(login)}
        title={`${login ? agentNames[login.provider] : 'CLI'} 登录说明`}
        description="仅显示本机 CLI 的正常登录方式。应用不会执行该命令、复制认证文件或代你登录。"
        onOpenChange={(value) => {
          if (!value) setLogin(null);
        }}
      >
        {login ? (
          <>
            <p className="field-hint">{login.command.notice}</p>
            <dl className="agent-login-fields">
              <dt>可执行文件</dt>
              <dd>{login.command.executable}</dd>
              <dt>参数</dt>
              <dd>{JSON.stringify(login.command.args || [])}</dd>
              <dt>环境作用域</dt>
              <dd>
                {Object.entries(login.command.environment || {}).map(([key, value]) => (
                  <div key={key}>
                    {key}={value}
                  </div>
                ))}
              </dd>
            </dl>
            <Field label="终端格式">
              <Select
                value={shell}
                onChange={(event) => setShell(event.target.value as 'posix' | 'powershell')}
              >
                <option value="posix">macOS / Linux · zsh / bash</option>
                <option value="powershell">Windows · PowerShell</option>
              </Select>
            </Field>
            <Field label="登录命令">
              <textarea
                ref={loginRef}
                className="input resize-none agent-login-command"
                rows={4}
                value={loginText}
                readOnly
                onFocus={(event) => event.currentTarget.select()}
              />
            </Field>
            {!loginText ? (
              <p className="form-error" role="alert">
                登录参数无法安全格式化，请按上方独立字段核对。
              </p>
            ) : null}
            {shell === 'powershell' ? (
              <p className="field-hint">PowerShell 环境变量作用于当前终端，登录后可关闭该终端。</p>
            ) : null}
            <p className="field-hint">
              登录完成后关闭说明并点击“重新检测”，登录不等于隔离检查已通过。
            </p>
            <div className="dialog-actions">
              <Button onClick={() => setLogin(null)}>关闭说明</Button>
              <Button disabled={!loginText} onClick={() => void copyLogin()}>
                <ClipboardCopy size={13} />
                复制登录命令
              </Button>
            </div>
            {notice ? (
              <p className="agent-notice" role="status">
                {notice}
              </p>
            ) : null}
          </>
        ) : null}
      </Modal>
      <Modal
        open={Boolean(review)}
        title="确认生成剪辑计划"
        description="这一次操作会调用所选本地 CLI，可能消耗账号额度。它只返回待核对的计划，不会自动应用或生成媒体。"
        busy={Boolean(busy)}
        onOpenChange={(value) => {
          if (!value && !busy) setReview(null);
        }}
      >
        {review ? (
          <>
            <dl className="agent-review">
              <dt>CLI / 工作流</dt>
              <dd>
                {agentNames[review.input.provider]} / {review.workflowName}
              </dd>
              <dt>工程版本</dt>
              <dd>
                {project.name} · {review.projectRevision}
              </dd>
              <dt>范围</dt>
              <dd>
                {review.selectionNames.length ? review.selectionNames.join('、') : '整个当前工程'}
              </dd>
              <dt>要求</dt>
              <dd>{review.input.instruction}</dd>
            </dl>
            <p className="agent-warning">
              账号费用与额度以 CLI 服务商规则为准，当前没有可验证的实时报价。
            </p>
            {uncertainReview.current?.input.id === review.input.id ? (
              <p className="agent-warning">
                上次提交结果未明确，本次沿用原请求编号核对，由本地服务去重；不会自动创建第二次规划。
              </p>
            ) : null}
            <div className="dialog-actions">
              <Button disabled={Boolean(busy)} onClick={() => setReview(null)}>
                返回修改
              </Button>
              <Button variant="primary" busy={Boolean(busy)} onClick={() => void startPlan()}>
                确认生成计划（可能消耗额度）
              </Button>
            </div>
          </>
        ) : null}
      </Modal>
    </div>
  );
}
