import { useEffect, useRef, useState } from 'react';
import type { Asset } from '../types';
import type { ShotIndexAPI, ShotIndexInput, ShotIndexView } from '../shot-index-types';
import { shotIndexAPI } from '../lib/bridge';
import { formatSourceSeconds } from '../lib/action-review';
import { Button, Field, Input } from './ui';
const defaultAPI: ShotIndexAPI = {
  get: (input) => shotIndexAPI.get(input),
  start: (input) => shotIndexAPI.start(input),
  cancel: (id) => shotIndexAPI.cancel(id),
};
export function ShotIndexPanel({
  projectId,
  sourceEpoch,
  asset,
  onSelect,
  api = defaultAPI,
}: {
  projectId: string;
  sourceEpoch: number;
  asset: Asset;
  onSelect: (position: number) => void;
  api?: ShotIndexAPI;
}) {
  const [opened, setOpened] = useState(false);
  const [threshold, setThreshold] = useState('10'),
    [minimum, setMinimum] = useState('250');
  const [view, setView] = useState<ShotIndexView | null>(null),
    [applied, setApplied] = useState<ShotIndexInput | null>(null);
  const [busy, setBusy] = useState(false),
    [error, setError] = useState(''),
    [limit, setLimit] = useState(20);
  const reads = useRef(0),
    pending = useRef(false),
    alive = useRef(false);
  const scope = JSON.stringify([projectId, sourceEpoch, asset.id, asset.path, asset.fingerprint]);
  const current = useRef(scope);
  current.current = scope;
  const resultScope = useRef('');
  const valid =
    Number.isInteger(Number(threshold)) &&
    threshold.trim() !== '' &&
    Number(threshold) >= 1 &&
    Number(threshold) <= 50 &&
    Number.isInteger(Number(minimum)) &&
    minimum.trim() !== '' &&
    Number(minimum) >= 1 &&
    Number(minimum) <= 5000;
  function input(): ShotIndexInput {
    return {
      projectId,
      sourceEpoch,
      assetId: asset.id,
      fingerprint: asset.fingerprint || '',
      threshold: Number(threshold),
      minDurationUs: Number(minimum) * 1000,
    };
  }
  async function refresh(value: ShotIndexInput, kind: 'get' | 'start' = 'get') {
    const read = ++reads.current,
      captured = scope;
    setBusy(true);
    setError('');
    setApplied(value);
    try {
      const result = await api[kind](value);
      if (!alive.current || read !== reads.current || captured !== current.current) return;
      if (
        result.projectId !== value.projectId ||
        result.sourceEpoch !== value.sourceEpoch ||
        result.assetId !== value.assetId ||
        (result.index &&
          (result.index.sourceFingerprint !== value.fingerprint ||
            result.index.sourceDurationUs !== asset.durationUs ||
            result.index.config.threshold !== value.threshold ||
            result.index.config.minDurationUs !== value.minDurationUs))
      )
        throw Error('镜头索引与当前源或参数不一致，请刷新。');
      resultScope.current = captured;
      setView(result);
    } catch (cause) {
      if (alive.current && read === reads.current && captured === current.current)
        setError(cause instanceof Error ? cause.message : '镜头索引未完成，可重试。');
    } finally {
      if (alive.current && read === reads.current && captured === current.current) setBusy(false);
    }
  }
  useEffect(() => {
    alive.current = true;
    setView(null);
    setBusy(false);
    setApplied(null);
    setLimit(20);
    setError('');
    pending.current = false;
    if (opened && valid) void refresh(input());
    return () => {
      alive.current = false;
      reads.current++;
    };
  }, [scope, api, opened]);
  useEffect(() => {
    if (view?.status !== 'running' || !applied) return;
    const timer = setInterval(() => {
      if (!pending.current) void refresh(applied);
    }, 1000);
    return () => clearInterval(timer);
  }, [view?.status, applied, scope, api]);
  async function start(force = false) {
    if (pending.current || !valid) return;
    pending.current = true;
    try {
      await refresh({ ...input(), force }, 'start');
    } finally {
      if (current.current === scope) pending.current = false;
    }
  }
  async function cancel() {
    if (!view?.job || pending.current) return;
    pending.current = true;
    setError('');
    try {
      await api.cancel(view.job.id);
      if (applied) await refresh(applied);
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : '取消未完成，请刷新实际状态。');
    } finally {
      if (current.current === scope) pending.current = false;
    }
  }
  const shown = resultScope.current === scope ? view : null;
  return (
    <details
      className="agent-details shot-index-panel"
      onToggle={(event) => setOpened(event.currentTarget.open)}
    >
      <summary>本地镜头索引</summary>
      <p className="field-hint">
        本机解码检测视觉变化，不上传、不调用模型或改工程；候选边界需回看，闪光和运动也可能触发。
      </p>
      <form
        noValidate
        onSubmit={(event) => {
          event.preventDefault();
          void start();
        }}
      >
        <div className="field-pair">
          <Field label="变化阈值">
            <Input
              type="number"
              min={1}
              max={50}
              step={1}
              value={threshold}
              disabled={busy || shown?.status === 'running'}
              onChange={(e) => setThreshold(e.target.value)}
            />
          </Field>
          <Field label="最短间隔 · ms">
            <Input
              type="number"
              min={1}
              max={5000}
              step={1}
              value={minimum}
              disabled={busy || shown?.status === 'running'}
              onChange={(e) => setMinimum(e.target.value)}
            />
          </Field>
        </div>
        {!valid ? <p className="form-error">阈值需1–50，间隔需1–5000毫秒整数。</p> : null}
        <div className="speech-actions">
          <Button type="submit" busy={busy} disabled={!valid || shown?.status === 'running'}>
            {shown?.status === 'cached' ? '核对并复用缓存' : '检测/读取镜头'}
          </Button>
          <Button disabled={busy || !valid} onClick={() => void refresh(input())}>
            刷新镜头状态
          </Button>
        </div>
      </form>
      {error ? (
        <p className="form-error" role="alert">
          {error}
        </p>
      ) : null}
      {shown?.reason ? <p className="field-hint">{shown.reason}</p> : null}
      {shown?.status === 'running' ? (
        <>
          <p role="status">
            {shown.job?.message || '本地检测进行中'} · {shown.job?.status}
          </p>
          <Button disabled={busy} onClick={() => void cancel()}>
            取消镜头检测
          </Button>
        </>
      ) : null}
      {shown?.index && shown.status === 'ready' ? (
        <>
          <p role="status">
            {shown.index.shots.length}段候选 · 实际视频覆盖{' '}
            {formatSourceSeconds(shown.index.coverageStartUs)}–
            {formatSourceSeconds(shown.index.coverageEndUs)}秒
          </p>
          <p className="field-hint">
            已检测参数：阈值{shown.index.config.threshold}，最短间隔
            {shown.index.config.minDurationUs / 1000}ms；不代表语义镜头或帧精确剪辑。
          </p>
          <ol className="shot-index-list">
            {shown.index.shots.slice(0, limit).map((shot, index) => (
              <li key={shot.id}>
                <span>
                  候选{index + 1} · {formatSourceSeconds(shot.startUs)}–
                  {formatSourceSeconds(shot.endUs)}秒
                </span>
                <Button onClick={() => onSelect(shot.sampleUs)}>
                  浏览该段取样 · {formatSourceSeconds(shot.sampleUs)}秒
                </Button>
              </li>
            ))}
          </ol>
          {limit < shown.index.shots.length ? (
            <Button onClick={() => setLimit((value) => value + 20)}>显示更多候选镜头</Button>
          ) : null}
          <Button disabled={busy || !valid} onClick={() => void start(true)}>
            重新检测镜头
          </Button>
        </>
      ) : null}
    </details>
  );
}
