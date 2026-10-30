import type { AgentInstallation } from '../agent-types';

export function executionUnavailable(info?: AgentInstallation): string {
  if (!info) return '尚未读取执行模式检测结果。';
  if (!info.installed) return info.authReason || '未找到此CLI。';
  if (info.authState !== 'signed_in')
    return info.authReason || '请在独立作用域正常登录后重新检查执行能力。';
  if (info.isolation.supported && info.isolation.status !== 'execution_ready')
    return '当前检测不是可执行的第一方工具会话，不能用计划模式结果替代。';
  if (!info.isolation.supported || info.isolation.status !== 'execution_ready')
    return info.isolation.reason || '尚未证明第一方MCP执行隔离，不能沿用计划模式的检测结论。';
  return '';
}
