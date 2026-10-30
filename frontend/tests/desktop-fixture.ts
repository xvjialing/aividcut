import { vi } from 'vitest';

type Method = (...args: unknown[]) => unknown;
const fixture = vi.hoisted(() => ({ methods: {} as Record<string, Method> }));

export const desktopModule = {
  isDesktop: () => Boolean(window._wails?.environment?.OS),
  callDesktop: async (name: string, args: unknown[]) => {
    const method = fixture.methods[name];
    if (!method) throw new ReferenceError('未知的桌面方法');
    return method(...args);
  },
  onDesktopEvent: vi.fn(async () => () => {}),
};

export function installDesktop(methods: Record<string, Method>) {
  window._wails = { environment: { OS: 'darwin' } };
  fixture.methods = { FrontendReady: () => undefined, ...methods };
}
