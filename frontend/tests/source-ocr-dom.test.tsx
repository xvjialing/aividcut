// @vitest-environment jsdom
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { SourceOCRPanel } from '../src/components/SourceOCRPanel';
import type { SourceOCRAPI, SourceOCRDraft } from '../src/source-ocr-types';
import type { Asset } from '../src/types';
import { generationBootstrap } from './generation-fixtures';
let root: Root, container: HTMLDivElement;
const asset: Asset = {
  id: 'ocr-image',
  kind: 'image',
  name: '测试画面',
  path: '/own/test.png',
  width: 960,
  height: 540,
  durationUs: 0,
  fpsNum: 0,
  fpsDen: 1,
  hasAudio: false,
  missing: false,
  fingerprint: 'sha256:' + 'a'.repeat(64),
};
function fixture() {
  const api = {
    capability: vi
      .fn<SourceOCRAPI['capability']>()
      .mockResolvedValue({ available: true, engine: 'apple-vision', reason: '本机识别' }),
    prepare: vi.fn<SourceOCRAPI['prepare']>(),
    accept: vi.fn<SourceOCRAPI['accept']>().mockResolvedValue(generationBootstrap),
    cancel: vi.fn<SourceOCRAPI['cancel']>().mockResolvedValue({}),
  };
  api.prepare.mockImplementation(async (input) => ({
    id: input.requestId,
    projectId: input.projectId,
    projectRevision: input.projectRevision,
    sourceEpoch: input.sourceEpoch,
    frame: {
      url: 'http://127.0.0.1/own-ocr-frame',
      evidence: {
        id: 'evidence',
        assetId: asset.id,
        assetName: asset.name,
        sourceFingerprint: asset.fingerprint!,
        requestedPositionUs: input.positionUs,
        positionUs: input.positionUs,
        imageSha256: 'b'.repeat(64),
        width: 960,
        height: 540,
        profile: 'detail',
        recipeVersion: 'detail-jpeg-v1',
      },
    },
    result: {
      version: 1,
      engine: 'apple-vision',
      revision: 3,
      languages: ['zh-Hans', 'en-US'],
      lines: [{ text: '原始画面文字', confidence: 0.82 }],
      truncated: false,
    },
  }));
  return api;
}

it('Windows系统结果保留实际语言和未知置信度，不显示Apple来源或零分', async () => {
  const api = fixture();
  api.capability.mockResolvedValue({
    available: true,
    engine: 'windows-media-ocr',
    reason: '系统语言',
  });
  const original = api.prepare.getMockImplementation()!;
  api.prepare.mockImplementation(async (input) => {
    const draft = await original(input);
    draft.result.engine = 'windows-media-ocr';
    draft.result.revision = 0;
    draft.result.languages = ['en-US'];
    draft.result.lines[0].confidence = null;
    return draft;
  });
  await render(api);
  await click(button('识别当前画面文字'));
  const modal = document.querySelector('[role="dialog"]')!;
  expect(modal.textContent).toContain('本机Windows系统文字识别');
  expect(modal.textContent).toContain('en-US');
  expect(modal.textContent).toContain('系统未提供逐行置信度');
  expect(modal.textContent).not.toContain('Apple');
  expect(modal.textContent).not.toContain('0%');
  await click(button('接纳画面文字'));
  expect(api.accept).toHaveBeenCalledOnce();
});

