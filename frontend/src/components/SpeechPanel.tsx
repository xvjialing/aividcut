import { useCallback, useEffect, useRef, useState } from 'react';
import { RefreshCw, Settings2 } from 'lucide-react';
import type { Bootstrap, Project } from '../types';
import type { ConnectionProfile } from '../generation-types';
import type {
  SpeechAPI,
  SpeechCatalog,
  SpeechDraftRequest,
  SpeechJob,
  SpeechMode,
  SpeechSummary,
  SpeechVoicePage,
  StartSpeechInput,
} from '../speech-types';
import { speechBridge } from '../lib/bridge';
import {
  speechConnections,
  speechModeNames,
  speechModelOptions,
  speechOutputFormatLabel,
} from '../lib/speech';
import { ConnectionsDialog } from './ConnectionsDialog';
import { SpeechCreationForm } from './SpeechCreationForm';
import { SourceTranscriptEditor } from './SourceTranscriptEditor';
import { SpeechJobs } from './SpeechJobs';
import { Button, Field, Input, Modal, Select } from './ui';
import type { TranscriptWordSelection } from '../transcript-cut-types';

type Review = { input: StartSpeechInput; projectId: string; revision: number };
export function SpeechPanel({
  project,
  onOpenConnections,
  connectionsVersion = 0,
  onBootstrap,
  applyToProject,
  prepareProject,
  api = speechBridge,
  sourceEpoch,
  onLocateWords,
}: {
  project: Project;
  onOpenConnections?: () => void;
  connectionsVersion?: number;
  onBootstrap: (value: Bootstrap) => void;
  applyToProject?: (label: string, action: () => Promise<Bootstrap>) => Promise<Bootstrap | null>;
  prepareProject?: () => Promise<Project | null>;
  api?: SpeechAPI;
  sourceEpoch?: number;
  onLocateWords?: (value: TranscriptWordSelection) => void;
}) {
  const [catalog, setCatalog] = useState<SpeechCatalog | null>(null);
  const [connections, setConnections] = useState<ConnectionProfile[]>([]);
  const [jobs, setJobs] = useState<SpeechSummary[]>([]);
  const [detail, setDetail] = useState<SpeechJob | null>(null);
  const [view, setView] = useState<'transcribe' | 'synthesize' | 'voices' | 'jobs'>('transcribe');
  const [focusTranscriptId, setFocusTranscriptId] = useState<string | undefined>();
  const [connectionID, setConnectionID] = useState('');
  const [showConnections, setShowConnections] = useState(false);
  const [voiceQuery, setVoiceQuery] = useState('');
  const [voicePage, setVoicePage] = useState<SpeechVoicePage>({ voices: [], hasMore: false });
  const [voiceConnection, setVoiceConnection] = useState('');
  const [committedVoiceQuery, setCommittedVoiceQuery] = useState('');
  const [loaded, setLoaded] = useState(false);
  const [loading, setLoading] = useState(false);
  const [busy, setBusy] = useState('');
  const pending = useRef(false);
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const [review, setReview] = useState<Review | null>(null);
  const uncertain = useRef<Review | null>(null);
  const projectRef = useRef(project);
  projectRef.current = project;
  const alive = useRef(false);
  const reads = useRef(0);
  const detailReads = useRef(0);
  const voiceReads = useRef(0);
  const mode: SpeechMode =
    view === 'voices' ? 'clone' : view === 'synthesize' ? 'synthesize' : 'transcribe';
  const profiles = catalog ? speechConnections(connections, catalog, mode) : [];
  const selected = profiles.find((value) => value.id === connectionID);
  const refreshJobs = useCallback(async () => {
    const token = ++reads.current;
    const values = (await api.listSpeechJobs()) || [];
    if (alive.current && token === reads.current) setJobs(values);
    return values;
  }, [api]);
  const load = useCallback(async () => {
    setLoading(true);
    setError('');
    try {
      const [capabilities, profiles] = await Promise.all([
        api.getSpeechCatalog(),
        api.getConnections(),
      ]);
      if (!alive.current) return;
      setCatalog(capabilities);
      setConnections(
        (profiles || []).filter((profile) => profile.providerId === capabilities.providerId),
      );
      await refreshJobs();
      if (alive.current) setLoaded(true);
    } catch (cause) {
      if (alive.current) setError(cause instanceof Error ? cause.message : '语音能力读取失败。');
    } finally {
      if (alive.current) setLoading(false);
    }
  }, [api, refreshJobs]);
  useEffect(() => {
    alive.current = true;
    void load();
    return () => {
      alive.current = false;
      reads.current++;
      detailReads.current++;
      voiceReads.current++;
    };
  }, [load]);
  useEffect(() => {
    if (!loaded) return;
    const interval = window.setInterval(() => {
      void refreshJobs().catch((cause) => {
        if (alive.current) setError(cause instanceof Error ? cause.message : '无法读取语音任务。');
      });
    }, 2000);
    return () => clearInterval(interval);
  }, [loaded, refreshJobs]);
  useEffect(() => {
    if (loaded)
      void refreshJobs().catch((cause) =>
        setError(cause instanceof Error ? cause.message : '无法核对工程导入状态。'),
      );
  }, [loaded, project.id, project.revision, refreshJobs]);
  useEffect(() => {
    if (!catalog) return;
    const choices = speechConnections(connections, catalog, mode);
    setConnectionID((current) =>
      choices.some((value) => value.id === current)
        ? current
        : choices.find((value) => value.configured)?.id || choices[0]?.id || '',
    );
  }, [mode, catalog, connections]);
  useEffect(() => {
    setReview(null);
    setDetail(null);
  }, [project.id]);
  async function inspect(id: string) {
    const token = ++detailReads.current;
    setError('');
    try {
      const value = await api.getSpeechJob(id);
      if (alive.current && token === detailReads.current) setDetail(value);
    } catch (cause) {
      if (alive.current && token === detailReads.current)
        setError(cause instanceof Error ? cause.message : '无法读取语音结果。');
    }
  }
  async function queryVoices(more = false) {
    if (pending.current || !selected?.configured || !catalog?.voiceListing.supported) return;
    if (
      more &&
      (!voicePage.hasMore || !voicePage.nextPageToken || voiceConnection !== connectionID)
    )
      return;
    pending.current = true;
    setBusy('读取音色');
    setError('');
    const connection = connectionID;
    const search = more ? committedVoiceQuery : voiceQuery.trim();
    const token = ++voiceReads.current;
    try {
      const value = await api.listVoices(connection, {
        search,
        pageSize: 20,
        ...(more ? { nextPageToken: voicePage.nextPageToken } : {}),
      });
      if (alive.current && token === voiceReads.current) {
        setVoiceConnection(connection);
        setCommittedVoiceQuery(search);
        setVoicePage((current) => ({
          ...value,
          voices: more
            ? [
                ...current.voices,
                ...(value.voices || []).filter(
                  (voice) => !current.voices.some((item) => item.id === voice.id),
                ),
              ]
            : value.voices || [],
        }));
        if (value.hasMore && !value.nextPageToken)
          setError('供应商报告有更多音色但没有翻页标识，未自动猜测下一页。');
      }
    } catch (cause) {
      if (alive.current) setError(cause instanceof Error ? cause.message : '音色查询失败。');
    } finally {
      pending.current = false;
      if (alive.current) setBusy('');
    }
  }
  async function openReview(input: SpeechDraftRequest) {
    if (pending.current) return;
    pending.current = true;
    setBusy('核对工程');
    setError('');
    try {
      const current = prepareProject ? await prepareProject() : projectRef.current;
      if (!current || !alive.current) return;
      if (current.id !== project.id || current.revision !== project.revision) {
        setError('工程已变化，请重新核对上传素材或配音内容。');
        return;
      }
      const previous = uncertain.current;
      const signature = (value: SpeechDraftRequest) =>
        JSON.stringify({
          connectionId: value.connectionId,
          mode: value.mode,
          transcribe: value.transcribe,
          synthesize: value.synthesize,
          clone: value.clone,
        });
      let id: string = crypto.randomUUID();
      if (
        previous &&
        previous.projectId === current.id &&
        previous.revision === current.revision &&
        signature(previous.input) === signature(input)
      ) {
        if (jobs.some((job) => job.id === previous.input.id)) {
          uncertain.current = null;
          setView('jobs');
          setNotice('已找到上次提交记录，请先核对；没有重新上传。');
          return;
        }
        id = previous.input.id;
      }
      setReview({
        input: { ...input, id, consent: false },
        projectId: current.id,
        revision: current.revision,
      });
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : '无法核对当前工程。');
    } finally {
      pending.current = false;
      if (alive.current) setBusy('');
    }
  }
  async function start() {
    if (!review || pending.current) return;
    if (
      review.projectId !== projectRef.current.id ||
      review.revision !== projectRef.current.revision
    ) {
      setReview(null);
      setError('工程版本已变化，请重新核对本次上传。');
      return;
    }
    pending.current = true;
    setBusy('提交语音任务');
    setError('');
    try {
      // 只在明确确认本次上传、用途和可能计费后授权。
      const value = await api.startSpeechJob({ ...review.input, consent: true });
      uncertain.current = null;
      reads.current++;
      if (alive.current) {
        setJobs((current) => [value, ...current.filter((job) => job.id !== value.id)]);
        setReview(null);
        setView('jobs');
        setNotice('语音任务已登记，结果不会自动写入时间线。');
      }
    } catch (cause) {
      uncertain.current = review;
      if (alive.current) {
        setReview(null);
        setError(
          `${cause instanceof Error ? cause.message : '提交结果未明确。'} 请核对语音任务和服务记录；不会自动重交。`,
        );
      }
    } finally {
      pending.current = false;
      if (alive.current) setBusy('');
    }
  }
  async function jobAction(id: string, action: () => Promise<SpeechSummary>): Promise<boolean> {
    if (pending.current) return false;
    pending.current = true;
    setBusy('更新语音任务');
    setError('');
    try {
      const value = await action();
      reads.current++;
      if (alive.current) setJobs((current) => current.map((job) => (job.id === id ? value : job)));
      return true;
    } catch (cause) {
      if (alive.current) setError(cause instanceof Error ? cause.message : '语音任务操作失败。');
      return false;
    } finally {
      pending.current = false;
      if (alive.current) setBusy('');
    }
  }
  async function writeProject(
    label: string,
    action: () => Promise<Bootstrap>,
    jobId?: string,
  ): Promise<Bootstrap | null> {
    if (pending.current) return null;
    if (jobId && !jobs.some((job) => job.id === jobId && job.projectId === projectRef.current.id)) {
      setError('请先打开语音任务所属工程。');
      return null;
    }
    pending.current = true;
    setBusy(label);
    setError('');
    try {
      const value = await (applyToProject ? applyToProject(label, action) : action());
      if (!value) return null;
      if (value.project.id !== projectRef.current.id) {
        setError('当前工程已切换，请在原工程核对结果。');
        return null;
      }
      if (!applyToProject) onBootstrap(value);
      setNotice(`${label}已完成，可使用撤销恢复。`);
      try {
        await refreshJobs();
      } catch {
        setError('工程修改已提交，但语音任务暂时无法刷新，请稍后读取。');
      }
      return value;
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : '工程修改未完成。');
      return null;
    } finally {
      pending.current = false;
      if (alive.current) setBusy('');
    }
  }
  const connectionChanged = (profile?: ConnectionProfile) => {
    if (profile) {
      setConnections((current) =>
        [...current.filter((value) => value.id !== profile.id), profile].filter(
          (value) => value.providerId === catalog?.providerId,
        ),
      );
      setConnectionID(profile.providerId === catalog?.providerId ? profile.id : '');
    } else
      void api
        .getConnections()
        .then((values) => {
          if (alive.current) {
            const profiles = (values || []).filter(
              (value) => value.providerId === catalog?.providerId,
            );
            setConnections(profiles);
            setConnectionID((current) =>
              profiles.some((profile) => profile.id === current) ? current : profiles[0]?.id || '',
            );
          }
        })
        .catch((cause) => setError(cause instanceof Error ? cause.message : '连接读取失败。'));
  };
  useEffect(() => {
    if (connectionsVersion > 0) connectionChanged();
  }, [connectionsVersion]);

  const visibleVoices = voiceConnection === connectionID ? voicePage.voices : [];
  return (
    <div className="speech-panel">
      <div className="speech-toolbar">
        <div role="group" aria-label="语音面板视图" className="speech-view-switch">
          {(
            [
              ['transcribe', '转写'],
              ['synthesize', '配音'],
              ['voices', '音色'],
              ['jobs', `任务${jobs.length ? ` ${jobs.length}` : ''}`],
            ] as const
          ).map(([value, label]) => (
            <Button
              key={value}
              variant={view === value ? 'outline' : 'ghost'}
              aria-pressed={view === value}
              onClick={() => {
                setFocusTranscriptId(undefined);
                setView(value);
              }}
            >
              {label}
            </Button>
          ))}
        </div>
        <Button
          variant="ghost"
          disabled={!loaded || Boolean(busy)}
          onClick={() => (onOpenConnections ? onOpenConnections() : setShowConnections(true))}
        >
          <Settings2 size={13} />
          管理连接
        </Button>
      </div>
      {error ? (
        <p className="form-error" role="alert">
          {error}
        </p>
      ) : null}
      {notice ? (
        <p className="speech-notice" role="status">
          {notice}
        </p>
      ) : null}
      {!loaded || !catalog ? (
        <div className="inspector-empty">
          <p>{loading ? '读取语音能力中' : '语音接口暂不可用'}</p>
          <Button busy={loading} onClick={() => void load()}>
            重新读取
          </Button>
        </div>
      ) : view === 'jobs' ? (
        <>
          <Button
            variant="ghost"
            onClick={() =>
              void refreshJobs().catch((cause) =>
                setError(cause instanceof Error ? cause.message : '任务刷新失败。'),
              )
            }
          >
            <RefreshCw size={12} />
            刷新语音任务
          </Button>
          <SpeechJobs
            jobs={jobs}
            detail={detail}
            error={error}
            project={project}
            busy={Boolean(busy)}
            prepareProject={prepareProject}
            onOpenCaptions={(id) => {
              setFocusTranscriptId(id);
              setView('transcribe');
            }}
            onAction={{
              inspect: (id) => void inspect(id),
              cancel: (id) => jobAction(id, () => api.cancelSpeechJob(id)),
              resume: (id) => jobAction(id, () => api.resumeSpeechLocal(id)),
              acknowledge: (id) => jobAction(id, () => api.acknowledgeSpeechUnknown(id)),
              accept: async (id, options) =>
                Boolean(
                  await writeProject(
                    '接受源转写',
                    () => api.acceptSpeechTranscript(id, options),
                    id,
                  ),
                ),
              importAudio: async (id, options) =>
                Boolean(
                  await writeProject('导入配音', () => api.importSpeechAudio(id, options), id),
                ),
            }}
          />
        </>
      ) : (
        <>
          {view !== 'transcribe' ? (
            <section className="speech-voice-browser" aria-label="供应商音色查询">
              <h3>查询可用音色</h3>
              <Field label="音色查询连接">
                <Select
                  value={connectionID}
                  disabled={Boolean(busy)}
                  onChange={(event) => setConnectionID(event.target.value)}
                >
                  <option value="">选择语音连接</option>
                  {profiles.map((profile) => (
                    <option key={profile.id} value={profile.id}>
                      {profile.name} · {profile.modelId}
                    </option>
                  ))}
                </Select>
              </Field>
              <Field label="搜索音色名称">
                <Input
                  value={voiceQuery}
                  disabled={Boolean(busy)}
                  onChange={(event) => setVoiceQuery(event.target.value)}
                />
              </Field>
              <Button
                disabled={!selected?.configured || Boolean(busy) || !catalog.voiceListing.supported}
                onClick={() => void queryVoices()}
              >
                查询音色
              </Button>
              {!catalog.voiceListing.supported ? (
                <p className="speech-warning">{catalog.voiceListing.reason}</p>
              ) : null}
              {visibleVoices.length ? (
                <ul className="speech-voices">
                  {visibleVoices.map((voice) => (
                    <li key={voice.id}>
                      <strong>{voice.name}</strong>
                      <span>
                        {voice.id} · 验证状态：{voice.verificationState || 'unverified'}
                      </span>
                      {voice.requiresVerification ? <span>供应商要求额外验证</span> : null}
                    </li>
                  ))}
                </ul>
              ) : (
                <p className="field-hint">尚未查询到此连接的音色；不会自动访问供应商。</p>
              )}
              {voiceConnection === connectionID && voicePage.hasMore ? (
                <Button
                  disabled={Boolean(busy) || !voicePage.nextPageToken}
                  onClick={() => void queryVoices(true)}
                >
                  下一页音色
                </Button>
              ) : null}
            </section>
          ) : null}
          <SpeechCreationForm
            key={`${project.id}:${mode}`}
            mode={mode}
            catalog={catalog}
            connections={connections}
            connectionId={connectionID}
            onConnectionChange={setConnectionID}
            project={project}
            voices={visibleVoices}
            busy={Boolean(busy)}
            onReview={(input) => void openReview(input)}
          />
          {view === 'transcribe' ? (
            <SourceTranscriptEditor
              sourceEpoch={sourceEpoch}
              onLocateWords={onLocateWords}
              applyWordCut={(action) => writeProject('按文字删减', action)}
              focusTranscriptId={focusTranscriptId}
              project={project}
              busy={Boolean(busy)}
              onCorrect={async (input) => {
                const value = await writeProject('保存源词纠错', () =>
                  api.correctSourceTranscript(input),
                );
                return (
                  value?.project.transcripts?.find(
                    (transcript) => transcript.id === input.transcriptId,
                  ) || null
                );
              }}
              prepareProject={prepareProject}
              onLayoutPreview={(input) => api.previewCaptionLayout(input)}
              onLayoutApply={(id) => writeProject('应用字幕分句', () => api.applyCaptionLayout(id))}
              onDetach={async (id) =>
                Boolean(
                  await writeProject('解绑字幕', () =>
                    api.detachCaption({ projectId: project.id, clipId: id }),
                  ),
                )
              }
            />
          ) : null}
        </>
      )}
      <ConnectionsDialog
        open={showConnections}
        onOpenChange={setShowConnections}
        connections={connections}
        catalog={[]}
        modelOptions={catalog ? speechModelOptions(catalog) : []}
        api={api}
        onChanged={connectionChanged}
      />
      <Modal
        open={Boolean(review)}
        title="确认上传与语音任务"
        description="确认后会将本次所选内容发送给语音供应商，可能按其规则计费。结果需要你单独接受或导入工程。"
        busy={Boolean(busy)}
        onOpenChange={(value) => {
          if (!value && !busy) setReview(null);
        }}
      >
        {review ? (
          <>
            <dl className="speech-review">
              <dt>供应商 / 模型 / 连接</dt>
              <dd>
                {catalog?.providerId} /{' '}
                {connections.find((value) => value.id === review.input.connectionId)?.modelId} /{' '}
                {connections.find((value) => value.id === review.input.connectionId)?.name}
              </dd>
              <dt>用途</dt>
              <dd>{speechModeNames[review.input.mode]}</dd>
              {review.input.mode === 'synthesize' ? (
                <>
                  <dt>配音时间戳</dt>
                  <dd>
                    {review.input.synthesize?.withTiming
                      ? '已请求，一次带时间的配音生成；不额外识别或自动回退。'
                      : '未请求，仅生成配音音频。'}
                  </dd>
                </>
              ) : null}
              <dt>上传内容</dt>
              <dd>
                {review.input.mode === 'synthesize'
                  ? review.input.synthesize?.text
                  : (review.input.mode === 'transcribe'
                      ? [review.input.transcribe!.assetId]
                      : review.input.clone!.assetIds
                    )
                      .map((id) => project.assets.find((asset) => asset.id === id)?.name || id)
                      .join('、')}
              </dd>
              <dt>参数</dt>
              <dd>
                {review.input.transcribe
                  ? `${review.input.transcribe.language || '自动识别语言'} · ${review.input.transcribe.granularity} · 说话人分离${review.input.transcribe.diarize ? '开' : '关'} · 声音事件${review.input.transcribe.tagAudioEvents ? '开' : '关'}`
                  : review.input.synthesize
                    ? `${review.input.synthesize.voiceId} · ${speechOutputFormatLabel(review.input.synthesize.outputFormat || '')}`
                    : `${review.input.clone?.name} · 主体${review.input.clone?.consent.subject} · ${review.input.clone?.consent.authority === 'self' ? '本人声明' : '授权声明'}`}
              </dd>
            </dl>
            {review.input.mode === 'clone' ? (
              <p className="speech-warning">
                你声明拥有本人或被授权使用这些样本的权利。创建音色仍不代表身份验证完成。
              </p>
            ) : null}
            <p className="speech-warning">
              实际费用与远端处理以供应商规则为准。停止本地等待不保证远端取消或退款；当前没有可验证的报价。
            </p>
            {uncertain.current?.input.id === review.input.id ? (
              <p className="speech-warning">沿用上次请求编号核对，本地服务去重，不自动重复上传。</p>
            ) : null}
            <div className="dialog-actions">
              <Button disabled={Boolean(busy)} onClick={() => setReview(null)}>
                返回修改
              </Button>
              <Button variant="primary" busy={Boolean(busy)} onClick={() => void start()}>
                确认上传并提交（可能计费）
              </Button>
            </div>
          </>
        ) : null}
      </Modal>
    </div>
  );
}
