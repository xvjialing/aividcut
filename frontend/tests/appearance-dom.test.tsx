// @vitest-environment jsdom
import { act, StrictMode, useState } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { initializeAppearance, useAppearance, appearanceStorageKey } from '../src/lib/appearance';
import { SettingsPage, type SettingsSection } from '../src/components/SettingsPage';

let host: HTMLDivElement, root: Root;
let dark: boolean;
let listeners: Set<() => void>;
function Harness() {
  const appearance = useAppearance();
  const [open, setOpen] = useState(true);
  const [section, setSection] = useState<SettingsSection>('appearance');
  return (
    <>
      <button id="settings-trigger" onClick={() => setOpen(true)}>
        设置
      </button>
      {open ? (
        <SettingsPage
          appearance={appearance}
          section={section}
          onSectionChange={setSection}
          onReturn={() => setOpen(false)}
        />
      ) : null}
    </>
  );
}
beforeEach(() => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
  localStorage.clear();
  delete document.documentElement.dataset.theme;
  dark = false;
  listeners = new Set();
  vi.stubGlobal(
    'matchMedia',
    vi.fn(() => ({
      get matches() {
        return dark;
      },
      addEventListener: (_: string, fn: () => void) => listeners.add(fn),
      removeEventListener: (_: string, fn: () => void) => listeners.delete(fn),
    })),
  );
  host = document.createElement('div');
  document.body.append(host);
  root = createRoot(host);
});
afterEach(async () => {
  await act(async () => root.unmount());
  host.remove();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  delete document.documentElement.dataset.theme;
});
async function mount() {
  await act(async () =>
    root.render(
      <StrictMode>
        <Harness />
      </StrictMode>,
    ),
  );
}
async function choose(value: string) {
  await act(async () =>
    document.querySelector<HTMLInputElement>(`input[value="${value}"]`)!.click(),
  );
}
async function systemChange(next: boolean) {
  await act(async () => {
    dark = next;
    listeners.forEach((fn) => fn());
  });
}
it('无偏好与损坏值保留深色；挂载前恢复浅色与系统偏好', () => {
  initializeAppearance();
  expect(document.documentElement.dataset.theme).toBe('dark');
  localStorage.setItem(appearanceStorageKey, 'broken');
  initializeAppearance();
  expect(document.documentElement.dataset.theme).toBe('dark');
  localStorage.setItem(appearanceStorageKey, 'light');
  initializeAppearance();
  expect(document.documentElement.dataset.theme).toBe('light');
  localStorage.setItem(appearanceStorageKey, 'system');
  initializeAppearance();
  expect(document.documentElement.dataset.theme).toBe('light');
  dark = true;
  initializeAppearance();
  expect(document.documentElement.dataset.theme).toBe('dark');
});
it('三种单选立即生效并保存；显式深浅色不受系统变化影响', async () => {
  await mount();
  expect(document.querySelectorAll('input[type="radio"]')).toHaveLength(3);
  await choose('light');
  expect(document.documentElement.dataset.theme).toBe('light');
  expect(localStorage.getItem(appearanceStorageKey)).toBe('light');
  await systemChange(true);
  expect(document.documentElement.dataset.theme).toBe('light');
  await choose('dark');
  await systemChange(false);
  expect(document.documentElement.dataset.theme).toBe('dark');
  expect(localStorage.getItem(appearanceStorageKey)).toBe('dark');
});
it('跟随系统实时切换，关闭设置后仍监听，重建组件后恢复选择', async () => {
  await mount();
  await choose('system');
  await systemChange(true);
  expect(document.documentElement.dataset.theme).toBe('dark');
  await act(async () =>
    Array.from(document.querySelectorAll('button'))
      .find((b) => b.textContent?.includes('返回应用'))!
      .click(),
  );
  expect(document.querySelector('[data-settings-page]')).toBeNull();
  await systemChange(false);
  expect(document.documentElement.dataset.theme).toBe('light');
  await act(async () => root.unmount());
  expect(listeners.size).toBe(0);
  root = createRoot(host);
  await mount();
  expect(document.querySelector<HTMLInputElement>('input[value="system"]')!.checked).toBe(true);
  expect(listeners.size).toBe(1);
});
it('写入失败保留已应用主题，提供明确错误和重试；不谎报已保存', async () => {
  await mount();
  const save = vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
    throw new Error('quota');
  });
  await choose('light');
  expect(document.documentElement.dataset.theme).toBe('light');
  expect(document.querySelector('[role="alert"]')!.textContent).toContain('无法保存');
  save.mockRestore();
  await act(async () =>
    Array.from(document.querySelectorAll('button'))
      .find((b) => b.textContent === '重试保存')!
      .click(),
  );
  expect(localStorage.getItem(appearanceStorageKey)).toBe('light');
  expect(document.querySelector('[role="alert"]')).toBeNull();
});
it('存储不可读与缺少系统检测时仍可打开设置和手动切换', async () => {
  vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
    throw new Error('blocked');
  });
  vi.stubGlobal('matchMedia', undefined);
  expect(() => initializeAppearance()).not.toThrow();
  await mount();
  await choose('light');
  expect(document.documentElement.dataset.theme).toBe('light');
  await choose('system');
  expect(document.documentElement.dataset.theme).toBe('dark');
});
it('其它窗口更新或清除偏好后同步；无关存储事件不改变当前设置', async () => {
  await mount();
  await choose('light');
  localStorage.setItem(appearanceStorageKey, 'dark');
  await act(async () => window.dispatchEvent(new StorageEvent('storage', { key: 'other' })));
  expect(document.documentElement.dataset.theme).toBe('light');
  await act(async () =>
    window.dispatchEvent(new StorageEvent('storage', { key: appearanceStorageKey })),
  );
  expect(document.documentElement.dataset.theme).toBe('dark');
  localStorage.clear();
  await act(async () => window.dispatchEvent(new StorageEvent('storage', { key: null })));
  expect(document.querySelector<HTMLInputElement>('input[value="dark"]')!.checked).toBe(true);
});
