import { useEffect, useRef, useState } from 'react';
import type { ProjectScope } from '../types';
import type { ResourceAPI, ResourcePhase, ResourceState } from '../resource-types';
import { resourceAPI, isDesktop } from '../lib/bridge';
import { useSettingsFormState, type SettingsFormEvents } from '../lib/settings';
import { Button, Field, Input, SettingsSurface, Select } from './ui';
const phases: Record<ResourcePhase, string> = {
  idle: '空闲',
  preview: '预览',
  editing: '剪辑与定位',
  export: '导出',
  mixed: '混合任务',
};
const roles: Record<string, string> = {
  application: '应用',
  media: '媒体引擎',
  encoding: '媒体工具',
  agent: '本地助手',
  webview: '界面进程',
  helper: '辅助进程',
};
const statuses: Record<string, string> = {
  running: '记录中',
  stopped: '已停止',
  complete: '达到记录上限',
  failed: '采集失败',
};
const memory = (value: number | null | undefined) =>
  value === null || value === undefined ? '未提供' : `${(value / 1048576).toFixed(1)} MiB`;
const cpu = (value: number | null | undefined) =>
  value === null || value === undefined ? '未提供' : `${value.toFixed(1)}%`;
export function ResourceMonitorDialog({
  scope,
  onClose,
  onRecording,
  onReturnFocus,
  inline = false,
  onBusyChange,
  onDirtyChange,
  api = resourceAPI,
}: {
  scope: ProjectScope;
  onClose: () => void;
  onRecording: (value: boolean) => void;
  onReturnFocus: () => void;
  api?: ResourceAPI;
} & SettingsFormEvents & { inline?: boolean }) {
  const [state, setState] = useState<ResourceState | null>(null),
    [phase, setPhase] = useState<ResourcePhase>('preview'),
    [selected, setSelected] = useState(''),
    [path, setPath] = useState(''),
    [overwrite, setOverwrite] = useState(false),
    [busy, setBusy] = useState(''),
    [error, setError] = useState(''),
    [notice, setNotice] = useState(''),
    [limit, setLimit] = useState(20);
  const epoch = useRef(0),
    reading = useRef(false),
    pending = useRef(false),
    alive = useRef(true);
  function accept(value: ResourceState) {
    setState(value);
    onRecording(Boolean(value.active));
    setSelected((old) =>
      value.reports.some((v) => v.id === old)
        ? old
        : value.active?.id || value.reports[0]?.id || '',
    );
  }
  async function read() {
    if (reading.current || pending.current) return;
    reading.current = true;
    const request = ++epoch.current;
    try {
      const value = await api.state();
      if (alive.current && request === epoch.current) {
        accept(value);
        setError('');
      }
    } catch (e) {
      if (alive.current && request === epoch.current)
        setError(e instanceof Error ? e.message : '资源读取失败，请刷新重试。');
    } finally {
      reading.current = false;
    }
  }
  useEffect(() => {
    alive.current = true;
    void read();
    const timer = window.setInterval(() => void read(), 1000);
    return () => {
      alive.current = false;
      epoch.current++;
      window.clearInterval(timer);
    };
  }, [api]);
  async function action(label: string, run: () => Promise<void>) {
    if (pending.current) return;
    pending.current = true;
    const request = ++epoch.current;
    setBusy(label);
    setError('');
    try {
      await run();
      if (alive.current && request === epoch.current) {
        const value = await api.status();
        if (alive.current && request === epoch.current) accept(value);
      }
    } catch (e) {
      if (alive.current && request === epoch.current)
        setError(e instanceof Error ? e.message : '操作未完成，已有记录保留。');
    } finally {
      pending.current = false;
      if (alive.current && request === epoch.current) setBusy('');
    }
  }
  const sample = state?.latest,
    snapshot = sample?.snapshot,
    report = state?.reports.find((v) => v.id === selected);
  useSettingsFormState(Boolean(busy) && busy !== '读取', false, { onBusyChange, onDirtyChange });
  return (
    <SettingsSurface
      inline={inline}
      open
      title="资源诊断"
      description="观察应用及已确认后代进程的采样占用。记录不改变工程，GPU/显存与无法确认归属的进程单独标明。"
      className="replacement-dialog resource-monitor-dialog"
      busy={Boolean(busy)}
      onOpenChange={(open) => {
        if (!open && !busy) onClose();
      }}
      onCloseAutoFocus={(event) => {
        event.preventDefault();
        onReturnFocus();
      }}
    >
      <div className="replacement-dialog-body">
        <div className="field-pair">
          <Field label="记录阶段">
            <Select
              aria-label="记录阶段"
              value={phase}
              disabled={Boolean(busy) || Boolean(state?.active)}
              onChange={(e) => setPhase(e.target.value as ResourcePhase)}
            >
              {Object.entries(phases).map(([key, label]) => (
                <option key={key} value={key}>
                  {label}
                </option>
              ))}
            </Select>
          </Field>
          <div className="speech-actions">
            {state?.active ? (
              <Button
                busy={busy === '停止'}
                disabled={Boolean(busy)}
                onClick={() =>
                  void action('停止', async () => {
                    await api.stop(state.active!.id);
                    setNotice('资源记录已停止，已采数据保留，可导出。');
                  })
                }
              >
                停止记录
              </Button>
            ) : (
              <Button
                variant="primary"
                busy={busy === '开始'}
                disabled={Boolean(busy) || !state}
                onClick={() =>
                  void action('开始', async () => {
                    const value = await api.start({ ...scope, phase });
                    setSelected(value.id);
                    setNotice('开始记录。关闭此窗口后仍会继续，最多5分钟。');
                  })
                }
              >
                开始记录
              </Button>
            )}
            <Button disabled={Boolean(busy) || reading.current} onClick={() => void read()}>
              刷新
            </Button>
          </div>
        </div>
        <p className="field-hint">
          按1秒采样，最多300份样本、128个进程，内存保留最近3份记录。退出前请导出需要的报告。CPU的100%表示一个逻辑核心；RSS包含共享页面，合计不是独占内存。
        </p>
        {state?.active ? (
          <p role="status">
            {phases[state.active.phase]} · 记录中 · 已采 {state.active.samples} 份
          </p>
        ) : null}
        {state?.error ? (
          <p role="alert" className="form-error">
            {state.error}
          </p>
        ) : null}
        {!state && !error ? <p role="status">正在读取资源…</p> : null}
        {snapshot ? (
          <>
            <div className="metadata-list resource-metrics">
              <div>
                <span>已确认进程RSS</span>
                <strong>{memory(snapshot.rssBytes)}</strong>
              </div>
              <div>
                <span>已确认进程CPU</span>
                <strong>{cpu(snapshot.cpuPercent)}</strong>
              </div>
              <div>
                <span>GPU / 显存</span>
                <strong>未提供</strong>
              </div>
            </div>
            <p className="field-hint">
              {snapshot.os} / {snapshot.architecture} · {snapshot.logicalCpus} 个逻辑核心 ·{' '}
              {new Date(snapshot.at).toLocaleTimeString('zh-CN')} · 查询{' '}
              {snapshot.queryMs.toFixed(2)} ms{snapshot.partial ? ' · 采样不完整' : ''}
            </p>
            {sample ? (
              <p className="field-hint">
                工程修订 {sample.project.revision} · {sample.project.width} ×{' '}
                {sample.project.height} · {sample.project.clips}{' '}
                个片段；此上下文不表示全部进程只服务于该工程。
              </p>
            ) : null}
            <div className="resource-table-scroll">
              <table className="resource-table">
                <caption className="sr-only">已确认应用进程资源</caption>
                <thead>
                  <tr>
                    <th>进程</th>
                    <th>类别</th>
                    <th>CPU</th>
                    <th>RSS</th>
                    <th>私有指标</th>
                  </tr>
                </thead>
                <tbody>
                  {snapshot.processes.slice(0, limit).map((p) => (
                    <tr key={p.identity}>
                      <td>
                        <span>{p.name}</span>
                        <small className="field-hint">
                          PID {p.pid} · 父进程 {p.ppid}
                          {p.association === 'observed_child' ? ' · 曾确认后代' : ''}
                        </small>
                        {p.issue ? <small className="form-error">{p.issue}</small> : null}
                      </td>
                      <td>{roles[p.role] || '辅助进程'}</td>
                      <td>{cpu(p.cpuPercent)}</td>
                      <td>{memory(p.rssBytes)}</td>
                      <td>
                        {memory(p.privateBytes)}
                        <small className="field-hint">
                          {p.privateKind === 'physicalFootprint'
                            ? '物理footprint'
                            : p.privateKind === 'privateCommit'
                              ? '私有提交'
                              : ''}
                        </small>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
            {snapshot.processes.length > limit ? (
              <Button onClick={() => setLimit((v) => v + 20)}>再显示20个进程</Button>
            ) : null}
            {snapshot.issues.length ? (
              <p className="form-error" role="status">
                {snapshot.issues.join('；')}
              </p>
            ) : null}
            <details>
              <summary>覆盖范围与缺失值</summary>
              {snapshot.uncovered.map((v) => (
                <p className="field-hint" key={v}>
                  {v}
                </p>
              ))}
              <p className="field-hint">
                首样本、身份变化或系统权限不足可能没有CPU/内存值，不按零处理；不同平台的私有指标不可直接混比。
              </p>
            </details>
          </>
        ) : null}
        <section aria-label="导出诊断记录">
          <h3>导出记录</h3>
          <Field label="诊断记录">
            <Select
              aria-label="诊断记录"
              value={selected}
              disabled={Boolean(busy) || !state?.reports.length}
              onChange={(e) => {
                setSelected(e.target.value);
                setNotice('');
              }}
            >
              {!state?.reports.length ? (
                <option value="">暂无记录</option>
              ) : (
                state.reports.map((r) => (
                  <option key={r.id} value={r.id}>
                    {phases[r.phase]} · {new Date(r.startedAt).toLocaleTimeString('zh-CN')} ·{' '}
                    {statuses[r.status]} · {r.samples}份
                  </option>
                ))
              )}
            </Select>
          </Field>
          {report ? (
            <p className="field-hint">
              采样RSS峰值 {memory(report.sampledPeakRss)} · 采样CPU峰值 {cpu(report.sampledPeakCpu)}
              {report.error ? ` · ${report.error}` : ''}
            </p>
          ) : null}
          {!isDesktop() ? (
            <Field label="JSON报告路径">
              <Input
                value={path}
                disabled={Boolean(busy)}
                onChange={(e) => setPath(e.target.value)}
              />
            </Field>
          ) : null}
          <label className="checkbox-field">
            <input
              type="checkbox"
              checked={overwrite}
              disabled={Boolean(busy)}
              onChange={(e) => setOverwrite(e.target.checked)}
            />
            允许替换同名报告
          </label>
          <Button
            disabled={Boolean(busy) || !selected || (!isDesktop() && !path.trim())}
            busy={busy === '导出'}
            onClick={() =>
              void action('导出', async () => {
                const target = isDesktop() ? await api.choosePath() : path.trim();
                if (!target) return;
                const saved = await api.export({ id: selected, path: target, overwrite });
                setNotice(`诊断报告已保存：${saved}`);
              })
            }
          >
            导出JSON报告
          </Button>
        </section>
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
        <Button disabled={Boolean(busy)} onClick={onClose}>
          {inline
            ? state?.active
              ? '返回应用，继续记录'
              : '返回应用'
            : state?.active
              ? '关闭窗口，继续记录'
              : '关闭诊断'}
        </Button>
      </div>
    </SettingsSurface>
  );
}
