// @vitest-environment jsdom
import { installDesktop } from './desktop-fixture';
vi.mock('../src/lib/desktop', async () => (await import('./desktop-fixture')).desktopModule);
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import App from '../src/App';
import { generationProject } from './generation-fixtures';
import type { Bootstrap } from '../src/types';
import type { RecoveryStatus } from '../src/recovery-types';

const bootstrap: Bootstrap = {
  project: {
    ...generationProject,
    id: 'empty',
    revision: 0,
    name: '未命名工程',
    assets: [],
    clips: [],
    transcripts: [],
  },
  sourceEpoch: 0,
  projectPath: '',
  dirty: false,
  canUndo: false,
  canRedo: false,
  capabilities: {},
};
const status: RecoveryStatus = {
  scope: { projectId: 'empty', projectRevision: 0, sourceEpoch: 0 },
  enabled: true,
  intervalSeconds: 5,
  pending: 1,
  startupPending: 1,
  unreadable: 0,
  protectedRevision: 0,
};
let root: Root, host: HTMLDivElement;
beforeEach(() => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
  vi.stubGlobal('fetch', vi.fn());
  vi.stubGlobal('cancelAnimationFrame', vi.fn());
  vi.stubGlobal(
    'ResizeObserver',
    class {
      observe() {}
      disconnect() {}
      unobserve() {}
    },
  );
  Object.assign(HTMLElement.prototype, {
    setPointerCapture: vi.fn(),
    hasPointerCapture: vi.fn().mockReturnValue(false),
    releasePointerCapture: vi.fn(),
  });
  host = document.createElement('div');
  document.body.append(host);
  root = createRoot(host);
});
afterEach(async () => {
  await act(async () => root.unmount());
  host.remove();
  delete window._wails;
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});
const settle = () => new Promise((resolve) => setTimeout(resolve, 5));
function desktop(readStatus: () => Promise<RecoveryStatus>) {
  installDesktop({
    GetBootstrap: async () => bootstrap,
    ListJobs: async () => [],
    GetMCPStatus: async () => ({ running: false, scopeValid: false, allowEdits: false, audit: [] }),
    GetRecoveryStatus: readStatus,
    ListProjectRecoveries: async () => ({
      records: [],
      total: 0,
      unreadable: 0,
      offset: 0,
      limit: 20,
    }),
  });
}
async function mount() {
  await act(async () => {
    root.render(<App />);
    await settle();
  });
}
async function waitFor(predicate: () => boolean) {
  const end = Date.now() + 3000;
  while (!predicate() && Date.now() < end) {
    await act(async () => {
      await settle();
    });
  }
  expect(predicate()).toBe(true);
}
it('启动发现副本只提示一次，暂不恢复不自动切换工程', async () => {
  desktop(async () => status);
  await mount();
  await waitFor(() => Boolean(document.querySelector('[role="dialog"]')));
  expect(document.querySelector('[role="dialog"]')?.textContent).toContain('恢复中心');
  await act(async () => {
    [...document.querySelectorAll<HTMLButtonElement>('button')]
      .find((b) => b.textContent === '暂不恢复')!
      .click();
    await settle();
  });
  expect(document.querySelector('[role="dialog"]')).toBeNull();
  expect(document.title).toContain('未命名工程');
});
it('状态晚到时不抢正在输入的焦点和草稿', async () => {
  let resolve!: (value: RecoveryStatus) => void;
  desktop(
    () =>
      new Promise((done) => {
        resolve = done;
      }),
  );
  await mount();
  await waitFor(() =>
    [...document.querySelectorAll<HTMLInputElement>('input')].some(
      (input) => input.value === '未命名工程',
    ),
  );
  const field = [...document.querySelectorAll<HTMLInputElement>('input')].find(
    (input) => input.value === '未命名工程',
  )!;
  await act(async () => {
    field.focus();
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(
      field,
      '正在输入',
    );
    field.dispatchEvent(new Event('input', { bubbles: true }));
    resolve(status);
    await settle();
  });
  expect(document.querySelector('[role="dialog"]')).toBeNull();
  expect(document.activeElement).toBe(field);
  expect(field.value).toBe('正在输入');
});
