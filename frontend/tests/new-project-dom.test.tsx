// @vitest-environment jsdom
import { act, useState } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { afterEach, beforeEach, it, expect } from 'vitest';
import { NewProjectSettings } from '../src/components/NewProjectSettings';
import {
  defaultNewProjectDraft,
  parseNewProject,
  changeProjectLayout,
} from '../src/lib/new-project';
import type { NewProjectErrors } from '../src/lib/new-project';
let host: HTMLDivElement, root: Root;
function Harness() {
  const [d, set] = useState(defaultNewProjectDraft);
  const [e, errors] = useState<NewProjectErrors>({});
  return (
    <form
      noValidate
      onSubmit={(ev) => {
        ev.preventDefault();
        const v = parseNewProject('工程', d);
        errors(v.errors);
        if (!v.settings)
          document.getElementById('new-project-' + Object.keys(v.errors)[0])?.focus();
      }}
    >
      <NewProjectSettings
        value={d}
        errors={e}
        disabled={false}
        onChange={(v) => {
          set(v);
          errors({});
        }}
      />
      <button type="submit">创建工程</button>
    </form>
  );
}
beforeEach(async () => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
  host = document.createElement('div');
  document.body.append(host);
  root = createRoot(host);
  await act(async () => root.render(<Harness />));
});
afterEach(async () => {
  await act(async () => root.unmount());
  host.remove();
});
async function change(label: string, value: string) {
  await act(async () => {
    const n = [...document.querySelectorAll('label')]
      .find((l) => l.textContent?.includes(label))!
      .querySelector('input,select')! as HTMLInputElement | HTMLSelectElement;
    Object.getOwnPropertyDescriptor(
      n.tagName === 'SELECT' ? HTMLSelectElement.prototype : HTMLInputElement.prototype,
      'value',
    )!.set!.call(n, value);
    n.dispatchEvent(new Event('change', { bubbles: true }));
    n.dispatchEvent(new Event('input', { bubbles: true }));
  });
}
it('预设改变实际横竖方形尺寸，29.97以分数显示且切自定义带入', async () => {
  expect(document.body.textContent).toContain('1920 × 1080 · 30/1 fps');
  await change('画幅', 'portrait');
  await change('时间线帧率', '30000/1001');
  expect(document.body.textContent).toContain('1080 × 1920 · 30000/1001 fps');
  await change('画幅', 'custom');
  expect(document.querySelector<HTMLInputElement>('#new-project-width')!.value).toBe('1080');
  expect(document.querySelector<HTMLInputElement>('#new-project-height')!.value).toBe('1920');
  await change('画幅', 'square');
  await change('分辨率', '2160');
  expect(document.body.textContent).toContain('2160 × 2160');
});
it('无效或空自定义数值保留原输入，错误关联且首错焦点', async () => {
  await change('画幅', 'custom');
  await change('宽度 · px', '');
  await act(async () => document.querySelector<HTMLButtonElement>('button')!.click());
  const n = document.querySelector<HTMLInputElement>('#new-project-width')!;
  expect(n.value).toBe('');
  expect(n.getAttribute('aria-invalid')).toBe('true');
  expect(n.getAttribute('aria-describedby')).toBe('new-project-error-width');
  expect(document.activeElement).toBe(n);
});
it('边界拒绝与约分不降低帧率精度，固定48kHz', () => {
  const d = defaultNewProjectDraft();
  expect(parseNewProject('默认', d).settings?.sampleRate).toBe(48000);
  const custom = {
    ...changeProjectLayout(d, 'custom'),
    frameRate: 'custom',
    fpsNum: '60000',
    fpsDen: '2002',
  };
  expect(parseNewProject('有理帧率', custom).settings?.fpsNum).toBe(30000);
  expect(parseNewProject('有理帧率', custom).settings?.fpsDen).toBe(1001);
  for (const bad of [
    { ...custom, width: '1919' },
    { ...custom, width: '7680', height: '4320' },
    { ...custom, fpsDen: '0' },
    { ...custom, fpsNum: '121', fpsDen: '1' },
  ])
    expect(parseNewProject('工程', bad).settings).toBeNull();
});
