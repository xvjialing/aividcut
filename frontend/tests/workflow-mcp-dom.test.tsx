// @vitest-environment jsdom
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { WorkflowPanel } from '../src/components/WorkflowPanel';
import { MCPPanel } from '../src/components/MCPPanel';
import { visualAnalysis } from './visual-analysis-fixtures';
import type { MCPAPI } from '../src/mcp-types';
import { validateWorkflowParameters } from '../src/lib/workflows';
import { agentApplied, agentProject } from './agent-fixtures';
import { configuredConnection } from './generation-fixtures';
import {
  fakeMCPAPI,
  fakeWorkflowAPI,
  mcpAction,
  runningMCP,
  workflowDefinition,
  workflowPreset,
  workflowPreview,
} from './workflow-mcp-fixtures';
let container: HTMLDivElement;
let root: Root;
let fetchGuard: ReturnType<typeof vi.fn>;
beforeEach(() => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
  fetchGuard = vi.fn().mockRejectedValue(new Error('测试禁止真实网络/CLI/收费'));
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
function button(text: string): HTMLButtonElement {
  const value = Array.from(document.querySelectorAll('button')).find(
    (item) => item.textContent?.trim() === text,
  );
  if (!value) throw new Error(`未找到按钮：${text}`);
  return value;
}
function field(text: string): HTMLInputElement | HTMLSelectElement | HTMLTextAreaElement {
  const label = Array.from(document.querySelectorAll('label')).find(
    (value) => value.querySelector('.field-label')?.textContent === text,
  );
  const input = label?.querySelector('input,select,textarea');
  if (!(
    input instanceof HTMLInputElement ||
    input instanceof HTMLSelectElement ||
    input instanceof HTMLTextAreaElement
  ))
    throw new Error(`未找到字段：${text}`);
  return input;
}
function check(text: string): HTMLInputElement {
  const input = Array.from(document.querySelectorAll('label'))
    .find((value) => value.textContent?.includes(text))
    ?.querySelector('input[type="checkbox"]');
  if (!(input instanceof HTMLInputElement)) throw new Error(`未找到开关：${text}`);
  return input;
}
async function click(value: HTMLElement) {
  await act(async () => {
    value.click();
    await flush();
  });
}
async function enter(
  input: HTMLInputElement | HTMLSelectElement | HTMLTextAreaElement,
  value: string,
) {
  await act(async () => {
    const prototype =
      input instanceof HTMLInputElement
        ? HTMLInputElement.prototype
        : input instanceof HTMLSelectElement
          ? HTMLSelectElement.prototype
          : HTMLTextAreaElement.prototype;
    Object.getOwnPropertyDescriptor(prototype, 'value')!.set!.call(input, value);
    input.dispatchEvent(new Event('input', { bubbles: true }));
    input.dispatchEvent(new Event('change', { bubbles: true }));
    await flush();
  });
}
function workflowPanel(
  api: ReturnType<typeof fakeWorkflowAPI>,
  options: Partial<React.ComponentProps<typeof WorkflowPanel>> = {},
) {
  return (
    <WorkflowPanel
      project={agentProject}
      selection={[agentProject.clips[0].id]}
      onBootstrap={vi.fn()}
      api={api}
      {...options}
    />
  );
}
const mcpProfiles = async () => [configuredConnection];
function mcpPanel(
  api: ReturnType<typeof fakeMCPAPI>,
  options: Partial<React.ComponentProps<typeof MCPPanel>> = {},
) {
  return (
    <MCPPanel
      project={agentProject}
      onStatus={vi.fn()}
      onOpenJobs={vi.fn()}
      getConnections={mcpProfiles}
      native={false}
      api={api}
      {...options}
    />
  );
}

describe('参数工作流与预设', () => {
  it('批量声音默认不扩大空选区，清除忽略设置参数并明确传范围', async () => {
    const api = fakeWorkflowAPI();
    api.getWorkflowCatalog.mockResolvedValue([
      {
        ...workflowDefinition,
        id: 'audio-fades',
        parameters: [
          {
            id: 'mode',
            name: '操作',
            type: 'select',
            required: true,
            description: '',
            options: [
              { value: 'set', name: '设置淡入淡出' },
              { value: 'clear', name: '清除已有淡化' },
            ],
          },
          {
            id: 'inMs',
            name: '淡入 · ms',
            type: 'integer',
            required: true,
            min: 0,
            max: 86400000,
            description: '',
          },
        ],
        defaults: { mode: 'set', inMs: 100 },
      },
    ]);
    await render(workflowPanel(api, { selection: [] }));
    expect(field('操作范围').value).toBe('selection');
    expect(button('预览影响').disabled).toBe(true);
    await enter(field('操作范围'), 'project');
    await enter(field('淡入 · ms'), '-1');
    await click(button('预览影响'));
    expect(api.previewWorkflow).not.toHaveBeenCalled();
    await enter(field('操作'), 'clear');
    expect(field('淡入 · ms').disabled).toBe(true);
    await click(button('预览影响'));
    expect(api.previewWorkflow).toHaveBeenCalledWith({
      workflowId: 'audio-fades',
      scope: 'project',
      selection: [],
      parameters: { mode: 'clear' },
    });
    expect(api.applyWorkflowPreview).not.toHaveBeenCalled();
  });
  it('预览期间选区变化不会接纳迟到的旧结果', async () => {
    const api = fakeWorkflowAPI();
    let finish!: (value: typeof workflowPreview) => void;
    api.previewWorkflow.mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          finish = resolve;
        }),
    );
    await render(workflowPanel(api));
    await click(button('预览影响'));
    expect(api.previewWorkflow).toHaveBeenCalledTimes(1);
    await render(workflowPanel(api, { selection: [] }));
    await act(async () => {
      finish(workflowPreview);
      await flush();
    });
    expect(document.body.textContent).toContain('旧结果未载入');
    expect(
      [...document.querySelectorAll('button')].some((b) => b.textContent === '应用预览并保留撤销'),
    ).toBe(false);
  });
  it('口播候选来源与全轨后果分开，显式选全轨且参数变更清旧预览', async () => {
    const api = fakeWorkflowAPI();
    const definition = {
      ...workflowDefinition,
      id: 'talking-head',
      parameters: [
        {
          id: 'rippleScope',
          name: '闭合范围',
          type: 'select' as const,
          required: true,
          description: '全轨包含未选音乐和标题',
          options: [
            { value: 'selection', name: '仅选中完整音画组' },
            { value: 'all_tracks', name: '所有轨道（含未选内容）' },
          ],
        },
      ],
      defaults: { rippleScope: 'selection' },
    };
    api.getWorkflowCatalog.mockResolvedValue([definition]);
    api.previewWorkflow.mockResolvedValue({
      ...workflowPreview,
      workflowId: 'talking-head',
      speechRippleSummary: {
        cutCount: 2,
        removedFrames: 94,
        beforeFrames: 300,
        afterFrames: 206,
        affectedTracks: 5,
        totalAffectedClips: 3500,
        changedOriginalClips: 3490,
        newClips: 10,
      },
      summary: '全轨闭合2处词间候选',
      warnings: ['会裁剪未选中、隐藏和静音轨'],
    });
    await render(workflowPanel(api));
    expect(field('闭合范围').value).toBe('selection');
    expect(field('停顿候选来源').value).toBe('selection');
    await enter(field('闭合范围'), 'all_tracks');
    expect(api.previewWorkflow).not.toHaveBeenCalled();
    await click(button('预览影响'));
    expect(api.previewWorkflow).toHaveBeenCalledWith(
      expect.objectContaining({ parameters: { rippleScope: 'all_tracks' } }),
    );
    expect(document.body.textContent).toContain('变更原片段 3490 个，新增存续片段 10 个');
    expect(document.body.textContent).toContain('隐藏和静音轨');
    expect(api.applyWorkflowPreview).not.toHaveBeenCalled();
    await enter(field('闭合范围'), 'selection');
    expect(
      [...document.querySelectorAll('button')].some((b) => b.textContent === '应用预览并保留撤销'),
    ).toBe(false);
  });
  it('动态参数执行范围/步长/类型校验，预览证据后显式应用才修改工程', async () => {
    const api = fakeWorkflowAPI();
    const onBootstrap = vi.fn();
    await render(workflowPanel(api, { onBootstrap }));
    expect(api.previewWorkflow).not.toHaveBeenCalled();
    await enter(field('保留边距 · 帧'), '2.5');
    await click(button('预览影响'));
    expect(api.previewWorkflow).not.toHaveBeenCalled();
    await enter(field('保留边距 · 帧'), '6');
    await click(field('保留文字'));
    await click(button('预览影响'));
    expect(api.previewWorkflow).toHaveBeenCalledWith(
      expect.objectContaining({
        workflowId: workflowDefinition.id,
        selection: [agentProject.clips[0].id],
        parameters: expect.objectContaining({ paddingFrames: 6, preserve: true, gain: 1 }),
      }),
    );
    expect(document.body.textContent).toContain('成片缩短 15 帧');
    expect(document.body.textContent).toContain('已接受源词证据');
    expect(api.applyWorkflowPreview).not.toHaveBeenCalled();
    await click(button('应用预览并保留撤销'));
    expect(api.applyWorkflowPreview).toHaveBeenCalledWith('preview-1');
    expect(onBootstrap).toHaveBeenCalledWith(agentApplied);
  });
  it('预设不自动载入，更新/删除携带baseVersion，保存本身不运行工作流', async () => {
    const api = fakeWorkflowAPI();
    await render(workflowPanel(api));
    await enter(field('参数预设'), workflowPreset.id);
    expect(field('保留边距 · 帧').value).toBe('3');
    await click(button('载入预设参数'));
    expect(field('保留边距 · 帧').value).toBe('8');
    await click(button('更新此预设…'));
    expect(api.saveWorkflowPreset).not.toHaveBeenCalled();
    await click(button('保存预设'));
    expect(api.saveWorkflowPreset).toHaveBeenCalledWith(
      expect.objectContaining({
        id: workflowPreset.id,
        baseVersion: 2,
        parameters: expect.objectContaining({ paddingFrames: 8 }),
      }),
    );
    await click(button('删除此预设…'));
    expect(api.deleteWorkflowPreset).not.toHaveBeenCalled();
    await click(button('确认删除预设'));
    expect(api.deleteWorkflowPreset).toHaveBeenCalledWith(workflowPreset.id, 3);
    expect(api.previewWorkflow).not.toHaveBeenCalled();
  });
  it('缺少源词证据禁预览；工程revision变化后旧预览禁应用', async () => {
    const api = fakeWorkflowAPI();
    await render(workflowPanel(api));
    await click(button('预览影响'));
    await render(workflowPanel(api, { project: { ...agentProject, revision: 8 } }));
    expect(button('应用预览并保留撤销').disabled).toBe(true);
    expect(api.applyWorkflowPreview).not.toHaveBeenCalled();
    await enter(field('工作流'), 'needs-transcript');
    expect(document.body.textContent).toContain('间隙不能当作静音证据');
    expect(button('预览影响').disabled).toBe(true);
  });
  it('后台重读发现新预设版本时，旧参数草稿仍用原baseVersion提交并显示冲突', async () => {
    const api = fakeWorkflowAPI();
    await render(workflowPanel(api));
    await enter(field('参数预设'), workflowPreset.id);
    await click(button('载入预设参数'));
    api.getWorkflowPresets.mockResolvedValue([
      {
        ...workflowPreset,
        version: 3,
        parameters: { ...workflowPreset.parameters, paddingFrames: 12 },
      },
    ]);
    api.saveWorkflowPreset.mockRejectedValue(new Error('预设修订已变化，请重新读取'));
    await render(workflowPanel(api, { project: { ...agentProject, revision: 8 } }));
    expect(field('保留边距 · 帧').value).toBe('8');
    await click(button('更新此预设…'));
    await click(button('保存预设'));
    expect(api.saveWorkflowPreset).toHaveBeenCalledWith(
      expect.objectContaining({
        id: workflowPreset.id,
        baseVersion: 2,
        parameters: expect.objectContaining({ paddingFrames: 8 }),
      }),
    );
    expect(document.querySelector('[role="dialog"]')?.textContent).toContain('预设修订已变化');
    expect(api.applyWorkflowPreview).not.toHaveBeenCalled();
  });
  it('参数默认值中的false保持布尔，不将小数或步长错误默认为可用', () => {
    expect(
      validateWorkflowParameters(workflowDefinition, { ...workflowDefinition.defaults, gain: 1.05 })
        .errors.gain,
    ).toBeTruthy();
    const result = validateWorkflowParameters(workflowDefinition, workflowDefinition.defaults);
    expect(result.errors).toEqual({});
    expect(result.values.preserve).toBe(false);
  });
});
describe('MCP显式连接与凭据作用域', () => {
  it('源画面默认关闭，明确开启仍保持只读并单独传递权限', async () => {
    const api = fakeMCPAPI();
    await render(mcpPanel(api));
    const frames = check('允许向外部工具提供');
    expect(frames.checked).toBe(false);
    await click(frames);
    expect(api.startMCP).not.toHaveBeenCalled();
    await click(button('启动只读连接'));
    expect(api.startMCP).toHaveBeenCalledWith({
      projectId: agentProject.id,
      allowEdits: false,
      allowSourceFrames: true,
    });
    expect(api.getMCPConnection).not.toHaveBeenCalled();
  });
  it('默认只读且不自动启动/取凭据；显式查看复制后关闭清空实际文本', async () => {
    const api = fakeMCPAPI();
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true });
    await render(mcpPanel(api));
    expect(check('允许外部工具直接提交').checked).toBe(false);
    expect(api.startMCP).not.toHaveBeenCalled();
    expect(api.getMCPConnection).not.toHaveBeenCalled();
    await click(button('启动只读连接'));
    expect(api.startMCP).toHaveBeenCalledWith({ projectId: agentProject.id, allowEdits: false });
    expect(api.getMCPConnection).not.toHaveBeenCalled();
    await click(button('查看连接配置…'));
    const config = field('MCP 连接配置');
    expect(config.value).toContain('fixture-only-test-token');
    await click(button('复制连接配置'));
    expect(writeText).toHaveBeenCalledWith(config.value);
    await click(button('关闭并清空'));
    expect(config.value).toBe('');
    expect(document.body.textContent).not.toContain('fixture-only-test-token');
    expect(api.approveMCPRequest).not.toHaveBeenCalled();
  });
  it('编辑权限必须明确勾选，scope失效禁查看并可显式停止', async () => {
    const api = fakeMCPAPI();
    api.startMCP.mockResolvedValue({ ...runningMCP, allowEdits: true });
    await render(mcpPanel(api));
    await click(check('允许外部工具直接提交'));
    await click(button('启动并允许可撤销编辑'));
    expect(api.startMCP).toHaveBeenCalledWith({ projectId: agentProject.id, allowEdits: true });
    await render(
      mcpPanel(api, { status: { ...runningMCP, scopeValid: false, reason: '原工程已切换' } }),
    );
    expect(button('查看连接配置…').disabled).toBe(true);
    expect(document.body.textContent).toContain('原工程已切换');
    await click(button('停止工具连接'));
    expect(api.stopMCP).toHaveBeenCalledOnce();
    expect(api.getMCPConnection).not.toHaveBeenCalled();
  });
  it('工程scope失效时立即清空已显示的真实配置文本，不再自动读取凭据', async () => {
    const api = fakeMCPAPI();
    api.getMCPStatus.mockResolvedValue(runningMCP);
    await render(mcpPanel(api));
    await click(button('查看连接配置…'));
    const config = field('MCP 连接配置');
    await render(
      mcpPanel(api, { status: { ...runningMCP, scopeValid: false, reason: '原实例已关闭' } }),
    );
    expect(config.value).toBe('');
    expect(document.querySelector('[role="dialog"]')).toBeNull();
    expect(api.getMCPConnection).toHaveBeenCalledOnce();
  });
});
describe('外部请求只准备，客户端明确审批', () => {
  it('正常执行结束后工具scope已停，请求自己的canApprove仍可单独确认', async () => {
    const api = fakeMCPAPI();
    api.getMCPStatus.mockResolvedValue({
      running: false,
      projectId: agentProject.id,
      projectRevision: 7,
      allowEdits: false,
      scopeValid: false,
      executionId: 'ended-run',
      audit: [],
    });
    api.listMCPRequests.mockResolvedValue([
      mcpAction({ executionId: 'ended-run', canApprove: true }),
    ]);
    await render(mcpPanel(api));
    expect(button('核对本次请求…').disabled).toBe(false);
    await click(button('核对本次请求…'));
    expect(api.approveMCPRequest).not.toHaveBeenCalled();
    await click(button('确认本次请求（可能计费）'));
    expect(api.approveMCPRequest).toHaveBeenCalledOnce();
  });
  it('新工具scope不能给旧请求授权，应用返回的逐请求原因决定禁用', async () => {
    const api = fakeMCPAPI();
    api.getMCPStatus.mockResolvedValue(runningMCP);
    api.listMCPRequests.mockResolvedValue([
      mcpAction({ canApprove: false, approvalReason: '原请求作用域已撤销' }),
    ]);
    await render(mcpPanel(api));
    expect(button('核对本次请求…').disabled).toBe(true);
    expect(document.body.textContent).toContain('原请求作用域已撤销');
    expect(api.approveMCPRequest).not.toHaveBeenCalled();
  });
  it('画面分析显示推断与离散证据，明确接纳才走共享工程通道且不调用收费确认', async () => {
    const api = {
      ...fakeMCPAPI(),
      acceptVisualAnalysis: vi
        .fn<NonNullable<MCPAPI['acceptVisualAnalysis']>>()
        .mockResolvedValue(agentApplied),
    };
    api.listMCPRequests.mockResolvedValue([
      mcpAction({ kind: 'analysis', generation: undefined, analysis: visualAnalysis }),
    ]);
    const apply = vi.fn(async (action: () => Promise<typeof agentApplied>) => action());
    await render(mcpPanel(api, { applyAnalysis: apply }));
    await click(button('核对本次请求…'));
    expect(document.querySelector('[role="dialog"]')?.textContent).toContain('未确认推断');
    expect(document.querySelector('[role="dialog"]')?.textContent).not.toContain(
      '实际费用按供应商规则',
    );
    expect(api.acceptVisualAnalysis).not.toHaveBeenCalled();
    expect(api.approveMCPRequest).not.toHaveBeenCalled();
    await click(button('接纳到工程（可撤销）'));
    expect(apply).toHaveBeenCalledTimes(1);
    expect(api.acceptVisualAnalysis).toHaveBeenCalledWith({
      id: 'prepared-action',
      sourceEpoch: 0,
      projectId: agentProject.id,
      projectRevision: agentProject.revision,
    });
    expect(api.approveMCPRequest).not.toHaveBeenCalled();
  });
  it('列表和核对框不审批，精确模型/数量/内容展示后才确认收费请求', async () => {
    const api = fakeMCPAPI();
    api.getMCPStatus.mockResolvedValue(runningMCP);
    api.listMCPRequests.mockResolvedValue([mcpAction()]);
    await render(mcpPanel(api));
    expect(api.approveMCPRequest).not.toHaveBeenCalled();
    await click(button('核对本次请求…'));
    expect(document.querySelector('[role="dialog"]')?.textContent).toContain(
      configuredConnection.modelId,
    );
    expect(document.querySelector('[role="dialog"]')?.textContent).toContain('补充城市远景');
    expect(api.approveMCPRequest).not.toHaveBeenCalled();
    await click(button('确认本次请求（可能计费）'));
    expect(api.approveMCPRequest).toHaveBeenCalledWith('prepared-action', { overwrite: false });
  });
  it('MCP 视频请求核对明确展示声音，不在打开时审批', async () => {
    const api = fakeMCPAPI();
    api.getMCPStatus.mockResolvedValue(runningMCP);
    api.listMCPRequests.mockResolvedValue([
      mcpAction({
        generation: {
          id: 'video-sound',
          providerId: 'seedance',
          modelId: 'doubao-seedance-2-5-260628',
          mode: 'video.text',
          prompt: '城市远景',
          generateAudio: false,
        },
      }),
    ]);
    await render(mcpPanel(api));
    await click(button('核对本次请求…'));
    expect(document.querySelector('[role="dialog"]')?.textContent).toContain('视频声音');
    expect(document.querySelector('[role="dialog"]')?.textContent).toContain('无声视频');
    expect(api.approveMCPRequest).not.toHaveBeenCalled();
  });
  it('克隆不采纳模型授权字段，用户必须重新填写主体与授权声明', async () => {
    const api = fakeMCPAPI();
    api.getMCPStatus.mockResolvedValue(runningMCP);
    api.listMCPRequests.mockResolvedValue([
      mcpAction({
        kind: 'speech',
        generation: undefined,
        speech: {
          id: 'speech-prepared',
          connectionId: 'voice-connection',
          mode: 'clone',
          consent: false,
          clone: {
            name: '自定义音色',
            assetIds: [],
            consent: { subject: '模型自称授权', authority: 'self', confirmed: true },
          },
        },
      }),
    ]);
    await render(mcpPanel(api));
    await click(button('核对本次请求…'));
    expect(field('重新填写声音主体').value).toBe('');
    await click(button('确认本次请求（可能计费）'));
    expect(api.approveMCPRequest).not.toHaveBeenCalled();
    await enter(field('重新填写声音主体'), '真人确认主体');
    await enter(field('本人的授权声明'), 'authorized');
    await click(check('我确认有权上传这些样本'));
    await click(button('确认本次请求（可能计费）'));
    expect(api.approveMCPRequest).toHaveBeenCalledWith('prepared-action', {
      overwrite: false,
      cloneConsent: { confirmed: true, subject: '真人确认主体', authority: 'authorized' },
    });
  });
  it('导出路径只由用户提供；旧revision请求禁确认', async () => {
    const api = fakeMCPAPI();
    api.getMCPStatus.mockResolvedValue(runningMCP);
    api.listMCPRequests.mockResolvedValue([
      mcpAction({ kind: 'export', generation: undefined, export: { preset: '1080p' } }),
    ]);
    await render(mcpPanel(api));
    await click(button('核对本次请求…'));
    await click(button('确认并选择导出'));
    expect(api.approveMCPRequest).not.toHaveBeenCalled();
    await enter(field('本机导出路径'), '/tmp/user-selected-export.mp4');
    await click(button('确认并选择导出'));
    expect(api.approveMCPRequest).toHaveBeenCalledWith('prepared-action', {
      overwrite: false,
      exportPath: '/tmp/user-selected-export.mp4',
    });
    api.listMCPRequests.mockResolvedValue([mcpAction({ projectRevision: 6 })]);
    await click(button('刷新工具状态'));
    expect(button('核对本次请求…').disabled).toBe(true);
  });
});

