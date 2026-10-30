// @vitest-environment jsdom
import { installDesktop, desktopModule } from './desktop-fixture';
vi.mock('../src/lib/desktop', async () => (await import('./desktop-fixture')).desktopModule);
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import App from '../src/App';
import { generationProject } from './generation-fixtures';
import type { Bootstrap } from '../src/types';
import { configuredConnection, openAICapability } from './generation-fixtures';
import { readyAgent, agentLogin } from './agent-fixtures';
import type { MCPStatus } from '../src/mcp-types';

const snapshot: Bootstrap = {
  project: { ...generationProject, name: '保留的工程', assets: [], clips: [], revision: 0 },
  projectPath: '',
  dirty: false,
  canUndo: false,
  canRedo: false,
  capabilities: {},
};
let root: Root, host: HTMLDivElement;
let events: Map<string, () => void>;
const apply = vi.fn(async () => snapshot);
let mcpState: MCPStatus;
const saveConnection = vi.fn(async () => configuredConnection);
const confirmClose = vi.fn(async () => undefined);
const startMCP = vi.fn(
  async (input: { projectId: string; allowEdits: boolean; allowSourceFrames?: boolean }) => {
    mcpState = { ...mcpState, ...input, running: true, scopeValid: true };
    return mcpState;
  },
);
const stopMCP = vi.fn(async () => {
  mcpState = { ...mcpState, running: false, scopeValid: false };
  return mcpState;
});
const getMCPConnection = vi.fn(async () => ({
  url: 'http://127.0.0.1:40123/mcp',
  transport: 'streamable-http',
  httpHeaders: { Authorization: 'Bearer settings-fixture-token' },
}));
const settle = () => new Promise((resolve) => setTimeout(resolve, 5));
beforeEach(() => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
  vi.stubGlobal('fetch', vi.fn());
  vi.stubGlobal(
    'ResizeObserver',
    class {
      observe() {}
      disconnect() {}
    },
  );
  vi.stubGlobal('cancelAnimationFrame', vi.fn());
  events = new Map();
  desktopModule.onDesktopEvent.mockImplementation(async (name: string, callback: () => void) => {
    events.set(name, callback);
    return () => {
      events.delete(name);
    };
  });
  apply.mockClear();
  saveConnection.mockClear();
  confirmClose.mockClear();
  startMCP.mockClear();
  stopMCP.mockClear();
  getMCPConnection.mockClear();
  mcpState = {
    running: false,
    projectRevision: 0,
    scopeValid: false,
    allowEdits: false,
    audit: [],
  };
  installDesktop({
    GetBootstrap: async () => snapshot,
    ListJobs: async () => [],
    GetMCPStatus: async () => mcpState,
    StartMCP: (input) => startMCP(input as Parameters<typeof startMCP>[0]),
    StopMCP: stopMCP,
    GetMCPConnection: getMCPConnection,
    ListMCPRequests: async () => [],
    GetConnections: async () => [],
    GetGenerationCatalog: async () => [openAICapability],
    GetConnectionHealth: async () => ({ pendingCleanup: 0, storage: 'fixture' }),
    SaveConnection: saveConnection,
    ConfirmClose: confirmClose,
    GetAgentStatus: async () => [readyAgent],
    GetAgentExecutionStatus: async () => {
      throw new Error('执行能力尚未验证');
    },
    GetAgentLoginInstructions: async () => agentLogin,
    GetRecoveryStatus: async () => ({
      enabled: false,
      scope: { projectId: snapshot.project.id, projectRevision: 0, sourceEpoch: 0 },
      pending: 0,
      unreadable: 0,
    }),
    ApplyEdit: apply,
  });
  host = document.createElement('div');
  document.body.append(host);
  root = createRoot(host);
});
afterEach(async () => {
  await act(async () => root.unmount());
  expect(events.has('app:open-settings')).toBe(false);
  host.remove();
  delete window._wails;
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});
async function mount() {
  await act(async () => {
    root.render(<App />);
    await settle();
  });
  await act(async () => {
    await settle();
  });
}
function returnButton() {
  return document.getElementById('settings-return') as HTMLButtonElement;
}
it('原生设置事件打开完整页面，移除工作台设置入口，返回后保留原DOM/搜索/滚动与焦点', async () => {
  await mount();
  expect(host.querySelector('#settings-trigger')).toBeNull();
  const search = host.querySelector<HTMLInputElement>('input[aria-label="搜索素材"]')!;
  const timeline = host.querySelector<HTMLElement>('.timeline-scroll')!;
  await act(async () => {
    search.focus();
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(
      search,
      '保留的搜索',
    );
    search.dispatchEvent(new Event('input', { bubbles: true }));
    timeline.scrollLeft = 240;
  });
  await act(async () => {
    events.get('app:open-settings')!();
  });
  expect(host.querySelector<HTMLElement>('.editor-shell')!.hidden).toBe(true);
  expect(host.querySelector<HTMLElement>('.editor-shell')!.hasAttribute('inert')).toBe(true);
  expect(document.querySelector('[role="dialog"]')).toBeNull();
  expect(document.activeElement?.id).toBe('settings-page-title');
  expect(document.title).toBe('外观 · 设置 · AIVidCut');
  expect(host.querySelectorAll('input[name="appearance"]')).toHaveLength(3);
  await act(async () => returnButton().click());
  expect(host.querySelector<HTMLElement>('.editor-shell')!.hidden).toBe(false);
  expect(host.querySelector('input[aria-label="搜索素材"]')).toBe(search);
  expect(search.value).toBe('保留的搜索');
  expect(timeline.scrollLeft).toBe(240);
  expect(document.activeElement).toBe(search);
  expect(document.title).toBe('保留的工程 · AIVidCut');
  expect(apply).not.toHaveBeenCalled();
});
it('系统快捷键可进入，设置页方向键/空格/删除/Cmd+Z不会触发隐藏的时间线命令', async () => {
  await mount();
  await act(async () =>
    document.body.dispatchEvent(
      new KeyboardEvent('keydown', { key: ',', metaKey: true, bubbles: true, cancelable: true }),
    ),
  );
  expect(returnButton()).not.toBeNull();
  const slider = host.querySelector<HTMLInputElement>('input[aria-label="播放位置（帧）"]')!;
  const frame = slider.value;
  const target = document.getElementById('settings-page-title')!;
  for (const key of ['ArrowRight', 'Delete', 'c', ' ', 'z']) {
    await act(async () =>
      target.dispatchEvent(
        new KeyboardEvent('keydown', {
          key,
          code: key === ' ' ? 'Space' : '',
          metaKey: key === 'z',
          bubbles: true,
          cancelable: true,
        }),
      ),
    );
  }
  expect(slider.value).toBe(frame);
  expect(apply).not.toHaveBeenCalled();
  await act(async () => {
    events.get('app:open-settings')!();
  });
  expect(host.querySelectorAll('[data-settings-page]')).toHaveLength(1);
});
it('编辑对话框打开时保留当前对话框，不从原生菜单遮盖未完成输入', async () => {
  await mount();
  await act(async () => document.getElementById('new-project-trigger')!.click());
  const dialog = document.querySelector('[role="dialog"]');
  expect(dialog).not.toBeNull();
  await act(async () => {
    events.get('app:open-settings')!();
  });
  expect(document.querySelector('[role="dialog"]')).toBe(dialog);
  expect(host.querySelector('[data-settings-page]')).toBeNull();
});

