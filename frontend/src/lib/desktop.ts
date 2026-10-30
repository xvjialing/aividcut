// 仅在原生环境中加载运行时，普通开发浏览器不会触发 Wails IPC 或初始化请求。
export const isDesktop = () => Boolean(window._wails?.environment?.OS);

export async function callDesktop(method: string, args: unknown[]): Promise<unknown> {
  const { Call } = await import('@wailsio/runtime');
  return Call.ByName(`main.App.${method}`, ...args);
}

export async function onDesktopEvent(name: string, callback: () => void): Promise<() => void> {
  const { Events } = await import('@wailsio/runtime');
  return Events.On(name, callback);
}
