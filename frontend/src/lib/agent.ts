import type { AgentInstallation, AgentJob, AgentLoginCommand } from '../agent-types';
import type { Command, Project } from '../types';

export const agentNames = { codex: 'Codex CLI', claude: 'Claude Code' };
export const agentJobStatus: Record<AgentJob['status'], string> = {
  queued: '等待规划',
  running: '正在规划',
  succeeded: '计划已生成',
  failed: '规划失败',
  cancelling: '正在取消本地规划',
  cancelled: '已取消本地规划',
};
export const agentAuthName = (state: string) =>
  (
    ({
      signed_in: '此作用域已登录',
      signed_out: '此作用域未登录',
      unknown: '登录状态未知',
    }) as Record<string, string>
  )[state] || '登录状态未知';

export function agentUnavailable(installation?: AgentInstallation): string {
  if (!installation) return '尚未检测到所选 CLI，请重新检测。';
  if (!installation.installed) return installation.authReason || '本机未安装此 CLI。';
  if (!installation.isolation.supported || installation.isolation.status !== 'ready')
    return installation.isolation.reason || '受限规划条件尚未通过检测。';
  if (installation.authState !== 'signed_in')
    return installation.authReason || '请在显示的独立作用域登录后重新检测。';
  return '';
}

export function agentApplyBlocked(job: AgentJob, project: Project): string {
  if (job.applied) return '此计划已应用。';
  if (job.projectId !== project.id) return '请打开计划所属工程；不会应用到当前其他工程。';
  if (
    job.projectRevision !== project.revision ||
    job.result?.request.baseRevision !== project.revision
  )
    return '工程版本已变化，请根据当前工程重新生成计划。';
  if (job.status !== 'succeeded' || !job.result) return '计划尚未成功生成。';
  if (!job.result.coreValidated) return '计划未通过编辑核心校验，不能应用。';
  if (!job.result.request.commands?.length) return '计划中没有可应用的编辑命令。';
  return '';
}

// 只将后端给出的登录程序、参数和作用域格式化为供人复制的文本；不执行。
export function formatAgentLogin(command: AgentLoginCommand, shell: 'posix' | 'powershell') {
  const entries = Object.entries(command.environment || {});
  if (!command.executable || entries.some(([key]) => !/^[A-Za-z_][A-Za-z0-9_]*$/.test(key)))
    return '';
  if (shell === 'powershell') {
    const quote = (value: string) => `'${value.replaceAll("'", "''")}'`;
    const environment = entries.map(([key, value]) => `$env:${key} = ${quote(value)}`).join('\n');
    return [environment, `& ${[command.executable, ...(command.args || [])].map(quote).join(' ')}`]
      .filter(Boolean)
      .join('\n');
  }
  const quote = (value: string) => `'${value.replaceAll("'", "'\"'\"'")}'`;
  return [
    'env',
    ...entries.map(([key, value]) => quote(`${key}=${value}`)),
    ...[command.executable, ...(command.args || [])].map(quote),
  ].join(' ');
}

