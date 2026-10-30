// @vitest-environment jsdom
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { ContentSearchPanel } from '../src/components/ContentSearchPanel';
import { visualAnalysis } from './visual-analysis-fixtures';
import type { ContentAPI, SourceContentQuery, SourceContentResult } from '../src/content-types';
import { generationProject } from './generation-fixtures';

const project = {
  ...generationProject,
  assets: [
    {
      ...generationProject.assets[0],
      id: 'source',
      kind: 'video',
      name: '访谈源',
      path: '/fixture.mp4',
      url: 'http://127.0.0.1/fixture.mp4',
      fingerprint: 'sha256:fixture',
      durationUs: 5e6,
      hasAudio: true,
    },
  ],
};
const hit = {
  id: 'hit',
  assetId: 'source',
  assetName: '访谈源',
  transcriptId: 'accepted',
  transcriptRevision: 2,
  sourceFingerprint: 'sha256:fixture',
  origin: 'asr',
  startUs: 1e6,
  endUs: 2e6,
  wordIds: ['one', 'two'],
  text: '你好世界，接下来介绍相机',
};
const response = (input: SourceContentQuery): SourceContentResult => ({
  ...input,
  version: 'accepted-text-v1',
  query: input.text,
  origin: input.origin || '',
  total: 1,
  hits: [hit],
  stats: { transcripts: 1, words: 10, skippedStale: 0, skippedMissing: 0 },
});
let host: HTMLDivElement, root: Root;
beforeEach(() => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
  host = document.createElement('div');
  document.body.append(host);
  root = createRoot(host);
  vi.spyOn(HTMLMediaElement.prototype, 'pause').mockImplementation(() => {});
  vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new Error('本地组件测试禁止网络')));
});
afterEach(async () => {
  await act(async () => root.unmount());
  host.remove();
  expect(fetch).not.toHaveBeenCalled();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});
async function render(
  api: ContentAPI,
  revision = project.revision,
  epoch = 0,
  analyses?: import('../src/visual-analysis-types').VisualAnalysis[],
) {
  await act(async () => {
    root.render(
      <ContentSearchPanel
        project={{ ...project, revision, visualAnalyses: analyses }}
        sourceEpoch={epoch}
        api={api}
      />,
    );
  });
}
function button(text: string) {
  const node = [...document.querySelectorAll('button')].find((v) => v.textContent?.trim() === text);
  if (!node) throw Error(text);
  return node;
}
function input() {
  return document.querySelector('input')!;
}
async function enter(value: string) {
  await act(async () => {
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(input(), value);
    input().dispatchEvent(new Event('input', { bubbles: true }));
  });
}
async function click(label: string) {
  await act(async () => {
    button(label).click();
    await Promise.resolve();
  });
}

