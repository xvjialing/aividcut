// @vitest-environment jsdom

import { act, useState } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { ConnectionsDialog } from '../src/components/ConnectionsDialog';
import { GenerationPanel } from '../src/components/GenerationPanel';
import { defaultGenerationDraft, makeGenerationRequest } from '../src/lib/generation';
import type { ConnectionProfile } from '../src/generation-types';
import {
  configuredConnection,
  fakeGenerationAPI,
  generationBootstrap,
  generationJob,
  generationProject,
  h3Capability,
  openAICapability,
  seedanceCapability,
} from './generation-fixtures';

let container: HTMLDivElement;
let root: Root;
let fetchGuard: ReturnType<typeof vi.fn>;

beforeEach(() => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
  fetchGuard = vi.fn().mockRejectedValue(new Error('测试禁止真实网络请求'));
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
function button(label: string) {
  const element = Array.from(document.querySelectorAll('button')).find(
    (item) => item.textContent?.trim() === label || item.getAttribute('aria-label') === label,
  );
  if (!element) throw new Error(`未找到按钮：${label}`);
  return element;
}
function field(label: string): HTMLInputElement | HTMLTextAreaElement | HTMLSelectElement {
  const wrapper = Array.from(document.querySelectorAll('label')).find(
    (item) => item.querySelector('.field-label')?.textContent === label,
  );
  const control = wrapper?.querySelector('input, textarea, select');
  if (!(
    control instanceof HTMLInputElement ||
    control instanceof HTMLTextAreaElement ||
    control instanceof HTMLSelectElement
  ))
    throw new Error(`未找到控件：${label}`);
  return control;
}
async function click(element: HTMLElement) {
  await act(async () => {
    element.click();
    await flush();
  });
}
async function enter(
  element: HTMLInputElement | HTMLTextAreaElement | HTMLSelectElement,
  value: string,
) {
  await act(async () => {
    const prototype =
      element instanceof HTMLTextAreaElement
        ? HTMLTextAreaElement.prototype
        : element instanceof HTMLSelectElement
          ? HTMLSelectElement.prototype
          : HTMLInputElement.prototype;
    Object.getOwnPropertyDescriptor(prototype, 'value')!.set!.call(element, value);
    element.dispatchEvent(new Event('input', { bubbles: true }));
    element.dispatchEvent(new Event('change', { bubbles: true }));
    await flush();
  });
}
function ConnectionsHarness({
  api,
  changed,
}: {
  api: ReturnType<typeof fakeGenerationAPI>;
  changed: (profile?: ConnectionProfile) => void;
}) {
  const [open, setOpen] = useState(true);
  const [profiles, setProfiles] = useState<ConnectionProfile[]>([]);
  return (
    <ConnectionsDialog
      open={open}
      onOpenChange={setOpen}
      connections={profiles}
      catalog={[openAICapability, seedanceCapability]}
      api={api}
      onChanged={(profile) => {
        if (profile)
          setProfiles((current) => [...current.filter((item) => item.id !== profile.id), profile]);
        changed(profile);
      }}
    />
  );
}

describe('模型连接的真实DOM与秘密清理', () => {
  it('默认遮盖密钥，取消时清空实际输入且不保存或生成', async () => {
    const api = fakeGenerationAPI();
    const changed = vi.fn();
    await render(<ConnectionsHarness api={api} changed={changed} />);
    const secret = field('API 密钥') as HTMLInputElement;
    expect(secret.type).toBe('password');
    await enter(secret, 'fixture-only-secret');
    await click(button('显示 API 密钥'));
    expect(secret.type).toBe('text');
    await click(button('取消'));
    expect(secret.value).toBe('');
    expect(document.querySelector('[role="dialog"]')).toBeNull();
    expect(api.saveConnection).not.toHaveBeenCalled();
    expect(api.submitGeneration).not.toHaveBeenCalled();
    expect(changed).not.toHaveBeenCalled();
  });
  it('保存后清理warning仍是已保存，密钥清空；旧凭据只在显式按钮时重试', async () => {
    const api = fakeGenerationAPI();
    const changed = vi.fn();
    api.saveConnection.mockResolvedValue({ ...configuredConnection, warning: '旧凭据等待清理' });
    api.getConnectionHealth.mockResolvedValue({ pendingCleanup: 1, storage: 'mock' });
    await render(<ConnectionsHarness api={api} changed={changed} />);
    await enter(field('连接名称'), '图片连接');
    const secret = field('API 密钥') as HTMLInputElement;
    await enter(secret, 'fixture-only-secret');
    await click(button('显示 API 密钥'));
    await click(button('保存连接'));
    expect(api.saveConnection).toHaveBeenCalledWith(
      expect.objectContaining({
        providerId: 'openai',
        modelId: openAICapability.modelId,
        apiKey: 'fixture-only-secret',
      }),
    );
    expect(secret.value).toBe('');
    expect(secret.type).toBe('password');
    expect(document.body.textContent).toContain('连接已保存，账号与模型权限尚未验证');
    expect(document.body.textContent).toContain('旧凭据等待清理');
    expect(document.body.textContent).not.toContain('fixture-only-secret');
    expect(api.cleanupCredentials).not.toHaveBeenCalled();
    expect(api.submitGeneration).not.toHaveBeenCalled();
    await click(button('重试清理旧凭据'));
    expect(api.cleanupCredentials).toHaveBeenCalledOnce();
    expect(changed).toHaveBeenCalledOnce();
  });
});

describe('生成提交的显式计费确认', () => {
  it('仅语音连接时生成页保持真实空态，编辑Modal不展示语音profile', async () => {
    const api = fakeGenerationAPI();
    api.getConnections.mockResolvedValue([
      {
        ...configuredConnection,
        id: 'speech-only',
        providerId: 'elevenlabs',
        modelId: 'scribe_v2',
        name: '语音专用连接',
      },
    ]);
    await render(<GenerationPanel project={generationProject} onBootstrap={vi.fn()} api={api} />);
    expect(document.body.textContent).toContain('先连接你的生成服务');
    expect(
      Array.from(field('模型连接').querySelectorAll('option')).some(
        (option) => option.value === 'speech-only',
      ),
    ).toBe(false);
    await click(button('管理连接'));
    expect(document.querySelector('[role="dialog"]')?.textContent).not.toContain('语音专用连接');
    expect(document.querySelectorAll('.connection-profile')).toHaveLength(0);
    expect(api.submitGeneration).not.toHaveBeenCalled();
    expect(api.saveConnection).not.toHaveBeenCalled();
  });
  it('混合连接只显示目录供应商，已知生成供应商的未声明模型保留原因且不能生成', async () => {
    const api = fakeGenerationAPI();
    const unknown = {
      ...configuredConnection,
      id: 'unknown-image-model',
      modelId: 'unlisted-image-model',
      name: '图片旧模型连接',
    };
    api.getConnections.mockResolvedValue([
      {
        ...configuredConnection,
        id: 'speech',
        providerId: 'elevenlabs',
        modelId: 'scribe_v2',
        name: '语音连接',
      },
      configuredConnection,
      unknown,
    ]);
    await render(<GenerationPanel project={generationProject} onBootstrap={vi.fn()} api={api} />);
    expect(
      Array.from(field('模型连接').querySelectorAll('option')).map((option) => option.value),
    ).toEqual(['', configuredConnection.id, unknown.id]);
    await enter(field('模型连接'), unknown.id);
    expect(document.body.textContent).toContain('当前不能提交生成');
    await click(button('管理连接'));
    expect(document.querySelectorAll('.connection-profile')).toHaveLength(2);
    expect(document.querySelector('[role="dialog"]')?.textContent).toContain('图片旧模型连接');
    expect(document.querySelector('[role="dialog"]')?.textContent).not.toContain('语音连接');
    expect(api.submitGeneration).not.toHaveBeenCalled();
  });
  it('加载、编辑与打开核对框不提交，只有确认按钮传consent=true且默认数量1', async () => {
    const api = fakeGenerationAPI();
    const submitted = generationJob();
    api.submitGeneration.mockImplementation(async (input) => ({
      ...submitted,
      id: input.request.id,
      request: input.request,
    }));
    await render(<GenerationPanel project={generationProject} onBootstrap={vi.fn()} api={api} />);
    expect(api.submitGeneration).not.toHaveBeenCalled();
    await enter(field('内容描述'), '城市夜景');
    await click(button('生成前核对…'));
    expect(api.submitGeneration).not.toHaveBeenCalled();
    expect(document.querySelector('[role="dialog"]')?.textContent).toContain('可能产生费用');
    await click(button('返回修改'));
    expect(api.submitGeneration).not.toHaveBeenCalled();
    await click(button('生成前核对…'));
    await click(button('确认并生成（可能计费）'));
    expect(api.submitGeneration).toHaveBeenCalledOnce();
    expect(api.submitGeneration.mock.calls[0][0]).toEqual(
      expect.objectContaining({
        connectionId: configuredConnection.id,
        consent: true,
        request: expect.objectContaining({
          providerId: 'openai',
          modelId: openAICapability.modelId,
          prompt: '城市夜景',
          count: 1,
          size: '1536x864',
        }),
      }),
    );
    expect(api.resumeGeneration).not.toHaveBeenCalled();
    expect(api.cleanupCredentials).not.toHaveBeenCalled();
  });
  it('费用参考按图片数量计算，提交后保存引用快照且不进入模型参数', async () => {
    const api = fakeGenerationAPI();
    api.submitGeneration.mockImplementation(async (input) => ({
      ...generationJob(),
      id: input.request.id,
      request: input.request,
      estimate: {
        basis: 'manual-unit-v1',
        currency: 'CNY',
        unit: 'image',
        unitPrice: '0.10',
        quantity: 3,
        amount: '0.30',
      },
    }));
    await render(<GenerationPanel project={generationProject} onBootstrap={vi.fn()} api={api} />);
    await enter(field('内容描述'), '城市夜景');
    await enter(field('生成数量'), '3');
    await click(button('生成前核对…'));
    expect(document.querySelector('[role="dialog"]')?.textContent).toContain('未填写费用参考');
    await enter(field('计价单位'), 'image');
    await enter(field('参考单价'), '0.1');
    expect(document.querySelector('[role="dialog"]')?.textContent).toContain('本次估价：CNY 0.30');
    expect(api.submitGeneration).not.toHaveBeenCalled();
    await click(button('确认并生成（可能计费）'));
    const input = api.submitGeneration.mock.calls[0][0];
    expect(input.estimate).toEqual({ currency: 'CNY', unit: 'image', unitPrice: '0.10' });
    expect(input.request).not.toHaveProperty('estimate');
    expect(document.body.textContent).toContain('提交时估价：CNY 0.30');
  });
  it('无效参考价聚焦字段并阻止提交，返回变参重新核对时清空', async () => {
    const api = fakeGenerationAPI();
    await render(<GenerationPanel project={generationProject} onBootstrap={vi.fn()} api={api} />);
    await enter(field('内容描述'), '城市夜景');
    await click(button('生成前核对…'));
    await enter(field('参考单价'), '1e-2');
    await click(button('确认并生成（可能计费）'));
    expect(api.submitGeneration).not.toHaveBeenCalled();
    expect(field('参考单价').getAttribute('aria-invalid')).toBe('true');
    expect(document.activeElement).toBe(field('参考单价'));
    expect(document.querySelector('[role="dialog"]')?.textContent).toContain('非负十进制数');
    await enter(field('参考单价'), '0.25');
    await click(button('返回修改'));
    await enter(field('内容描述'), '新的镜头');
    await click(button('生成前核对…'));
    expect(field('参考单价').value).toBe('');
    await click(button('确认并生成（可能计费）'));
    expect(api.submitGeneration.mock.calls[0][0]).not.toHaveProperty('estimate');
  });
  it('网络回执未知时沿用原请求与估价，防止改价后二次提交', async () => {
    const api = fakeGenerationAPI();
    api.submitGeneration.mockRejectedValue(new Error('临时网络中断'));
    await render(<GenerationPanel project={generationProject} onBootstrap={vi.fn()} api={api} />);
    await enter(field('内容描述'), '城市夜景');
    await click(button('生成前核对…'));
    await enter(field('估价币种'), 'USD');
    await enter(field('参考单价'), '0.29');
    await click(button('确认并生成（可能计费）'));
    const first = api.submitGeneration.mock.calls[0][0];
    await click(button('生成前核对…'));
    expect(field('参考单价').value).toBe('0.29');
    expect(field('参考单价').closest('fieldset')?.disabled).toBe(true);
    expect(document.querySelector('[role="dialog"]')?.textContent).toContain(
      '沿用上次提交的费用参考',
    );
    await click(button('确认并生成（可能计费）'));
    expect(api.submitGeneration.mock.calls[1][0]).toEqual(first);
  });
  it('视频秒价使用显式时长，零参考价仍与未知区分', async () => {
    const api = fakeGenerationAPI();
    api.getConnections.mockResolvedValue([
      { ...configuredConnection, providerId: 'minimax', modelId: h3Capability.modelId },
    ]);
    await render(<GenerationPanel project={generationProject} onBootstrap={vi.fn()} api={api} />);
    await enter(field('内容描述'), '缓慢推近');
    await click(button('生成前核对…'));
    await enter(field('计价单位'), 'video_second');
    await enter(field('参考单价'), '0');
    expect(document.querySelector('[role="dialog"]')?.textContent).toContain('本次估价：CNY 0.00');
    await click(button('确认并生成（可能计费）'));
    expect(api.submitGeneration.mock.calls[0][0].estimate).toEqual({
      currency: 'CNY',
      unit: 'video_second',
      unitPrice: '0.00',
    });
  });
  it('切换工程后失败重试不继承上一工程的请求编号和估价', async () => {
    const api = fakeGenerationAPI();
    api.submitGeneration.mockRejectedValue(new Error('临时网络中断'));
    await render(<GenerationPanel project={generationProject} onBootstrap={vi.fn()} api={api} />);
    await enter(field('内容描述'), '同样的请求描述');
    await click(button('生成前核对…'));
    await enter(field('参考单价'), '0.29');
    await click(button('确认并生成（可能计费）'));
    const first = api.submitGeneration.mock.calls[0][0];
    await render(
      <GenerationPanel
        project={{ ...generationProject, id: 'another-project' }}
        onBootstrap={vi.fn()}
        api={api}
      />,
    );
    await enter(field('内容描述'), '同样的请求描述');
    await click(button('生成前核对…'));
    expect(field('参考单价').value).toBe('');
    expect(field('参考单价').closest('fieldset')?.disabled).toBe(false);
    await click(button('确认并生成（可能计费）'));
    const next = api.submitGeneration.mock.calls[1][0];
    expect(next.request.id).not.toBe(first.request.id);
    expect(next).not.toHaveProperty('estimate');
    expect(next.context?.projectId).toBe('another-project');
  });
  it('H3切换首尾帧与参考模式时清除互斥参考，提交用途匹配', async () => {
    const api = fakeGenerationAPI();
    api.getConnections.mockResolvedValue([
      { ...configuredConnection, providerId: 'minimax', modelId: h3Capability.modelId },
    ]);
    await render(<GenerationPanel project={generationProject} onBootstrap={vi.fn()} api={api} />);
    await enter(field('内容描述'), '缓慢推近');
    await enter(field('创作任务'), 'video.image');
    await enter(field('首帧'), 'png');
    await click(button('生成前核对…'));
    expect(document.querySelector('[role="dialog"]')?.textContent).toContain('首帧：参考图.png');
    await click(button('返回修改'));
    await enter(field('创作任务'), 'video.reference');
    const checkbox = Array.from(document.querySelectorAll('label'))
      .find((item) => item.textContent?.trim() === '产品.jpg')
      ?.querySelector('input');
    if (!(checkbox instanceof HTMLInputElement)) throw new Error('缺少实际参考图选择器');
    await click(checkbox);
    await click(button('生成前核对…'));
    const review = document.querySelector('[role="dialog"]');
    expect(review?.textContent).toContain('参考图：产品.jpg');
    expect(review?.textContent).not.toContain('首帧：');
    expect(api.submitGeneration).not.toHaveBeenCalled();
  });
  it('Seedance 声音选择在核对中展示，取消不提交、明确确认仅提交一次', async () => {
    const api = fakeGenerationAPI();
    api.getConnections.mockResolvedValue([
      { ...configuredConnection, providerId: 'seedance', modelId: seedanceCapability.modelId },
    ]);
    api.submitGeneration.mockResolvedValue(
      generationJob({
        request: {
          id: 'test',
          providerId: 'seedance',
          modelId: seedanceCapability.modelId,
          mode: 'video.text',
          prompt: '城市镜头',
          generateAudio: false,
        },
      }),
    );
    await render(<GenerationPanel project={generationProject} onBootstrap={vi.fn()} api={api} />);
    expect(document.body.textContent).toContain('已配置不代表账号与模型权限已验证');
    expect(field('视频声音').value).toBe('on');
    expect(field('输出尺寸').value).toBe('720p');
    await enter(field('内容描述'), '城市镜头');
    await click(button('生成前核对…'));
    expect(document.querySelector('[role="dialog"]')?.textContent).toContain('生成同步声音');
    expect(api.submitGeneration).not.toHaveBeenCalled();
    await click(button('返回修改'));
    await enter(field('视频声音'), 'off');
    await click(button('生成前核对…'));
    expect(document.querySelector('[role="dialog"]')?.textContent).toContain('无声视频');
    await click(button('确认并生成（可能计费）'));
    expect(api.submitGeneration).toHaveBeenCalledTimes(1);
    expect(api.submitGeneration).toHaveBeenCalledWith(
      expect.objectContaining({
        consent: true,
        request: expect.objectContaining({
          modelId: seedanceCapability.modelId,
          size: '720p',
          outputFormat: 'mp4',
          durationSeconds: 5,
          generateAudio: false,
        }),
      }),
    );
  });
  it('未声明的Seedance旧模型禁提交且清楚说明', async () => {
    const api = fakeGenerationAPI();
    api.getConnections.mockResolvedValue([
      { ...configuredConnection, providerId: 'seedance', modelId: '' },
    ]);
    await render(<GenerationPanel project={generationProject} onBootstrap={vi.fn()} api={api} />);
    expect(document.body.textContent).toContain('当前不能提交生成');
    expect(
      Array.from(document.querySelectorAll('button')).some(
        (item) => item.textContent?.trim() === '生成前核对…',
      ),
    ).toBe(false);
    expect(api.submitGeneration).not.toHaveBeenCalled();
  });
});

describe('生成任务条件同步', () => {
  const firstFingerprint = 'a'.repeat(64);
  const secondFingerprint = 'b'.repeat(64);
  function polling() {
    const timer = vi.spyOn(window, 'setInterval');
    return async () => {
      const callback = timer.mock.calls.find(([, delay]) => delay === 2500)?.[0];
      if (typeof callback !== 'function') throw Error('没有任务轮询');
      await act(async () => {
        callback();
        await flush();
      });
    };
  }
  it('无变化保留未显示页未知请求，手动刷新强制全量且明确空数组清空', async () => {
    const tick = polling();
    const api = fakeGenerationAPI();
    const request = makeGenerationRequest(
      'conditional-unknown',
      configuredConnection,
      openAICapability,
      defaultGenerationDraft(openAICapability, generationProject, '条件同步镜头'),
    );
    let records = Array.from({ length: 40 }, (_, i) => generationJob({ id: `sync-${i}` }));
    records.push(generationJob({ id: request.id, request, status: 'unknown' }));
    api.syncGenerationJobs.mockImplementation(async ({ fingerprint }) =>
      fingerprint
        ? { fingerprint, changed: false }
        : { fingerprint: firstFingerprint, changed: true, jobs: records },
    );
    await render(<GenerationPanel project={generationProject} onBootstrap={vi.fn()} api={api} />);
    await enter(field('内容描述'), '条件同步镜头');
    await tick();
    expect(api.syncGenerationJobs).toHaveBeenLastCalledWith({ fingerprint: firstFingerprint });
    expect(button('生成前核对…').disabled).toBe(true);
    expect(document.body.textContent).toContain('生成任务 41');
    records = [];
    await click(button('生成任务 41'));
    await click(button('刷新'));
    expect(api.syncGenerationJobs).toHaveBeenLastCalledWith({ fingerprint: '' });
    expect(document.body.textContent).toContain('还没有生成任务');
  });
  it('较旧全量回复不能覆盖新列表或同步摘要', async () => {
    const tick = polling();
    const api = fakeGenerationAPI();
    api.syncGenerationJobs.mockResolvedValue({
      fingerprint: firstFingerprint,
      changed: true,
      jobs: [generationJob({ message: '原始记录' })],
    });
    await render(<GenerationPanel project={generationProject} onBootstrap={vi.fn()} api={api} />);
    let resolveOld!: (value: Awaited<ReturnType<typeof api.syncGenerationJobs>>) => void;
    api.syncGenerationJobs.mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          resolveOld = resolve;
        }),
    );
    await tick();
    api.syncGenerationJobs.mockResolvedValue({
      fingerprint: secondFingerprint,
      changed: true,
      jobs: [generationJob({ message: '较新权威记录' })],
    });
    await click(button('生成任务 1'));
    await click(button('刷新'));
    await act(async () => {
      resolveOld({
        fingerprint: firstFingerprint,
        changed: true,
        jobs: [generationJob({ message: '迟到旧记录' })],
      });
      await flush();
    });
    expect(document.body.textContent).toContain('较新权威记录');
    expect(document.body.textContent).not.toContain('迟到旧记录');
    await tick();
    expect(api.syncGenerationJobs).toHaveBeenLastCalledWith({ fingerprint: secondFingerprint });
  });
  it('错误或不匹配的无变化回复保留已知任务，不接纳错误摘要', async () => {
    const tick = polling();
    const api = fakeGenerationAPI();
    api.syncGenerationJobs.mockResolvedValue({
      fingerprint: firstFingerprint,
      changed: true,
      jobs: [generationJob({ status: 'unknown' })],
    });
    await render(<GenerationPanel project={generationProject} onBootstrap={vi.fn()} api={api} />);
    api.syncGenerationJobs.mockRejectedValueOnce(Error('本地服务暂不可达'));
    await tick();
    api.syncGenerationJobs.mockResolvedValue({ fingerprint: secondFingerprint, changed: false });
    await tick();
    expect(document.body.textContent).toContain('任务同步回复无效');
    expect(document.body.textContent).toContain('生成任务 1');
    await tick();
    expect(api.syncGenerationJobs).toHaveBeenLastCalledWith({ fingerprint: firstFingerprint });
    api.syncGenerationJobs.mockResolvedValue({ fingerprint: firstFingerprint, changed: false });
    await tick();
    expect(document.querySelector('[role="alert"]')).toBeNull();
    expect(document.body.textContent).toContain('生成任务 1');
  });
  it('迟到的本地动作回执使摘要失效，下次必须完整核对', async () => {
    const tick = polling();
    const api = fakeGenerationAPI();
    const unknown = generationJob({ status: 'unknown', message: '等待人工核对' });
    api.syncGenerationJobs.mockResolvedValue({
      fingerprint: firstFingerprint,
      changed: true,
      jobs: [unknown],
    });
    await render(<GenerationPanel project={generationProject} onBootstrap={vi.fn()} api={api} />);
    let resolveAction!: (value: typeof unknown) => void;
    api.acknowledgeGenerationUnknown.mockImplementation(
      () =>
        new Promise((resolve) => {
          resolveAction = resolve;
        }),
    );
    await click(button('生成任务 1'));
    await click(button('已核对服务记录…'));
    await click(
      document.querySelector('[role="dialog"] input[type="checkbox"]') as HTMLInputElement,
    );
    await click(button('记录已核对'));
    api.syncGenerationJobs.mockResolvedValue({
      fingerprint: secondFingerprint,
      changed: true,
      jobs: [{ ...unknown, acknowledged: true, message: '较新服务记录' }],
    });
    await tick();
    await act(async () => {
      resolveAction({ ...unknown, acknowledged: true });
      await flush();
    });
    await tick();
    expect(api.syncGenerationJobs).toHaveBeenLastCalledWith({ fingerprint: '' });
    expect(document.body.textContent).toContain('较新服务记录');
    expect(api.submitGeneration).not.toHaveBeenCalled();
  });
  it('更换API后首次读取不沿用上一服务的摘要', async () => {
    const tick = polling();
    const api = fakeGenerationAPI();
    await render(<GenerationPanel project={generationProject} onBootstrap={vi.fn()} api={api} />);
    await tick();
    const next = fakeGenerationAPI();
    await render(<GenerationPanel project={generationProject} onBootstrap={vi.fn()} api={next} />);
    expect(next.syncGenerationJobs.mock.calls[0]).toEqual([{ fingerprint: '' }]);
  });
});

