import { useEffect } from 'react';

export type SettingsFormEvents = {
  onBusyChange?: (busy: boolean) => void;
  onDirtyChange?: (dirty: boolean) => void;
};

// 设置页与既有表单共用忙碌/草稿状态，不在页面壳复制业务状态。
export function useSettingsFormState(busy: boolean, dirty: boolean, events: SettingsFormEvents) {
  const { onBusyChange, onDirtyChange } = events;
  useEffect(() => {
    onBusyChange?.(busy);
  }, [busy, onBusyChange]);
  useEffect(() => {
    onDirtyChange?.(dirty);
  }, [dirty, onDirtyChange]);
  useEffect(
    () => () => {
      onBusyChange?.(false);
      onDirtyChange?.(false);
    },
    [onBusyChange, onDirtyChange],
  );
}

export const connectionsChangedEvent = 'settings:connections-changed';