function button(label: string) {
  const found = [...document.querySelectorAll<HTMLButtonElement>('button')].find(
    (b) => b.textContent?.trim() === label,
  );
  if (!found) throw new Error(`未找到按钮：${label}`);
  return found;
}
async function clickButton(label: string) {
  await act(async () => {
    button(label).click();
    await settle();
  });
}
async function openMCPSettings() {
  await act(async () => {
    events.get('app:open-settings')!();
  });
  await clickButton('MCP');
}
it('设置中的MCP分类复用唯一面板，不自动启动或取凭据；分类与文档标题同步', async () => {
  await mount();
  await openMCPSettings();
  expect(document.title).toBe('MCP · 设置 · AIVidCut');
  expect(button('MCP').getAttribute('aria-current')).toBe('page');
  expect(button('外观').hasAttribute('aria-current')).toBe(false);
  expect(document.querySelectorAll('.mcp-panel')).toHaveLength(1);
  expect(
    document.querySelector<HTMLInputElement>('.mcp-panel input[type="checkbox"]')!.checked,
  ).toBe(false);
  expect(startMCP).not.toHaveBeenCalled();
  expect(getMCPConnection).not.toHaveBeenCalled();
  await clickButton('外观');
  expect(document.title).toBe('外观 · 设置 · AIVidCut');
  expect(document.querySelector('.mcp-panel')).toBeNull();
});
it('MCP设置支持默认只读启动、显式查看与清空配置、停止；完成后可返回工程', async () => {
  await mount();
  await openMCPSettings();
  await clickButton('启动只读连接');
  expect(startMCP).toHaveBeenCalledWith({ projectId: snapshot.project.id, allowEdits: false });
  expect(getMCPConnection).not.toHaveBeenCalled();
  await clickButton('查看连接配置…');
  const config = document.querySelector<HTMLTextAreaElement>('[role="dialog"] textarea')!;
  expect(config.value).toContain('settings-fixture-token');
  await clickButton('关闭并清空');
  expect(config.value).toBe('');
  await clickButton('停止工具连接');
  expect(stopMCP).toHaveBeenCalledOnce();
  expect(button('启动只读连接').disabled).toBe(false);
  await clickButton('返回应用');
  expect(document.querySelector<HTMLElement>('.editor-shell')!.hidden).toBe(false);
  expect(apply).not.toHaveBeenCalled();
});
it('启动中阻止切换分类或离开设置；失败显示原错误并恢复导航，可重试', async () => {
  await mount();
  await openMCPSettings();
  let reject!: (error: Error) => void;
  startMCP.mockImplementationOnce(
    () =>
      new Promise<MCPStatus>((_, fail) => {
        reject = fail;
      }),
  );
  await clickButton('启动只读连接');
  expect(button('返回应用').disabled).toBe(true);
  expect(button('外观').disabled).toBe(true);
  expect(document.querySelector('.mcp-panel')!.getAttribute('aria-busy')).toBe('true');
  await act(async () => {
    reject(new Error('本机端口暂时不可用'));
    await settle();
  });
  expect(document.querySelector('[role="alert"]')!.textContent).toContain('本机端口暂时不可用');
  expect(button('返回应用').disabled).toBe(false);
  expect(button('外观').disabled).toBe(false);
  expect(button('启动只读连接').disabled).toBe(false);
});
it('助手原工具连接入口直接进入MCP设置，只有一个控制面板', async () => {
  await mount();
  await act(async () => {
    document.getElementById('side-tabs-assistant')!.click();
    await settle();
  });
  await clickButton('连接设置');
  expect(document.title).toBe('MCP · 设置 · AIVidCut');
  expect(button('MCP').getAttribute('aria-current')).toBe('page');
  expect(document.querySelectorAll('.mcp-panel')).toHaveLength(1);
});

