// @vitest-environment jsdom
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { TranscriptCutPanel } from '../src/components/TranscriptCutPanel';
import type { TranscriptCutAPI, TranscriptWordSelection } from '../src/transcript-cut-types';
import type { WorkflowPreview } from '../src/workflow-types';
import { speechBootstrap, speechProject, sourceTranscript } from './speech-fixtures';

let root: Root;
let container: HTMLDivElement;
let fetchGuard: ReturnType<typeof vi.fn>;
beforeEach(() => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
  fetchGuard = vi.fn().mockRejectedValue(new Error('测试禁止真实网络/模型'));
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
const transcript = {
  ...sourceTranscript,
  acceptedWords: [
    { id: 'a', text: '前句', startUs: 0, endUs: 500000 },
    { id: 'b', text: '删词', startUs: 500000, endUs: 1000000 },
    { id: 'c', text: '后句', startUs: 1000000, endUs: 2000000 },
  ],
};
const project = { ...speechProject, transcripts: [transcript] };
const selection: TranscriptWordSelection = {
  projectId: project.id,
  projectRevision: project.revision,
  transcriptId: transcript.id,
  transcriptRevision: transcript.revision,
  clipId: 'source-clip',
  assetId: transcript.assetId,
  wordIds: ['a', 'b'],
  text: '前句删词',
  sourceStartUs: 0,
  sourceEndUs: 1000000,
  startFrame: 0,
  endFrame: 30,
  warnings: [],
};
const preview: WorkflowPreview = {
  id: 'cut-1',
  workflowId: 'transcript-cut',
  workflowVersion: '1.0.0',
  projectId: project.id,
  projectRevision: project.revision,
  createdAt: '2026-10-26T00:00:00Z',
  applied: false,
  request: {
    baseRevision: project.revision,
    idempotencyKey: 'cut-1',
    label: '按文字删减',
    commands: [],
  },
  summary: '删减2词；其它实例不变',
  warnings: ['请试听边界'],
  affectedClipIds: ['source-clip', 'bound-caption'],
  savedFrames: 30,
  effects: [],
  evidence: [
    {
      kind: 'accepted-word-cut',
      clipId: 'source-clip',
      assetId: transcript.assetId,
      transcriptId: transcript.id,
      wordIds: ['a', 'b'],
      sourceStartUs: 0,
      sourceEndUs: 1000000,
      startFrame: 0,
      endFrame: 30,
      description: '前句删词',
    },
  ],
};
function api(): TranscriptCutAPI {
  return {
    locate: vi.fn().mockResolvedValue(selection),
    preview: vi.fn().mockResolvedValue(preview),
    apply: vi.fn().mockResolvedValue(speechBootstrap),
  };
}
const bootstrapApply = vi.fn(async (action: () => Promise<typeof speechBootstrap>) => action());
async function render(value: React.ReactNode) {
  await act(async () => {
    root.render(value);
  });
}
function button(text: string) {
  const result = Array.from(document.querySelectorAll('button')).find(
    (value) => value.textContent?.trim() === text,
  );
  if (!result) throw Error(`未找到${text}`);
  return result;
}
async function click(text: string, shift = false) {
  await act(async () => {
    button(text).dispatchEvent(new MouseEvent('click', { bubbles: true, shiftKey: shift }));
  });
}
function panel(
  apiValue: TranscriptCutAPI,
  extra: Partial<React.ComponentProps<typeof TranscriptCutPanel>> = {},
) {
  return (
    <TranscriptCutPanel
      project={project}
      transcript={transcript}
      sourceEpoch={4}
      busy={false}
      api={apiValue}
      applyToProject={bootstrapApply}
      {...extra}
    />
  );
}
it('连续词定位和预览不写工程，确认通过共享应用通道', async () => {
  const service = api();
  const locate = vi.fn();
  bootstrapApply.mockClear();
  await render(panel(service, { onLocate: locate }));
  expect(button('定位到时间线').disabled).toBe(true);
  await click('前句');
  await click('删词', true);
  await click('定位到时间线');
  expect(service.locate).toHaveBeenCalledWith(
    expect.objectContaining({ wordIds: ['a', 'b'], sourceEpoch: 4, clipId: 'source-clip' }),
  );
  expect(locate).toHaveBeenCalledWith(selection);
  await click('预览删减影响');
  expect(document.querySelector('[role="dialog"]')?.textContent).toContain('请试听边界');
  expect(service.apply).not.toHaveBeenCalled();
  await click('应用删减');
  expect(bootstrapApply).toHaveBeenCalledOnce();
  expect(service.apply).toHaveBeenCalledWith('cut-1');
  expect(document.querySelector('[role="dialog"]')).toBeNull();
});
it('重复上轨要求明确实例，纠错草稿阻止剪辑', async () => {
  const service = api();
  const repeated = {
    ...project,
    clips: [...project.clips, { ...project.clips[0], id: 'second', startFrame: 100 }],
  };
  await render(panel(service, { project: repeated }));
  await click('删词');
  expect(button('预览删减影响').disabled).toBe(true);
  const select = container.querySelector('select')!;
  await act(async () => {
    select.value = 'second';
    select.dispatchEvent(new Event('change', { bubbles: true }));
  });
  await click('预览删减影响');
  expect(service.preview).toHaveBeenCalledWith(expect.objectContaining({ clipId: 'second' }));
  await render(panel(service, { blockedReason: '请先保存文字纠错' }));
  expect(button('预览删减影响').disabled).toBe(true);
  expect(container.textContent).toContain('请先保存文字纠错');
});
it('迟到候选不能覆盖新工程版本，失败保留选择可重试', async () => {
  const service = api();
  let resolve!: (value: WorkflowPreview) => void;
  service.preview = vi
    .fn()
    .mockImplementationOnce(
      () =>
        new Promise((r) => {
          resolve = r;
        }),
    )
    .mockRejectedValueOnce(new Error('源文件已变化'))
    .mockResolvedValue(preview);
  await render(panel(service));
  await click('删词');
  await click('预览删减影响');
  await render(panel(service, { sourceEpoch: 5 }));
  await act(async () => resolve(preview));
  expect(document.querySelector('[role="dialog"]')).toBeNull();
  await click('删词');
  await click('预览删减影响');
  expect(container.textContent).toContain('源文件已变化');
  expect(button('删词').getAttribute('aria-pressed')).toBe('true');
  await click('预览删减影响');
  expect(document.querySelector('[role="dialog"]')).not.toBeNull();
  await click('返回检查');
  expect(button('删词').getAttribute('aria-pressed')).toBe('true');
});
it('双击只发送一个预览，接受版陈旧时禁用操作', async () => {
  const service = api();
  let resolve!: (value: WorkflowPreview) => void;
  service.preview = vi.fn(
    () =>
      new Promise<WorkflowPreview>((r) => {
        resolve = r;
      }),
  );
  await render(panel(service));
  await click('删词');
  await act(async () => {
    button('预览删减影响').click();
    button('预览删减影响').click();
  });
  expect(service.preview).toHaveBeenCalledOnce();
  await act(async () => resolve(preview));
  await render(panel(service, { transcript: { ...transcript, stale: true } }));
  expect(button('定位到时间线').disabled).toBe(true);
});
it('上轨实例被移除时不暗中改选其它实例', async () => {
  const service = api();
  await render(panel(service));
  await click('删词');
  await render(panel(service, { project: { ...project, clips: [] } }));
  expect(button('预览删减影响').disabled).toBe(true);
  expect(container.textContent).toContain('尚未上轨');
});

it('全轨位置选项明确扩大范围并向同一预览传递rippleAll', async () => {
  const service = api();
  service.preview = vi.fn().mockResolvedValue({
    ...preview,
    workflowId: 'ripple-delete-range',
    summary: '全轨删除，所有后段前移',
    warnings: ['含未选轨道'],
    rangeSummary: {
      fromFrame: 0,
      toFrame: 30,
      beforeFrames: 60,
      afterFrames: 30,
      deleted: 0,
      split: 1,
      trimmed: 0,
      moved: 1,
      captionChanges: 0,
      affectedTracks: 2,
      totalAffectedClips: 4,
    },
  });
  await render(panel(service));
  const select = container.querySelectorAll('select')[1];
  await act(async () => {
    select.value = 'all';
    select.dispatchEvent(new Event('change', { bubbles: true }));
  });
  await click('删词');
  await click('预览删减影响');
  expect(service.preview).toHaveBeenCalledWith(
    expect.objectContaining({ closeGap: false, rippleAll: true }),
  );
  expect(document.querySelector('[role="dialog"]')?.textContent).toContain('含未选轨道');
  expect(service.apply).not.toHaveBeenCalled();
});
