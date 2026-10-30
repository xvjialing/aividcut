import type { Clip, Project } from '../types';

export function sourceSecondsToMicroseconds(value: string): number | null {
  const text = value.trim();
  if (text.length > 24) return null;
  if (!/^(?:\d+(?:\.\d{0,6})?|\.\d{1,6})$/.test(text)) return null;
  const [whole, fraction = ''] = text.split('.');
  const microseconds = BigInt(whole || '0') * 1000000n + BigInt(fraction.padEnd(6, '0') || '0');
  return microseconds <= BigInt(Number.MAX_SAFE_INTEGER) ? Number(microseconds) : null;
}
export function formatSourceSeconds(microseconds: number): string {
  const seconds = (microseconds / 1e6).toFixed(6);
  const [whole, fraction] = seconds.split('.');
  return `${whole}.${fraction.replace(/0+$/, '').padEnd(3, '0')}`;
}

// 仅核对界面可确认的上下文；源文件、工程实例与权限仍由后端原子校验。
export function reviewContextIssue({
  project,
  projectId,
  revision,
  pendingConflict,
  inspectorDraft,
  timelineGesture,
}: {
  project: Project;
  projectId: string;
  revision: number;
  pendingConflict: boolean;
  inspectorDraft: boolean;
  timelineGesture: boolean;
}): string {
  if (pendingConflict) return '请先处理挂起的工程更改，再核对本次操作。';
  if (timelineGesture) return '请先结束时间线拖动或修剪，再核对本次操作。';
  if (inspectorDraft) return '请先完成属性草稿的提交或修正，再核对本次操作。';
  if (project.id !== projectId || project.revision !== revision)
    return '工程或版本已变化，请重新核对。';
  return '';
}

export function replacementTargetIssue(
  project: Project,
  clipId: string | null,
  candidateKind: string,
  candidateProjectId: string,
): string {
  if (candidateProjectId !== project.id) return '请先打开候选结果所属工程。';
  if (!['image', 'video'].includes(candidateKind)) return '此候选不是可替换镜头的图片或视频。';
  const clip = project.clips.find((value) => value.id === clipId);
  if (!clip || !['image', 'video'].includes(clip.kind)) return '请先选择一个视频或图片片段。';
  const track = project.tracks.find((value) => value.id === clip.trackId);
  if (!track || track.kind !== 'video') return '当前镜头不在可用的视频轨上。';
  if (track.locked) return '当前镜头所在轨道已锁定，请先处理轨道状态。';
  return '';
}

export function originalShotTiming(clip: Clip) {
  return {
    startFrame: clip.startFrame,
    durationFrames: clip.durationFrames,
    sourceInUs: clip.sourceInUs,
    speedNum: clip.speedNum,
    speedDen: clip.speedDen,
  };
}