describe('生成任务恢复与候选导入', () => {
  it('未显示页中的未确认请求仍参与重复提交保护', async () => {
    const api = fakeGenerationAPI();
    const draft = defaultGenerationDraft(openAICapability, generationProject, '同一镜头请求');
    const request = makeGenerationRequest(
      'off-page-unknown',
      configuredConnection,
      openAICapability,
      draft,
    );
    const records = Array.from({ length: 40 }, (_, i) =>
      generationJob({ id: `completed-${i}`, status: 'succeeded' }),
    );
    records.push(
      generationJob({
        id: request.id,
        request,
        status: 'unknown',
        acknowledged: false,
        remoteId: undefined,
        canResume: false,
      }),
    );
    api.listGenerationJobs.mockResolvedValue(records);
    await render(<GenerationPanel project={generationProject} onBootstrap={vi.fn()} api={api} />);
    await enter(field('内容描述'), '同一镜头请求');
    expect(button('生成前核对…').disabled).toBe(true);
    expect(api.submitGeneration).not.toHaveBeenCalled();
  });
  it('停止只请求本地等待，保留当前快照并轮询稳定状态；恢复只查询已有远端任务', async () => {
    const api = fakeGenerationAPI();
    const running = generationJob({
      status: 'running',
      message: '远端生成中',
      remoteId: 'remote-existing',
      canResume: false,
    });
    const waiting = {
      ...running,
      status: 'waiting' as const,
      message: '本地等待已停止',
      canResume: true,
    };
    api.listGenerationJobs.mockResolvedValue([running]);
    api.stopGenerationWaiting.mockImplementation(async () => {
      api.listGenerationJobs.mockResolvedValue([waiting]);
      return running;
    });
    api.resumeGeneration.mockResolvedValue(running);
    await render(<GenerationPanel project={generationProject} onBootstrap={vi.fn()} api={api} />);
    await click(button('生成任务 1'));
    await click(button('停止本地等待'));
    expect(api.stopGenerationWaiting).not.toHaveBeenCalled();
    const confirmation = Array.from(document.querySelectorAll('[role="dialog"] button')).find(
      (item) => item.textContent?.trim() === '停止本地等待',
    );
    if (!(confirmation instanceof HTMLButtonElement)) throw new Error('缺少停止等待确认按钮');
    await click(confirmation);
    expect(api.stopGenerationWaiting).toHaveBeenCalledWith(running.id);
    expect(document.body.textContent).toContain('这不代表服务商已取消生成');
    expect(document.body.textContent).toContain('远端生成中');
    const readsAfterStop = api.listGenerationJobs.mock.calls.length;
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 2550));
    });
    expect(api.listGenerationJobs.mock.calls.length).toBeGreaterThan(readsAfterStop);
    expect(document.body.textContent).toContain('本地等待已停止');
    await click(button('恢复查询'));
    expect(api.resumeGeneration).toHaveBeenCalledWith(running.id);
    expect(api.submitGeneration).not.toHaveBeenCalled();
  });
  it('无远端ID的unknown只核对记录，不提供恢复或重交，确认核对不会提交生成', async () => {
    const api = fakeGenerationAPI();
    const unknown = generationJob({
      status: 'unknown',
      message: '提交结果未知',
      remoteId: undefined,
      canResume: false,
    });
    api.listGenerationJobs.mockResolvedValue([unknown]);
    await render(<GenerationPanel project={generationProject} onBootstrap={vi.fn()} api={api} />);
    await click(button('生成任务 1'));
    expect(document.body.textContent).toContain('这里不会重交');
    expect(
      Array.from(document.querySelectorAll('button')).some((item) =>
        item.textContent?.includes('恢复查询'),
      ),
    ).toBe(false);
    await click(button('已核对服务记录…'));
    expect(button('记录已核对').disabled).toBe(true);
    const check = document.querySelector('[role="dialog"] input[type="checkbox"]');
    if (!(check instanceof HTMLInputElement)) throw new Error('缺少人工核对确认');
    await click(check);
    await click(button('记录已核对'));
    expect(api.acknowledgeGenerationUnknown).toHaveBeenCalledWith(unknown.id);
    expect(api.submitGeneration).not.toHaveBeenCalled();
    expect(api.resumeGeneration).not.toHaveBeenCalled();
  });
  it('逐候选导入只传显式选择，部分导入不禁掉其他候选，也不自动替换镜头', async () => {
    const api = fakeGenerationAPI();
    const onBootstrap = vi.fn();
    const outputs = ['a', 'b'].map((id) => ({
      id,
      kind: 'image',
      name: `${id}.png`,
      path: `/saved/${id}.png`,
      mime: 'image/png',
      sha256: `sha-${id}`,
      bytes: 1024,
      recipeId: 'recipe',
    }));
    const job = generationJob({
      status: 'succeeded',
      message: '结果已落盘',
      assets: outputs,
      imported: false,
      importedAssetIds: ['a'],
    });
    api.listGenerationJobs.mockResolvedValue([job]);
    api.importGenerationAssets.mockImplementation(async () => {
      api.listGenerationJobs.mockResolvedValue([
        { ...job, imported: true, importedAssetIds: ['a', 'b'] },
      ]);
      return generationBootstrap;
    });
    await render(
      <GenerationPanel project={generationProject} onBootstrap={onBootstrap} api={api} />,
    );
    await click(button('生成任务 1'));
    const labels = Array.from(document.querySelectorAll('.candidate-select'));
    const first = labels[0]?.querySelector('input');
    const second = labels[1]?.querySelector('input');
    if (!(first instanceof HTMLInputElement && second instanceof HTMLInputElement))
      throw new Error('候选未渲染');
    expect(first.checked).toBe(true);
    expect(first.disabled).toBe(true);
    expect(second.disabled).toBe(false);
    expect(api.importGenerationAssets).not.toHaveBeenCalled();
    await click(second);
    await click(button('加入所选候选'));
    expect(api.importGenerationAssets).toHaveBeenCalledWith(job.id, ['b']);
    expect(onBootstrap).toHaveBeenCalledWith(generationBootstrap);
    expect(api.importGenerationResult).not.toHaveBeenCalled();
    expect(api.submitGeneration).not.toHaveBeenCalled();
  });
});

it('管理连接跳转设置，连接元信息刷新保留创作草稿且不自动生成', async () => {
  const api = fakeGenerationAPI(),
    open = vi.fn();
  const view = (version: number) => (
    <GenerationPanel
      project={generationProject}
      onBootstrap={vi.fn()}
      api={api}
      onOpenConnections={open}
      connectionsVersion={version}
    />
  );
  await render(view(0));
  await enter(field('内容描述'), '保留当前提示词');
  await click(button('管理连接'));
  expect(open).toHaveBeenCalledOnce();
  expect(document.querySelector('[role="dialog"]')).toBeNull();
  const reads = api.getConnections.mock.calls.length;
  await render(view(1));
  expect(api.getConnections.mock.calls.length).toBeGreaterThan(reads);
  expect(field('内容描述').value).toBe('保留当前提示词');
  expect(api.submitGeneration).not.toHaveBeenCalled();
});
