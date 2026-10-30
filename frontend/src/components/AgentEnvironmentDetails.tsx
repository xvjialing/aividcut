import type { AgentInstallation } from '../agent-types';
import { agentAuthName, agentUnavailable } from '../lib/agent';
import { executionUnavailable } from '../lib/agent-execution';

export function AgentEnvironmentDetails({
  installation,
  mode,
}: {
  installation?: AgentInstallation;
  mode: 'plan' | 'execution';
}) {
  const unavailable =
    mode === 'plan' ? agentUnavailable(installation) : executionUnavailable(installation);
  return (
    <section className="agent-installation">
      <div className="job-heading">
        <strong>{installation?.installed ? `已安装 · ${installation.version}` : '未安装'}</strong>
        <span>{agentAuthName(installation?.authState || 'unknown')}</span>
      </div>
      <p className="field-hint">{installation?.authReason}</p>
      <p className={unavailable ? 'agent-warning' : 'agent-validation'}>
        {unavailable || '当前模式条件已通过检测'}
      </p>
      {installation?.path ? <p className="file-path">程序位置：{installation.path}</p> : null}
      {installation?.profileDirectory ? (
        <p className="file-path">登录作用域：{installation.profileDirectory}</p>
      ) : null}
      <details className="agent-details">
        <summary>{mode === 'plan' ? '隔离检查与限制' : '执行隔离证据与限制'}</summary>
        <p>{installation?.isolation.reason}</p>
        {(installation?.isolation.checks || []).map((check) => (
          <p key={check.id}>
            {check.id} · {check.status} · {check.reason}
            {check.nextAction ? ` · ${check.nextAction}` : ''}
          </p>
        ))}
        {installation?.isolation.missingFlags?.length ? (
          <p>缺少选项：{installation.isolation.missingFlags.join('、')}</p>
        ) : null}
        {(installation?.isolation.evidence || []).map((value, i) => (
          <p key={i}>{value}</p>
        ))}
        {(installation?.isolation.limitations || []).map((value, i) => (
          <p className="agent-warning" key={i}>
            {value}
          </p>
        ))}
      </details>
    </section>
  );
}
