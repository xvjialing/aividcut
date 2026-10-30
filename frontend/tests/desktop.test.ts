import { afterEach, describe, expect, it, vi } from 'vitest';
import { callDesktop, isDesktop, onDesktopEvent } from '../src/lib/desktop';

const runtime = vi.hoisted(() => ({
  Call: { ByName: vi.fn(async () => ({ project: 'actual-result' })) },
  Events: { On: vi.fn(() => vi.fn()) },
}));
vi.mock('@wailsio/runtime', () => runtime);
afterEach(() => {
  vi.unstubAllGlobals();
  vi.clearAllMocks();
});

describe('Wails v3 运行时适配', () => {
  it('普通浏览器的空_wails对象不会被当作已连接桌面', () => {
    vi.stubGlobal('window', { _wails: {} });
    expect(isDesktop()).toBe(false);
    expect(runtime.Call.ByName).not.toHaveBeenCalled();
    window._wails = { environment: { OS: 'darwin' } };
    expect(isDesktop()).toBe(true);
  });
  it('调用正式绑定名称，保留结构化参数和真实返回值', async () => {
    const request = { baseRevision: 4, commands: [{ type: 'split_clip', clipId: 'video' }] };
    expect(await callDesktop('ApplyEdit', [request])).toEqual({ project: 'actual-result' });
    expect(runtime.Call.ByName).toHaveBeenCalledWith('main.App.ApplyEdit', request);
  });
  it('关闭订阅返回v3取消函数，调用方可在卸载时释放', async () => {
    const listener = vi.fn();
    const stop = await onDesktopEvent('project:close-request', listener);
    expect(runtime.Events.On).toHaveBeenCalledWith('project:close-request', listener);
    stop();
    expect(runtime.Events.On.mock.results[0].value).toHaveBeenCalledOnce();
  });
});
