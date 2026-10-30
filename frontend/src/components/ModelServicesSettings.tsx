import { useCallback, useEffect, useRef, useState } from 'react';
import type { ConnectionProfile, GenerationCapability } from '../generation-types';
import { generationBridge, speechBridge } from '../lib/bridge';
import { generationConnections } from '../lib/generation';
import { speechModelOptions } from '../lib/speech';
import { connectionsChangedEvent, type SettingsFormEvents } from '../lib/settings';
import { ConnectionsDialog } from './ConnectionsDialog';
import { Button, Modal, PanelTabs } from './ui';

export type ModelServiceKind = 'generation' | 'speech';
export function ModelServicesSettings({
  initialKind = 'generation',
  onBusyChange,
  onDirtyChange,
}: { initialKind?: ModelServiceKind } & SettingsFormEvents) {
  const [reload, setReload] = useState(0);
  const [kind, setKind] = useState(initialKind);
  const [profiles, setProfiles] = useState<ConnectionProfile[]>([]);
  const [catalog, setCatalog] = useState<GenerationCapability[]>([]);
  const [models, setModels] = useState<{ providerId: string; modelId: string }[] | undefined>();
  const [loading, setLoading] = useState(true),
    [error, setError] = useState('');
  const [dirty, setDirty] = useState(false),
    [busy, setBusy] = useState(false);
  const [nextKind, setNextKind] = useState<ModelServiceKind | null>(null);
  const alive = useRef(false),
    epoch = useRef(0);
  const api = kind === 'generation' ? generationBridge : speechBridge;
  const dirtyChanged = useCallback(
    (value: boolean) => {
      setDirty(value);
      onDirtyChange?.(value);
    },
    [onDirtyChange],
  );
  const busyChanged = useCallback(
    (value: boolean) => {
      setBusy(value);
      onBusyChange?.(value);
    },
    [onBusyChange],
  );
  useEffect(() => {
    alive.current = true;
    const request = ++epoch.current;
    setLoading(true);
    setError('');
    void Promise.all([
      api.getConnections(),
      kind === 'generation'
        ? generationBridge.getGenerationCatalog()
        : speechBridge.getSpeechCatalog(),
    ])
      .then(([connections, capabilities]) => {
        if (!alive.current || request !== epoch.current) return;
        if (Array.isArray(capabilities)) {
          setCatalog(capabilities);
          setModels(undefined);
          setProfiles(generationConnections(connections, capabilities));
        } else {
          setCatalog([]);
          setModels(speechModelOptions(capabilities));
          setProfiles(connections.filter((p) => p.providerId === capabilities.providerId));
        }
      })
      .catch((cause) => {
        if (alive.current && request === epoch.current)
          setError(cause instanceof Error ? cause.message : '模型服务读取失败。');
      })
      .finally(() => {
        if (alive.current && request === epoch.current) setLoading(false);
      });
    return () => {
      alive.current = false;
      epoch.current++;
    };
  }, [kind, api, reload]);
  async function changed() {
    window.dispatchEvent(new Event(connectionsChangedEvent));
    try {
      const values = await api.getConnections();
      if (!alive.current) return;
      setProfiles(
        kind === 'generation'
          ? generationConnections(values, catalog)
          : values.filter((p) => models?.some((m) => m.providerId === p.providerId)),
      );
    } catch {
      if (alive.current) setError('设置已提交，但列表读取失败。请重新打开此分类核对。');
    }
  }
  return (
    <>
      <PanelTabs
        id="model-services"
        label="模型服务分类"
        value={kind}
        disabled={busy}
        items={[
          { value: 'generation', label: '图片与视频' },
          { value: 'speech', label: '语音' },
        ]}
        onChange={(value) => {
          if (dirty) setNextKind(value);
          else setKind(value);
        }}
      />
      {loading ? (
        <p role="status">正在读取模型服务…</p>
      ) : error ? (
        <div>
          <p role="alert" className="form-error">
            {error}
          </p>
          <Button onClick={() => setReload((value) => value + 1)}>重新读取模型服务</Button>
        </div>
      ) : (
        <ConnectionsDialog
          key={kind}
          inline
          open
          onOpenChange={() => {}}
          connections={profiles}
          catalog={catalog}
          modelOptions={models}
          api={api}
          onChanged={() => void changed()}
          onBusyChange={busyChanged}
          onDirtyChange={dirtyChanged}
        />
      )}
      <Modal
        open={nextKind !== null}
        title="放弃未保存的连接配置？"
        description="切换分类会清空尚未保存的输入，包括 API 密钥。已保存的连接不受影响。"
        onOpenChange={(open) => {
          if (!open) setNextKind(null);
        }}
      >
        <div className="dialog-actions">
          <Button onClick={() => setNextKind(null)}>继续编辑</Button>
          <Button
            variant="danger"
            onClick={() => {
              if (nextKind) setKind(nextKind);
              setNextKind(null);
              dirtyChanged(false);
            }}
          >
            放弃并切换
          </Button>
        </div>
      </Modal>
    </>
  );
}
