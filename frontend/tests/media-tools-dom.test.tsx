// @vitest-environment jsdom
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { beforeEach, afterEach, it, expect, vi } from 'vitest';
import { MediaToolsDialog } from '../src/components/MediaToolsDialog';
import type {
  MediaToolsAPI,
  MediaToolsView,
  MediaToolsInspection,
  MediaToolSettings,
} from '../src/media-tools-types';
const empty: MediaToolSettings = {
  ffmpeg: '',
  ffprobe: '',
  worker: '',
  repository: '',
  profiles: '',
};
const feature = { available: false, reason: '未配置' };
const capabilities = {
  ffmpeg: feature,
  ffprobe: feature,
  drawText: feature,
  lut3d: feature,
  exportVideo: feature,
  encoders: {},
  ffmpegVersion: '',
  ffprobeVersion: '',
};
const view: MediaToolsView = {
  saved: empty,
  active: { ...empty, ffmpeg: '/active/ffmpeg' },
  origins: {
    ffmpeg: 'environment',
    ffprobe: 'path',
    worker: 'unset',
    repository: 'unset',
    profiles: 'unset',
  },
  restartRequired: false,
  capabilities,
};
const checked: MediaToolsInspection = {
  id: 'checked-a',
  settings: { ...empty, ffmpeg: '/new/ffmpeg' },
  canSave: true,
  errors: {},
  capabilities,
  native: null,
  nativeReason: '未配置原生引擎',
  checkedAt: '2026-10-27T02:00:00Z',
};
let root: Root,
  host: HTMLDivElement,
  api: MediaToolsAPI,
  close: ReturnType<typeof vi.fn<() => void>>;
beforeEach(() => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
  host = document.createElement('div');
  document.body.append(host);
  root = createRoot(host);
  api = {
    get: vi.fn().mockResolvedValue(view),
    inspect: vi.fn().mockResolvedValue(checked),
    save: vi.fn().mockResolvedValue({ ...view, saved: checked.settings, restartRequired: true }),
    choose: vi.fn(),
  };
  close = vi.fn();
});
afterEach(async () => {
  await act(async () => root.unmount());
  host.remove();
  vi.restoreAllMocks();
});
const render = async () => {
  await act(async () =>
    root.render(<MediaToolsDialog onClose={close} onReturnFocus={vi.fn()} api={api} />),
  );
};
const button = (text: string) => {
  const value = [...document.querySelectorAll<HTMLButtonElement>('button')].find((b) =>
    b.textContent?.includes(text),
  );
  if (!value) throw Error(text);
  return value;
};
const click = async (text: string) => {
  await act(async () => button(text).click());
};
async function input(value: string) {
  await act(async () => {
    const node = document.querySelector<HTMLInputElement>('input[aria-label="FFmpeg程序"]')!;
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(node, value);
    node.dispatchEvent(new Event('input', { bubbles: true }));
  });
}
it('读取不自动执行或保存候选，来源和重启语义明确', async () => {
  await render();
  expect(api.get).toHaveBeenCalledTimes(1);
  expect(api.inspect).not.toHaveBeenCalled();
  expect(api.save).not.toHaveBeenCalled();
  expect(document.body.textContent).toContain('环境变量覆盖');
  expect(button('保存，重启后生效').disabled).toBe(true);
  await click('填入当前路径');
  expect(document.querySelector<HTMLInputElement>('input[aria-label="FFmpeg程序"]')?.value).toBe(
    '/active/ffmpeg',
  );
  expect(api.inspect).not.toHaveBeenCalled();
});

it('随包模式禁止手动路径并保留切换前草稿，来源与损坏错误可读', async () => {
  vi.mocked(api.get).mockResolvedValue({
    ...view,
    saved: { ...empty, useBundled: true },
    origins: { ...view.origins, ffmpeg: 'bundled' },
  });
  await render();
  const checkbox = document.querySelector<HTMLInputElement>('input[type="checkbox"]')!;
  expect(checkbox.checked).toBe(true);
  expect(document.querySelector<HTMLInputElement>('input[aria-label="FFmpeg程序"]')!.disabled).toBe(
    true,
  );
  expect(document.body.textContent).toContain('随包工具');
  await act(async () => checkbox.click());
  await input('/manual/ffmpeg');
  await act(async () => checkbox.click());
  await act(async () => checkbox.click());
  expect(document.querySelector<HTMLInputElement>('input[aria-label="FFmpeg程序"]')!.value).toBe(
    '/manual/ffmpeg',
  );
  await act(async () => checkbox.click());
  vi.mocked(api.inspect).mockResolvedValue({
    ...checked,
    settings: { ...empty, useBundled: true },
    canSave: false,
    errors: { runtime: '随包文件指纹不匹配' },
  });
  await click('检测');
  expect(api.inspect).toHaveBeenLastCalledWith({ ...empty, useBundled: true });
  expect(document.body.textContent).toContain('随包文件指纹不匹配');
});
it('检测后修改路径清除旧证明，保存只传检测身份且不热切换', async () => {
  await render();
  await input('/new/ffmpeg');
  await click('检测候选设置');
  expect(api.inspect).toHaveBeenCalledWith({ ...empty, ffmpeg: '/new/ffmpeg' });
  expect(button('保存，重启后生效').disabled).toBe(false);
  await input('/changed/ffmpeg');
  expect(button('保存，重启后生效').disabled).toBe(true);
  await click('检测候选设置');
  await click('保存，重启后生效');
  expect(api.save).toHaveBeenCalledWith(checked.id);
  expect(document.body.textContent).toContain('当前会话未切换工具');
  expect(document.body.textContent).toContain('/active/ffmpeg');
});
it('错误关联字段、聚焦并保留输入，重复检测不并发', async () => {
  await render();
  await input('/missing/tool');
  let finish!: (value: MediaToolsInspection) => void;
  vi.mocked(api.inspect).mockImplementationOnce(() => new Promise((resolve) => (finish = resolve)));
  await click('检测候选设置');
  expect(button('检测候选设置').disabled).toBe(true);
  await act(async () =>
    finish({ ...checked, id: '', canSave: false, errors: { ffmpeg: '程序不可执行' } }),
  );
  const node = document.querySelector<HTMLInputElement>('input[aria-label="FFmpeg程序"]')!;
  expect(node.value).toBe('/missing/tool');
  expect(node.getAttribute('aria-invalid')).toBe('true');
  expect(document.activeElement).toBe(node);
  expect(button('保存，重启后生效').disabled).toBe(true);
});
it('保存失败不清输入或宣称生效，取消不写文件', async () => {
  await render();
  await input('/new/ffmpeg');
  await click('检测候选设置');
  vi.mocked(api.save).mockRejectedValueOnce(new Error('工具已变化'));
  await click('保存，重启后生效');
  expect(document.body.textContent).toContain('工具已变化');
  expect(button('保存，重启后生效').disabled).toBe(true);
  expect(document.body.textContent).not.toContain('候选设置已检测，可保存');
  expect(document.querySelector<HTMLInputElement>('input[aria-label="FFmpeg程序"]')?.value).toBe(
    '/new/ffmpeg',
  );
  await click('关闭设置');
  expect(close).toHaveBeenCalledTimes(1);
  expect(api.save).toHaveBeenCalledTimes(1);
});
