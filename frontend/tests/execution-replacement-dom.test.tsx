// @vitest-environment jsdom
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { AgentExecutionPanel } from '../src/components/AgentExecutionPanel';
import {
  GenerationReplacementDialog,
  replacementSourceRange,
} from '../src/components/GenerationReplacementDialog';
import { GenerationPanel } from '../src/components/GenerationPanel';
import { executionUnavailable } from '../src/lib/agent-execution';
import { readyAgent } from './agent-fixtures';
import { fakeGenerationAPI } from './generation-fixtures';
import {
  candidateAsset,
  candidateJob,
  candidateOutput,
  executionInstallation,
  executionJob,
  fakeExecutionAPI,
  fakeReplacementAPI,
  originalClip,
  replacedBootstrap,
  replacementPreview,
  replacementProject,
} from './execution-replacement-fixtures';

let container: HTMLDivElement;
let root: Root;
let fetchGuard: ReturnType<typeof vi.fn>;
const workflowGetter = async () => [];
beforeEach(() => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
  fetchGuard = vi.fn().mockRejectedValue(new Error('测试禁止真实CLI/收费/网络'));
  vi.stubGlobal('fetch', fetchGuard);
  vi.spyOn(HTMLMediaElement.prototype, 'pause').mockImplementation(() => {});
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
function field(text: string): HTMLInputElement | HTMLTextAreaElement | HTMLSelectElement {
  const label = Array.from(document.querySelectorAll('label')).find(
    (item) => item.querySelector('.field-label')?.textContent === text,
  );
  const value = label?.querySelector('input,select,textarea');
  if (!(
    value instanceof HTMLInputElement ||
    value instanceof HTMLTextAreaElement ||
    value instanceof HTMLSelectElement
  ))
    throw new Error(`未找到字段：${text}`);
  return value;
}
function check(text: string): HTMLInputElement {
  const value = Array.from(document.querySelectorAll('label'))
    .find((item) => item.textContent?.includes(text))
    ?.querySelector('input[type="checkbox"]');
  if (!(value instanceof HTMLInputElement)) throw new Error(`未找到勾选：${text}`);
  return value;
}
async function click(value: HTMLElement) {
  await act(async () => {
    value.click();
    await flush();
  });
}
async function enter(
  input: HTMLInputElement | HTMLTextAreaElement | HTMLSelectElement,
  value: string,
) {
  await act(async () => {
    const prototype =
      input instanceof HTMLInputElement
        ? HTMLInputElement.prototype
        : input instanceof HTMLTextAreaElement
          ? HTMLTextAreaElement.prototype
          : HTMLSelectElement.prototype;
    Object.getOwnPropertyDescriptor(prototype, 'value')!.set!.call(input, value);
    input.dispatchEvent(new Event('input', { bubbles: true }));
    input.dispatchEvent(new Event('change', { bubbles: true }));
    await flush();
  });
}
function executionPanel(
  api: ReturnType<typeof fakeExecutionAPI>,
  options: Partial<React.ComponentProps<typeof AgentExecutionPanel>> = {},
) {
  return (
    <AgentExecutionPanel
      project={replacementProject}
      selection={[originalClip.id]}
      onOpenMCP={vi.fn()}
      getWorkflows={workflowGetter}
      api={api}
      {...options}
    />
  );
}
function replacementDialog(
  api: ReturnType<typeof fakeReplacementAPI>,
  options: Partial<React.ComponentProps<typeof GenerationReplacementDialog>> = {},
) {
  return (
    <GenerationReplacementDialog
      project={replacementProject}
      clipId={originalClip.id}
      job={candidateJob}
      candidate={candidateOutput}
      onClose={vi.fn()}
      onBootstrap={vi.fn()}
      api={api}
      {...options}
    />
  );
}

describe('第一方MCP执行独立能力与单次授权', () => {
  it('CLI 源画面权限默认关闭、进入原收费核对且不在打开时启动', async () => {
    const api = fakeExecutionAPI();
    await render(executionPanel(api));
    const frames = Array.from(document.querySelectorAll('label'))
      .find((label) => label.textContent?.includes('允许向CLI提供'))
      ?.querySelector('input') as HTMLInputElement;
    expect(frames.checked).toBe(false);
    await click(frames);
    await enter(field('执行要求'), '读取素材画面并引用源时间');
    await click(button('执行前核对…'));
    expect(document.querySelector('[role="dialog"]')?.textContent).toContain(
      '可能传给所选CLI的模型服务',
    );
    expect(api.startAgentExecution).not.toHaveBeenCalled();
    await click(button('确认执行（可能消耗CLI额度）'));
    expect(api.startAgentExecution).toHaveBeenCalledWith(
      expect.objectContaining({ allowSourceFrames: true, consent: true }),
    );
  });
  it('执行ready与计划ready分开，检测不自动启动真实推理', async () => {
    expect(executionUnavailable(executionInstallation)).toBe('');
    expect(executionUnavailable(readyAgent)).toContain('不能用计划模式');
    const api = fakeExecutionAPI();
    api.getAgentExecutionStatus.mockResolvedValue([readyAgent]);
    await render(executionPanel(api));
    expect(button('执行前核对…').disabled).toBe(true);
    expect(api.startAgentExecution).not.toHaveBeenCalled();
  });
  it('默认允许编辑但单次确认整个工程权限，选区只作上下文且不授权模型收费', async () => {
    const api = fakeExecutionAPI();
    const prepareProject = vi.fn().mockResolvedValue(replacementProject);
    await render(executionPanel(api, { prepareProject }));
    expect(check('允许直接编辑整个当前工程').checked).toBe(true);
    await enter(field('执行要求'), '把当前镜头移动十帧');
    await click(button('执行前核对…'));
    expect(api.startAgentExecution).not.toHaveBeenCalled();
    expect(document.querySelector('[role="dialog"]')?.textContent).toContain('选区不限制写入范围');
    expect(document.querySelector('[role="dialog"]')?.textContent).toContain(
      '不授权额外的模型收费',
    );
    await click(button('确认执行（可能消耗CLI额度）'));
    expect(api.startAgentExecution).toHaveBeenCalledOnce();
    expect(api.startAgentExecution.mock.calls[0][0]).toEqual(
      expect.objectContaining({
        provider: 'codex',
        projectId: replacementProject.id,
        projectRevision: 7,
        selection: [originalClip.id],
        allowEdits: true,
        consent: true,
      }),
    );
  });
  it('可明确选只读，不静默接管手工MCP或绕过未提交草稿', async () => {
    const api = fakeExecutionAPI();
    await render(
      executionPanel(api, {
        mcpStatus: {
          running: true,
          projectId: replacementProject.id,
          projectRevision: 7,
          allowEdits: false,
          scopeValid: true,
          audit: [],
        },
      }),
    );
    expect(button('执行前核对…').disabled).toBe(true);
    expect(api.startAgentExecution).not.toHaveBeenCalled();
    const prepareProject = vi.fn().mockResolvedValue(null);
    await render(executionPanel(api, { prepareProject }));
    await click(check('允许直接编辑整个当前工程'));
    await enter(field('执行要求'), '只读核对');
    await click(button('执行前核对…'));
    expect(api.startAgentExecution).not.toHaveBeenCalled();
    prepareProject.mockResolvedValue(replacementProject);
    await click(button('执行前核对…'));
    await click(button('确认执行（可能消耗CLI额度）'));
    expect(api.startAgentExecution.mock.calls[0][0].allowEdits).toBe(false);
  });
  it('运行审计显式读取，取消保留已完成编辑，终态经受保护入口同步', async () => {
    const api = fakeExecutionAPI();
    const onProgress = vi.fn();
    api.listAgentExecutions.mockResolvedValue([executionJob()]);
    await render(executionPanel(api, { onProgress }));
    await click(button('运行记录 1'));
    expect(api.getAgentExecution).not.toHaveBeenCalled();
    await click(button('读取运行审计'));
    expect(document.body.textContent).toContain('aividcut_apply_edit');
    await click(button('取消后续执行…'));
    expect(document.querySelector('[role="dialog"]')?.textContent).toContain(
      '已经提交的工程编辑保留',
    );
    expect(api.cancelAgentExecution).not.toHaveBeenCalled();
    await click(button('确认取消后续执行'));
    expect(api.cancelAgentExecution).toHaveBeenCalledWith('execution-1');
    expect(document.body.textContent).toContain('已完成编辑保留');
    expect(onProgress).toHaveBeenLastCalledWith(
      expect.objectContaining({ status: 'cancelled', endRevision: 8 }),
      true,
    );
    expect(api.startAgentExecution).not.toHaveBeenCalled();
  });
});
describe('候选镜头替换先预览再提交', () => {
  it('保持镜头时序和原源入点展示，视频真实DOM按源入点/速度限制且双路静音', async () => {
    const api = fakeReplacementAPI();
    const onBootstrap = vi.fn();
    const onClose = vi.fn();
    await render(replacementDialog(api, { onBootstrap, onClose }));
    expect(api.previewGenerationReplacement).not.toHaveBeenCalled();
    expect(button('应用替换并保留撤销').disabled).toBe(true);
    const originalVideo = document.querySelector('video')!;
    await act(async () => originalVideo.dispatchEvent(new Event('loadedmetadata')));
    expect(originalVideo.currentTime).toBe(1.2);
    expect(originalVideo.playbackRate).toBe(2);
    expect(originalVideo.muted).toBe(true);
    await click(button('预览替换变化'));
    expect(api.previewGenerationReplacement).toHaveBeenCalledWith({
      jobId: candidateJob.id,
      candidateId: candidateOutput.id,
      clipId: originalClip.id,
      projectId: replacementProject.id,
      projectRevision: 7,
      sourceInUs: 0,
      audioMode: 'keep_original',
      lutMode: 'keep',
      speedMode: 'preserve',
    });
    expect(api.applyGenerationReplacement).not.toHaveBeenCalled();
    const videos = Array.from(document.querySelectorAll('video'));
    expect(videos).toHaveLength(2);
    for (const video of videos) {
      await act(async () => video.dispatchEvent(new Event('loadedmetadata')));
      expect(video.muted).toBe(true);
    }
    expect(videos[1].currentTime).toBe(0);
    expect(videos[1].playbackRate).toBe(2);
    const range = replacementSourceRange(originalClip, replacementProject);
    expect(range.end).toBe(5.2);
    await act(async () => {
      videos[0].currentTime = 6;
      videos[0].dispatchEvent(new Event('timeupdate'));
    });
    expect(videos[0].currentTime).toBeLessThan(5.2);
    expect(document.body.textContent).toContain('字幕人工覆盖保留');
    await click(button('应用替换并保留撤销'));
    expect(api.applyGenerationReplacement).toHaveBeenCalledWith(replacementPreview.id);
    expect(onBootstrap).toHaveBeenCalledWith(replacedBootstrap);
    expect(onClose).toHaveBeenCalledOnce();
    expect(replacedBootstrap.project.clips[0].id).toBe(originalClip.id);
    expect(replacedBootstrap.canUndo).toBe(true);
  });
  it('改参数或工程即失效；无音轨/短候选的后端拒绝不伪造成功', async () => {
    const api = fakeReplacementAPI();
    await render(replacementDialog(api));
    await click(button('预览替换变化'));
    await enter(field('声音处理'), 'use_candidate');
    expect(button('应用替换并保留撤销').disabled).toBe(true);
    api.previewGenerationReplacement.mockRejectedValue(new Error('候选没有音轨或余量不足'));
    await click(button('预览替换变化'));
    expect(document.body.textContent).toContain('候选没有音轨或余量不足');
    expect(api.applyGenerationReplacement).not.toHaveBeenCalled();
    api.previewGenerationReplacement.mockResolvedValue(replacementPreview);
    await enter(field('声音处理'), 'mute');
    await enter(field('LUT处理'), 'clear');
    await enter(field('候选速度'), 'normal');
    await enter(field('候选源入点 · 秒'), '0.250000');
    await click(button('预览替换变化'));
    expect(api.previewGenerationReplacement).toHaveBeenLastCalledWith(
      expect.objectContaining({
        sourceInUs: 250000,
        audioMode: 'mute',
        lutMode: 'clear',
        speedMode: 'normal',
      }),
    );
    await render(replacementDialog(api, { project: { ...replacementProject, revision: 8 } }));
    expect(button('应用替换并保留撤销').disabled).toBe(true);
  });
  it('图片固定0入点与1x，未就绪工程上下文阻止预览API', async () => {
    const api = fakeReplacementAPI();
    const image = { ...candidateOutput, kind: 'image', name: '候选.png', mime: 'image/png' };
    const prepareProject = vi.fn().mockResolvedValue(null);
    await render(replacementDialog(api, { candidate: image, prepareProject }));
    expect(field('候选源入点 · 秒').disabled).toBe(true);
    expect(field('候选速度').value).toBe('normal');
    await click(button('预览替换变化'));
    expect(api.previewGenerationReplacement).not.toHaveBeenCalled();
    prepareProject.mockResolvedValue(replacementProject);
    api.previewGenerationReplacement.mockResolvedValue({
      ...replacementPreview,
      candidateAsset: { ...candidateAsset, kind: 'image' },
      replacementClip: {
        ...replacementPreview.replacementClip,
        kind: 'image',
        speedNum: 1,
        speedDen: 1,
      },
    });
    await click(button('预览替换变化'));
    expect(api.previewGenerationReplacement).toHaveBeenCalledWith(
      expect.objectContaining({ sourceInUs: 0, speedMode: 'normal' }),
    );
  });
  it('生成候选入口使用当前视觉片段，跨工程和非视觉选择不允许替换', async () => {
    const api = fakeGenerationAPI();
    const replacementAPI = fakeReplacementAPI();
    api.listGenerationJobs.mockResolvedValue([candidateJob]);
    await render(
      <GenerationPanel
        project={replacementProject}
        selectedClipId={originalClip.id}
        onBootstrap={vi.fn()}
        api={api}
        replacementAPI={replacementAPI}
      />,
    );
    await click(button('生成任务 1'));
    await click(button('替换当前镜头…'));
    expect(document.querySelector('[role="dialog"]')?.textContent).toContain('用候选替换当前镜头');
    expect(replacementAPI.previewGenerationReplacement).not.toHaveBeenCalled();
  });
});
