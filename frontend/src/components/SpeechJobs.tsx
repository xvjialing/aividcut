import { lazy, Suspense, useState } from 'react';
import type { Project } from '../types';
import type { SpeechAudioImportOptions, SpeechJob, SpeechSummary } from '../speech-types';
import { speechModeNames, speechStatusNames } from '../lib/speech';
import { Button, Field, Modal, Select } from './ui';
import { displayTimingSeconds, speechTimingSummary } from '../lib/speech-timing';
const SpeechAudioResult = lazy(() =>
  import('./SpeechAudioResult').then((module) => ({ default: module.SpeechAudioResult })),
);

export interface SpeechJobActions {
  inspect: (id: string) => void;
  cancel: (id: string) => Promise<boolean>;
  resume: (id: string) => Promise<boolean>;
  acknowledge: (id: string) => Promise<boolean>;
  accept: (id: string, options: { trackId: string; generateCaptions: boolean }) => Promise<boolean>;
  importAudio: (id: string, options: SpeechAudioImportOptions) => Promise<boolean>;
}
export function SpeechJobs({
  jobs,
  detail,
  project,
  busy,
  onAction,
  error,
  prepareProject,
  onOpenCaptions,
}: {
  jobs: SpeechSummary[];
  detail: SpeechJob | null;
  project: Project;
  busy: boolean;
  onAction: SpeechJobActions;
  error?: string;
  prepareProject?: () => Promise<Project | null>;
  onOpenCaptions?: (transcriptId: string) => void;
}) {
  const [limit, setLimit] = useState(12);
  return (
    <div className="speech-jobs">
      {jobs.length ? (
        jobs
          .slice(0, limit)
          .map((job) => (
            <SpeechJobCard
              key={job.id}
              job={job}
              detail={detail?.id === job.id ? detail : null}
              project={project}
              busy={busy}
              onAction={onAction}
              error={error}
              prepareProject={prepareProject}
              onOpenCaptions={onOpenCaptions}
            />
          ))
      ) : (
        <div className="inspector-empty">
          <p>还没有语音任务</p>
          <span>明确确认后提交，结果保存后仍需单独接受或导入。</span>
        </div>
      )}
      {jobs.length > limit ? (
        <Button onClick={() => setLimit((value) => value + 12)}>显示更多语音任务</Button>
      ) : null}
    </div>
  );
}
function SpeechJobCard({
  job,
  detail,
  project,
  busy,
  onAction,
  error,
  prepareProject,
  onOpenCaptions,
}: {
  job: SpeechSummary;
  detail: SpeechJob | null;
  project: Project;
  busy: boolean;
  onAction: SpeechJobActions;
  error?: string;
  prepareProject?: () => Promise<Project | null>;
  onOpenCaptions?: (transcriptId: string) => void;
}) {
  const [confirmation, setConfirmation] = useState<'cancel' | 'ack' | null>(null);
  const [checked, setChecked] = useState(false);
  const [generateCaptions, setGenerateCaptions] = useState(false);
  const [captionTrack, setCaptionTrack] = useState('');
  const [wordLimit, setWordLimit] = useState(40);
  const targetOpen = job.projectId === project.id;
  const active = ['queued', 'preparing', 'submitting', 'running', 'saving', 'cancelling'].includes(
    job.status,
  );
  const transcript = detail?.transcription;
  const wordTime = (value: number | null | undefined) =>
    displayTimingSeconds(value == null ? null : value / 1e6);
  const acceptedWords = (transcript?.words || []).filter((word) => word.type === 'word');
  const canAccept =
    transcript &&
    acceptedWords.length > 0 &&
    acceptedWords.every(
      (word) => word.timingComplete && word.startUs != null && word.endUs != null,
    );
  return (
    <article
      className="speech-job"
      aria-label={`${speechModeNames[job.mode]} ${speechStatusNames[job.status]}`}
    >
      <div className="job-heading">
        <strong>{speechModeNames[job.mode]}</strong>
        <span>{speechStatusNames[job.status]}</span>
      </div>
      <p className="field-hint">
        {job.providerId} · {job.modelId || '音色管理'}
      </p>
      <p>{job.message}</p>
      {job.mode === 'synthesize' ? <p className="field-hint">{speechTimingSummary(job)}</p> : null}
      {job.totalParts > 0 ? (
        <p className="field-hint">
          已保存分段 {job.completedParts} / {job.totalParts}；分段数不代表完成百分比。
        </p>
      ) : null}
      {(job.warnings || []).map((value, index) => (
        <p className="speech-warning" key={index}>
          {value}
        </p>
      ))}
      {job.status === 'unknown' ? (
        <p className="speech-warning">
          供应商可能已受理并计费。请核对服务记录；本面板不会重做未知请求。
        </p>
      ) : null}
      {job.acknowledged ? <p className="field-hint">已记录人工核对，不代表远端已取消。</p> : null}
      <div className="speech-actions">
        <Button variant="ghost" disabled={busy} onClick={() => onAction.inspect(job.id)}>
          {detail ? '重新读取结果' : '查看结果与处理'}
        </Button>
        {active ? (
          <Button
            variant="ghost"
            disabled={busy || job.status === 'cancelling'}
            onClick={() => setConfirmation('cancel')}
          >
            停止本地等待
          </Button>
        ) : null}
        {job.canResumeLocal ? (
          <Button disabled={busy} onClick={() => void onAction.resume(job.id)}>
            恢复本地保存
          </Button>
        ) : null}
        {job.status === 'unknown' && !job.acknowledged ? (
          <Button
            variant="ghost"
            disabled={busy}
            onClick={() => {
              setChecked(false);
              setConfirmation('ack');
            }}
          >
            已核对服务记录…
          </Button>
        ) : null}
      </div>
      {job.canResumeLocal ? (
        <p className="field-hint">恢复仅保存已返回的本地结果，不重新发起收费识别或生成。</p>
      ) : null}
      {detail ? (
        <>
          {detail.parts?.length ? (
            <details className="speech-details">
              <summary>源偏移与分段记录</summary>
              {detail.parts.map((part) => (
                <p key={part.id}>
                  {part.id} · 源偏移 {part.offsetUs} 微秒 · 源长 {part.durationUs} 微秒 ·{' '}
                  {part.status} · {part.message}
                </p>
              ))}
            </details>
          ) : null}
          {transcript ? (
            <section className="speech-transcript-result">
              <h4>原始识别结果</h4>
              <p className="speech-transcript-text">{transcript.text}</p>
              <p className="field-hint">
                源时间戳{transcript.timingComplete ? '完整' : '有缺失'}
                ；缺失项原样保留，声音事件和空白不当作普通字幕词。
              </p>
              {(transcript.words || []).slice(0, wordLimit).map((word, index) => (
                <p className="speech-word-result" key={index}>
                  <strong>{word.text || '空白'}</strong>
                  <span>
                    {word.type}
                    {word.speakerId ? ` · ${word.speakerId}` : ''} · {wordTime(word.startUs)} →{' '}
                    {wordTime(word.endUs)}
                  </span>
                </p>
              ))}
              {(transcript.words || []).length > wordLimit ? (
                <Button variant="ghost" onClick={() => setWordLimit((value) => value + 40)}>
                  再显示 40 个原始词条
                </Button>
              ) : null}
              {job.status === 'succeeded' ? (
                <>
                  <label className="checkbox-field">
                    <input
                      type="checkbox"
                      checked={generateCaptions}
                      disabled={busy || !targetOpen || Boolean(job.acceptedTranscriptId)}
                      onChange={(event) => setGenerateCaptions(event.target.checked)}
                    />
                    接受时生成跟随源剪辑的绑定字幕
                  </label>
                  {generateCaptions ? (
                    <Field label="绑定字幕轨">
                      <Select
                        value={captionTrack}
                        disabled={busy}
                        onChange={(event) => setCaptionTrack(event.target.value)}
                      >
                        <option value="">新建字幕轨</option>
                        {project.tracks
                          .filter((track) => track.kind === 'caption')
                          .map((track) => (
                            <option key={track.id} value={track.id} disabled={track.locked}>
                              {track.name}
                              {track.locked ? '（已锁定）' : ''}
                            </option>
                          ))}
                      </Select>
                    </Field>
                  ) : null}
                  <Button
                    variant="primary"
                    disabled={
                      busy || !targetOpen || !canAccept || Boolean(job.acceptedTranscriptId)
                    }
                    onClick={() =>
                      void onAction.accept(job.id, { trackId: captionTrack, generateCaptions })
                    }
                  >
                    {job.acceptedTranscriptId ? '转写已接受到工程' : '接受源转写到工程'}
                  </Button>
                  {!canAccept ? (
                    <p className="speech-warning">
                      普通词缺少完整源时间，不能接受为绑定字幕；原始证据仍可查看。
                    </p>
                  ) : null}
                </>
              ) : null}
            </section>
          ) : null}
          {job.audio ? (
            <Suspense fallback={<p className="field-hint">读取配音结果面板…</p>}>
              <SpeechAudioResult
                job={job}
                detail={detail}
                project={project}
                busy={busy}
                prepareProject={prepareProject}
                onOpenCaptions={onOpenCaptions}
                onImport={(options) => onAction.importAudio(job.id, options)}
              />
            </Suspense>
          ) : null}
          {job.voice ? (
            <section>
              <h4>{job.voice.name}</h4>
              <p className="speech-warning">
                创建结果：未验证。音色 ID {job.voice.id}；授权声明不等于供应商身份验证。
              </p>
            </section>
          ) : null}
          {!targetOpen ? (
            <p className="speech-warning">
              请打开任务所属工程后接受或导入结果；不会添加到当前其他工程。
            </p>
          ) : null}
        </>
      ) : null}
      <Modal
        open={Boolean(confirmation)}
        title={confirmation === 'ack' ? '记录已核对语音服务记录' : '停止本地等待？'}
        description={
          confirmation === 'ack'
            ? '请先在服务商核查受理与费用。本操作只记录人工核对，不重交、不取消远端、不保证退款。'
            : '仅停止本机等待和后续分段；已发出的请求可能继续处理或计费。'
        }
        busy={busy}
        onOpenChange={(value) => {
          if (!value && !busy) setConfirmation(null);
        }}
      >
        {confirmation === 'ack' ? (
          <label className="checkbox-field">
            <input
              type="checkbox"
              checked={checked}
              onChange={(event) => setChecked(event.target.checked)}
            />
            我已核对供应商受理与费用记录
          </label>
        ) : null}
        <div className="dialog-actions">
          <Button disabled={busy} onClick={() => setConfirmation(null)}>
            返回语音任务
          </Button>
          <Button
            variant="primary"
            disabled={busy || (confirmation === 'ack' && !checked)}
            onClick={() =>
              void (
                confirmation === 'ack' ? onAction.acknowledge(job.id) : onAction.cancel(job.id)
              ).then((success) => {
                if (success) setConfirmation(null);
              })
            }
          >
            {confirmation === 'ack' ? '记录已核对' : '确认停止本地等待'}
          </Button>
        </div>
        {error ? (
          <p className="form-error" role="alert">
            {error}
          </p>
        ) : null}
      </Modal>
    </article>
  );
}
