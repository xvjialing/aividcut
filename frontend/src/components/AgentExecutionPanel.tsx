import { useCallback, useEffect, useRef, useState } from 'react';
import type { Project } from '../types';
import type { AgentInstallation, AgentLoginCommand, AgentProvider } from '../agent-types';
import type {
  AgentExecution,
  AgentExecutionAPI,
  AgentExecutionInput,
} from '../agent-execution-types';
import type { MCPStatus } from '../mcp-types';
import type { WorkflowDefinition } from '../workflow-types';
import { agentBridge, executionBridge, workflowBridge } from '../lib/bridge';
import { agentNames, formatAgentLogin } from '../lib/agent';
import { executionUnavailable } from '../lib/agent-execution';
import { AgentEnvironmentDetails } from './AgentEnvironmentDetails';
import { agentEnvironmentChangedEvent } from './LocalAISettings';
import { Button, Field, Modal, Select } from './ui';
import { SourceFrameEvidenceViewer } from './SourceFrameEvidenceViewer';

const labels: Record<AgentExecution['status'], string> = {
  queued: '排队中',
  running: '执行中',
  cancelling: '正在取消后续执行',
  succeeded: '执行已结束',
  failed: '执行失败',
  cancelled: '已取消后续执行',
  interrupted: '上次执行已中断',
};
const terminal = (job: AgentExecution) =>
  ['succeeded', 'failed', 'cancelled', 'interrupted'].includes(job.status);
const defaultWorkflowGetter = workflowBridge.getWorkflowCatalog;
const defaultLoginGetter = agentBridge.getAgentLoginInstructions;

