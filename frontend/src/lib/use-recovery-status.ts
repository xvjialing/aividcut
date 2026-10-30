import { useCallback, useEffect, useRef, useState } from 'react';
import type { RecoveryAPI, RecoveryStatus } from '../recovery-types';
import { recoveryAPI } from './bridge';

export function useRecoveryStatus(ready: boolean, api: RecoveryAPI = recoveryAPI) {
  const [status, setStatus] = useState<RecoveryStatus | null>(null);
  const read = useRef<() => Promise<void>>(async () => {});
  useEffect(() => {
    if (!ready) return;
    let alive = true,
      reading = false;
    const refresh = async () => {
      if (reading || !alive) return;
      reading = true;
      try {
        const value = await api.status();
        if (alive) setStatus(value);
      } catch {
        /* 旧桥或启动中的服务不阻断编辑，中心仍可显示具体连接错误并重试。 */
      } finally {
        reading = false;
      }
    };
    read.current = refresh;
    void refresh();
    const interval = window.setInterval(() => void refresh(), 5000);
    return () => {
      alive = false;
      read.current = async () => {};
      window.clearInterval(interval);
    };
  }, [ready, api]);
  const refresh = useCallback(() => read.current(), []);
  return { status, refresh };
}