it('设置中的预设管理载入当前参数但不执行工作流，保存保持版本核对', async () => {
  const api = fakeWorkflowAPI();
  const dirty = vi.fn();
  await render(
    workflowPanel(api, {
      presetManagement: true,
      initialDraft: {
        workflowId: workflowDefinition.id,
        parameters: { ...workflowDefinition.defaults, paddingFrames: 14 },
        presetId: workflowPreset.id,
        presetVersion: 1,
      },
      onDirtyChange: dirty,
    }),
  );
  expect(field('保留边距 · 帧').value).toBe('14');
  expect(
    [...document.querySelectorAll('button')].some((b) => b.textContent?.trim() === '预览影响'),
  ).toBe(false);
  await enter(field('保留边距 · 帧'), '16');
  expect(dirty).toHaveBeenLastCalledWith(true);
  await click(button('更新此预设…'));
  await click(button('保存预设'));
  expect(api.saveWorkflowPreset).toHaveBeenCalledWith(
    expect.objectContaining({
      id: workflowPreset.id,
      baseVersion: 1,
      parameters: expect.objectContaining({ paddingFrames: 16 }),
    }),
  );
  expect(api.previewWorkflow).not.toHaveBeenCalled();
  expect(api.applyWorkflowPreview).not.toHaveBeenCalled();
  expect(dirty).toHaveBeenLastCalledWith(false);
});

it('工作台预设入口传递当前草稿，主面板只保留载入与执行', async () => {
  const api = fakeWorkflowAPI(),
    manage = vi.fn();
  await render(workflowPanel(api, { onManagePresets: manage }));
  await enter(field('保留边距 · 帧'), '12');
  await click(button('管理预设'));
  expect(manage).toHaveBeenCalledWith(
    expect.objectContaining({
      workflowId: workflowDefinition.id,
      parameters: expect.objectContaining({ paddingFrames: '12' }),
    }),
  );
  expect(
    [...document.querySelectorAll('button')].some((b) => b.textContent?.trim() === '保存为新预设…'),
  ).toBe(false);
  expect(api.saveWorkflowPreset).not.toHaveBeenCalled();
  expect(button('预览影响')).toBeTruthy();
});
