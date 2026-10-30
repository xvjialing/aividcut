import { useId, useRef, useState } from 'react';
import type { Project } from '../types';
import type { ConnectionProfile } from '../generation-types';
import type { SpeechCatalog, SpeechDraftRequest, SpeechMode, SpeechVoice } from '../speech-types';
import {
  speechConnections,
  speechSources,
  speechUnavailable,
  validateSpeechRequest,
  speechOutputFormatLabel,
} from '../lib/speech';
import { Button, Field, Input, Select } from './ui';

export function SpeechCreationForm({
  mode,
  catalog,
  connections,
  project,
  voices,
  busy,
  onReview,
  connectionId,
  onConnectionChange,
}: {
  mode: SpeechMode;
  catalog: SpeechCatalog;
  connections: ConnectionProfile[];
  project: Project;
  voices: SpeechVoice[];
  busy: boolean;
  onReview: (request: SpeechDraftRequest) => void;
  connectionId?: string;
  onConnectionChange?: (value: string) => void;
}) {
  const profiles = speechConnections(connections, catalog, mode);
  const [localConnectionID, setConnectionID] = useState(
    profiles.find((value) => value.configured)?.id || profiles[0]?.id || '',
  );
  const connectionID = connectionId ?? localConnectionID;
  const profile = profiles.find((value) => value.id === connectionID);
  const [source, setSource] = useState('');
  const [language, setLanguage] = useState('');
  const [granularity, setGranularity] = useState(catalog.timestampGranularities[0] || 'word');
  const [diarize, setDiarize] = useState(false);
  const [tagAudioEvents, setTagAudioEvents] = useState(false);
  const [text, setText] = useState('');
  const [voice, setVoice] = useState('');
  const [format, setFormat] = useState(catalog.outputFormats[0] || '');
  const [withTiming, setWithTiming] = useState(false);
  const [name, setName] = useState('');
  const [description, setDescription] = useState('');
  const [samples, setSamples] = useState<string[]>([]);
  const [authority, setAuthority] = useState<'self' | 'authorized'>('self');
  const [subject, setSubject] = useState('');
  const [confirmed, setConfirmed] = useState(false);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const form = useRef<HTMLFormElement>(null);
  const prefix = useId();
  const unavailable = speechUnavailable(catalog, mode);
  const feedback = (field: string) =>
    errors[field] ? (
      <span id={`${prefix}-${field}`} className="form-error">
        {errors[field]}
      </span>
    ) : null;
  const invalid = (field: string) => ({
    'aria-invalid': Boolean(errors[field]),
    'aria-describedby': errors[field] ? `${prefix}-${field}` : undefined,
  });
  function submit(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const request: SpeechDraftRequest = { connectionId: connectionID, mode };
    if (mode === 'transcribe')
      request.transcribe = {
        assetId: source,
        modelId: profile?.modelId,
        language: language.trim() || undefined,
        granularity,
        diarize,
        tagAudioEvents,
      };
    else if (mode === 'synthesize')
      request.synthesize = {
        modelId: profile?.modelId,
        voiceId: voice,
        text,
        outputFormat: format,
        ...(withTiming ? { withTiming: true } : {}),
      };
    else
      request.clone = {
        name: name.trim(),
        description: description.trim() || undefined,
        assetIds: samples,
        consent: { confirmed, subject: subject.trim(), authority },
      };
    const validation = validateSpeechRequest(request, catalog, profile, project.assets);
    if (mode === 'synthesize' && !voices.some((value) => value.id === voice))
      validation.voice = '所选音色已不在当前查询结果中，请重新选择。';
    setErrors(validation);
    if (Object.keys(validation).length) {
      setTimeout(
        () => form.current?.querySelector<HTMLElement>('[aria-invalid="true"]')?.focus(),
        0,
      );
      return;
    }
    onReview(request);
  }
  return (
    <form ref={form} noValidate className="speech-form" onSubmit={submit}>
      <Field label="语音模型连接">
        <Select
          value={connectionID}
          disabled={busy}
          {...invalid('connection')}
          onChange={(event) => {
            setConnectionID(event.target.value);
            onConnectionChange?.(event.target.value);
            setVoice('');
          }}
        >
          <option value="">选择已配置连接</option>
          {profiles.map((value) => (
            <option value={value.id} key={value.id}>
              {value.name} · {value.modelId} · {value.configured ? '已配置，未验证' : '无凭据'}
            </option>
          ))}
        </Select>
        {feedback('connection')}
      </Field>
      {unavailable ? <p className="speech-warning">{unavailable}</p> : null}
      {mode === 'transcribe' ? (
        <>
          <Field label="转写源素材">
            <Select
              value={source}
              disabled={busy}
              {...invalid('source')}
              onChange={(event) => setSource(event.target.value)}
            >
              <option value="">选择已有音视频素材</option>
              {speechSources(project.assets).map((asset) => (
                <option value={asset.id} key={asset.id}>
                  {asset.name}
                </option>
              ))}
            </Select>
            {feedback('source')}
          </Field>
          <Field label="语言代码" hint="留空自动识别；例如 zh、en。">
            <Input
              value={language}
              disabled={busy}
              {...invalid('language')}
              onChange={(event) => setLanguage(event.target.value)}
            />
            {feedback('language')}
          </Field>
          <Field label="时间戳粒度">
            <Select
              value={granularity}
              disabled={busy}
              {...invalid('granularity')}
              onChange={(event) => setGranularity(event.target.value)}
            >
              {catalog.timestampGranularities.map((value) => (
                <option value={value} key={value}>
                  {value === 'word' ? '词' : value === 'character' ? '字符与词' : value}
                </option>
              ))}
            </Select>
            {feedback('granularity')}
          </Field>
          <label className="checkbox-field">
            <input
              type="checkbox"
              checked={diarize}
              disabled={busy || !catalog.diarization.supported}
              onChange={(event) => setDiarize(event.target.checked)}
            />
            区分说话人（若供应商返回）
          </label>
          <label className="checkbox-field">
            <input
              type="checkbox"
              checked={tagAudioEvents}
              disabled={busy}
              onChange={(event) => setTagAudioEvents(event.target.checked)}
            />
            保留笑声等声音事件
          </label>
          <p className="field-hint">
            上传已登记源素材的音频内容进行识别。时间以源素材为准；缺失时间不会补成零，识别完成不会自动覆盖字幕。
          </p>
        </>
      ) : mode === 'synthesize' ? (
        <>
          <Field label="配音音色">
            <Select
              value={voice}
              disabled={busy}
              {...invalid('voice')}
              onChange={(event) => setVoice(event.target.value)}
            >
              <option value="">先查询并选择音色</option>
              {voices.map((value) => (
                <option value={value.id} key={value.id}>
                  {value.name} ·{' '}
                  {value.verificationState === 'verified' ? '供应商报告验证' : '未验证'}
                </option>
              ))}
            </Select>
            {feedback('voice')}
          </Field>
          <Field label="配音文本">
            <textarea
              className="input resize-none speech-text"
              rows={6}
              value={text}
              disabled={busy}
              {...invalid('text')}
              onChange={(event) => setText(event.target.value)}
            />
            {feedback('text')}
          </Field>
          <Field label="音频格式">
            <Select
              value={format}
              disabled={busy}
              {...invalid('outputFormat')}
              onChange={(event) => setFormat(event.target.value)}
            >
              {catalog.outputFormats.map((value) => (
                <option key={value} value={value}>
                  {speechOutputFormatLabel(value)}
                </option>
              ))}
            </Select>
            {feedback('outputFormat')}
          </Field>
          <p className="field-hint">
            输出保存到本机后可试听，按真实探测时长导入；不按文字长度估算对齐。
          </p>
          <label className="checkbox-field">
            <input
              type="checkbox"
              checked={withTiming}
              disabled={busy || (!catalog.speechTiming?.supported && !withTiming)}
              aria-describedby={errors.withTiming ? `${prefix}-withTiming` : undefined}
              aria-invalid={Boolean(errors.withTiming)}
              onChange={(event) => {
                setWithTiming(event.target.checked);
                setErrors((current) => ({ ...current, withTiming: '' }));
              }}
            />
            获取配音时间戳
          </label>
          {feedback('withTiming')}
          <p className="field-hint">
            {catalog.speechTiming?.supported
              ? '使用一次带时间的配音生成；不会另做语音识别、自动回退或补造时间。'
              : catalog.speechTiming?.reason || '当前服务未提供配音时间戳，仍可生成普通音频。'}
          </p>
        </>
      ) : (
        <>
          <Field label="音色名称">
            <Input
              value={name}
              disabled={busy}
              {...invalid('name')}
              onChange={(event) => setName(event.target.value)}
            />
            {feedback('name')}
          </Field>
          <Field label="音色描述">
            <textarea
              rows={3}
              className="input resize-none"
              value={description}
              disabled={busy}
              {...invalid('description')}
              onChange={(event) => setDescription(event.target.value)}
            />
            {feedback('description')}
          </Field>
          <fieldset className="speech-samples" disabled={busy}>
            <legend>已登记音频样本 · 最多 {catalog.maxCloneFiles} 个</legend>
            {speechSources(project.assets, true).map((asset) => (
              <label key={asset.id} className="checkbox-field">
                <input
                  type="checkbox"
                  checked={samples.includes(asset.id)}
                  disabled={!samples.includes(asset.id) && samples.length >= catalog.maxCloneFiles}
                  onChange={(event) =>
                    setSamples((current) =>
                      event.target.checked
                        ? [...current, asset.id]
                        : current.filter((id) => id !== asset.id),
                    )
                  }
                />
                {asset.name}
              </label>
            ))}
            {feedback('samples')}
          </fieldset>
          <Field label="授权关系">
            <Select
              value={authority}
              disabled={busy}
              onChange={(event) => {
                setAuthority(event.target.value as 'self' | 'authorized');
                setConfirmed(false);
              }}
            >
              <option value="self">本人的声音</option>
              <option value="authorized">已获得声音主体授权</option>
            </Select>
          </Field>
          <Field label="声音主体">
            <Input
              value={subject}
              disabled={busy}
              {...invalid('consent')}
              onChange={(event) => {
                setSubject(event.target.value);
                setConfirmed(false);
              }}
            />
            {feedback('consent')}
          </Field>
          <label className="checkbox-field">
            <input
              type="checkbox"
              checked={confirmed}
              disabled={busy}
              onChange={(event) => setConfirmed(event.target.checked)}
            />
            我确认是本人或已获明确授权，允许上传所选样本创建音色
          </label>
          <p className="speech-warning">
            这是你的授权声明，创建成功仍为未验证音色，不代表身份或权限验证完成。
          </p>
        </>
      )}
      <p className="speech-warning">
        提交将向语音供应商上传所选内容，可能按其规则计费；下一步会核对本次上传与操作。
      </p>
      <Button
        variant="primary"
        type="submit"
        disabled={busy || !profile?.configured || Boolean(unavailable)}
      >
        提交前核对…
      </Button>
    </form>
  );
}
