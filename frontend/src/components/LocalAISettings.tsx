import { useEffect, useRef, useState } from 'react';
import type { AgentInstallation, AgentLoginCommand, AgentProvider } from '../agent-types';
import { agentBridge, executionBridge } from '../lib/bridge';
import { formatAgentLogin } from '../lib/agent';
import { useSettingsFormState, type SettingsFormEvents } from '../lib/settings';
import { AgentEnvironmentDetails } from './AgentEnvironmentDetails';
import { Button, Field, PanelTabs, Select } from './ui';

export const agentEnvironmentChangedEvent = 'settings:agent-environment-changed';
export function LocalAISettings({ onBusyChange, onDirtyChange }: SettingsFormEvents) {
  const [provider, setProvider] = useState<AgentProvider>('codex');
  const [mode, setMode] = useState<'plan' | 'execution'>('plan');
  const [plan, setPlan] = useState<AgentInstallation[]>([]),
    [execution, setExecution] = useState<AgentInstallation[]>([]);
  const [errors, setErrors] = useState({ plan: '', execution: '' });
  const [reading, setReading] = useState(false),
    [loginBusy, setLoginBusy] = useState(false);
  const [command, setCommand] = useState<AgentLoginCommand | null>(null),
    [shell, setShell] = useState<'posix' | 'powershell'>('posix');
  const [loginError, setLoginError] = useState(''),
    [copyNotice, setCopyNotice] = useState('');
  const alive = useRef(false),
    reads = useRef(0),
    input = useRef<HTMLTextAreaElement>(null);
  useSettingsFormState(loginBusy, false, { onBusyChange, onDirtyChange });
  async function detect() {
    const request = ++reads.current;
    setReading(true);
    setCommand(null);
    setLoginError('');
    const values = await Promise.allSettled([
      agentBridge.getAgentStatus(),
      executionBridge.getAgentExecutionStatus(),
    ]);
    if (!alive.current || request !== reads.current) return;
    setErrors({
      plan:
        values[0].status === 'rejected'
          ? String(values[0].reason?.message || '规划能力检测失败')
          : '',
      execution:
        values[1].status === 'rejected'
          ? String(values[1].reason?.message || '执行能力检测失败')
          : '',
    });
    if (values[0].status === 'fulfilled') setPlan(values[0].value || []);
    if (values[1].status === 'fulfilled') setExecution(values[1].value || []);
    setReading(false);
    window.dispatchEvent(new Event(agentEnvironmentChangedEvent));
  }
  useEffect(() => {
    alive.current = true;
    void detect();
    return () => {
      alive.current = false;
      reads.current++;
      if (input.current) input.current.value = '';
    };
  }, []);
  const installation = (mode === 'plan' ? plan : execution).find(
    (value) => value.provider === provider,
  );
  return (
    <div className="settings-stack">
      <p className="field-hint">
        管理本机 CLI 的安装、登录与可用条件。规划和执行分别检测；不会自动登录或启动推理。
      </p>
      <div className="settings-group">
        <Field label="本地工具">
          <Select
            value={provider}
            onChange={(event) => {
              setProvider(event.target.value as AgentProvider);
              setCommand(null);
              setLoginError('');
            }}
            disabled={loginBusy}
          >
            <option value="codex">Codex CLI</option>
            <option value="claude">Claude Code</option>
          </Select>
        </Field>
        <PanelTabs
          id="agent-environment"
          label="能力检测模式"
          value={mode}
          items={[
            { value: 'plan', label: '剪辑规划' },
            { value: 'execution', label: '直接执行' },
          ]}
          onChange={(value) => {
            setMode(value);
            setCommand(null);
          }}
          disabled={loginBusy}
        />
        {reading ? (
          <p role="status">正在检测本地工具…</p>
        ) : errors[mode] ? (
          <p className="form-error" role="alert">
            {errors[mode]}
          </p>
        ) : (
          <AgentEnvironmentDetails installation={installation} mode={mode} />
        )}
        <div className="speech-actions">
          <Button disabled={reading || loginBusy} onClick={() => void detect()}>
            重新检测
          </Button>
          <Button
            disabled={!installation?.installed || reading || loginBusy || Boolean(errors[mode])}
            busy={loginBusy}
            onClick={async () => {
              setLoginBusy(true);
              setLoginError('');
              setCopyNotice('');
              try {
                const value = await agentBridge.getAgentLoginInstructions(provider);
                if (alive.current) setCommand(value);
              } catch (cause) {
                if (alive.current)
                  setLoginError(cause instanceof Error ? cause.message : '登录说明读取失败');
              } finally {
                if (alive.current) setLoginBusy(false);
              }
            }}
          >
            查看登录说明
          </Button>
        </div>
        {loginError ? (
          <p role="alert" className="form-error">
            {loginError}
          </p>
        ) : null}
      </div>
      {command ? (
        <section className="settings-group" aria-label="本地登录说明">
          <h3>登录说明</h3>
          <p className="field-hint">{command.notice}</p>
          <Field label="终端格式">
            <Select
              value={shell}
              onChange={(event) => setShell(event.target.value as 'posix' | 'powershell')}
            >
              <option value="posix">macOS / Linux</option>
              <option value="powershell">Windows PowerShell</option>
            </Select>
          </Field>
          <Field label="登录命令">
            <textarea
              ref={input}
              className="input resize-none agent-login-command"
              rows={5}
              readOnly
              value={formatAgentLogin(command, shell)}
              onFocus={(event) => event.currentTarget.select()}
            />
          </Field>
          <Button
            onClick={async () => {
              try {
                await navigator.clipboard.writeText(formatAgentLogin(command, shell));
                setCopyNotice('命令已复制，请自行在终端执行。');
              } catch {
                input.current?.focus();
                input.current?.select();
                setCopyNotice('请手动复制已选中的命令。');
              }
            }}
          >
            复制登录命令
          </Button>
          <p role="status">{copyNotice}</p>
        </section>
      ) : null}
    </div>
  );
}
