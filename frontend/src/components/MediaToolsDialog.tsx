import { useEffect, useRef, useState } from 'react';
import type {
  MediaToolKey,
  MediaToolsAPI,
  MediaToolsInspection,
  MediaToolsView,
  MediaToolSettings,
} from '../media-tools-types';
import { mediaToolsAPI, isDesktop } from '../lib/bridge';
import { Button, Field, Input, Modal } from './ui';
const labels: Record<MediaToolKey, string> = {
  ffmpeg: 'FFmpeg程序',
  ffprobe: 'FFprobe程序',
  worker: '原生媒体程序（实验）',
  repository: '引擎模块目录',
  profiles: '引擎配置目录',
};
const origins: Record<string, string> = {
  environment: '环境变量覆盖',
  saved: '已保存设置',
  bundled: '随包工具',
  bundled_invalid: '随包校验失败',
  path: 'PATH自动发现',
  unset: '未配置',
};
const empty: MediaToolSettings = {
  ffmpeg: '',
  ffprobe: '',
  worker: '',
  repository: '',
  profiles: '',
};
export function MediaToolsDialog({
  onClose,
  onReturnFocus,
  api = mediaToolsAPI,
}: {
  onClose: () => void;
  onReturnFocus: () => void;
  api?: MediaToolsAPI;
}) {
  const [view, setView] = useState<MediaToolsView | null>(null),
    [draft, setDraft] = useState(empty),
    [inspection, setInspection] = useState<MediaToolsInspection | null>(null),
    [busy, setBusy] = useState('读取'),
    [error, setError] = useState(''),
    [notice, setNotice] = useState('');
  const epoch = useRef(0),
    manualDraft = useRef<MediaToolSettings>(empty),
    pending = useRef(false),
    alive = useRef(true);
  useEffect(() => {
    alive.current = true;
    const request = ++epoch.current;
    setBusy('读取');
    void api
      .get()
      .then((value) => {
        if (alive.current && request === epoch.current) {
          setView(value);
          setDraft(value.saved);
          if (!value.saved.useBundled) manualDraft.current = value.saved;
        }
      })
      .catch((e) => {
        if (alive.current && request === epoch.current)
          setError(e instanceof Error ? e.message : '设置读取失败，请重试。');
      })
      .finally(() => {
        if (alive.current && request === epoch.current) setBusy('');
      });
    return () => {
      alive.current = false;
      epoch.current++;
    };
  }, [api]);
  function change(key: MediaToolKey, value: string) {
    setDraft((old) => {
      const { useBundled: _mode, ...manual } = old;
      const next = { ...manual, [key]: value };
      manualDraft.current = next;
      return next;
    });
    setInspection(null);
    setError('');
    setNotice('');
  }
  async function perform(label: string, run: () => Promise<void>) {
    if (pending.current) return;
    pending.current = true;
    setBusy(label);
    setError('');
    setNotice('');
    const request = ++epoch.current;
    try {
      await run();
    } catch (e) {
      if (alive.current && request === epoch.current)
        setError(e instanceof Error ? e.message : '操作未完成，原设置保留。');
    } finally {
      pending.current = false;
      if (alive.current && request === epoch.current) setBusy('');
    }
  }
  useEffect(() => {
    if (!inspection || inspection.canSave) return;
    const key = Object.keys(inspection.errors).find((k) => k in labels);
    if (key) {
      const input = document.getElementById(`media-tools-input-${key}`);
      input?.focus();
      input?.scrollIntoView?.({ block: 'nearest' });
    }
  }, [inspection]);
  const result = inspection?.capabilities;
  return (
    <Modal
      open
      title="媒体工具设置"
      description="选择随包或手动配置的媒体工具，检测后保存。重启客户端后生效，当前播放、导出和工程保持使用现有工具。"
      className="replacement-dialog media-tools-dialog"
      busy={Boolean(busy)}
      onOpenChange={(open) => {
        if (!open && !busy) onClose();
      }}
      onCloseAutoFocus={(event) => {
        event.preventDefault();
        onReturnFocus();
      }}
    >
      <div className="replacement-dialog-body">
        {view?.restartRequired ? (
          <p role="status">设置已保存，重启后应用。请先保存工程并正常退出客户端。</p>
        ) : null}
        {view?.warning ? (
          <p role="alert" className="form-error">
            {view.warning}
          </p>
        ) : null}
        {busy === '读取' ? <p role="status">正在读取媒体设置…</p> : null}
        {view ? (
          <details>
            <summary>当前会话使用的工具与来源</summary>
            <dl className="media-tools-current">
              {(Object.keys(labels) as MediaToolKey[]).map((key) => (
                <div key={key}>
                  <dt>
                    {labels[key]} · {origins[view.origins[key]] || '未提供'}
                  </dt>
                  <dd className="file-path">{view.active[key] || '未找到或未配置'}</dd>
                </div>
              ))}
            </dl>
            <p className="field-hint">
              环境变量覆盖当前模式。自动模式优先随包；手动模式的空FFmpeg/FFprobe沿PATH，空原生媒体程序表示未启用。检测候选忽略环境覆盖。
            </p>
            <p className="field-hint">
              {view.capabilities.ffmpegVersion || '当前FFmpeg不可用'} ·{' '}
              {view.capabilities.ffprobeVersion || '当前FFprobe不可用'}
            </p>
          </details>
        ) : null}
        <div className="speech-actions">
          <Button
            disabled={Boolean(busy) || !view}
            onClick={() => {
              if (view) {
                setDraft({ ...view.active });
                setInspection(null);
                setNotice('已填入当前会话路径，请检测后再保存。');
              }
            }}
          >
            填入当前路径
          </Button>
          <Button
            disabled={Boolean(busy)}
            variant="ghost"
            onClick={() => {
              setDraft(empty);
              manualDraft.current = empty;
              setInspection(null);
              setNotice('已清空候选，FFmpeg/FFprobe改为PATH，原生引擎不配置。请检测后保存。');
            }}
          >
            清空候选路径
          </Button>
        </div>
        <label className="checkbox-field">
          <input
            type="checkbox"
            checked={Boolean(draft.useBundled)}
            disabled={Boolean(busy)}
            onChange={(event) => {
              if (event.target.checked) {
                manualDraft.current = draft;
                setDraft({ ...empty, useBundled: true });
              } else {
                const { useBundled: _mode, ...manual } = manualDraft.current;
                setDraft(manual);
              }
              setInspection(null);
              setError('');
              setNotice('');
            }}
          />
          使用随包媒体工具（自动）
        </label>
        {draft.useBundled ? (
          <p className="field-hint">
            从安装目录核对本产品配套工具。没有随包清单时，FFmpeg/FFprobe沿PATH发现；清单校验失败时停止自动回落。保存只记录此模式，重启后应用。
          </p>
        ) : null}
        {(Object.keys(labels) as MediaToolKey[]).map((key) => (
          <Field
            key={key}
            label={labels[key]}
            hint={
              key === 'ffmpeg' || key === 'ffprobe'
                ? '可留空沿PATH发现；保存的路径需要是本机程序完整路径。'
                : key === 'worker'
                  ? '仅使用本产品明确构建与验证的原生媒体程序，配套目录可在下面指定。'
                  : '选择与该原生媒体程序配套的目录；留空使用程序自身默认。'
            }
          >
            <div className="media-tool-path">
              <Input
                id={`media-tools-input-${key}`}
                aria-label={labels[key]}
                value={draft[key]}
                disabled={Boolean(busy) || Boolean(draft.useBundled)}
                aria-invalid={Boolean(inspection?.errors[key])}
                aria-describedby={inspection?.errors[key] ? `media-tools-error-${key}` : undefined}
                onChange={(e) => change(key, e.target.value)}
              />
              {isDesktop() ? (
                <Button
                  disabled={Boolean(busy) || Boolean(draft.useBundled)}
                  onClick={() =>
                    void perform('选择', async () => {
                      const path = await api.choose(key);
                      if (path && alive.current) change(key, path);
                    })
                  }
                >
                  选择…
                </Button>
              ) : null}
            </div>
            {inspection?.errors[key] ? (
              <p id={`media-tools-error-${key}`} className="form-error">
                {inspection.errors[key]}
              </p>
            ) : null}
          </Field>
        ))}
        <p className="field-hint">
          不自动下载、安装或扫描其它应用。请使用已确认来源的独立工具；检测会启动所选程序查询能力。保存设置不会进入工程包。
        </p>
        <Button
          busy={busy === '检测'}
          disabled={Boolean(busy) || !view}
          onClick={() =>
            void perform('检测', async () => {
              const request = epoch.current;
              setInspection(null);
              const value = await api.inspect(draft);
              if (alive.current && request === epoch.current) {
                setInspection(value);
                setNotice(
                  value.canSave
                    ? '候选设置已检测，可保存供下次启动使用。'
                    : '部分候选不能使用，请按具体错误修正后重新检测。',
                );
              }
            })
          }
        >
          检测候选设置
        </Button>
        {inspection ? (
          <section aria-label="媒体工具检测结果">
            <h3>检测结果</h3>
            <p className="field-hint">
              {new Date(inspection.checkedAt).toLocaleTimeString('zh-CN')} ·
              仅验证本次候选，声明能力不代表当前工程或硬件实测。
            </p>
            <dl className="media-tools-current">
              <div>
                <dt>FFmpeg</dt>
                <dd>
                  {result?.ffmpeg.available
                    ? result.ffmpegVersion
                    : result?.ffmpeg.reason || '未检测'}
                </dd>
              </div>
              <div>
                <dt>FFprobe</dt>
                <dd>
                  {result?.ffprobe.available
                    ? result.ffprobeVersion
                    : result?.ffprobe.reason || '未检测'}
                </dd>
              </div>
              <div>
                <dt>FFmpeg文字 / LUT</dt>
                <dd>
                  {result?.drawText.available ? '已声明文字' : '文字未就绪'} ·{' '}
                  {result?.lut3d.available ? '已声明LUT' : 'LUT未就绪'}
                </dd>
              </div>
              <div>
                <dt>原生引擎</dt>
                <dd>
                  {inspection.native?.available
                    ? `MLT ${inspection.native.mltVersion}，协议与基础插件已核对；使用工程时仍需准备。`
                    : inspection.nativeReason || inspection.errors.worker || '未检测'}
                </dd>
              </div>
            </dl>
            {inspection.errors.files ? (
              <p role="alert" className="form-error">
                {inspection.errors.files}
              </p>
            ) : null}
            {inspection.errors.runtime ? (
              <p role="alert" className="form-error">
                {inspection.errors.runtime}
              </p>
            ) : null}
          </section>
        ) : null}
      </div>
      <div className="replacement-dialog-status">
        {error ? (
          <p role="alert" className="form-error">
            {error}
          </p>
        ) : null}
        {notice ? <p role="status">{notice}</p> : null}
      </div>
      <div className="dialog-actions replacement-dialog-actions">
        <Button disabled={Boolean(busy)} onClick={onClose}>
          关闭设置
        </Button>
        <Button
          variant="primary"
          disabled={Boolean(busy) || !inspection?.canSave || !inspection.id}
          busy={busy === '保存'}
          onClick={() =>
            void perform('保存', async () => {
              if (!inspection?.id) return;
              const value = await api.save(inspection.id).catch((cause) => {
                if (alive.current) setInspection(null);
                throw cause;
              });
              if (alive.current) {
                setView(value);
                setInspection(null);
                setNotice(
                  value.restartRequired
                    ? '媒体设置已保存，重启客户端后应用；当前会话未切换工具。'
                    : '媒体设置已保存，与当前启动设置一致。',
                );
              }
            })
          }
        >
          保存，重启后生效
        </Button>
      </div>
    </Modal>
  );
}
