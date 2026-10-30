import { useEffect, useState } from 'react';

export type Appearance = 'light' | 'dark' | 'system';
export const appearanceStorageKey = 'aividcut.appearance';

export function parseAppearance(value: string | null): Appearance {
  return value === 'light' || value === 'system' ? value : 'dark';
}

function readAppearance(): Appearance {
  try {
    return parseAppearance(localStorage.getItem(appearanceStorageKey));
  } catch {
    return 'dark';
  }
}

function systemAppearance() {
  return typeof window.matchMedia === 'function'
    ? window.matchMedia('(prefers-color-scheme: dark)')
    : null;
}

export function applyAppearance(preference: Appearance, systemDark: boolean) {
  const resolved = preference === 'system' ? (systemDark ? 'dark' : 'light') : preference;
  if (document.documentElement.dataset.theme !== resolved)
    document.documentElement.dataset.theme = resolved;
  return resolved;
}

// 在 React 挂载前恢复主题，避免已保存的浅色先显示为深色工作区。
export function initializeAppearance() {
  applyAppearance(readAppearance(), systemAppearance()?.matches ?? true);
}

export function useAppearance() {
  const [preference, setPreference] = useState(readAppearance);
  const [media] = useState(systemAppearance);
  const [systemDark, setSystemDark] = useState(media?.matches ?? true);
  const [saveError, setSaveError] = useState(false);
  const resolved = preference === 'system' ? (systemDark ? 'dark' : 'light') : preference;

  useEffect(() => {
    if (!media) return;
    const change = () => setSystemDark(media.matches);
    change();
    media.addEventListener('change', change);
    return () => media.removeEventListener('change', change);
  }, [media]);

  useEffect(() => {
    applyAppearance(preference, systemDark);
  }, [preference, systemDark]);

  useEffect(() => {
    const change = (event: StorageEvent) => {
      if (event.key !== appearanceStorageKey && event.key !== null) return;
      setPreference(readAppearance());
      setSaveError(false);
    };
    window.addEventListener('storage', change);
    return () => window.removeEventListener('storage', change);
  }, []);

  function choose(next: Appearance) {
    setPreference(next);
    applyAppearance(next, media?.matches ?? true);
    try {
      localStorage.setItem(appearanceStorageKey, next);
      setSaveError(false);
    } catch {
      setSaveError(true);
    }
  }

  return { preference, resolved, choose, saveError };
}
