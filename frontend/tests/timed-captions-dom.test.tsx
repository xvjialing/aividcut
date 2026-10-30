// @vitest-environment jsdom
import { act, useState } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { SpeechPanel } from '../src/components/SpeechPanel';
import { SpeechAudioResult } from '../src/components/SpeechAudioResult';
import { CaptionLayoutPanel } from '../src/components/CaptionLayoutPanel';
import { SourceTranscriptEditor } from '../src/components/SourceTranscriptEditor';
import { captionLayoutDraft, parseCaptionLayout } from '../src/lib/caption-layout';
import { speechTimingSummary } from '../src/lib/speech-timing';
import {
  fakeSpeechAPI,
  sourceTranscript,
  speechProject,
  speechSourceClip,
} from './speech-fixtures';
import {
  captionLayoutPreview,
  layoutApplied,
  readableLayout,
  timedAudio,
  timedJob,
  timedSummary,
  ttsTranscript,
} from './timed-captions-fixtures';
let container: HTMLDivElement;
let root: Root;
let fetchGuard: ReturnType<typeof vi.fn>;
beforeEach(() => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
  fetchGuard = vi.fn().mockRejectedValue(new Error('禁止真实语音/收费/ASR回退'));
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
function checkbox(text: string): HTMLInputElement {
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
async function waitFor(selector: string) {
  for (let index = 0; index < 100; index++) {
    const value = document.querySelector(selector);
    if (value) return value;
    await act(async () => {
      await flush();
    });
  }
  throw new Error(`未加载界面：${selector}`);
}
const emptyProject = { ...speechProject, assets: [], clips: [], transcripts: [] };
function audioResult(
  onImport = vi.fn().mockResolvedValue(true),
  options: Partial<React.ComponentProps<typeof SpeechAudioResult>> = {},
) {
  return (
    <SpeechAudioResult
      job={timedSummary}
      detail={timedJob}
      project={emptyProject}
      busy={false}
      onImport={onImport}
      {...options}
    />
  );
}

describe('显式带时配音请求', () => {
  it('开关默认不选，只有支持时选择且一次计费确认才携带withTiming', async () => {
    const api = fakeSpeechAPI();
    api.startSpeechJob.mockResolvedValue(timedSummary);
    await render(<SpeechPanel project={emptyProject} onBootstrap={vi.fn()} api={api} />);
    await click(button('配音'));
    expect(checkbox('获取配音时间戳').checked).toBe(false);
    expect(api.listVoices).not.toHaveBeenCalled();
    await click(button('查询音色'));
    await enter(field('配音音色'), 'voice-1');
    await enter(field('配音文本'), '12');
    await click(checkbox('获取配音时间戳'));
    expect(field('音频格式').textContent).toContain('MP3 · 44.1kHz · 128kbps');
    await click(button('提交前核对…'));
    expect(api.startSpeechJob).not.toHaveBeenCalled();
    expect(document.querySelector('[role="dialog"]')?.textContent).toContain(
      '一次带时间的配音生成',
    );
    await click(button('确认上传并提交（可能计费）'));
    expect(api.startSpeechJob).toHaveBeenCalledOnce();
    expect(api.startSpeechJob.mock.calls[0][0].synthesize?.withTiming).toBe(true);
    expect(api.acceptSpeechTranscript).not.toHaveBeenCalled();
  });
  it('供应商不支持时禁用开关；摘要结构状态不冒充应用资格，也不自动读详情', async () => {
    const api = fakeSpeechAPI();
    api.getSpeechCatalog.mockImplementation(async () => ({
      ...(await import('./speech-fixtures')).speechCatalog,
      speechTiming: { supported: false, reason: '该适配未提供带时配音' },
    }));
    api.listSpeechJobs.mockResolvedValue([timedSummary]);
    await render(<SpeechPanel project={emptyProject} onBootstrap={vi.fn()} api={api} />);
    await click(button('配音'));
    expect(checkbox('获取配音时间戳').disabled).toBe(true);
    await click(button('任务 1'));
    expect(document.body.textContent).toContain('配音时间：原文可核对 / 朗读文本可核对');
    expect(api.getSpeechJob).not.toHaveBeenCalled();
    expect(speechTimingSummary({ ...timedSummary, timingComplete: false })).toContain(
      '原文时间有缺失',
    );
    expect(api.startSpeechJob).not.toHaveBeenCalled();
  });
});
describe('真实时间导入与已有配音范围', () => {
  it('普通音频导入不带被忽略字幕参数；开关取消后也不残留字幕参数', async () => {
    const onImport = vi.fn().mockResolvedValue(true);
    await render(audioResult(onImport));
    await click(button('导入配音到素材库'));
    expect(onImport).toHaveBeenLastCalledWith({ trackId: '', startFrame: 0, addToTimeline: false });
    await click(checkbox('一并添加真实时间字幕'));
    await enter(field('字幕呈现'), 'readable');
    await click(checkbox('一并添加真实时间字幕'));
    await click(button('导入配音并加入时间线'));
    const options = onImport.mock.calls.at(-1)![0];
    expect(options).not.toHaveProperty('captionLayout');
    expect(options).not.toHaveProperty('alignmentSource');
    expect(options).not.toHaveProperty('captionTrackId');
  });
  it('原文缺时间不借用朗读文本；显式选择朗读来源后才能带字幕导入', async () => {
    const onImport = vi.fn().mockResolvedValue(true);
    const detail = {
      ...timedJob,
      timing: { ...timedJob.timing!, alignment: null, timingComplete: false },
      timingChoices: [
        {
          source: 'original' as const,
          available: false,
          reason: '原文字符时间缺失',
          text: '',
          wordCount: 0,
        },
        timedJob.timingChoices![1],
      ],
    };
    await render(
      audioResult(onImport, {
        job: { ...timedSummary, timingAvailable: false, timingComplete: false },
        detail,
      }),
    );
    await click(checkbox('一并添加真实时间字幕'));
    expect(field('字幕文字来源').value).toBe('original');
    expect(button('添加配音与字幕').disabled).toBe(true);
    expect(onImport).not.toHaveBeenCalled();
    await enter(field('字幕文字来源'), 'normalized');
    expect(document.body.textContent).toContain('十二');
    await click(button('添加配音与字幕'));
    expect(onImport).toHaveBeenCalledWith(
      expect.objectContaining({
        alignmentSource: 'normalized',
        generateCaptions: true,
        captionLayout: { mode: 'word' },
        addToTimeline: true,
      }),
    );
  });
  it('两套时间均不可用仍可单独导入，null原样显示未提供且不自动ASR', async () => {
    const onImport = vi.fn().mockResolvedValue(true);
    const detail = {
      ...timedJob,
      timing: {
        ...timedJob.timing!,
        alignment: { characters: ['1'], startTimesSeconds: [null], endTimesSeconds: [0.1] },
        normalizedAlignment: null,
        timingComplete: false,
      },
      timingChoices: [
        {
          source: 'original' as const,
          available: false,
          reason: '原文缺时间',
          text: '',
          wordCount: 0,
        },
        {
          source: 'normalized' as const,
          available: false,
          reason: '无朗读时间',
          text: '',
          wordCount: 0,
        },
      ],
    };
    await render(audioResult(onImport, { detail }));
    expect(checkbox('一并添加真实时间字幕').disabled).toBe(true);
    const disclosure = document.querySelector('.alignment-inspector summary') as HTMLElement;
    await click(disclosure);
    await waitFor('.alignment-inspector[open] .speech-word-result');
    expect(document.body.textContent).toContain('未提供 → 0.100秒');
    await click(button('导入配音到素材库'));
    expect(onImport).toHaveBeenCalledWith({ trackId: '', startFrame: 0, addToTimeline: false });
  });
  it('已有分割/重复音频时展示全部范围，只补字幕而不新增配音或发送新位置', async () => {
    const onImport = vi.fn().mockResolvedValue(true);
    const project = {
      ...emptyProject,
      assets: [timedAudio],
      clips: [
        { ...speechSourceClip, assetId: timedAudio.id },
        {
          ...speechSourceClip,
          id: 'second-audio',
          assetId: timedAudio.id,
          startFrame: 90,
          durationFrames: 30,
        },
      ],
    };
    await render(
      audioResult(onImport, {
        project,
        job: { ...timedSummary, audioImported: true, audioOnTimeline: true },
      }),
    );
    expect(document.body.textContent).toContain('使用 2 段');
    expect(document.body.textContent).toContain('全部上轨片段');
    expect(document.body.textContent).toContain('分割与重复使用');
    await click(checkbox('一并添加真实时间字幕'));
    await enter(field('字幕呈现'), 'readable');
    await click(button('为已有配音添加字幕'));
    expect(onImport).toHaveBeenCalledWith({
      trackId: '',
      startFrame: 0,
      addToTimeline: false,
      generateCaptions: true,
      captionTrackId: '',
      alignmentSource: 'original',
      captionLayout: readableLayout,
    });
  });
  it('接纳后固定文字来源，重复导入不重排人工字幕，引导先预览分句', async () => {
    const onImport = vi.fn();
    const onOpenCaptions = vi.fn();
    const project = {
      ...emptyProject,
      assets: [timedAudio],
      transcripts: [ttsTranscript],
      clips: [{ ...speechSourceClip, assetId: timedAudio.id }],
    };
    await render(
      audioResult(onImport, {
        project,
        onOpenCaptions,
        job: {
          ...timedSummary,
          acceptedTranscriptId: ttsTranscript.id,
          captionTimingSource: 'normalized',
          audioImported: true,
          audioOnTimeline: true,
        },
      }),
    );
    expect(document.body.textContent).toContain('字幕已使用朗读文本时间');
    expect(document.body.textContent).toContain('不会重排或覆盖人工修改');
    await click(button('查看或预览字幕分句'));
    expect(onOpenCaptions).toHaveBeenCalledWith(ttsTranscript.id);
    expect(onImport).not.toHaveBeenCalled();
  });
});
describe('来源字幕阅读分句预览', () => {
  it('显式新建轨发送createNewTrack，成功后采用返回的新轨而不是再次建轨', async () => {
    const preview = vi.fn().mockResolvedValue(captionLayoutPreview);
    const apply = vi.fn().mockResolvedValue({
      ...layoutApplied,
      project: {
        ...layoutApplied.project,
        tracks: [
          ...layoutApplied.project.tracks,
          { ...speechProject.tracks[1], id: 'caption-created', name: '新字幕轨' },
        ],
        transcripts: [
          { ...layoutApplied.project.transcripts![0], captionTrackId: 'caption-created' },
        ],
      },
    });
    function Harness() {
      const [project, setProject] = useState(speechProject);
      return (
        <CaptionLayoutPanel
          project={project}
          transcript={project.transcripts![0]}
          busy={false}
          onPreview={preview}
          onApply={async (id) => {
            const value = await apply(id);
            setProject(value.project);
            return value;
          }}
        />
      );
    }
    await render(<Harness />);
    await enter(field('目标字幕轨'), '');
    await click(button('预览字幕分句'));
    expect(preview).toHaveBeenLastCalledWith(
      expect.objectContaining({ trackId: '', createNewTrack: true }),
    );
    await click(button('应用字幕分句并保留撤销'));
    expect(field('目标字幕轨').value).toBe('caption-created');
    await click(button('预览字幕分句'));
    expect(preview.mock.calls.at(-1)![0].trackId).toBe('caption-created');
    expect(preview.mock.calls.at(-1)![0]).not.toHaveProperty('createNewTrack');
  });
  it('ownApply新轨后Undo移除目标，同步到恢复轨并保留未提交排版草稿', async () => {
    const preview = vi.fn().mockResolvedValue(captionLayoutPreview);
    const created = {
      ...layoutApplied,
      project: {
        ...layoutApplied.project,
        tracks: [
          ...layoutApplied.project.tracks,
          { ...speechProject.tracks[1], id: 'caption-created', name: '新字幕轨' },
        ],
        transcripts: [
          { ...layoutApplied.project.transcripts![0], captionTrackId: 'caption-created' },
        ],
      },
    };
    function Harness() {
      const [project, setProject] = useState(speechProject);
      return (
        <>
          <CaptionLayoutPanel
            project={project}
            transcript={project.transcripts![0]}
            busy={false}
            onPreview={preview}
            onApply={async () => {
              setProject(created.project);
              return created;
            }}
          />
          <button onClick={() => setProject({ ...speechProject, revision: 5 })}>
            模拟撤销新轨
          </button>
        </>
      );
    }
    await render(<Harness />);
    await enter(field('目标字幕轨'), '');
    await click(button('预览字幕分句'));
    await click(button('应用字幕分句并保留撤销'));
    expect(field('目标字幕轨').value).toBe('caption-created');
    await enter(field('每行最多字数'), '24');
    await click(button('模拟撤销新轨'));
    expect(field('目标字幕轨').value).toBe(sourceTranscript.captionTrackId);
    expect(field('每行最多字数').value).toBe('24');
    expect(field('字幕呈现').value).toBe('readable');
    expect(document.body.textContent).toContain('排版参数保留，请重新预览');
    await click(button('预览字幕分句'));
    expect(preview.mock.calls.at(-1)![0]).toEqual(
      expect.objectContaining({
        projectRevision: 5,
        trackId: sourceTranscript.captionTrackId,
        layout: expect.objectContaining({ maxCharsPerLine: 24 }),
      }),
    );
    expect(preview.mock.calls.at(-1)![0]).not.toHaveProperty('createNewTrack');
  });
  it('外部移除所有字幕轨时明确选择新建，旧预览失效且不再发送已删ID', async () => {
    const preview = vi.fn().mockResolvedValue(captionLayoutPreview);
    const apply = vi.fn();
    const panel = (project = speechProject, transcript = sourceTranscript) => (
      <CaptionLayoutPanel
        project={project}
        transcript={transcript}
        busy={false}
        onPreview={preview}
        onApply={apply}
      />
    );
    await render(panel());
    await enter(field('字幕呈现'), 'readable');
    await enter(field('每行最多字数'), '21');
    await click(button('预览字幕分句'));
    expect(button('应用字幕分句并保留撤销').disabled).toBe(false);
    const transcript = { ...sourceTranscript, captionTrackId: undefined };
    const project = {
      ...speechProject,
      revision: 4,
      tracks: speechProject.tracks.filter((track) => track.kind !== 'caption'),
      clips: speechProject.clips.filter((clip) => clip.kind !== 'caption'),
      transcripts: [transcript],
    };
    await render(panel(project, transcript));
    expect(field('目标字幕轨').value).toBe('');
    expect(field('每行最多字数').value).toBe('21');
    expect(document.body.textContent).toContain('已切换为新建字幕轨');
    expect(
      Array.from(document.querySelectorAll('button')).some(
        (button) => button.textContent === '应用字幕分句并保留撤销',
      ),
    ).toBe(false);
    expect(apply).not.toHaveBeenCalled();
    await click(button('预览字幕分句'));
    expect(preview.mock.calls.at(-1)![0]).toEqual(
      expect.objectContaining({
        projectRevision: 4,
        trackId: '',
        createNewTrack: true,
        layout: expect.objectContaining({ maxCharsPerLine: 21 }),
      }),
    );
  });
  it('从配音入口聚焦时自动展开并滚入分句，不抹已有纠错草稿', async () => {
    const scrollIntoView = vi.fn();
    Object.defineProperty(HTMLElement.prototype, 'scrollIntoView', {
      configurable: true,
      value: scrollIntoView,
    });
    const props = {
      busy: false,
      onCorrect: vi.fn(),
      onDetach: vi.fn(),
      onLayoutPreview: vi.fn().mockResolvedValue(captionLayoutPreview),
      onLayoutApply: vi.fn().mockResolvedValue(layoutApplied),
    };
    await render(
      <SourceTranscriptEditor
        project={speechProject}
        focusTranscriptId={sourceTranscript.id}
        {...props}
      />,
    );
    await waitFor('.source-caption-layout[open] .caption-layout-editor');
    expect((document.querySelector('.source-caption-layout') as HTMLDetailsElement).open).toBe(
      true,
    );
    expect(scrollIntoView).toHaveBeenCalled();
    const other = { ...sourceTranscript, id: 'another-source' };
    const project = { ...speechProject, transcripts: [sourceTranscript, other] };
    await render(<SourceTranscriptEditor project={project} {...props} />);
    await enter(field('文本'), '尚未提交的纠错');
    await render(
      <SourceTranscriptEditor project={project} focusTranscriptId={other.id} {...props} />,
    );
    expect(field('文本').value).toBe('尚未提交的纠错');
    expect(field('字幕来源').value).toBe(sourceTranscript.id);
    expect(document.body.textContent).toContain('请先保存或重新读取当前纠错');
  });
  it('默认逐词兼容，阅读参数用秒/字/行并精确转微秒；不静默接受非法默认值', () => {
    expect(parseCaptionLayout(captionLayoutDraft()).value).toEqual({ mode: 'word' });
    const draft = { ...captionLayoutDraft(), mode: 'readable' as const };
    expect(parseCaptionLayout(draft).value).toEqual(readableLayout);
    expect(
      parseCaptionLayout({ ...draft, maxDurationSeconds: '0.100001' }).value?.maxDurationUs,
    ).toBe(100001);
    expect(parseCaptionLayout({ ...draft, maxLines: '0' }).value).toBeNull();
    expect(parseCaptionLayout({ ...draft, pauseThresholdSeconds: '0' }).value).toBeNull();
  });
  it('预览影响和人工保护后才以ID应用；参数改变/旧工程会禁用旧结果', async () => {
    const onPreview = vi.fn().mockResolvedValue(captionLayoutPreview);
    const onApply = vi.fn().mockResolvedValue(layoutApplied);
    const panel = (project = speechProject) => (
      <CaptionLayoutPanel
        project={project}
        transcript={sourceTranscript}
        busy={false}
        onPreview={onPreview}
        onApply={onApply}
      />
    );
    await render(panel());
    expect(field('字幕呈现').value).toBe('word');
    expect(field('字幕呈现').textContent).toContain('逐词显示');
    await enter(field('字幕呈现'), 'readable');
    await click(button('预览字幕分句'));
    expect(onPreview).toHaveBeenCalledWith({
      projectId: speechProject.id,
      projectRevision: speechProject.revision,
      transcriptId: sourceTranscript.id,
      transcriptRevision: sourceTranscript.revision,
      trackId: sourceTranscript.captionTrackId,
      layout: readableLayout,
    });
    expect(onApply).not.toHaveBeenCalled();
    expect(document.body.textContent).toContain('人工修改保护 1 条');
    expect(document.body.textContent).toContain('手改字幕');
    await render(panel({ ...speechProject, revision: 4 }));
    expect(button('应用字幕分句并保留撤销').disabled).toBe(true);
    await render(panel());
    await enter(field('每行最多字数'), '20');
    expect(
      Array.from(document.querySelectorAll('button')).some(
        (button) => button.textContent === '应用字幕分句并保留撤销',
      ),
    ).toBe(false);
    await click(button('预览字幕分句'));
    await click(button('应用字幕分句并保留撤销'));
    expect(onApply).toHaveBeenCalledWith('layout-preview');
    expect(layoutApplied.canUndo).toBe(true);
  });
  it('未保存纠错/保护组拒绝时不修改字幕，TTS来源明确不说成识别', async () => {
    const onPreview = vi.fn().mockRejectedValue(new Error('人工字幕组部分可见，不能安全重排'));
    const onApply = vi.fn();
    await render(
      <CaptionLayoutPanel
        project={speechProject}
        transcript={sourceTranscript}
        busy={false}
        blockedReason="请先保存文字纠错"
        onPreview={onPreview}
        onApply={onApply}
      />,
    );
    expect(button('预览字幕分句').disabled).toBe(true);
    expect(onPreview).not.toHaveBeenCalled();
    await render(
      <CaptionLayoutPanel
        project={speechProject}
        transcript={sourceTranscript}
        busy={false}
        onPreview={onPreview}
        onApply={onApply}
      />,
    );
    await click(button('预览字幕分句'));
    expect(document.body.textContent).toContain('不能安全重排');
    expect(onApply).not.toHaveBeenCalled();
    await render(
      <SourceTranscriptEditor
        project={{ ...speechProject, transcripts: [ttsTranscript], assets: [timedAudio] }}
        busy={false}
        onCorrect={vi.fn()}
        onDetach={vi.fn()}
      />,
    );
    expect(field('字幕来源').textContent).toContain('配音字幕源／朗读文本');
    expect(document.body.textContent).toContain('不是语音识别');
  });
  it('源时间以秒编辑但后台保留精确微秒与稳定词身份', async () => {
    const correct = vi.fn().mockResolvedValue(sourceTranscript);
    await render(
      <SourceTranscriptEditor
        project={speechProject}
        busy={false}
        onCorrect={correct}
        onDetach={vi.fn()}
      />,
    );
    expect(field('起点 · 秒').value).toBe('0.100');
    await enter(field('起点 · 秒'), '0.100001');
    await click(button('保存源词纠错'));
    expect(correct).toHaveBeenCalledWith(
      expect.objectContaining({
        words: [
          expect.objectContaining({
            id: 'word-1',
            startUs: 100001,
            endUs: 800000,
            speakerId: 'speaker-1',
          }),
        ],
      }),
    );
  });
});
