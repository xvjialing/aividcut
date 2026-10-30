import { useEffect, useRef, useState } from 'react';
import { rendererNames } from '../lib/export-options';
import type { Bootstrap, Project } from '../types';
import type { ConnectionProfile } from '../generation-types';
import type { MCPAction, MCPAPI, MCPConnection, MCPStatus } from '../mcp-types';
import { generationBridge, isDesktop, mcpBridge } from '../lib/bridge';
import { Button, Field, Input, Modal, Select } from './ui';
import { SourceFrameEvidenceViewer } from './SourceFrameEvidenceViewer';
import { visualNoteKind, analysisProducer } from '../visual-analysis-types';

export function MCPPanel({
  project,
  sourceEpoch = 0,
  applyAnalysis,
  status,
  onStatus,
  prepareProject,
  onOpenJobs,
  onOpenExecution,
  onBusyChange,
  api = mcpBridge,
  getConnections = generationBridge.getConnections,
  native = isDesktop(),
}: {
  project: Project;
  sourceEpoch?: number;
  applyAnalysis?: (action: () => Promise<Bootstrap>) => Promise<Bootstrap | null>;
  status?: MCPStatus | null;
  onStatus: (value: MCPStatus) => void | Promise<void>;
  prepareProject?: () => Promise<Project | null>;
  onOpenJobs: (kind: MCPAction['kind']) => void;
  onOpenExecution?: () => void;
  onBusyChange?: (busy: boolean) => void;
  api?: MCPAPI;
  getConnections?: () => Promise<ConnectionProfile[]>;
  native?: boolean;
}) {
  const [current, setCurrent] = useState<MCPStatus | null>(status || null);
  const [allowEdits, setAllowEdits] = useState(false);
  const [allowSourceFrames, setAllowSourceFrames] = useState(false);
  const [actions, setActions] = useState<MCPAction[]>([]);
  const [profiles, setProfiles] = useState<ConnectionProfile[]>([]);
  const [connection, setConnection] = useState<MCPConnection | null>(null);
  const connectionInput = useRef<HTMLTextAreaElement>(null);
  const [review, setReview] = useState<MCPAction | null>(null);
  const [subject, setSubject] = useState('');
  const subjectInput = useRef<HTMLInputElement>(null);
  const cloneCheckbox = useRef<HTMLInputElement>(null);
  const exportInput = useRef<HTMLInputElement>(null);
  const [authority, setAuthority] = useState<'self' | 'authorized'>('self');
  const [cloneConfirmed, setCloneConfirmed] = useState(false);
  const [exportPath, setExportPath] = useState('');
  const [overwrite, setOverwrite] = useState(false);
  const [busy, setBusy] = useState('');
  const pending = useRef(false);
  const alive = useRef(false);
  const reads = useRef(0);
  const projectRef = useRef(project);
  projectRef.current = project;
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const running = current?.running;
  useEffect(() => {
    onBusyChange?.(Boolean(busy));
  }, [busy, onBusyChange]);
  useEffect(() => () => onBusyChange?.(false), [onBusyChange]);
  const valid = Boolean(current?.scopeValid && current.projectId === project.id);
  function clearConnection() {
    if (connectionInput.current) connectionInput.current.value = '';
    setConnection(null);
  }
  function safeError(cause: unknown) {
    let message = cause instanceof Error ? cause.message : '工具连接操作未完成。';
    for (const value of Object.values(connection?.httpHeaders || {}).flatMap((value) => [
      value,
      value.replace(/^Bearer\s+/i, ''),
    ]))
      if (value) message = message.split(value).join('（凭据已隐藏）');
    return message;
  }
  async function acceptStatus(value: MCPStatus) {
    if (!alive.current) return;
    setCurrent(value);
    await onStatus(value);
  }
  async function refresh() {
    const token = ++reads.current;
    try {
      const [value, requests] = await Promise.all([api.getMCPStatus(), api.listMCPRequests()]);
      if (!alive.current || token !== reads.current) return;
      setActions(requests || []);
      await acceptStatus(value);
    } catch (cause) {
      if (alive.current) setError(safeError(cause));
    }
  }
  useEffect(() => {
    alive.current = true;
    void refresh();
    void getConnections()
      .then((values) => {
        if (alive.current) setProfiles(values || []);
      })
      .catch(() => {});
    return () => {
      alive.current = false;
      reads.current++;
      if (connectionInput.current) connectionInput.current.value = '';
    };
  }, [api, getConnections]);
  useEffect(() => {
    if (status) setCurrent(status);
  }, [status]);
  useEffect(() => {
    if (current?.running) setAllowEdits(current.allowEdits);
    else setAllowEdits(false);
  }, [current?.running, current?.allowEdits]);
  useEffect(() => {
    if (!running) return;
    const interval = window.setInterval(() => {
      // 全局编辑器负责状态和revision同步；这里只读取待核对请求。
      void api
        .listMCPRequests()
        .then((values) => {
          if (alive.current) setActions(values || []);
        })
        .catch((cause) => {
          if (alive.current) setError(safeError(cause));
        });
    }, 2000);
    return () => clearInterval(interval);
  }, [running, api]);
  useEffect(() => {
    if (!running || !valid) clearConnection();
  }, [running, valid, project.id]);
  useEffect(() => {
    setReview(null);
    setSubject('');
    setCloneConfirmed(false);
    setAllowSourceFrames(false);
  }, [project.id]);
  async function start() {
    if (pending.current) return;
    pending.current = true;
    setBusy('启动工具连接');
    setError('');
    try {
      const value = prepareProject ? await prepareProject() : projectRef.current;
      if (!value) return;
      if (value.id !== project.id || value.revision !== project.revision) {
        setError('工程已变化，请重新核对后启动连接。');
        return;
      }
      const result = await api.startMCP({
        projectId: value.id,
        allowEdits,
        ...(allowSourceFrames ? { allowSourceFrames: true } : {}),
      });
      await acceptStatus(result);
      setNotice(
        result.allowEdits
          ? '已允许此作用域提交可撤销编辑；收费请求仍需客户端单独确认。'
          : '只读工具连接已启动，未开放普通编辑提交。',
      );
    } catch (cause) {
      setError(safeError(cause));
    } finally {
      pending.current = false;
      if (alive.current) setBusy('');
    }
  }
  async function stop() {
    if (pending.current) return;
    pending.current = true;
    setBusy('停止工具连接');
    setError('');
    clearConnection();
    try {
      await acceptStatus(await api.stopMCP());
      setNotice('工具连接已停止，未确认请求不会执行。');
      await refresh();
    } catch (cause) {
      setError(safeError(cause));
    } finally {
      pending.current = false;
      if (alive.current) setBusy('');
    }
  }
  async function showConnection() {
    if (pending.current || !running || !valid) return;
    pending.current = true;
    setBusy('读取连接配置');
    setError('');
    try {
      const value = await api.getMCPConnection();
      if (alive.current) setConnection(value);
    } catch (cause) {
      setError(safeError(cause));
    } finally {
      pending.current = false;
      if (alive.current) setBusy('');
    }
  }
  async function copyConnection() {
    if (!connection) return;
    try {
      if (!navigator.clipboard?.writeText) throw new Error('clipboard_unavailable');
      await navigator.clipboard.writeText(JSON.stringify(connection, null, 2));
      setNotice('连接配置已复制。请自行粘贴到所选工具，应用不会修改其配置。');
    } catch {
      connectionInput.current?.focus();
      connectionInput.current?.select();
      setNotice('剪贴板不可用，已选中文本供手动复制。');
    }
  }
  async function reject(action: MCPAction) {
    if (pending.current) return;
    pending.current = true;
    setBusy('拒绝请求');
    setError('');
    try {
      const value = await api.rejectMCPRequest(action.id);
      setActions((current) => current.map((item) => (item.id === value.id ? value : item)));
    } catch (cause) {
      setError(safeError(cause));
    } finally {
      pending.current = false;
      if (alive.current) setBusy('');
    }
  }
  function openReview(action: MCPAction) {
    setReview(action);
    setSubject('');
    setAuthority('self');
    setCloneConfirmed(false);
    setExportPath('');
    setOverwrite(false);
    setError('');
  }
  async function approve() {
    if (pending.current || !review || review.status !== 'pending') return;
    if (
      !review.canApprove ||
      !actions.find((action) => action.id === review.id)?.canApprove ||
      review.projectId !== projectRef.current.id ||
      review.projectRevision !== projectRef.current.revision
    ) {
      setError(
        actions.find((action) => action.id === review.id)?.approvalReason ||
          '此请求的工程许可或版本已变化，请重新准备请求。',
      );
      return;
    }
    const clone = review.kind === 'speech' && review.speech?.mode === 'clone';
    if (clone && (!subject.trim() || [...subject].length > 200 || !cloneConfirmed)) {
      setError('请重新填写声音主体并确认本人或授权声明，外部工具的声明不能代替你确认。');
      if (!subject.trim() || [...subject].length > 200) subjectInput.current?.focus();
      else cloneCheckbox.current?.focus();
      return;
    }
    if (review.kind === 'export' && !native && !exportPath.trim()) {
      setError('请输入本机导出路径。');
      exportInput.current?.focus();
      return;
    }
    pending.current = true;
    setBusy('确认外部请求');
    setError('');
    try {
      // Approve仅在客户端的人为确认入口调用，外部工具不拥有此授权方法。
      if (review.kind === 'analysis') {
        if (!api.acceptVisualAnalysis) throw Error('当前接口不支持接纳画面分析，请更新客户端。');
        const action = () =>
          api.acceptVisualAnalysis!({
            id: review.id,
            sourceEpoch,
            projectId: review.projectId,
            projectRevision: review.projectRevision,
          });
        const accepted = applyAnalysis ? await applyAnalysis(action) : await action();
        if (!accepted) return;
        setReview(null);
        setNotice('已接纳画面分析记录，可撤销；未调用模型或修改时间线。');
        await refresh();
        return;
      }
      const value = await api.approveMCPRequest(review.id, {
        overwrite,
        ...(clone ? { cloneConsent: { confirmed: true, subject: subject.trim(), authority } } : {}),
        ...(review.kind === 'export' && !native ? { exportPath: exportPath.trim() } : {}),
      });
      setActions((current) => current.map((item) => (item.id === value.id ? value : item)));
      setReview(null);
      setSubject('');
      setCloneConfirmed(false);
      setNotice(value.message);
    } catch (cause) {
      setError(`${safeError(cause)} 不会自动重复确认。`);
    } finally {
      pending.current = false;
      if (alive.current) setBusy('');
    }
  }
  const actionName = (action: MCPAction) =>
    action.kind === 'analysis'
      ? '画面分析接纳'
      : action.kind === 'generation'
        ? '图片/视频生成'
        : action.kind === 'speech'
          ? action.speech?.mode === 'clone'
            ? '创建自定义音色'
            : action.speech?.mode === 'transcribe'
              ? '源素材转写'
              : '文本配音'
          : '导出视频';
  const reviewProfile = profiles.find(
    (profile) => profile.id === review?.connectionId || profile.id === review?.speech?.connectionId,
  );
  return (
    <div className="agent-panel mcp-panel" aria-busy={Boolean(busy)}>
      <p className="field-hint">
        把当前工程连接到支持 MCP
        的本机工具。只读为默认；普通可撤销编辑可单独允许，收费请求始终需要本客户端确认。
      </p>
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
      <section className="agent-installation">
        <div className="job-heading">
          <strong>
            {!current ? '工具状态尚未就绪' : running ? '工具连接运行中' : '工具连接未启动'}
          </strong>
          <span>{current?.allowEdits ? '允许可撤销编辑' : '只读'}</span>
        </div>
        {current?.projectId ? (
          <p className="field-hint">
            工程 {current.projectId} · 版本 {current.projectRevision}
          </p>
        ) : null}
        {current?.address ? <p className="file-path">{current.address}</p> : null}
        {current?.reason ? <p className="agent-warning">{current.reason}</p> : null}
        <label className="checkbox-field">
          <input
            type="checkbox"
            checked={allowEdits}
            disabled={!current || Boolean(busy) || Boolean(running)}
            onChange={(event) => setAllowEdits(event.target.checked)}
          />
          允许外部工具直接提交可撤销编辑
        </label>
        <label className="checkbox-field">
          <input
            type="checkbox"
            checked={running ? Boolean(current?.allowSourceFrames) : allowSourceFrames}
            disabled={!current || Boolean(busy) || Boolean(running)}
            onChange={(event) => setAllowSourceFrames(event.target.checked)}
          />
          允许向外部工具提供当前工程的源画面
        </label>
        <p className="field-hint">
          当前连接{current?.allowSourceFrames ? '已允许源画面' : '未开放源画面'}
          。开启后，所读取图片可能交给工具使用的模型服务；不额外授予编辑权限。
        </p>
        <p className="field-hint">
          运行中变更工程或权限需停止后重新启动。允许编辑后，普通编辑不逐条弹确认。
        </p>
        <div className="speech-actions">
          {running ? (
            <>
              {current?.executionId ? (
                <Button disabled={!onOpenExecution} onClick={onOpenExecution}>
                  打开执行记录取消
                </Button>
              ) : (
                <Button disabled={Boolean(busy)} onClick={() => void stop()}>
                  停止工具连接
                </Button>
              )}
              {!current?.executionId ? (
                <Button disabled={Boolean(busy) || !valid} onClick={() => void showConnection()}>
                  查看连接配置…
                </Button>
              ) : (
                <p className="field-hint">执行专用连接由应用管理，不展示访问凭据。</p>
              )}
            </>
          ) : (
            <Button
              variant="primary"
              disabled={!current || Boolean(busy)}
              onClick={() => void start()}
            >
              {allowEdits ? '启动并允许可撤销编辑' : '启动只读连接'}
            </Button>
          )}
          <Button variant="ghost" disabled={Boolean(busy)} onClick={() => void refresh()}>
            刷新工具状态
          </Button>
        </div>
        <p className="mcp-operation-status field-hint" role="status">
          {busy ? `${busy}…` : ''}
        </p>
      </section>
      <section className="agent-plan">
        <h3>待核对请求</h3>
        <p className="field-hint">
          外部工具只准备请求，生成、上传、克隆与导出由这里核对。普通允许的编辑不在此列表。
        </p>
        {actions.length ? (
          actions.map((action) => (
            <article className="agent-plan" key={action.id}>
              <div className="job-heading">
                <strong>{actionName(action)}</strong>
                <span>
                  {
                    (
                      {
                        pending: '待核对',
                        submitting: '正在提交',
                        started: '已启动',
                        failed: '失败',
                        rejected: '已拒绝',
                      } as Record<string, string>
                    )[action.status]
                  }
                </span>
              </div>
              <p>{action.message}</p>
              <p className="field-hint">
                工程版本 {action.projectRevision}
                {action.jobId ? ` · 任务 ${action.jobId}` : ''}
              </p>
              {action.status === 'pending' ? (
                <div className="speech-actions">
                  <Button
                    variant="primary"
                    disabled={
                      Boolean(busy) ||
                      !action.canApprove ||
                      action.projectId !== project.id ||
                      action.projectRevision !== project.revision
                    }
                    onClick={() => openReview(action)}
                  >
                    核对本次请求…
                  </Button>
                  <Button
                    variant="ghost"
                    disabled={Boolean(busy)}
                    onClick={() => void reject(action)}
                  >
                    拒绝此请求
                  </Button>
                </div>
              ) : null}
              {action.status === 'started' && action.kind === 'analysis' ? (
                <p className="field-hint">
                  {action.analysisPresent
                    ? '当前工程包含该分析记录。'
                    : '历史上已接纳，当前工程可能已撤销或移除；不会自动重放。'}
                </p>
              ) : null}
              {action.status === 'started' && action.kind !== 'analysis' ? (
                <Button variant="ghost" onClick={() => onOpenJobs(action.kind)}>
                  打开对应面板
                </Button>
              ) : null}
              {action.status === 'pending' &&
              (!action.canApprove ||
                action.projectId !== project.id ||
                action.projectRevision !== project.revision) ? (
                <p className="agent-warning">
                  {action.approvalReason || '原工程或版本已变化，请重新准备请求。'}
                </p>
              ) : null}
            </article>
          ))
        ) : (
          <p className="field-hint">没有待处理的外部请求，不会自动发起收费或导出。</p>
        )}
      </section>
      <details className="agent-details">
        <summary>工具调用记录 · {current?.audit?.length || 0}</summary>
        {(current?.audit || [])
          .slice(-40)
          .reverse()
          .map((entry) => (
            <p key={entry.id}>
              {entry.tool} · {entry.success ? '成功' : '已拒绝或失败'} · 版本 {entry.revision} ·{' '}
              {entry.createdAt}
            </p>
          ))}
      </details>
      <Modal
        open={Boolean(connection)}
        title="复制当前工程的 MCP 连接配置"
        description="配置包含本机会话访问凭据，仅向你信任的工具提供。应用不会写入 Codex、Claude 或其他工具的配置；停止连接后失效。"
        onOpenChange={(value) => {
          if (!value) clearConnection();
        }}
      >
        <Field label="MCP 连接配置">
          <textarea
            ref={connectionInput}
            rows={9}
            className="input resize-none agent-login-command"
            readOnly
            value={connection ? JSON.stringify(connection, null, 2) : ''}
            onFocus={(event) => event.currentTarget.select()}
          />
        </Field>
        <div className="dialog-actions">
          <Button onClick={clearConnection}>关闭并清空</Button>
          <Button onClick={() => void copyConnection()}>复制连接配置</Button>
        </div>
        {notice ? (
          <p className="agent-notice" role="status">
            {notice}
          </p>
        ) : null}
      </Modal>
      <Modal
        open={Boolean(review)}
        title={review?.kind === 'analysis' ? '核对画面分析' : '核对外部准备的请求'}
        description={
          review?.kind === 'analysis'
            ? '接纳可撤销的分析记录，不调用模型或修改时间线。取样观察需核对，不能当整段事实。'
            : review?.kind === 'export'
              ? '确认后将导出当前核对的工程快照，输出路径由你选择。'
              : '确认后会调用供应商并可能计费。外部工具准备的内容不能代替你的本次授权。'
        }
        busy={Boolean(busy)}
        onOpenChange={(value) => {
          if (!value && !busy) {
            setReview(null);
            setSubject('');
            setCloneConfirmed(false);
          }
        }}
      >
        {review ? (
          <>
            <dl className="agent-review">
              <dt>用途</dt>
              <dd>{actionName(review)}</dd>
              <dt>
                {review.kind === 'analysis'
                  ? '提交来源（具体模型未记录）'
                  : review.kind === 'export'
                    ? '合成 / 画幅 / 编码 / 资源'
                    : '供应商 / 精确模型 / 数量'}
              </dt>
              <dd>
                {review.analysis
                  ? analysisProducer[review.analysis.producer]
                  : review.generation
                    ? `${review.generation.providerId} / ${review.generation.modelId} / ${review.generation.count || 1}`
                    : review.speech
                      ? `${reviewProfile?.providerId || '连接供应商未找到'} / ${review.speech.transcribe?.modelId || review.speech.synthesize?.modelId || (review.speech.mode === 'clone' ? '音色创建（无模型ID）' : reviewProfile?.modelId || '未提供')} / 1`
                      : `${rendererNames[review.export?.renderer || 'auto']} / ${review.export?.preset || '默认预设'} / ${review.export?.videoEncoder || '默认编码器'} / ${review.export?.resourceMode === 'lower_memory' ? '较低内存' : '标准'}`}
              </dd>
              {review.export?.resourceMode === 'lower_memory' ? (
                <>
                  <dt>资源档位说明</dt>
                  <dd>减少编码缓存，可能更慢，文件大小与压缩画质可能变化；不是内存上限。</dd>
                </>
              ) : null}
              <dt>工程</dt>
              <dd>
                {review.projectId} · 版本 {review.projectRevision}
              </dd>
              {review.analysis ? (
                <>
                  <dt>分析记录（仅取样，不代表整段事实）</dt>
                  <dd>
                    {review.analysis.notes.map((note) => (
                      <div key={note.id}>
                        <strong>{visualNoteKind[note.kind]}</strong>
                        <p>{note.text}</p>
                        {note.tags?.length ? <p>标签：{note.tags.join('、')}</p> : null}
                      </div>
                    ))}
                  </dd>
                  <dd>
                    <SourceFrameEvidenceViewer
                      inline
                      project={project}
                      sourceEpoch={sourceEpoch}
                      runProjectID={review.projectId}
                      frames={review.analysis.frames}
                    />
                  </dd>
                </>
              ) : null}
              {!review.analysis ? (
                <>
                  <dt>描述 / 文本 / 参数</dt>
                  <dd>
                    {review.generation
                      ? JSON.stringify(review.generation, null, 2)
                      : review.speech
                        ? JSON.stringify(
                            {
                              mode: review.speech.mode,
                              transcribe: review.speech.transcribe,
                              synthesize: review.speech.synthesize,
                              clone: review.speech.clone
                                ? {
                                    name: review.speech.clone.name,
                                    description: review.speech.clone.description,
                                  }
                                : undefined,
                            },
                            null,
                            2,
                          )
                        : JSON.stringify(review.export, null, 2)}
                  </dd>
                </>
              ) : null}
              {review.generation?.generateAudio !== undefined ? (
                <>
                  <dt>视频声音</dt>
                  <dd>{review.generation.generateAudio ? '生成同步声音' : '无声视频'}</dd>
                </>
              ) : null}
              {review.speech?.mode === 'synthesize' ? (
                <>
                  <dt>配音时间</dt>
                  <dd>
                    {review.speech.synthesize?.withTiming
                      ? '请求一次带时间配音，不额外识别或回退。'
                      : '普通配音，不另取时间戳。'}
                  </dd>
                </>
              ) : null}
              <dt>{review.kind === 'analysis' ? '分析素材' : '上传源素材'}</dt>
              <dd>
                {(
                  (review.analysis ? [review.analysis.assetId] : undefined) ||
                  review.generation?.references?.map((reference) => reference.assetId) ||
                  (review.speech?.transcribe
                    ? [review.speech.transcribe.assetId]
                    : review.speech?.clone?.assetIds) ||
                  []
                )
                  .map((id) => project.assets.find((asset) => asset.id === id)?.name || id)
                  .join('、') || '无媒体上传；配音会提交上方文本'}
              </dd>
            </dl>
            {review.speech?.mode === 'clone' ? (
              <>
                <Field label="重新填写声音主体">
                  <Input
                    ref={subjectInput}
                    value={subject}
                    onChange={(event) => {
                      setSubject(event.target.value);
                      setCloneConfirmed(false);
                    }}
                  />
                </Field>
                <Field label="本人的授权声明">
                  <Select
                    value={authority}
                    onChange={(event) => {
                      setAuthority(event.target.value as 'self' | 'authorized');
                      setCloneConfirmed(false);
                    }}
                  >
                    <option value="self">本人的声音</option>
                    <option value="authorized">已获声音主体授权</option>
                  </Select>
                </Field>
                <label className="checkbox-field">
                  <input
                    type="checkbox"
                    checked={cloneConfirmed}
                    ref={cloneCheckbox}
                    onChange={(event) => setCloneConfirmed(event.target.checked)}
                  />
                  我确认有权上传这些样本，且明白创建结果仍未验证
                </label>
              </>
            ) : null}
            {review.kind === 'export' ? (
              <>
                {!native ? (
                  <Field label="本机导出路径">
                    <Input
                      ref={exportInput}
                      value={exportPath}
                      onChange={(event) => setExportPath(event.target.value)}
                    />
                  </Field>
                ) : (
                  <p className="field-hint">确认后打开系统保存选择器；取消选择不会导出。</p>
                )}
                <label className="checkbox-field">
                  <input
                    type="checkbox"
                    checked={overwrite}
                    onChange={(event) => setOverwrite(event.target.checked)}
                  />
                  允许覆盖所选现有输出文件
                </label>
              </>
            ) : review.kind === 'analysis' ? (
              <p className="field-hint">
                仅保存分析记录，不调用模型或修改时间线。画面观察仍需人工判断，推断和建议不会自动成为事实。
              </p>
            ) : (
              <p className="agent-warning">
                实际费用按供应商规则，当前无可验证报价。上传和远端处理不能靠停止本地等待保证取消或退款。
              </p>
            )}
            {error ? (
              <p className="form-error" role="alert">
                {error}
              </p>
            ) : null}
            <div className="dialog-actions">
              <Button
                disabled={Boolean(busy)}
                onClick={() => {
                  setReview(null);
                  setSubject('');
                  setCloneConfirmed(false);
                }}
              >
                返回不提交
              </Button>
              <Button
                variant="primary"
                busy={Boolean(busy)}
                disabled={
                  !actions.find((action) => action.id === review.id)?.canApprove ||
                  review.projectId !== project.id ||
                  review.projectRevision !== project.revision
                }
                onClick={() => void approve()}
              >
                {review.kind === 'analysis'
                  ? '接纳到工程（可撤销）'
                  : review.kind === 'export'
                    ? '确认并选择导出'
                    : '确认本次请求（可能计费）'}
              </Button>
            </div>
          </>
        ) : null}
      </Modal>
    </div>
  );
}