export interface AgentCommandSummary {
  title: string;
  details: string[];
}
export function summarizeAgentCommand(
  command: Command,
  project: Project,
  compare = true,
): AgentCommandSummary {
  const clip = command.clip || project.clips.find((value) => value.id === command.clipId);
  const previous = compare ? project.clips.find((value) => value.id === clip?.id) : undefined;
  const track = command.track || project.tracks.find((value) => value.id === command.trackId);
  const clipName = clip?.name || clip?.id || command.clipId || '片段';
  const trackName = track?.name || track?.id || command.trackId || '轨道';
  const titles: Record<string, string> = {
    split_clips: `分割选中片段：${command.clipIds?.length || 0} 个`,
    duplicate_clips: `复制到目标帧：${command.clipIds?.length || 0} 个`,
    move_clips: `移动选中片段：${command.clipIds?.length || 0} 个`,
    trim_clips: `修剪选中片段：${command.clipIds?.length || 0} 个`,
    delete_clips: `删除选中片段：${command.clipIds?.length || 0} 个`,
    add_clip: `添加片段：${clipName}`,
    update_clip: `修改片段：${clipName}`,
    delete_clip: `删除片段：${clipName}`,
    split_clip: `分割片段：${clipName}`,
    add_track: `添加轨道：${trackName}`,
    update_track: `修改轨道：${trackName}`,
    delete_track: `删除轨道：${trackName}`,
    update_project: '修改工程属性',
  };
  const details: string[] = [];
  const changed = (
    label: string,
    before: unknown,
    after: unknown,
    unit = '',
    includeEmpty = false,
  ) => {
    if ((!includeEmpty && after === undefined) || JSON.stringify(before) === JSON.stringify(after))
      return;
    const show = (value: unknown) =>
      value === undefined
        ? '空'
        : typeof value === 'string'
          ? value || '空'
          : JSON.stringify(value);
    details.push(
      `${label}：${before === undefined ? '' : `${show(before)} → `}${show(after)}${unit}`,
    );
  };
  if (command.type === 'add_clip' || command.type === 'update_clip') {
    const before = command.type === 'update_clip' ? previous : undefined;
    if (clip) {
      changed('名称', before?.name, clip.name);
      changed('类型', before?.kind, clip.kind);
      changed('轨道', before?.trackId, clip.trackId);
      changed('素材', before?.assetId, clip.assetId, '', true);
      changed('时间线起点', before?.startFrame, clip.startFrame, ' 帧');
      changed('时长', before?.durationFrames, clip.durationFrames, ' 帧');
      changed('素材入点', before?.sourceInUs, clip.sourceInUs, ' 微秒');
      changed(
        '速度',
        before ? `${before.speedNum}/${before.speedDen}` : undefined,
        `${clip.speedNum}/${clip.speedDen}`,
      );
      changed('音量', before?.volume, clip.volume);
      changed('静音', before?.muted, clip.muted);
      changed('变换', before?.transform, clip.transform);
      changed('文字', before?.text, clip.text, '', true);
      changed('文字样式', before?.textStyle, clip.textStyle);
      changed('LUT', before?.lut, clip.lut, '', true);
    }
  } else if (command.clipIds?.length) {
    const ids = command.clipIds;
    details.push(
      `选中：${ids
        .slice(0, 8)
        .map((id) => project.clips.find((c) => c.id === id)?.name || id)
        .join('、')}${ids.length > 8 ? `，另 ${ids.length - 8} 个` : ''}`,
    );
    if (command.atFrame !== undefined) details.push(`目标位置：${command.atFrame} 帧`);
    if (command.frameDelta !== undefined)
      details.push(
        `${command.type === 'trim_clips' ? '修剪量' : '水平位移'}：${command.frameDelta} 帧`,
      );
    if (command.edge) details.push(`修剪边缘：${command.edge === 'start' ? '起点' : '终点'}`);
    if (command.trackDelta !== undefined) details.push(`轨道行偏移：${command.trackDelta}`);
  } else if (command.type === 'split_clip')
    details.push(`分割位置：${command.atFrame ?? '未提供'} 帧`);
  else if (command.type === 'add_track' || command.type === 'update_track') {
    const before =
      command.type === 'update_track' && compare
        ? project.tracks.find((value) => value.id === track?.id)
        : undefined;
    if (track) {
      changed('名称', before?.name, track.name);
      changed('类型', before?.kind, track.kind);
      changed('锁定', before?.locked, track.locked);
      changed('静音', before?.muted, track.muted);
      changed('隐藏', before?.hidden, track.hidden);
    }
  } else if (command.type === 'update_project') {
    changed('工程名称', compare ? project.name : undefined, command.name);
    changed('工程设置', compare ? project.settings : undefined, command.settings);
  }
  if (command.includeLinked !== undefined)
    details.push(command.includeLinked ? '包含关联音画片段' : '不包含关联音画片段');
  return { title: titles[command.type] || `未识别操作：${command.type}`, details };
}