export function AgentExecutionPanel({
  project,
  sourceEpoch = 0,
  selection,
  mcpStatus,
  prepareProject,
  onProgress,
  onOpenMCP,
  onOpenSettings,
  api = executionBridge,
  getWorkflows = defaultWorkflowGetter,
  getLoginInstructions = defaultLoginGetter,
}: {
  project: Project;
  sourceEpoch?: number;
  selection: string[];
  mcpStatus?: MCPStatus | null;
  prepareProject?: () => Promise<Project | null>;
  onProgress?: (job: AgentExecution, force: boolean) => void | Promise<unknown>;
  onOpenMCP: () => void;
  onOpenSettings?: () => void;
  api?: AgentExecutionAPI;
  getWorkflows?: () => Promise<WorkflowDefinition[]>;
  getLoginInstructions?: (provider: AgentProvider) => Promise<AgentLoginCommand>;
}) {
  const [installations, setInstallations] = useState<AgentInstallation[]>([]);
  const [workflows, setWorkflows] = useState<WorkflowDefinition[]>([]);
  const [jobs, setJobs] = useState<AgentExecution[]>([]);
  const [detail, setDetail] = useState<AgentExecution | null>(null);
  const [provider, setProvider] = useState<AgentProvider>('codex');
  const [workflow, setWorkflow] = useState('manual-plan');
  const [scope, setScope] = useState<'project' | 'selection'>(
    selection.length ? 'selection' : 'project',
  );
  const [instruction, setInstruction] = useState('');
  const [allowEdits, setAllowEdits] = useState(true);
  const [allowSourceFrames, setAllowSourceFrames] = useState(false);
  useEffect(() => setAllowSourceFrames(false), [project.id]);
  const [view, setView] = useState<'execute' | 'records'>('execute');
  const [review, setReview] = useState<AgentExecutionInput | null>(null);
  const uncertain = useRef<AgentExecutionInput | null>(null);
  const [login, setLogin] = useState<AgentLoginCommand | null>(null);
  const [loginShell, setLoginShell] = useState<'posix' | 'powershell'>('posix');
  const [cancelling, setCancelling] = useState<AgentExecution | null>(null);
  const [loaded, setLoaded] = useState(false);
  const [statusFresh, setStatusFresh] = useState(false);
  const [loading, setLoading] = useState(false);
  const [busy, setBusy] = useState('');
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const [limit, setLimit] = useState(12);
  const [fieldError, setFieldError] = useState('');
  const instructionInput = useRef<HTMLTextAreaElement>(null);
  const alive = useRef(false);
  const pending = useRef(false);
  const reads = useRef(0);
  const detailReads = useRef(0);
  const projectRef = useRef(project);
  projectRef.current = project;
  const progressRef = useRef(onProgress);
  progressRef.current = onProgress;
  const reconciled = useRef(new Map<string, string>());
  const currentSelection = selection.filter((id) => project.clips.some((clip) => clip.id === id));
  const installation = installations.find((value) => value.provider === provider);
  const unavailable = executionUnavailable(installation);
  const currentWorkflow = workflows.find((value) => value.id === workflow);
  const workflowAvailable = workflow === 'manual-plan' || currentWorkflow?.available;
  const active = jobs.some((job) => !terminal(job));
  const connectionInUse = Boolean(mcpStatus?.running);
  const formEnabled =
    loaded &&
    statusFresh &&
    !loading &&
    !busy &&
    !unavailable &&
    workflowAvailable &&
    !active &&
    !connectionInUse &&
    (scope !== 'selection' || currentSelection.length > 0);
  const notify = useCallback(async (values: AgentExecution[]) => {
    const relevant = values.filter((job) => job.projectId === projectRef.current.id);
    const latestEnded = relevant
      .filter(terminal)
      .sort((a, b) => b.updatedAt.localeCompare(a.updatedAt))[0];
    for (const job of relevant
      .filter((value) => !terminal(value))
      .concat(latestEnded ? [latestEnded] : [])) {
      const key = `${job.status}:${job.endRevision}:${job.updatedAt}`;
      if (reconciled.current.get(job.id) === key) continue;
      reconciled.current.set(job.id, key);
      try {
        await progressRef.current?.(job, terminal(job));
      } catch {
        reconciled.current.delete(job.id);
        if (alive.current)
          setError('运行记录已读取，但工程状态暂时未同步；当前输入保留，请重新核对。');
      }
    }
  }, []);
  const refreshJobs = useCallback(async () => {
    const token = ++reads.current;
    const values = (await api.listAgentExecutions()) || [];
    if (alive.current && token === reads.current) {
      setJobs(values);
      await notify(values);
    }
  }, [api, notify]);
  const detect = useCallback(async () => {
    setLoading(true);
    setError('');
    try {
      const [values, catalog] = await Promise.all([api.getAgentExecutionStatus(), getWorkflows()]);
      if (!alive.current) return;
      setInstallations(values || []);
      setStatusFresh(true);
      setWorkflows(catalog || []);
      await refreshJobs();
      if (alive.current) setLoaded(true);
    } catch (cause) {
      setStatusFresh(false);
      if (alive.current) setError(cause instanceof Error ? cause.message : '执行模式检测失败。');
    } finally {
      if (alive.current) setLoading(false);
    }
  }, [api, getWorkflows, refreshJobs]);
  useEffect(() => {
    alive.current = true;
    void detect();
    return () => {
      alive.current = false;
      reads.current++;
      detailReads.current++;
    };
  }, [detect]);
  useEffect(() => {
    if (!loaded) return;
    const interval = window.setInterval(
      () =>
        void refreshJobs().catch((cause) => {
          if (alive.current)
            setError(cause instanceof Error ? cause.message : '执行记录读取失败。');
        }),
      2000,
    );
    return () => clearInterval(interval);
  }, [loaded, refreshJobs]);
  useEffect(() => {
    if (loaded)
      void getWorkflows()
        .then((values) => {
          if (alive.current) setWorkflows(values || []);
        })
        .catch((cause) => setError(cause instanceof Error ? cause.message : '工作流读取失败。'));
  }, [project.id, project.revision, getWorkflows]);
  useEffect(() => {
    setReview(null);
    setInstruction('');
    setDetail(null);
    setFieldError('');
  }, [project.id]);
  async function openReview(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!instruction.trim() || new TextEncoder().encode(instruction).length > 32768) {
      setFieldError('请填写不超过 32 KiB 的明确执行要求。');
      instructionInput.current?.focus();
      return;
    }
    if (!formEnabled || pending.current) return;
    pending.current = true;
    setBusy('核对执行上下文');
    setError('');
    try {
      const current = prepareProject ? await prepareProject() : projectRef.current;
      if (!current) return;
      if (current.id !== project.id || current.revision !== project.revision) {
        setError('工程已变化，请重新核对本次执行。');
        return;
      }
      const input: AgentExecutionInput = {
        id: crypto.randomUUID(),
        provider,
        projectId: current.id,
        projectRevision: current.revision,
        instruction: instruction.trim(),
        workflow,
        selection: scope === 'selection' ? currentSelection : [],
        allowEdits,
        ...(allowSourceFrames ? { allowSourceFrames: true } : {}),
        consent: false,
      };
      const same = (value: AgentExecutionInput) => {
        const { id: _id, ...payload } = value;
        return JSON.stringify(payload);
      };
      if (uncertain.current && same(uncertain.current) === same(input)) {
        if (jobs.some((job) => job.id === uncertain.current!.id)) {
          setView('records');
          setNotice('已找到上次运行记录，请先核对；没有重新推理。');
          uncertain.current = null;
          return;
        }
        input.id = uncertain.current.id;
      }
      setReview(input);
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : '无法核对工程。');
    } finally {
      pending.current = false;
      if (alive.current) setBusy('');
    }
  }
  async function start() {
    if (!review || pending.current) return;
    if (
      review.projectId !== projectRef.current.id ||
      review.projectRevision !== projectRef.current.revision
    ) {
      setReview(null);
      setError('工程或版本已变化，请重新核对。');
      return;
    }
    pending.current = true;
    setBusy('登记本地执行');
    setError('');
    try {
      const value = await api.startAgentExecution({ ...review, consent: true });
      uncertain.current = null;
      reads.current++;
      if (alive.current) {
        setJobs((current) => [value, ...current.filter((job) => job.id !== value.id)]);
        setReview(null);
        setView('records');
        setNotice('执行意图已登记；工具提交会直接进入工程并逐批保留撤销。');
        await notify([value]);
      }
    } catch (cause) {
      uncertain.current = review;
      if (alive.current) {
        setReview(null);
        setError(
          `${cause instanceof Error ? cause.message : '登记结果未明确。'} 请核对运行和CLI使用记录；不会自动重交。`,
        );
      }
    } finally {
      pending.current = false;
      if (alive.current) setBusy('');
    }
  }
  async function inspect(job: AgentExecution) {
    const token = ++detailReads.current;
    try {
      const value = await api.getAgentExecution(job.id);
      if (alive.current && token === detailReads.current) {
        setDetail(value);
        await notify([value]);
      }
    } catch (cause) {
      if (alive.current) setError(cause instanceof Error ? cause.message : '运行详情读取失败。');
    }
  }
  async function cancel() {
    if (!cancelling || pending.current) return;
    pending.current = true;
    setBusy('取消后续执行');
    setError('');
    try {
      const value = await api.cancelAgentExecution(cancelling.id);
      reads.current++;
      setJobs((current) => current.map((job) => (job.id === value.id ? value : job)));
      setCancelling(null);
      setNotice('已请求取消后续执行，已完成的编辑保留，请核对工程与审计。');
      await notify([value]);
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : '取消请求未完成。');
    } finally {
      pending.current = false;
      if (alive.current) setBusy('');
    }
  }
  async function showLogin() {
    if (pending.current) return;
    pending.current = true;
    setBusy('读取登录说明');
    setError('');
    try {
      const value = await getLoginInstructions(provider);
      if (alive.current) setLogin(value);
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : '无法读取登录说明。');
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
    <div className="agent-panel execution-panel">
      <div className="agent-toolbar">
        <div className="agent-view-switch" role="group" aria-label="执行模式视图">
          <Button
            aria-pressed={view === 'execute'}
            variant={view === 'execute' ? 'outline' : 'ghost'}
            onClick={() => setView('execute')}
          >
            通过MCP执行
          </Button>
          <Button
            aria-pressed={view === 'records'}
            variant={view === 'records' ? 'outline' : 'ghost'}
            onClick={() => setView('records')}
          >
            运行记录{jobs.length ? ` ${jobs.length}` : ''}
          </Button>
        </div>
        {!onOpenSettings ? (
          <Button
            variant="ghost"
            disabled={Boolean(busy)}
            busy={loading}
            onClick={() => void detect()}
          >
            重查执行能力
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
          <p>正在核对执行模式能力</p>
          <span>执行隔离与原计划模式分别检查，不自动启动CLI。</span>
        </div>
      ) : view === 'records' ? (
        <>
          <Button
            variant="ghost"
            onClick={() =>
              void refreshJobs().catch((cause) =>
                setError(cause instanceof Error ? cause.message : '运行记录读取失败。'),
              )
            }
          >
            刷新运行记录
          </Button>
          {jobs.slice(0, limit).map((job) => (
            <article className="agent-plan" key={job.id}>
              <div className="job-heading">
                <strong>{agentNames[job.provider]}</strong>
                <span>{labels[job.status]}</span>
              </div>
              <p>{job.message}</p>
              <p className="field-hint">
                工程 {job.projectId === project.id ? project.name : job.projectId} · 工作流{' '}
                {job.workflow === 'manual-plan' ? '按明确要求执行' : job.workflow} ·{' '}
                {job.selection.length ? `${job.selection.length}个片段作上下文` : '工程概况'}
              </p>
              <p className="field-hint">
                开始版本 {job.projectRevision} → 当前/结束版本 {job.endRevision} ·{' '}
                {job.allowEdits ? '允许整个工程的可撤销编辑' : '只读工具'} ·{' '}
                {job.allowSourceFrames ? '已允许源画面' : '未提供源画面'} · 阶段 {job.stage}
              </p>
              {job.summary ? <p>{job.summary}</p> : null}
              <p className="field-hint">
                CLI总结不代替工具审计；已经完成的编辑不会因取消或失败自动回滚。
              </p>
              {(job.warnings || []).map((warning, index) => (
                <p className="agent-warning" key={index}>
                  {warning}
                </p>
              ))}
              <div className="speech-actions">
                <Button disabled={Boolean(busy)} onClick={() => void inspect(job)}>
                  读取运行审计
                </Button>
                {!terminal(job) ? (
                  <Button
                    disabled={Boolean(busy) || job.status === 'cancelling'}
                    onClick={() => setCancelling(job)}
                  >
                    取消后续执行…
                  </Button>
                ) : null}
              </div>
              {detail?.id === job.id ? (
                <details className="agent-details" open>
                  <summary>工具审计 · {detail.audit?.length || 0} 项</summary>
                  {detail.frameEvidence?.length ? (
                    <SourceFrameEvidenceViewer
                      project={project}
                      sourceEpoch={sourceEpoch}
                      runProjectID={detail.projectId}
                      frames={detail.frameEvidence}
                    />
                  ) : null}
                  {(detail.audit || []).map((entry) => (
                    <p key={entry.id}>
                      {entry.tool} · {entry.success ? '成功' : '失败或拒绝'} · 版本 {entry.revision}{' '}
                      · {entry.createdAt}
                    </p>
                  ))}
                </details>
              ) : null}
            </article>
          ))}
          {jobs.length > limit ? (
            <Button onClick={() => setLimit((value) => value + 12)}>显示更多执行记录</Button>
          ) : null}
          {!jobs.length ? (
            <p className="field-hint">没有执行记录。明确确认后才会启动本次CLI会话。</p>
          ) : null}
          <Button onClick={onOpenMCP}>核对模型与导出请求</Button>
        </>
      ) : (
        <form className="agent-form" noValidate onSubmit={(event) => void openReview(event)}>
          <Field label="执行CLI">
            <Select
              value={provider}
              disabled={Boolean(busy)}
              onChange={(event) => setProvider(event.target.value as AgentProvider)}
            >
              <option value="codex">Codex CLI</option>
              <option value="claude">Claude Code</option>
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
          ) : (
            <>
              <AgentEnvironmentDetails installation={installation} mode="execution" />
              <Button
                variant="ghost"
                disabled={!installation?.installed || Boolean(busy)}
                onClick={() => void showLogin()}
              >
                查看执行登录说明
              </Button>
            </>
          )}
          <Field label="执行工作流">
            <Select
              value={workflow}
              disabled={Boolean(busy)}
              onChange={(event) => setWorkflow(event.target.value)}
            >
              <option value="manual-plan">按明确要求执行</option>
              {workflows.map((value) => (
                <option key={value.id} value={value.id} disabled={!value.available}>
                  {value.name}
                  {value.available ? '' : '（前置数据不足）'}
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
          <Field label="任务上下文">
            <Select
              value={scope}
              disabled={Boolean(busy)}
              onChange={(event) => setScope(event.target.value as 'project' | 'selection')}
            >
              <option value="project">整个工程的概况</option>
              <option value="selection">当前选中片段作为上下文</option>
            </Select>
          </Field>
          {scope === 'selection' ? (
            <p className="field-hint">
              {currentSelection
                .map((id) => project.clips.find((clip) => clip.id === id)?.name || id)
                .join('、') || '请先选择片段；不会隐式扩大任务上下文。'}
            </p>
          ) : null}
          <Field label="执行要求">
            <textarea
              ref={instructionInput}
              className="input resize-none agent-instruction"
              rows={5}
              value={instruction}
              required
              disabled={Boolean(busy)}
              aria-invalid={Boolean(fieldError)}
              onChange={(event) => {
                setInstruction(event.target.value);
                setFieldError('');
              }}
            />
            {fieldError ? <span className="form-error">{fieldError}</span> : null}
          </Field>
          <label className="checkbox-field">
            <input
              type="checkbox"
              checked={allowSourceFrames}
              disabled={Boolean(busy)}
              onChange={(event) => setAllowSourceFrames(event.target.checked)}
            />
            允许向CLI提供当前工程的源画面
          </label>
          <p className="field-hint">
            默认不提供图片。开启后CLI可按需读取当前工程的图片/视频抽样，并可能交给其模型服务；选区只是上下文，不限制素材读取范围。
          </p>
          <label className="checkbox-field">
            <input
              type="checkbox"
              checked={allowEdits}
              disabled={Boolean(busy)}
              onChange={(event) => setAllowEdits(event.target.checked)}
            />
            允许直接编辑整个当前工程，选区仅是任务上下文
          </label>
          <p className="agent-warning">
            本次执行可能消耗CLI账号额度。允许编辑后，多次工具提交直接进入工程，各批可撤销；取消不回滚先前编辑。模型生成、语音克隆和导出仍需单独人工核对。
          </p>
          {connectionInUse ? (
            <p className="agent-warning">
              已有工具连接或执行会话占用，请先到MCP或运行记录处理；不会自动接管连接。
            </p>
          ) : null}
          {active ? <p className="field-hint">已有未结束的执行，请先核对运行记录。</p> : null}
          <div className="speech-actions">
            <Button variant="primary" type="submit" disabled={!formEnabled}>
              执行前核对…
            </Button>
            <Button onClick={onOpenMCP}>打开MCP核对</Button>
          </div>
        </form>
      )}
      <Modal
        open={Boolean(review)}
        title="确认本次CLI直接执行"
        description="这次许可包括CLI账号额度及下面的工程工具权限，不授权额外的模型收费或导出路径。"
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
                {agentNames[review.provider]} /{' '}
                {review.workflow === 'manual-plan'
                  ? '按明确要求执行'
                  : workflows.find((value) => value.id === review.workflow)?.name ||
                    review.workflow}
              </dd>
              <dt>工程 / 版本</dt>
              <dd>
                {project.name} · {review.projectId} · {review.projectRevision}
              </dd>
              <dt>任务上下文</dt>
              <dd>
                {review.selection.length
                  ? review.selection
                      .map((id) => project.clips.find((clip) => clip.id === id)?.name || id)
                      .join('、')
                  : '整个工程的概况'}
              </dd>
              <dt>真正的工具权限</dt>
              <dd>
                {review.allowEdits
                  ? '整个当前工程的可撤销编辑。选区不限制写入范围。'
                  : '只读工具，不注册普通编辑工具。'}
              </dd>
              <dt>源画面</dt>
              <dd>
                {review.allowSourceFrames
                  ? '允许读取当前工程的可用图片/视频源帧，可能传给所选CLI的模型服务。'
                  : '不提供源画面。'}
              </dd>
              <dt>执行要求</dt>
              <dd>{review.instruction}</dd>
            </dl>
            <p className="agent-warning">
              可能消耗CLI额度。执行中可取消后续调用；已完成的编辑保留并逐批可Undo，不能承诺会话原子回滚。
            </p>
            {uncertain.current?.id === review.id ? (
              <p className="agent-warning">
                上次登记结果未明确，沿用原请求编号由本地服务去重；不会自动重启推理。
              </p>
            ) : null}
            <div className="dialog-actions">
              <Button disabled={Boolean(busy)} onClick={() => setReview(null)}>
                返回修改
              </Button>
              <Button variant="primary" busy={Boolean(busy)} onClick={() => void start()}>
                确认执行（可能消耗CLI额度）
              </Button>
            </div>
          </>
        ) : null}
      </Modal>
      <Modal
        open={Boolean(cancelling)}
        title="取消后续执行？"
        description="停止本机等待、子进程和本次工具连接。已经提交的工程编辑保留，可逐批撤销；已确认的远端任务可能继续处理和计费。"
        busy={Boolean(busy)}
        onOpenChange={(value) => {
          if (!value && !busy) setCancelling(null);
        }}
      >
        {cancelling ? (
          <p className="field-hint">
            {agentNames[cancelling.provider]} · 工程 {cancelling.projectId} · 开始版本{' '}
            {cancelling.projectRevision} → {cancelling.endRevision}
          </p>
        ) : null}
        <div className="dialog-actions">
          <Button disabled={Boolean(busy)} onClick={() => setCancelling(null)}>
            继续等待
          </Button>
          <Button variant="primary" busy={Boolean(busy)} onClick={() => void cancel()}>
            确认取消后续执行
          </Button>
        </div>
        {error ? (
          <p className="form-error" role="alert">
            {error}
          </p>
        ) : null}
      </Modal>
      <Modal
        open={Boolean(login)}
        title="执行模式正常登录说明"
        description="与计划模式使用同一独立登录作用域；这里只显示命令，不代你执行或复制认证文件。"
        onOpenChange={(value) => {
          if (!value) setLogin(null);
        }}
      >
        {login ? (
          <>
            <p className="field-hint">{login.notice}</p>
            <Field label="登录终端格式">
              <Select
                value={loginShell}
                onChange={(event) => setLoginShell(event.target.value as 'posix' | 'powershell')}
              >
                <option value="posix">macOS / Linux · zsh / bash</option>
                <option value="powershell">Windows · PowerShell</option>
              </Select>
            </Field>
            <Field label="执行模式登录命令">
              <textarea
                className="input resize-none agent-login-command"
                rows={5}
                readOnly
                value={formatAgentLogin(login, loginShell)}
                onFocus={(event) => event.currentTarget.select()}
              />
            </Field>
            <p className="field-hint">可选中文本手动复制；登录后重新检查执行模式隔离。</p>
            <Button onClick={() => setLogin(null)}>关闭执行登录说明</Button>
          </>
        ) : null}
      </Modal>
    </div>
  );
}
