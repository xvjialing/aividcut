// @vitest-environment jsdom

import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { AgentPanel } from '../src/components/AgentPanel';
import {
  agentApplied,
  agentClip,
  agentJob,
  agentProject,
  fakeAgentAPI,
  readyAgent,
} from './agent-fixtures';

let container: HTMLDivElement;
let root: Root;
let fetchGuard: ReturnType<typeof vi.fn>;
beforeEach(() => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
  fetchGuard = vi.fn().mockRejectedValue(new Error('测试禁止真实网络或CLI调用'));
  vi.stubGlobal('fetch', fetchGuard);
  container = document.createElement('div');
  document.body.append(container);
  root = createRoot(container);
});
afterEach(async () => {
  await act(async () => root.unmount());
  container.remove();
  expect(fetchGuard).not.toHaveBeenCalled();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});
async function flush() {
  await new Promise((resolve) => setTimeout(resolve, 5));
}
async function render(element: React.ReactNode) {
  await act(async () => {
    root.render(element);
    await flush();
  });
}
function button(label: string): HTMLButtonElement {
  const element = Array.from(document.querySelectorAll('button')).find(
    (value) => value.textContent?.trim() === label,
  );
  if (!element) throw new Error(`未找到按钮：${label}`);
  return element;
}
function field(label: string): HTMLTextAreaElement | HTMLSelectElement {
  const wrapper = Array.from(document.querySelectorAll('label')).find(
    (value) => value.querySelector('.field-label')?.textContent === label,
  );
  const element = wrapper?.querySelector('textarea,select');
  if (!(element instanceof HTMLTextAreaElement || element instanceof HTMLSelectElement))
    throw new Error(`未找到字段：${label}`);
  return element;
}
async function click(element: HTMLElement) {
  await act(async () => {
    element.click();
    await flush();
  });
}
async function enter(element: HTMLTextAreaElement | HTMLSelectElement, value: string) {
  await act(async () => {
    const prototype =
      element instanceof HTMLTextAreaElement
        ? HTMLTextAreaElement.prototype
        : HTMLSelectElement.prototype;
    Object.getOwnPropertyDescriptor(prototype, 'value')!.set!.call(element, value);
    element.dispatchEvent(new Event('input', { bubbles: true }));
    element.dispatchEvent(new Event('change', { bubbles: true }));
    await flush();
  });
}
function panel(
  api: ReturnType<typeof fakeAgentAPI>,
  options: Partial<React.ComponentProps<typeof AgentPanel>> = {},
) {
  return (
    <AgentPanel
      project={agentProject}
      selection={[agentClip.id]}
      onBootstrap={vi.fn()}
      onOpenGeneration={vi.fn()}
      api={api}
      {...options}
    />
  );
}

