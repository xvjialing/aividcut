import { describe, expect, it } from 'vitest';
import {
  agentApplyBlocked,
  agentUnavailable,
  formatAgentLogin,
  summarizeAgentCommand,
} from '../src/lib/agent';
import { agentClip, agentJob, agentLogin, agentProject, readyAgent } from './agent-fixtures';

describe('本地 Agent 可用性和工程边界', () => {
  it('修剪摘要说明边缘与同量帧，不误称移动', () => {
    const s = summarizeAgentCommand(
      { type: 'trim_clips', clipIds: [agentClip.id], edge: 'end', frameDelta: -10 },
      agentProject,
    );
    expect(s.details).toContain('修剪边缘：终点');
    expect(s.details).toContain('修剪量：-10 帧');
  });
  it('批量构造摘要显示完整选择数量与实际目标帧', () => {
    const summary = summarizeAgentCommand(
      { type: 'duplicate_clips', clipIds: [agentClip.id, 'other'], atFrame: 300, trackDelta: 1 },
      agentProject,
    );
    expect(summary.title).toContain('2 个');
    expect(summary.details).toContain('目标位置：300 帧');
    expect(summary.details).toContain('轨道行偏移：1');
    expect(summary.details.join(' ')).toContain(agentClip.name);
  });
  it('安装、登录、隔离分别满足才能规划，不因已安装降级放行', () => {
    expect(agentUnavailable(readyAgent)).toBe('');
    expect(agentUnavailable({ ...readyAgent, installed: false })).not.toBe('');
    expect(agentUnavailable({ ...readyAgent, authState: 'unknown' })).not.toBe('');
    expect(
      agentUnavailable({ ...readyAgent, isolation: { ...readyAgent.isolation, supported: false } }),
    ).not.toBe('');
    expect(
      agentUnavailable({
        ...readyAgent,
        isolation: { ...readyAgent.isolation, status: 'policy_unverified' },
      }),
    ).not.toBe('');
  });
  it('旧工程、旧revision、未校验和已应用历史均不能应用', () => {
    expect(agentApplyBlocked(agentJob(), agentProject)).toBe('');
    expect(agentApplyBlocked(agentJob(), { ...agentProject, id: 'other' })).toContain('其他工程');
    expect(agentApplyBlocked(agentJob(), { ...agentProject, revision: 8 })).toContain('版本已变化');
    const invalid = agentJob();
    invalid.result!.coreValidated = false;
    expect(agentApplyBlocked(invalid, agentProject)).toContain('未通过');
    expect(agentApplyBlocked(agentJob({ applied: true }), agentProject)).toContain('已应用');
  });
  it('摘要显示实际帧数变化，旧快照只列目标值', () => {
    const command = { type: 'update_clip', clip: { ...agentClip, startFrame: 90 } };
    expect(summarizeAgentCommand(command, agentProject)).toEqual({
      title: '修改片段：开场标题',
      details: ['时间线起点：0 → 90 帧'],
    });
    expect(summarizeAgentCommand(command, agentProject, false).details).toContain(
      '时间线起点：90 帧',
    );
    expect(summarizeAgentCommand(command, agentProject, false).details.join(' ')).not.toContain(
      '0 → 90',
    );
  });
});
describe('登录文本只格式化后端参数', () => {
  it('POSIX和PowerShell分别转义引号，不执行内容或混入剪辑prompt', () => {
    const command = {
      ...agentLogin,
      executable: "/path/with ' quote/codex",
      environment: { CODEX_HOME: "/profile/it's private" },
      args: ['login', '$(fixture-only); echo fixture'],
    };
    const posix = formatAgentLogin(command, 'posix');
    expect(posix).toContain("'\"'\"'");
    expect(posix).toContain("'$(fixture-only); echo fixture'");
    const powershell = formatAgentLogin(command, 'powershell');
    expect(powershell).toContain("it''s private");
    expect(powershell).toContain("& '/path/with '' quote/codex'");
    expect(formatAgentLogin({ ...command, environment: { 'bad;key': 'x' } }, 'posix')).toBe('');
  });
});
