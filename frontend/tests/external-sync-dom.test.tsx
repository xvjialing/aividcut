// @vitest-environment jsdom
import { installDesktop } from './desktop-fixture';
vi.mock('../src/lib/desktop', async () => (await import('./desktop-fixture')).desktopModule);
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { useEditor } from '../src/lib/use-editor';
import App from '../src/App';
import { Inspector } from '../src/components/Inspector';
import { Timeline } from '../src/components/Timeline';
import { TooltipProvider } from '../src/components/ui';
import { prepareToClose, commitActiveDraft } from '../src/lib/close-protection';
import { agentProject } from './agent-fixtures';
import type { Bootstrap } from '../src/types';
import type { MCPStatus } from '../src/mcp-types';

let container: HTMLDivElement;
let root: Root;
let current: ReturnType<typeof useEditor>;
const stopped: MCPStatus = {
  running: false,
  projectRevision: 7,
  allowEdits: false,
  scopeValid: false,
  audit: [],
};
const snapshot = (revision = 7): Bootstrap => ({
  project: { ...agentProject, revision },
  projectPath: '',
  dirty: false,
  canUndo: false,
  canRedo: false,
  capabilities: {},
});
function Harness({ hideInspector = false }: { hideInspector?: boolean }) {
  current = useEditor();
  const value = current.bootstrap;
  return value ? (
    <TooltipProvider>
      <output data-revision>{value.project.revision}</output>
      <output data-conflict>{current.externalChange?.reason}</output>
      {!hideInspector ? (
        <Inspector
          key={current.draftEpoch}
          project={value.project}
          clip={value.project.clips[0]}
          busy={Boolean(current.busy)}
          onUpdate={(clip) => void current.edit('更新片段', [{ type: 'update_clip', clip }])}
          onAddAsset={vi.fn()}
          onProjectName={vi.fn()}
        />
      ) : null}
      <button onClick={() => void current.reloadExternal()}>明确放弃草稿并读取</button>
      <Timeline
        project={value.project}
        clock={current.clock}
        selected={value.project.clips[0].id}
        busy={Boolean(current.busy)}
        onSelect={vi.fn()}
        onUpdateClip={(clip) => void current.edit('更新片段', [{ type: 'update_clip', clip }])}
        onUpdateTrack={vi.fn()}
        onSplit={vi.fn()}
        onDelete={vi.fn()}
        onText={vi.fn()}
        onAddTrack={vi.fn()}
      />
    </TooltipProvider>
  ) : null;
}
beforeEach(() => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
  vi.stubGlobal('cancelAnimationFrame', vi.fn());
  vi.stubGlobal(
    'ResizeObserver',
    class {
      observe() {}
      disconnect() {}
      unobserve() {}
    },
  );
  vi.spyOn(HTMLElement.prototype, 'clientWidth', 'get').mockReturnValue(1000);
  Object.assign(HTMLElement.prototype, {
    setPointerCapture: vi.fn(),
    hasPointerCapture: vi.fn().mockReturnValue(false),
    releasePointerCapture: vi.fn(),
  });
  container = document.createElement('div');
  document.body.append(container);
  root = createRoot(container);
});
afterEach(async () => {
  await act(async () => root.unmount());
  container.remove();
  delete window._wails;
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});
async function flush() {
  await new Promise((resolve) => setTimeout(resolve, 5));
}
async function mount() {
  const get = vi.fn().mockResolvedValue(snapshot());
  const apply = vi.fn().mockRejectedValue(new Error('工程版本已变化'));
  const status = vi.fn().mockResolvedValue(stopped);
  const fetch = vi.fn();
  vi.stubGlobal('fetch', fetch);
  installDesktop({
    GetBootstrap: get,
    ApplyEdit: apply,
    ListJobs: vi.fn().mockResolvedValue([]),
    GetMCPStatus: status,
  });
  await act(async () => {
    root.render(<Harness />);
    await flush();
  });
  return { get, apply, status, fetch };
}
function input() {
  const label = Array.from(container.querySelectorAll('label')).find(
    (value) => value.querySelector('.field-label')?.textContent === '起点 · 帧',
  );
  const control = label?.querySelector('input');
  if (!(control instanceof HTMLInputElement)) throw new Error('未渲染属性控件');
  return control;
}
async function type(value: string) {
  await act(async () => {
    const control = input();
    control.focus();
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(control, value);
    control.dispatchEvent(new Event('input', { bubbles: true }));
    control.dispatchEvent(new Event('change', { bubbles: true }));
    await flush();
  });
}
describe('外部MCP工程变化与实际属性草稿', () => {
  it('接受新模型时权威getter立即更新，早于React延后清理', async () => {
    await mount();
    const next = snapshot(8);
    await act(async () => {
      const rendered = current.bootstrap;
      current.acceptBootstrap(next);
      expect(rendered?.project.revision).toBe(7);
      expect(current.getCurrentBootstrap()).toBe(next);
    });
    expect(current.bootstrap?.project.revision).toBe(8);
  });
  it('挂起冲突即使属性DOM卸载仍阻止操作/关闭，后续状态读取不能暗中解除', async () => {
    const { get } = await mount();
    await type('25');
    get.mockResolvedValue(snapshot(8));
    const changed = {
      ...stopped,
      running: true,
      scopeValid: true,
      projectId: agentProject.id,
      projectRevision: 8,
    };
    await act(async () => {
      await current.acceptMCPStatus(changed);
      root.render(<Harness hideInspector />);
      await flush();
    });
    expect(container.querySelector('[data-editor-draft-scope]')).toBeNull();
    expect(await current.waitForIdle()).toBe(false);
    await act(async () => {
      await current.acceptMCPStatus(changed);
      await flush();
    });
    expect(get).toHaveBeenCalledTimes(1);
    expect(container.querySelector('[data-revision]')?.textContent).toBe('7');
    const read = vi.fn();
    expect(
      await prepareToClose({
        commitDraft: commitActiveDraft,
        waitForIdle: current.waitForIdle,
        read,
        accept: vi.fn(),
        discard: vi.fn(),
      }),
    ).toBe('invalid');
    expect(read).not.toHaveBeenCalled();
  });
  it('执行监听已结束且scope无效仍核对终态，重复相同终态不全量拉取', async () => {
    const { get } = await mount();
    get.mockResolvedValue(snapshot(8));
    const ended = {
      ...stopped,
      projectId: agentProject.id,
      projectRevision: 8,
      executionId: 'execution-ended',
    };
    await act(async () => {
      await current.acceptMCPStatus(ended);
      await flush();
    });
    expect(get).toHaveBeenCalledTimes(2);
    expect(container.querySelector('[data-revision]')?.textContent).toBe('8');
    await act(async () => {
      await current.acceptMCPStatus(ended);
      await flush();
    });
    expect(get).toHaveBeenCalledTimes(2);
  });
  it('真实App面板/片段导航先守住失败草稿，明确重读后才允许离开Inspector', async () => {
    const get = vi.fn().mockResolvedValue(snapshot());
    const apply = vi.fn().mockRejectedValue(new Error('工程版本已变化'));
    vi.stubGlobal('fetch', vi.fn());
    vi.spyOn(HTMLMediaElement.prototype, 'pause').mockImplementation(() => {});
    installDesktop({
      GetBootstrap: get,
      ApplyEdit: apply,
      ListJobs: vi.fn().mockResolvedValue([]),
      GetMCPStatus: vi.fn().mockResolvedValue(stopped),
      GetConnections: vi.fn().mockResolvedValue([]),
      GetGenerationCatalog: vi.fn().mockResolvedValue([]),
      ListGenerationJobs: vi.fn().mockResolvedValue([]),
    });
    await act(async () => {
      root.render(<App />);
      await flush();
    });
    const clip = container.querySelector('.timeline-clip .clip-face') as HTMLButtonElement;
    await act(async () => {
      clip.click();
      await flush();
    });
    await type('25');
    get.mockResolvedValue(snapshot(8));
    await act(async () => {
      input().blur();
      await flush();
    });
    const generation = container.querySelector('#side-tabs-generation') as HTMLButtonElement;
    await act(async () => {
      generation.click();
      await flush();
    });
    expect(input().value).toBe('25');
    expect(container.querySelector('#side-tabs-inspector')?.getAttribute('aria-selected')).toBe(
      'true',
    );
    const reload = Array.from(container.querySelectorAll('button')).find(
      (button) => button.textContent === '重新读取外部更改…',
    )!;
    await act(async () => {
      reload.click();
      await flush();
    });
    const confirm = Array.from(document.querySelectorAll('button')).find(
      (button) => button.textContent === '放弃本地草稿并读取',
    )!;
    await act(async () => {
      confirm.click();
      await flush();
    });
    expect(input().value).toBe('0');
    await act(async () => {
      generation.click();
      await flush();
    });
    expect(container.querySelector('#side-tabs-generation')?.getAttribute('aria-selected')).toBe(
      'true',
    );
  });
  it('真实时间线拖动标记阻止同步，旧手势不能配合新revision覆盖外部片段', async () => {
    const { get, apply } = await mount();
    get.mockResolvedValue(snapshot(8));
    const target = container.querySelector('.timeline-clip .clip-face');
    if (!(target instanceof HTMLButtonElement)) throw new Error('未渲染时间线片段');
    await act(async () => {
      target.dispatchEvent(
        new MouseEvent('pointerdown', { bubbles: true, button: 0, clientX: 0, clientY: 0 }),
      );
      await flush();
    });
    expect(container.querySelector('[data-editor-gesture="true"]')).not.toBeNull();
    await act(async () => {
      target.dispatchEvent(
        new MouseEvent('pointermove', { bubbles: true, clientX: 20, clientY: 0 }),
      );
      await current.acceptMCPStatus({
        ...stopped,
        running: true,
        scopeValid: true,
        projectId: agentProject.id,
        projectRevision: 8,
      });
      await flush();
    });
    expect(get).toHaveBeenCalledTimes(1);
    await act(async () => {
      target.dispatchEvent(new MouseEvent('pointerup', { bubbles: true, clientX: 20, clientY: 0 }));
      await flush();
    });
    expect(apply).not.toHaveBeenCalled();
    expect(container.querySelector('[data-revision]')?.textContent).toBe('7');
    expect(container.querySelector('[data-conflict]')?.textContent).toContain('重新读取后重试');
  });
  it('有输入草稿时只挂起同步，冲突失败也不擦掉草稿；明确重读才重挂同值属性', async () => {
    const { get, apply, fetch } = await mount();
    await type('25');
    get.mockResolvedValue(snapshot(8));
    await act(async () => {
      await current.acceptMCPStatus({
        ...stopped,
        running: true,
        scopeValid: true,
        projectId: agentProject.id,
        projectRevision: 8,
      });
      await flush();
    });
    expect(get).toHaveBeenCalledTimes(1);
    expect(input().value).toBe('25');
    expect(container.querySelector('[data-revision]')?.textContent).toBe('7');
    await act(async () => {
      input().blur();
      await flush();
    });
    expect(apply).not.toHaveBeenCalled();
    expect(await current.waitForIdle()).toBe(false);
    expect(input().value).toBe('25');
    const read = vi.fn();
    expect(
      await prepareToClose({
        commitDraft: commitActiveDraft,
        waitForIdle: current.waitForIdle,
        read,
        accept: vi.fn(),
        discard: vi.fn(),
      }),
    ).toBe('invalid');
    expect(read).not.toHaveBeenCalled();
    await act(async () => {
      await current.reloadExternal();
      await flush();
    });
    expect(input().value).toBe('0');
    expect(container.querySelector('[data-revision]')?.textContent).toBe('8');
    expect(fetch).not.toHaveBeenCalled();
  });
  it('在状态检测前已提交的旧revision失败，自动错误恢复仍保留真实DOM草稿', async () => {
    const { get, apply } = await mount();
    await type('25');
    get.mockResolvedValue(snapshot(8));
    await act(async () => {
      input().blur();
      await flush();
    });
    expect(apply).toHaveBeenCalledOnce();
    expect(input().value).toBe('25');
    expect(container.querySelector('[data-revision]')?.textContent).toBe('7');
    expect(container.querySelector('[data-conflict]')?.textContent).toContain('草稿已保留');
    expect(await current.waitForIdle()).toBe(false);
  });
  it('没有草稿时仅revision变化才拉工程，同版本轮询不全量读取', async () => {
    const { get } = await mount();
    get.mockResolvedValue(snapshot(8));
    const changed = {
      ...stopped,
      running: true,
      scopeValid: true,
      projectId: agentProject.id,
      projectRevision: 8,
    };
    await act(async () => {
      await current.acceptMCPStatus(changed);
      await flush();
    });
    expect(get).toHaveBeenCalledTimes(2);
    expect(container.querySelector('[data-revision]')?.textContent).toBe('8');
    await act(async () => {
      await current.acceptMCPStatus(changed);
      await flush();
    });
    expect(get).toHaveBeenCalledTimes(2);
  });
});
