// @vitest-environment jsdom
import { act, useState } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { beforeEach, afterEach, describe, expect, it } from 'vitest';
import { ExportSettings } from '../src/components/ExportSettings';
import { defaultExportDraft, exportOptions, ExportRangeError } from '../src/lib/export-options';
let host: HTMLDivElement, root: Root;
beforeEach(() => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
  host = document.createElement('div');
  document.body.append(host);
  root = createRoot(host);
});
afterEach(async () => {
  await act(async () => root.unmount());
  host.remove();
});
function Harness({ nativeAvailable = true }: { nativeAvailable?: boolean }) {
  const [draft, setDraft] = useState(defaultExportDraft);
  const [invalid, setInvalid] = useState<'from' | 'to'>();
  const [error, setError] = useState('');
  return (
    <form
      noValidate
      onSubmit={(event) => {
        event.preventDefault();
        try {
          exportOptions(draft, 30);
          setError('已校验');
        } catch (reason) {
          setError((reason as Error).message);
          if (reason instanceof ExportRangeError) setInvalid(reason.field);
        }
      }}
    >
      <ExportSettings
        value={draft}
        onChange={setDraft}
        duration={30}
        nativeAvailable={nativeAvailable}
        invalidField={invalid}
      />
      {error ? <p id="dialog-error">{error}</p> : null}
      <button>校验</button>
    </form>
  );
}
async function select(index: number, value: string) {
  await act(async () => {
    const input = host.querySelectorAll('select')[index];
    input.value = value;
    input.dispatchEvent(new Event('change', { bubbles: true }));
  });
}
describe('导出设置表单', () => {
  it('自动方式说明本次实际路线，未配置时禁用明确原生选择', async () => {
    await act(async () => root.render(<Harness nativeAvailable={false} />));
    expect(host.textContent).toContain('本次使用FFmpeg合成');
    expect(host.querySelector<HTMLOptionElement>('option[value="native"]')?.disabled).toBe(true);
  });
  it('区间切换保留草稿、选择编码不丢帧数，覆盖需显式选择', async () => {
    await act(async () => root.render(<Harness />));
    expect(host.textContent).toContain('本次使用原生合成');
    await select(3, 'custom');
    expect(host.querySelector<HTMLInputElement>('[data-export-field="to"]')?.value).toBe('30');
    expect(host.querySelector<HTMLInputElement>('input[type="checkbox"]')?.checked).toBe(false);
    await select(2, 'mpeg4');
    await select(3, 'all');
    await select(3, 'custom');
    expect(host.querySelector<HTMLInputElement>('[data-export-field="to"]')?.value).toBe('30');
    expect(host.querySelectorAll('select')[2].value).toBe('mpeg4');
  });
  it('错误通过可见文字和字段关联呈现，保留范围草稿', async () => {
    await act(async () => root.render(<Harness />));
    await select(3, 'custom');
    await act(async () => {
      const input = host.querySelector<HTMLInputElement>('[data-export-field="to"]')!;
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(input, '0');
      input.dispatchEvent(new Event('input', { bubbles: true }));
      host
        .querySelector('form')!
        .dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
    });
    // React提交后再提交，以实际受控值校验，避免同一批次读到旧闭包。
    await act(async () =>
      host
        .querySelector('form')!
        .dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })),
    );
    expect(host.querySelector<HTMLInputElement>('[data-export-field="to"]')?.value).toBe('0');
    expect(host.querySelector('[data-export-field="to"]')?.getAttribute('aria-invalid')).toBe(
      'true',
    );
    expect(host.querySelector('[data-export-field="to"]')?.getAttribute('aria-describedby')).toBe(
      'dialog-error',
    );
    expect(host.textContent).toContain('请保留想要的范围');
  });
});

it('较低内存是明确选择，换不兼容编码器回标准并保留范围', async () => {
  await act(async () => root.render(<Harness />));
  const mode = host.querySelector<HTMLSelectElement>('select[aria-label="编码资源"]')!;
  expect(mode.value).toBe('standard');
  await act(async () => {
    mode.value = 'lower_memory';
    mode.dispatchEvent(new Event('change', { bubbles: true }));
  });
  expect(host.textContent).toContain('压缩画质可能变化');
  expect(
    exportOptions({ ...defaultExportDraft, resourceMode: 'lower_memory' }, 30).resourceMode,
  ).toBe('lower_memory');
  await select(2, 'mpeg4');
  expect(host.querySelector<HTMLSelectElement>('select[aria-label="编码资源"]')!.value).toBe(
    'standard',
  );
  expect(host.querySelector<HTMLSelectElement>('select[aria-label="编码资源"]')!.disabled).toBe(
    true,
  );
  expect(() =>
    exportOptions({ ...defaultExportDraft, encoder: 'mpeg4', resourceMode: 'lower_memory' }, 30),
  ).toThrow('仅支持H.264软件编码');
  expect(exportOptions(defaultExportDraft, 30).resourceMode).toBeUndefined();
});