describe('素材内容检索与源回看', () => {
  it('仅明确搜索才读取、打开回看保持源时间且不自动播放或编辑', async () => {
    const api = { search: vi.fn(async (q: SourceContentQuery) => response(q)) };
    await render(api);
    await enter('你好世界');
    expect(api.search).not.toHaveBeenCalled();
    await click('搜索内容');
    expect(api.search).toHaveBeenCalledWith(
      expect.objectContaining({
        text: '你好世界',
        sourceEpoch: 0,
        projectRevision: project.revision,
      }),
    );
    expect(document.body.textContent).toContain('原话转写');
    expect(document.querySelector('video')).toBeNull();
    await click('回看源区间');
    const video = document.querySelector('video')!;
    expect(video).not.toBeNull();
    expect(video.autoplay).toBe(false);
    expect(video.muted).toBe(false);
    await act(async () => video.dispatchEvent(new Event('loadedmetadata')));
    expect(video.currentTime).toBe(1);
    video.currentTime = 2.2;
    await act(async () => video.dispatchEvent(new Event('timeupdate')));
    expect(video.currentTime).toBeLessThan(2);
    expect(video.pause).toHaveBeenCalled();
    expect(document.querySelector('[role="dialog"]')?.textContent).toContain('转写版本 2');
    expect(api.search).toHaveBeenCalledTimes(1);
  });
  it('翻页沿用已执行条件，输入未提交的新词不污染已显示结果', async () => {
    const api = {
      search: vi.fn(async (q: SourceContentQuery) => ({
        ...response(q),
        total: 2,
        hits: [{ ...hit, id: `hit-${q.offset}` }],
      })),
    };
    await render(api);
    await enter('世界');
    await click('搜索内容');
    await enter('尚未查询');
    await click('加载更多匹配');
    expect(api.search.mock.calls[1][0]).toEqual(
      expect.objectContaining({ text: '世界', offset: 1 }),
    );
    expect(document.body.textContent).toContain('“世界”');
    expect(document.querySelectorAll('.content-search-results li')).toHaveLength(2);
  });
  it('失败保留查询、空结果有下一步提示，拒绝错误版本或来源响应', async () => {
    const api = {
      search: vi
        .fn()
        .mockRejectedValueOnce(new Error('本地读取失败'))
        .mockImplementation(async (q: SourceContentQuery) => ({
          ...response(q),
          total: 0,
          hits: [],
        })),
    };
    await render(api);
    await enter('相机');
    await click('搜索内容');
    expect(document.body.textContent).toContain('本地读取失败');
    expect(input().value).toBe('相机');
    await click('搜索内容');
    expect(document.body.textContent).toContain('未找到匹配内容');
    api.search.mockImplementation(async (q: SourceContentQuery) => ({
      ...response(q),
      hits: [{ ...hit, origin: '' }],
    }));
    await click('搜索内容');
    expect(document.body.textContent).toContain('旧版转写（来源未标注）');
    api.search.mockImplementation(async (q: SourceContentQuery) => ({
      ...response(q),
      sourceEpoch: 5,
    }));
    await click('搜索内容');
    expect(document.body.textContent).toContain('检索结果条件已变化');
    expect(document.querySelector('.content-search-results')).toBeNull();
  });
  it('画面记录按推断标注，时间显示为取样且回看不伪造连续视频区间', async () => {
    const api = {
      search: vi.fn(async (q: SourceContentQuery) => ({
        ...response(q),
        hits: [
          {
            ...hit,
            kind: 'visual' as const,
            origin: 'visual',
            analysisId: visualAnalysis.id,
            noteId: 'visual-note',
            noteKind: 'inference' as const,
            producer: 'external-mcp' as const,
            tags: ['海边'],
            frames: visualAnalysis.frames,
            startUs: 0,
            endUs: 0,
          },
        ],
      })),
    };
    await render(api);
    await enter('海边');
    await click('搜索内容');
    expect(document.body.textContent).toContain('未确认推断');
    expect(document.body.textContent).toContain('取样约');
    await click('查看分析与证据');
    expect(document.querySelector('video')).toBeNull();
    expect(document.querySelector('[role="dialog"]')?.textContent).toContain('不代表连续事件');
  });
  it('来源失效的记录仍可在管理入口移除，不依赖有效搜索或模型调用', async () => {
    const value = { ...visualAnalysis, stale: true, acceptedAt: '2026-10-26T00:00:00Z' };
    const api = {
      search: vi.fn(async (q: SourceContentQuery) => response(q)),
      removeAnalysis: vi.fn<NonNullable<ContentAPI['removeAnalysis']>>().mockResolvedValue({
        project: { ...project, revision: project.revision + 1 },
        projectPath: '',
        dirty: true,
        canUndo: true,
        canRedo: false,
        capabilities: {},
      }),
    };
    await render(api, project.revision, 0, [value]);
    expect(document.body.textContent).toContain('来源已变化');
    await click('移除整份记录');
    expect(api.removeAnalysis).toHaveBeenCalledWith(
      expect.objectContaining({
        id: value.id,
        projectId: project.id,
        projectRevision: project.revision,
        sourceEpoch: 0,
      }),
    );
    expect(api.search).not.toHaveBeenCalled();
  });
  it('旧响应不能在工程更新或同ID重开后恢复结果与播放器', async () => {
    let resolve!: (r: SourceContentResult) => void;
    let pending!: SourceContentQuery;
    const api = {
      search: vi.fn((q: SourceContentQuery) => {
        pending = q;
        return new Promise<SourceContentResult>((r) => {
          resolve = r;
        });
      }),
    };
    await render(api);
    await enter('相机');
    await click('搜索内容');
    expect(document.body.textContent).toContain('正在检索');
    await render(api, project.revision, 1);
    await act(async () => resolve(response(pending)));
    expect(document.querySelector('.content-search-results')).toBeNull();
    expect(document.querySelector('video')).toBeNull();
  });
});