async function enterSettingField(label: string, value: string) {
  const input = [...document.querySelectorAll('label')]
    .find((element) => element.querySelector('.field-label')?.textContent === label)
    ?.querySelector('input');
  if (!(input instanceof HTMLInputElement)) throw new Error(`未找到字段 ${label}`);
  await act(async () => {
    input.focus();
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(input, value);
    input.dispatchEvent(new Event('input', { bubbles: true }));
    input.dispatchEvent(new Event('change', { bubbles: true }));
    await settle();
  });
  return input;
}
it('模型配置嵌入设置，未保存输入切页或关闭应用都先核对，放弃后清空实际密钥节点', async () => {
  await mount();
  await act(async () => {
    events.get('app:open-settings')!();
  });
  await clickButton('模型服务');
  expect(document.querySelector('[role="dialog"]')).toBeNull();
  await enterSettingField('连接名称', '未保存的服务');
  const key = await enterSettingField('API 密钥', 'fixture-unsaved-secret');
  expect(key.type).toBe('password');
  await clickButton('MCP');
  expect(document.querySelector('[role="dialog"]')!.textContent).toContain('放弃未保存');
  await clickButton('继续编辑');
  expect(key.value).toBe('fixture-unsaved-secret');
  await act(async () => {
    events.get('project:close-request')!();
    await settle();
  });
  expect(document.querySelector('[role="dialog"]')!.textContent).toContain('放弃未保存');
  expect(confirmClose).not.toHaveBeenCalled();
  await clickButton('继续编辑');
  await clickButton('MCP');
  await clickButton('放弃并离开');
  expect(document.title).toBe('MCP · 设置 · AIVidCut');
  expect(key.value).toBe('');
  expect(saveConnection).not.toHaveBeenCalled();
});
it('设置保存连接不生成媒体，保存中禁止离开或直接退出，完成后可正常返回', async () => {
  await mount();
  await act(async () => {
    events.get('app:open-settings')!();
  });
  await clickButton('模型服务');
  await enterSettingField('连接名称', '测试服务');
  await enterSettingField('API 密钥', 'fixture-save-secret');
  let finish!: (value: typeof configuredConnection) => void;
  saveConnection.mockImplementationOnce(
    () =>
      new Promise((resolve) => {
        finish = resolve;
      }),
  );
  await clickButton('保存连接');
  expect(button('返回应用').disabled).toBe(true);
  await act(async () => {
    events.get('project:close-request')!();
    await settle();
  });
  expect(document.querySelector('[role="dialog"]')!.textContent).toContain('设置操作尚未完成');
  expect(confirmClose).not.toHaveBeenCalled();
  await clickButton('继续等待');
  await act(async () => {
    finish(configuredConnection);
    await settle();
  });
  expect(saveConnection).toHaveBeenCalledOnce();
  expect(apply).not.toHaveBeenCalled();
  expect(button('返回应用').disabled).toBe(false);
});
it('本地AI分别展示规划与执行检测，完整诊断与登录命令只在设置读取', async () => {
  await mount();
  await act(async () => {
    events.get('app:open-settings')!();
  });
  await clickButton('本地 AI');
  expect(document.body.textContent).toContain('/fixture/codex');
  await clickButton('直接执行');
  expect(document.querySelector('[role="alert"]')!.textContent).toContain('执行能力尚未验证');
  expect(button('查看登录说明').disabled).toBe(true);
  await clickButton('剪辑规划');
  await clickButton('查看登录说明');
  expect(document.querySelector<HTMLTextAreaElement>('textarea')!.value).toContain(
    '/fixture/bin/codex',
  );
  expect(apply).not.toHaveBeenCalled();
});
it('工程低频操作只在工程菜单，摘要可键盘关闭且后台配置不再常驻任务页', async () => {
  await mount();
  const menu = document.querySelector<HTMLDetailsElement>('.project-menu')!;
  expect(menu.open).toBe(false);
  menu.open = true;
  await act(async () =>
    document
      .getElementById('project-menu-trigger')!
      .dispatchEvent(
        new KeyboardEvent('keydown', { key: 'Escape', bubbles: true, cancelable: true }),
      ),
  );
  expect(menu.open).toBe(false);
  expect(document.activeElement?.id).toBe('project-menu-trigger');
  expect(document.getElementById('media-tools-trigger')).toBeNull();
  expect(document.getElementById('resource-monitor-trigger')).toBeNull();
  expect(document.getElementById('assistant-tabs-mcp')).toBeNull();
});

it('退出时明确放弃设置立即清空密钥，再进入原有工程关闭核对，不遗留可见草稿', async () => {
  await mount();
  await act(async () => {
    events.get('app:open-settings')!();
  });
  await clickButton('模型服务');
  const key = await enterSettingField('API 密钥', 'fixture-discard-on-exit');
  await act(async () => {
    events.get('project:close-request')!();
    await settle();
  });
  await clickButton('放弃并离开');
  expect(key.value).toBe('');
  expect(confirmClose).toHaveBeenCalledWith('discard');
  expect(apply).not.toHaveBeenCalled();
});

it('外部连接运行时主界面保留状态直达入口，停止后收起提示', async () => {
  await mount();
  await openMCPSettings();
  await clickButton('启动只读连接');
  await clickButton('返回应用');
  expect(button('MCP 只读')).toBeTruthy();
  await clickButton('MCP 只读');
  expect(document.title).toBe('MCP · 设置 · AIVidCut');
  await clickButton('停止工具连接');
  await clickButton('返回应用');
  expect([...document.querySelectorAll('button')].some((b) => b.textContent === 'MCP 只读')).toBe(
    false,
  );
});
