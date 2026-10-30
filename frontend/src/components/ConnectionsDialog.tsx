import { useEffect, useId, useRef, useState } from 'react';
import { Eye, EyeOff, Plus, Trash2 } from 'lucide-react';
import type { ConnectionAPI, ConnectionProfile, GenerationCapability } from '../generation-types';
import { generationBridge } from '../lib/bridge';
import { canGenerate, providerNames, safeConnectionError } from '../lib/generation';
import { useSettingsFormState, type SettingsFormEvents } from '../lib/settings';
import { Button, Field, Input, SettingsSurface, Select } from './ui';

export function ConnectionsDialog({
  open,
  onOpenChange,
  connections,
  catalog,
  modelOptions,
  onChanged,
  api = generationBridge,
  inline = false,
  onBusyChange,
  onDirtyChange,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  connections: ConnectionProfile[];
  catalog: GenerationCapability[];
  modelOptions?: Array<{ providerId: string; modelId: string }>;
  onChanged: (profile?: ConnectionProfile) => void;
  api?: ConnectionAPI;
  inline?: boolean;
} & SettingsFormEvents) {
  const available = modelOptions || catalog.filter(canGenerate);
  const [dirty, setDirty] = useState(false);
  const [id, setID] = useState('');
  const [name, setName] = useState('');
  const [provider, setProvider] = useState(available[0]?.providerId || 'openai');
  const [model, setModel] = useState(available[0]?.modelId || '');
  const [secret, setSecret] = useState('');
  const secretInput = useRef<HTMLInputElement>(null);
  const [revealed, setRevealed] = useState(false);
  const [busy, setBusy] = useState('');
  const pending = useRef(false);
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const [warning, setWarning] = useState('');
  const [health, setHealth] = useState<{ pendingCleanup: number; storage: string } | null>(null);
  const [deleting, setDeleting] = useState<ConnectionProfile | null>(null);
  const errorID = useId();
  useSettingsFormState(Boolean(busy), dirty, { onBusyChange, onDirtyChange });
  const selected = connections.find((profile) => profile.id === id);
  const canKeepCredential = Boolean(selected?.configured && selected.providerId === provider);
  const models = available.filter((capability) => capability.providerId === provider);
  const supported = models.some((capability) => capability.modelId === model);
  const providers = [
    ...new Set([...catalog, ...available].map((capability) => capability.providerId)),
  ];

  useEffect(() => {
    const element = secretInput.current;
    return () => {
      if (element) element.value = '';
    };
  }, [open, inline]);
  function clearSecret() {
    if (secretInput.current) secretInput.current.value = '';
    setSecret('');
    setRevealed(false);
  }
  function newConnection() {
    setDirty(false);
    clearSecret();
    setID('');
    setName('');
    setProvider(available[0]?.providerId || 'openai');
    setModel(available[0]?.modelId || '');
    setError('');
    setNotice('');
    setDeleting(null);
  }
  useEffect(() => {
    if (!open) {
      clearSecret();
      return;
    }
    newConnection();
    let mounted = true;
    void api
      .getConnectionHealth()
      .then((value) => {
        if (mounted) setHealth(value);
      })
      .catch((cause) => {
        if (mounted) setError(`无法读取旧凭据清理状态：${safeConnectionError(cause)}`);
      });
    return () => {
      mounted = false;
    };
  }, [open]);

  function choose(profile: ConnectionProfile) {
    setDirty(false);
    clearSecret();
    setID(profile.id);
    setName(profile.name);
    setProvider(profile.providerId);
    setModel(profile.modelId);
    setError('');
    setNotice('');
    setWarning(profile.warning || '');
    setDeleting(null);
  }
  function close() {
    if (inline) {
      newConnection();
      return;
    }
    if (pending.current) return;
    clearSecret();
    setError('');
    setDeleting(null);
    onOpenChange(false);
  }
  async function refreshHealth() {
    try {
      setHealth(await api.getConnectionHealth());
    } catch {
      setWarning((previous) => previous || '配置已提交，但暂时无法读取旧凭据清理状态。');
    }
  }
  async function save(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (pending.current) return;
    setError('');
    if (!name.trim() || !model || !supported || (!canKeepCredential && !secret.trim())) {
      setError('请填写连接名称、已声明模型和 API 密钥。');
      return;
    }
    pending.current = true;
    setBusy('保存连接');
    try {
      const profile = await api.saveConnection({
        id: id || undefined,
        providerId: provider,
        name: name.trim(),
        modelId: model,
        ...(secret.trim() ? { apiKey: secret.trim() } : {}),
      });
      clearSecret();
      setID(profile.id);
      setWarning(profile.warning ? safeConnectionError(new Error(profile.warning), secret) : '');
      setDirty(false);
      setNotice('连接已保存，账号与模型权限尚未验证。');
      onChanged(profile);
      await refreshHealth();
    } catch (cause) {
      setError(safeConnectionError(cause, secret));
    } finally {
      pending.current = false;
      setBusy('');
    }
  }
  async function remove() {
    if (!deleting || pending.current) return;
    pending.current = true;
    setBusy('删除连接');
    setError('');
    try {
      const result = await api.deleteConnection(deleting.id);
      clearSecret();
      setID('');
      setName('');
      setDeleting(null);
      setWarning(result?.warning || '');
      setNotice('连接已删除，已有生成素材会保留。');
      onChanged();
      await refreshHealth();
    } catch (cause) {
      setError(safeConnectionError(cause));
    } finally {
      pending.current = false;
      setBusy('');
    }
  }
  async function cleanup() {
    if (pending.current) return;
    pending.current = true;
    setBusy('清理旧凭据');
    setError('');
    try {
      const result = await api.cleanupCredentials();
      setHealth((previous) => ({
        pendingCleanup: result.pendingCleanup,
        storage: previous?.storage || '',
      }));
      if (!result.pendingCleanup) setWarning('');
      setNotice(
        result.pendingCleanup
          ? '仍有旧凭据等待清理，可在系统存储可用后再试。'
          : '旧凭据清理已完成。',
      );
    } catch (cause) {
      setError(safeConnectionError(cause));
    } finally {
      pending.current = false;
      setBusy('');
    }
  }

  return (
    <SettingsSurface
      inline={inline}
      open={open}
      title="模型连接"
      description="API 密钥交给本机凭据存储；保存配置不会验证账号，也不会发起收费生成。"
      busy={Boolean(busy)}
      onOpenChange={(value) => {
        if (!value) close();
      }}
    >
      <div className="connection-profile-list" aria-label="已保存模型连接">
        {connections.map((profile) => (
          <button
            type="button"
            className={`connection-profile ${id === profile.id ? 'selected' : ''}`}
            key={profile.id}
            onClick={() => choose(profile)}
            disabled={Boolean(busy)}
          >
            <strong>{profile.name}</strong>
            <span>
              {providerNames[profile.providerId] || profile.providerId} ·{' '}
              {profile.configured ? '已配置，未验证' : '未配置凭据'}
            </span>
          </button>
        ))}
        <Button variant="ghost" onClick={newConnection} disabled={Boolean(busy)}>
          <Plus size={14} />
          新增连接
        </Button>
      </div>
      {deleting ? (
        <div>
          <p className="dialog-description">
            删除“{deleting.name}
            ”的连接与凭据？已有生成素材不会删除。正在使用的连接会由服务拒绝删除。
          </p>
          <div className="dialog-actions">
            <Button disabled={Boolean(busy)} onClick={() => setDeleting(null)}>
              保留连接
            </Button>
            <Button variant="danger" busy={Boolean(busy)} onClick={() => void remove()}>
              <Trash2 size={14} />
              删除连接
            </Button>
          </div>
        </div>
      ) : (
        <form noValidate onSubmit={save}>
          <Field label="连接名称">
            <Input
              value={name}
              required
              onChange={(event) => {
                setDirty(true);
                setName(event.target.value);
              }}
              disabled={Boolean(busy)}
            />
          </Field>
          <div className="field-pair">
            <Field label="服务商">
              <Select
                value={provider}
                disabled={Boolean(busy)}
                onChange={(event) => {
                  setDirty(true);
                  clearSecret();
                  setProvider(event.target.value);
                  setModel(
                    available.find((capability) => capability.providerId === event.target.value)
                      ?.modelId || '',
                  );
                }}
              >
                {providers.map((value) => (
                  <option
                    key={value}
                    value={value}
                    disabled={!available.some((capability) => capability.providerId === value)}
                  >
                    {providerNames[value] || value}
                    {available.some((capability) => capability.providerId === value)
                      ? ''
                      : '（未开放）'}
                  </option>
                ))}
              </Select>
            </Field>
            <Field label="模型">
              <Select
                value={model}
                disabled={Boolean(busy) || !models.length}
                onChange={(event) => {
                  setDirty(true);
                  setModel(event.target.value);
                }}
              >
                {!supported && model ? <option value={model}>{model}（无能力声明）</option> : null}
                {models.map((capability) => (
                  <option key={capability.modelId} value={capability.modelId}>
                    {capability.modelId}
                  </option>
                ))}
              </Select>
            </Field>
          </div>
          {!supported ? (
            <p className="field-hint">当前模型没有已开放能力，请选择目录中的可用模型。</p>
          ) : null}
          <Field
            label="API 密钥"
            hint={
              canKeepCredential
                ? '留空保留现有凭据；不会读回密钥。'
                : '只用于所选服务商，不写入工程或浏览器持久存储。'
            }
          >
            <div className="secret-field">
              <Input
                ref={secretInput}
                type={revealed ? 'text' : 'password'}
                value={secret}
                autoComplete="off"
                required={!canKeepCredential}
                spellCheck={false}
                onChange={(event) => {
                  setDirty(true);
                  setSecret(event.target.value);
                  if (!event.target.value) setRevealed(false);
                }}
                disabled={Boolean(busy)}
              />
              <Button
                variant="ghost"
                aria-label={revealed ? '隐藏 API 密钥' : '显示 API 密钥'}
                aria-pressed={revealed}
                disabled={!secret || Boolean(busy)}
                onClick={() => setRevealed((value) => !value)}
              >
                {revealed ? <EyeOff size={15} /> : <Eye size={15} />}
              </Button>
            </div>
          </Field>
          <div className="dialog-actions">
            <span className="grow" />
            {selected ? (
              <Button
                variant="ghost"
                disabled={Boolean(busy)}
                onClick={() => {
                  clearSecret();
                  setDeleting(selected);
                  setError('');
                }}
              >
                删除连接
              </Button>
            ) : null}
            <Button disabled={Boolean(busy)} onClick={close}>
              {inline ? '重置输入' : '取消'}
            </Button>
            <Button type="submit" variant="primary" busy={Boolean(busy)} disabled={!supported}>
              保存连接
            </Button>
          </div>
        </form>
      )}
      {notice ? (
        <p className="generation-notice" role="status">
          {notice}
        </p>
      ) : null}
      {warning ? (
        <p className="generation-warning" role="status">
          旧凭据清理提示：{warning}
        </p>
      ) : null}
      {health?.pendingCleanup ? (
        <div className="credential-cleanup">
          <span>{health.pendingCleanup} 项旧凭据等待清理</span>
          <Button disabled={Boolean(busy)} onClick={() => void cleanup()}>
            重试清理旧凭据
          </Button>
        </div>
      ) : null}
      {error ? (
        <p className="form-error" id={errorID} role="alert">
          {error}
        </p>
      ) : null}
    </SettingsSurface>
  );
}
