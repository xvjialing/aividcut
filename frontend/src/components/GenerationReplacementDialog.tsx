import { useEffect, useRef, useState } from 'react';
import type { Bootstrap, Clip, Project } from '../types';
import type { GenerationJob, GenerationOutputAsset } from '../generation-types';
import type {
  GenerationReplacementInput,
  GenerationReplacementPreview,
  ReplacementAPI,
} from '../replacement-types';
import { replacementBridge } from '../lib/bridge';
import { framesToSeconds, timecode } from '../lib/editor';
import {
  formatSourceSeconds,
  replacementTargetIssue,
  sourceSecondsToMicroseconds,
} from '../lib/action-review';
import { Button, Field, Input, Modal, Select } from './ui';
import { SourceRangeMedia } from './SourceRangeMedia';

export function replacementSourceRange(clip: Clip, project: Project) {
  const speed = clip.kind === 'image' ? 1 : clip.speedNum / clip.speedDen;
  const start = clip.sourceInUs / 1e6;
  return {
    start,
    end: start + framesToSeconds(clip.durationFrames, project.settings) * speed,
    speed,
  };
}
export function GenerationReplacementDialog({
  project,
  clipId,
  job,
  candidate,
  onClose,
  onBootstrap,
  prepareProject,
  applyToProject,
  api = replacementBridge,
}: {
  project: Project;
  clipId: string;
  job: GenerationJob;
  candidate: GenerationOutputAsset;
  onClose: () => void;
  onBootstrap: (value: Bootstrap) => void;
  prepareProject?: () => Promise<Project | null>;
  applyToProject?: (action: () => Promise<Bootstrap>) => Promise<Bootstrap | null>;
  api?: ReplacementAPI;
}) {
  const [sourceSeconds, setSourceSeconds] = useState('0.000');
  const [audioMode, setAudioMode] =
    useState<GenerationReplacementInput['audioMode']>('keep_original');
  const [lutMode, setLutMode] = useState<GenerationReplacementInput['lutMode']>('keep');
  const [speedMode, setSpeedMode] = useState<GenerationReplacementInput['speedMode']>(
    candidate.kind === 'image' ? 'normal' : 'preserve',
  );
  const [preview, setPreview] = useState<GenerationReplacementPreview | null>(null);
  const [busy, setBusy] = useState('');
  const pending = useRef(false);
  const [error, setError] = useState('');
  const projectRef = useRef(project);
  projectRef.current = project;
  useEffect(() => {
    setPreview(null);
  }, [project.id, project.revision, candidate.id, job.id, clipId]);
  const original = project.clips.find((clip) => clip.id === clipId);
  const originalAsset = project.assets.find((asset) => asset.id === original?.assetId);
  const targetIssue = replacementTargetIssue(project, clipId, candidate.kind, job.projectId);
  const stale = Boolean(
    preview &&
    (preview.applied ||
      preview.projectId !== project.id ||
      preview.projectRevision !== project.revision ||
      preview.clipId !== clipId ||
      preview.candidateId !== candidate.id ||
      preview.jobId !== job.id),
  );
  function change(action: () => void) {
    action();
    setPreview(null);
    setError('');
  }
  async function inspect() {
    if (pending.current || targetIssue) return;
    const source = candidate.kind === 'image' ? 0 : sourceSecondsToMicroseconds(sourceSeconds);
    if (source === null) {
      setError('候选源入点需为非负秒数，最多六位小数。');
      return;
    }
    pending.current = true;
    setBusy('探测并预览替换');
    setError('');
    try {
      const current = prepareProject ? await prepareProject() : projectRef.current;
      if (!current) {
        setError('请先处理属性草稿、外部冲突或时间线手势。');
        return;
      }
      if (current.id !== project.id || current.revision !== project.revision) {
        setError('工程已变化，请重新核对原镜头。');
        return;
      }
      const value = await api.previewGenerationReplacement({
        jobId: job.id,
        candidateId: candidate.id,
        clipId,
        projectId: current.id,
        projectRevision: current.revision,
        sourceInUs: source,
        audioMode,
        lutMode,
        speedMode: candidate.kind === 'image' ? 'normal' : speedMode,
      });
      setPreview(value);
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : '替换预览失败，工程未修改。');
    } finally {
      pending.current = false;
      setBusy('');
    }
  }
  async function apply() {
    if (!preview || stale || targetIssue || pending.current) return;
    const current = projectRef.current;
    if (current.id !== preview.projectId || current.revision !== preview.projectRevision) {
      setError('工程版本已变化，请重新预览。');
      return;
    }
    pending.current = true;
    setBusy('应用镜头替换');
    setError('');
    try {
      const action = () => api.applyGenerationReplacement(preview.id);
      const result = await (applyToProject ? applyToProject(action) : action());
      if (!result) {
        setError('替换未提交，请核对工程冲突或错误后重试。');
        return;
      }
      if (!applyToProject) onBootstrap(result);
      onClose();
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : '替换失败，现有工程保留。');
    } finally {
      pending.current = false;
      setBusy('');
    }
  }
  return (
    <Modal
      open
      title="用候选替换当前镜头"
      description="保留镜头在时间线上的位置、时长与画面构图，先预览变化再应用，不覆盖原始素材。"
      className="replacement-dialog"
      busy={Boolean(busy)}
      onOpenChange={(value) => {
        if (!value && !busy) onClose();
      }}
    >
      <div className="replacement-dialog-body">
        <div className="replacement-comparison">
          <SourceRangeMedia
            title="原镜头"
            asset={preview?.originalAsset || originalAsset}
            range={
              preview?.originalClip || original
                ? replacementSourceRange((preview?.originalClip || original)!, project)
                : null
            }
          />
          <SourceRangeMedia
            title="候选镜头"
            asset={preview?.candidateAsset}
            range={
              preview?.replacementClip
                ? replacementSourceRange(preview.replacementClip, project)
                : null
            }
          />
        </div>
        {original ? (
          <p className="field-hint">
            保持时间线起点 {timecode(original.startFrame, project.settings)}（{original.startFrame}
            帧）与时长 {original.durationFrames}帧；原素材入点{' '}
            {formatSourceSeconds(original.sourceInUs)}秒。
          </p>
        ) : null}
        <p className="field-hint">源素材对照默认静音，最终画面与声音请在工程预览中核对。</p>
        <div className="replacement-parameters">
          <Field label="候选源入点 · 秒">
            <Input
              type="text"
              inputMode="decimal"
              value={candidate.kind === 'image' ? '0.000' : sourceSeconds}
              disabled={Boolean(busy) || candidate.kind === 'image'}
              onChange={(event) => change(() => setSourceSeconds(event.target.value))}
            />
          </Field>
          <Field label="候选速度">
            <Select
              value={candidate.kind === 'image' ? 'normal' : speedMode}
              disabled={Boolean(busy) || candidate.kind === 'image'}
              onChange={(event) =>
                change(() =>
                  setSpeedMode(event.target.value as GenerationReplacementInput['speedMode']),
                )
              }
            >
              <option value="preserve">沿用原视频速度</option>
              <option value="normal">明确使用1x</option>
            </Select>
          </Field>
          <Field label="声音处理">
            <Select
              value={audioMode}
              disabled={Boolean(busy)}
              onChange={(event) =>
                change(() =>
                  setAudioMode(event.target.value as GenerationReplacementInput['audioMode']),
                )
              }
            >
              <option value="keep_original">保留原镜头声音</option>
              <option value="use_candidate">使用候选声音（需实际音轨）</option>
              <option value="mute">静音相关声音</option>
            </Select>
          </Field>
          <Field label="LUT处理">
            <Select
              value={lutMode}
              disabled={Boolean(busy)}
              onChange={(event) =>
                change(() =>
                  setLutMode(event.target.value as GenerationReplacementInput['lutMode']),
                )
              }
            >
              <option value="keep">保留原LUT</option>
              <option value="clear">清除原LUT</option>
            </Select>
          </Field>
        </div>
        <details className="agent-details">
          <summary>预览与声音说明</summary>
          <p>
            画面对照不含工程LUT、画面变换或字幕合成。原片内嵌声音、链接音轨及来源字幕的处理，以预览变化说明为准；视频余量不足会拒绝，不自动补帧、循环或变速。
          </p>
        </details>
        {preview ? (
          <section className="replacement-effects">
            <h3>{stale ? '上次预览（已过期）' : '预览变化'}</h3>
            <p>{preview.summary}</p>
            <p className="field-hint">
              受影响片段 {preview.affectedClipIds.length} 个 · 工程版本 {preview.projectRevision}
            </p>
            {preview.effects.map((effect, index) => (
              <p key={index}>{effect.description}</p>
            ))}
            {preview.warnings.map((warning, index) => (
              <p className="generation-warning" key={index}>
                {warning}
              </p>
            ))}
            {stale ? (
              <p className="generation-warning">工程、候选或预览状态已变化，不能应用旧预览。</p>
            ) : null}
          </section>
        ) : (
          <p className="field-hint">参数变化后需重新预览。预览本身不导入候选或修改工程。</p>
        )}
      </div>
      {targetIssue || error ? (
        <p className="form-error replacement-dialog-status" role="alert">
          {targetIssue || error}
        </p>
      ) : null}
      <div className="dialog-actions replacement-dialog-actions">
        <Button disabled={Boolean(busy)} onClick={onClose}>
          取消替换
        </Button>
        <Button
          busy={busy === '探测并预览替换'}
          disabled={Boolean(busy) || Boolean(targetIssue)}
          onClick={() => void inspect()}
        >
          预览替换变化
        </Button>
        <Button
          variant="primary"
          busy={busy === '应用镜头替换'}
          disabled={Boolean(busy) || !preview || stale || Boolean(targetIssue)}
          onClick={() => void apply()}
        >
          应用替换并保留撤销
        </Button>
      </div>
    </Modal>
  );
}