describe('本地CLI检测与手动登录说明', () => {
  it('未登录时明确禁用规划，登录说明只读取并复制，不自动执行或推理', async () => {
    const api = fakeAgentAPI();
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true });
    api.getAgentStatus.mockResolvedValue([
      {
        ...readyAgent,
        authState: 'signed_out',
        authReason: '独立作用域未登录',
        isolation: {
          ...readyAgent.isolation,
          supported: false,
          status: 'needs_login',
          reason: '请在独立作用域正常登录',
        },
      },
    ]);
    await render(panel(api));
    expect(document.body.textContent).toContain('此作用域未登录');
    expect(button('生成计划前核对…').disabled).toBe(true);
    const unavailable = Array.from(field('剪辑工作流').querySelectorAll('option')).find(
      (option) => option.value === 'talking-head',
    );
    expect(unavailable?.disabled).toBe(true);
    expect(document.body.textContent).toContain('需要源转写和停顿证据');
    expect(api.getAgentLoginInstructions).not.toHaveBeenCalled();
    await click(button('查看登录说明'));
    expect(api.getAgentLoginInstructions).toHaveBeenCalledWith('codex');
    expect(document.querySelector('[role="dialog"]')?.textContent).toContain('应用不会执行该命令');
    expect(field('登录命令').value).toContain('CODEX_HOME=/fixture/private/codex');
    await click(button('复制登录命令'));
    expect(writeText).toHaveBeenCalledWith(field('登录命令').value);
    await enter(field('终端格式'), 'powershell');
    expect(field('登录命令').value).toContain('$env:CODEX_HOME');
    await click(button('关闭说明'));
    expect(api.startAgentPlan).not.toHaveBeenCalled();
    expect(api.applyAgentPlan).not.toHaveBeenCalled();
  });
  it('安装但策略未验证时不因已登录而降级放行', async () => {
    const api = fakeAgentAPI();
    api.getAgentStatus.mockResolvedValue([
      {
        ...readyAgent,
        provider: 'claude',
        isolation: {
          ...readyAgent.isolation,
          supported: false,
          status: 'policy_unverified',
          reason: '策略 hooks 仍待验证',
        },
      },
    ]);
    await render(panel(api));
    expect(document.body.textContent).toContain('策略 hooks 仍待验证');
    expect(button('生成计划前核对…').disabled).toBe(true);
    expect(api.startAgentPlan).not.toHaveBeenCalled();
  });
});
describe('明确规划与单独应用', () => {
  it('只有额度确认才start，prompt仅为数据；摘要核对后显式应用到统一工程状态', async () => {
    const api = fakeAgentAPI();
    const onBootstrap = vi.fn();
    const prepareProject = vi.fn().mockResolvedValue(agentProject);
    const queued = agentJob({ status: 'queued', result: undefined, message: '等待本地规划' });
    api.startAgentPlan.mockResolvedValue(queued);
    await render(panel(api, { onBootstrap, prepareProject }));
    await click(button('生成计划前核对…'));
    expect(document.activeElement).toBe(field('剪辑要求'));
    expect(api.startAgentPlan).not.toHaveBeenCalled();
    const instruction = '将标题移到90帧；$(fixture-only) 只是描述文本';
    await enter(field('剪辑要求'), instruction);
    await click(button('生成计划前核对…'));
    expect(prepareProject).toHaveBeenCalledOnce();
    expect(document.querySelector('[role="dialog"]')?.textContent).toContain('可能消耗账号额度');
    expect(api.startAgentPlan).not.toHaveBeenCalled();
    await click(button('返回修改'));
    expect(api.startAgentPlan).not.toHaveBeenCalled();
    await click(button('生成计划前核对…'));
    await click(button('确认生成计划（可能消耗额度）'));
    expect(api.startAgentPlan).toHaveBeenCalledOnce();
    expect(api.startAgentPlan.mock.calls[0][0]).toEqual(
      expect.objectContaining({
        provider: 'codex',
        instruction,
        workflow: 'manual-plan',
        selection: [agentClip.id],
        consent: true,
      }),
    );
    expect(api.applyAgentPlan).not.toHaveBeenCalled();
    expect(onBootstrap).not.toHaveBeenCalled();
    api.listAgentPlans.mockResolvedValue([agentJob()]);
    await click(button('刷新记录'));
    expect(document.body.textContent).toContain('时间线起点：0 → 90 帧');
    expect(document.body.textContent).toContain('已通过工程副本校验');
    expect(api.applyAgentPlan).not.toHaveBeenCalled();
    await click(button('应用计划到时间线'));
    expect(api.applyAgentPlan).toHaveBeenCalledWith('agent-job');
    expect(onBootstrap).toHaveBeenCalledWith(agentApplied);
    expect(agentApplied.canUndo).toBe(true);
  });
  it('旧工程、旧版本和未校验计划禁应用，Undo不解除已应用历史', async () => {
    const api = fakeAgentAPI();
    api.listAgentPlans.mockResolvedValue([agentJob()]);
    await render(panel(api));
    await click(button('规划记录 1'));
    await render(panel(api, { project: { ...agentProject, revision: 8 } }));
    expect(button('应用计划到时间线').disabled).toBe(true);
    expect(document.body.textContent).toContain('版本已变化');
    await render(panel(api, { project: { ...agentProject, id: 'other' } }));
    expect(button('应用计划到时间线').disabled).toBe(true);
    expect(document.body.textContent).toContain('当前其他工程');
    const unvalidated = agentJob();
    unvalidated.result!.coreValidated = false;
    api.listAgentPlans.mockResolvedValue([unvalidated]);
    await render(panel(api));
    await click(button('刷新记录'));
    expect(button('应用计划到时间线').disabled).toBe(true);
    expect(document.body.textContent).toContain('未通过编辑核心校验');
    api.listAgentPlans.mockResolvedValue([agentJob({ applied: true })]);
    await render(panel(api));
    await click(button('刷新记录'));
    expect(button('计划已应用').disabled).toBe(true);
    expect(api.applyAgentPlan).not.toHaveBeenCalled();
  });
  it('取消显示服务真实的cancelling再读取稳定状态，不伪造已取消或应用', async () => {
    const api = fakeAgentAPI();
    const running = agentJob({ status: 'running', result: undefined });
    api.listAgentPlans.mockResolvedValue([running]);
    api.cancelAgentPlan.mockResolvedValue({
      ...running,
      status: 'cancelling',
      message: '取消请求已记录，正在结束本地进程',
    });
    await render(panel(api));
    await click(button('规划记录 1'));
    await click(button('取消本地规划'));
    expect(api.cancelAgentPlan).toHaveBeenCalledWith(running.id);
    expect(document.body.textContent).toContain('正在取消本地规划');
    expect(document.body.textContent).not.toContain('已取消本地规划');
    api.listAgentPlans.mockResolvedValue([
      { ...running, status: 'cancelled', message: '本地进程已结束' },
    ]);
    await click(button('刷新记录'));
    expect(document.body.textContent).toContain('已取消本地规划');
    expect(api.startAgentPlan).not.toHaveBeenCalled();
    expect(api.applyAgentPlan).not.toHaveBeenCalled();
  });
  it('核对前工程已变化时停止，失败保留描述且不自动重交', async () => {
    const api = fakeAgentAPI();
    const prepareProject = vi.fn().mockResolvedValue({ ...agentProject, revision: 8 });
    await render(panel(api, { prepareProject }));
    await enter(field('剪辑要求'), '移动标题到90帧');
    await click(button('生成计划前核对…'));
    expect(document.body.textContent).toContain('工程已变化');
    expect(api.startAgentPlan).not.toHaveBeenCalled();
    prepareProject.mockResolvedValue(agentProject);
    await click(button('生成计划前核对…'));
    await click(button('确认生成计划（可能消耗额度）'));
    expect(api.startAgentPlan).toHaveBeenCalledOnce();
    expect(document.body.textContent).toContain('不会自动重交');
    expect(field('剪辑要求').value).toBe('移动标题到90帧');
    await click(button('重新检测'));
    expect(api.startAgentPlan).toHaveBeenCalledOnce();
    expect(api.applyAgentPlan).not.toHaveBeenCalled();
    const firstRequestID = api.startAgentPlan.mock.calls[0][0].id;
    await click(button('生成计划前核对…'));
    expect(document.querySelector('[role="dialog"]')?.textContent).toContain('沿用原请求编号');
    expect(api.startAgentPlan).toHaveBeenCalledOnce();
    await click(button('确认生成计划（可能消耗额度）'));
    expect(api.startAgentPlan).toHaveBeenCalledTimes(2);
    expect(api.startAgentPlan.mock.calls[1][0].id).toBe(firstRequestID);
  });
  it('应用已提交而记录刷新失败时仍标明已应用，不能误报失败或重复提交', async () => {
    const api = fakeAgentAPI();
    const onBootstrap = vi.fn();
    api.listAgentPlans.mockResolvedValue([agentJob()]);
    api.applyAgentPlan.mockImplementation(async () => {
      api.listAgentPlans.mockRejectedValue(new Error('记录暂不可读'));
      return agentApplied;
    });
    await render(panel(api, { onBootstrap }));
    await click(button('规划记录 1'));
    await click(button('应用计划到时间线'));
    expect(onBootstrap).toHaveBeenCalledWith(agentApplied);
    expect(document.body.textContent).toContain('计划已应用，但暂时无法刷新规划记录');
    expect(button('计划已应用').disabled).toBe(true);
    expect(api.applyAgentPlan).toHaveBeenCalledOnce();
  });
});