it('便携Windows缺少MSIX身份时显示原因，不提供可点击识别', async () => {
  const api = fixture();
  api.capability.mockResolvedValue({
    available: false,
    engine: 'windows-media-ocr',
    reason: 'Windows系统OCR需要MSIX包身份，当前便携运行方式不可用',
  });
  await render(api);
  expect(button('识别当前画面文字').disabled).toBe(true);
  expect(document.body.textContent).toContain('MSIX包身份');
  expect(api.prepare).not.toHaveBeenCalled();
});
beforeEach(() => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
  container = document.createElement('div');
  document.body.append(container);
  root = createRoot(container);
});
afterEach(async () => {
  await act(async () => root.unmount());
  container.remove();
  vi.restoreAllMocks();
});
async function flush() {
  await new Promise((resolve) => setTimeout(resolve, 5));
}
async function render(
  api: SourceOCRAPI,
  positionUs = 0,
  apply = (action: () => Promise<typeof generationBootstrap>) => action(),
) {
  await act(async () => {
    root.render(
      <SourceOCRPanel
        projectId="ocr-project"
        projectRevision={2}
        sourceEpoch={3}
        asset={asset}
        positionUs={positionUs}
        applyAnalysis={apply}
        api={api}
      />,
    );
    await flush();
  });
}
function button(text: string) {
  const b = [...document.querySelectorAll('button')].find((x) => x.textContent?.trim() === text);
  if (!b) throw Error('缺少按钮' + text);
  return b;
}
async function click(b: HTMLElement) {
  await act(async () => {
    b.click();
    await flush();
  });
}
it('明确识别后核对图像和原始置信，人工修正再接纳', async () => {
  const api = fixture();
  await render(api);
  expect(api.prepare).not.toHaveBeenCalled();
  await click(button('识别当前画面文字'));
  expect(document.querySelector('[role="dialog"]')?.textContent).toContain('82%');
  expect(api.accept).not.toHaveBeenCalled();
  const input = document.querySelector('textarea')!;
  await act(async () => {
    Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')!.set!.call(
      input,
      '人工修正\n保留第二行',
    );
    input.dispatchEvent(new Event('input', { bubbles: true }));
    await flush();
  });
  await click(button('接纳画面文字'));
  expect(api.accept).toHaveBeenCalledOnce();
  expect(api.accept).toHaveBeenCalledWith(
    expect.objectContaining({
      projectId: 'ocr-project',
      projectRevision: 2,
      sourceEpoch: 3,
      lines: ['人工修正', '保留第二行'],
    }),
  );
  expect(document.querySelector('[role="dialog"]')).toBeNull();
});
it('取消中的迟到识别结果不能打开核对窗口', async () => {
  const api = fixture();
  let resolve!: (value: SourceOCRDraft) => void;
  const original = api.prepare.getMockImplementation()!;
  let input!: Parameters<SourceOCRAPI['prepare']>[0];
  api.prepare.mockImplementation((value) => {
    input = value;
    return new Promise((r) => {
      resolve = r;
    });
  });
  await render(api);
  await click(button('识别当前画面文字'));
  await click(button('取消识别'));
  expect(api.cancel).toHaveBeenCalledWith(input.requestId);
  await act(async () => {
    resolve(await original(input));
    await flush();
  });
  expect(document.querySelector('[role="dialog"]')).toBeNull();
  expect(api.accept).not.toHaveBeenCalled();
});
it('源位置改变取消旧候选，避免接纳别的取样帧', async () => {
  const api = fixture();
  await render(api);
  await click(button('识别当前画面文字'));
  await render(api, 1000000);
  expect(document.querySelector('[role="dialog"]')).toBeNull();
  expect(api.cancel).toHaveBeenCalled();
  expect(api.accept).not.toHaveBeenCalled();
});
it('平台不可用保留真实原因，不提交识别或模型', async () => {
  const api = fixture();
  api.capability.mockResolvedValue({
    available: false,
    engine: '',
    reason: '当前平台尚未配置本地文字识别适配器',
  });
  await render(api);
  expect(button('识别当前画面文字').disabled).toBe(true);
  expect(document.body.textContent).toContain('当前平台尚未配置');
  expect(api.prepare).not.toHaveBeenCalled();
});
it('识别失败后可明确重试，没有文字时不可接纳', async () => {
  const api = fixture();
  const original = api.prepare.getMockImplementation()!;
  api.prepare.mockRejectedValueOnce(Error('识别进程失败')).mockImplementationOnce(async (input) => {
    const draft = await original(input);
    draft.result.lines = [];
    return draft;
  });
  await render(api);
  await click(button('识别当前画面文字'));
  expect(document.body.textContent).toContain('识别进程失败');
  await click(button('识别当前画面文字'));
  expect(document.body.textContent).toContain('没有识别到文字');
  expect(button('接纳画面文字').disabled).toBe(true);
  await click(button('放弃结果'));
  expect(api.accept).not.toHaveBeenCalled();
});
it('过量人工文字提示关联字段，下一次核对清理旧校验状态', async () => {
  const api = fixture();
  await render(api);
  await click(button('识别当前画面文字'));
  const input = document.querySelector('textarea')!;
  await act(async () => {
    Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')!.set!.call(
      input,
      '字'.repeat(1001),
    );
    input.dispatchEvent(new Event('input', { bubbles: true }));
    await flush();
  });
  await click(button('接纳画面文字'));
  expect(api.accept).not.toHaveBeenCalled();
  expect(input.getAttribute('aria-invalid')).toBe('true');
  expect(document.activeElement).toBe(input);
  await click(button('放弃结果'));
  await click(button('识别当前画面文字'));
  expect(document.querySelector('textarea')?.getAttribute('aria-invalid')).toBe('false');
});

it('接纳失败保留候选和核对文字，允许明确重试', async () => {
  const api = fixture();
  api.accept.mockRejectedValueOnce(Error('核对期间源文件变化'));
  await render(api);
  await click(button('识别当前画面文字'));
  await click(button('接纳画面文字'));
  expect(document.querySelector('[role="dialog"]')).not.toBeNull();
  expect(document.querySelector('textarea')?.value).toBe('原始画面文字');
  expect(document.body.textContent).toContain('核对期间源文件变化');
  expect(button('接纳画面文字').disabled).toBe(false);
  expect(api.accept).toHaveBeenCalledOnce();
});
