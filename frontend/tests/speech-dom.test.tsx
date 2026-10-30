// @vitest-environment jsdom
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { SpeechPanel } from '../src/components/SpeechPanel';
import { SourceTranscriptEditor } from '../src/components/SourceTranscriptEditor';
import { SpeechCreationForm } from '../src/components/SpeechCreationForm';
import {
  fakeSpeechAPI,
  sourceTranscript,
  speechBootstrap,
  speechCatalog,
  speechConnections,
  speechJob,
  speechProject,
  speechSource,
  speechSummary,
} from './speech-fixtures';

let container: HTMLDivElement;
let root: Root;
let fetchGuard: ReturnType<typeof vi.fn>;
beforeEach(() => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
  fetchGuard = vi.fn().mockRejectedValue(new Error('禁止真实语音网络'));
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
async function waitForElement(selector: string) {
  for (let attempt = 0; attempt < 100; attempt++) {
    const value = document.querySelector(selector);
    if (value) return value;
    await act(async () => {
      await flush();
    });
  }
  throw new Error(`未加载预期界面：${selector}`);
}
async function render(element: React.ReactNode) {
  await act(async () => {
    root.render(element);
    await flush();
  });
}
function button(label: string): HTMLButtonElement {
  const value = Array.from(document.querySelectorAll('button')).find(
    (item) => item.textContent?.trim() === label,
  );
  if (!value) throw new Error(`未找到按钮 ${label}`);
  return value;
}
function field(
  label: string,
  index = 0,
): HTMLInputElement | HTMLTextAreaElement | HTMLSelectElement {
  const wrapper = Array.from(document.querySelectorAll('label')).filter(
    (item) => item.querySelector('.field-label')?.textContent === label,
  )[index];
  const input = wrapper?.querySelector('input,select,textarea');
  if (!(
    input instanceof HTMLInputElement ||
    input instanceof HTMLTextAreaElement ||
    input instanceof HTMLSelectElement
  ))
    throw new Error(`未找到字段 ${label}`);
  return input;
}
async function click(value: HTMLElement) {
  await act(async () => {
    value.click();
    await flush();
  });
}
async function enter(
  element: HTMLInputElement | HTMLTextAreaElement | HTMLSelectElement,
  value: string,
) {
  await act(async () => {
    const prototype =
      element instanceof HTMLInputElement
        ? HTMLInputElement.prototype
        : element instanceof HTMLTextAreaElement
          ? HTMLTextAreaElement.prototype
          : HTMLSelectElement.prototype;
    Object.getOwnPropertyDescriptor(prototype, 'value')!.set!.call(element, value);
    element.dispatchEvent(new Event('input', { bubbles: true }));
    element.dispatchEvent(new Event('change', { bubbles: true }));
    await flush();
  });
}
function checkbox(text: string): HTMLInputElement {
  const input = Array.from(document.querySelectorAll('label'))
    .find((item) => item.textContent?.includes(text))
    ?.querySelector('input[type="checkbox"]');
  if (!(input instanceof HTMLInputElement)) throw new Error(`未找到勾选 ${text}`);
  return input;
}
function panel(
  api: ReturnType<typeof fakeSpeechAPI>,
  onBootstrap = vi.fn(),
  project = speechProject,
) {
  return <SpeechPanel project={project} onBootstrap={onBootstrap} api={api} />;
}

describe('语音提交必须明确上传和计费确认', () => {
  it('语音连接Modal按目录供应商隔离，保留该供应商未声明模型供用户核对', async () => {
    const api = fakeSpeechAPI();
    const unknown = {
      ...speechConnections[0],
      id: 'speech-unknown',
      modelId: 'legacy-unlisted',
      name: '旧语音模型',
    };
    api.getConnections.mockResolvedValue([
      ...speechConnections,
      unknown,
      {
        ...speechConnections[0],
        id: 'image-profile',
        providerId: 'openai',
        modelId: 'gpt-image-2.5-sunburst',
        name: '图片连接',
      },
    ]);
    await render(panel(api));
    expect(
      Array.from(field('语音模型连接').querySelectorAll('option')).map((option) => option.value),
    ).toEqual(['', speechConnections[0].id]);
    await click(button('管理连接'));
    expect(document.querySelectorAll('.connection-profile')).toHaveLength(3);
    expect(document.querySelector('[role="dialog"]')?.textContent).toContain('旧语音模型');
    expect(document.querySelector('[role="dialog"]')?.textContent).not.toContain('图片连接');
    expect(api.startSpeechJob).not.toHaveBeenCalled();
    expect(api.saveConnection).not.toHaveBeenCalled();
  });
  it('加载和编辑不调用供应商，只有确认才提交登记源ID且不自动接受', async () => {
    const api = fakeSpeechAPI();
    api.startSpeechJob.mockResolvedValue(speechSummary({ status: 'queued' }));
    await render(panel(api));
    expect(api.startSpeechJob).not.toHaveBeenCalled();
    expect(api.listVoices).not.toHaveBeenCalled();
    await enter(field('转写源素材'), speechSource.id);
    await click(button('提交前核对…'));
    expect(document.querySelector('[role="dialog"]')?.textContent).toContain('已登记口播.wav');
    expect(api.startSpeechJob).not.toHaveBeenCalled();
    await click(button('返回修改'));
    expect(api.startSpeechJob).not.toHaveBeenCalled();
    await click(button('提交前核对…'));
    await click(button('确认上传并提交（可能计费）'));
    expect(api.startSpeechJob).toHaveBeenCalledOnce();
    expect(api.startSpeechJob.mock.calls[0][0]).toEqual(
      expect.objectContaining({
        connectionId: speechConnections[0].id,
        mode: 'transcribe',
        consent: true,
        transcribe: expect.objectContaining({ assetId: speechSource.id, modelId: 'scribe_v2' }),
      }),
    );
    expect(JSON.stringify(api.startSpeechJob.mock.calls[0][0])).not.toContain('/fixture/');
    expect(api.acceptSpeechTranscript).not.toHaveBeenCalled();
  });
  it('音色查询与翻页均显式，配音沿用所选模型和真实音色ID', async () => {
    const api = fakeSpeechAPI();
    api.startSpeechJob.mockResolvedValue(speechSummary({ mode: 'synthesize', status: 'queued' }));
    api.listVoices
      .mockResolvedValueOnce({
        voices: [{ id: 'voice-1', name: '音色一', verificationState: 'unverified' }],
        hasMore: true,
        nextPageToken: 'next-opaque',
      })
      .mockResolvedValueOnce({
        voices: [{ id: 'voice-2', name: '音色二', verificationState: 'unverified' }],
        hasMore: false,
      });
    await render(panel(api));
    await click(button('配音'));
    expect(api.listVoices).not.toHaveBeenCalled();
    await click(button('查询音色'));
    expect(api.listVoices).toHaveBeenCalledTimes(1);
    expect(api.startSpeechJob).not.toHaveBeenCalled();
    await click(button('下一页音色'));
    expect(api.listVoices).toHaveBeenLastCalledWith(
      speechConnections[1].id,
      expect.objectContaining({ nextPageToken: 'next-opaque', pageSize: 20 }),
    );
    await enter(field('配音音色'), 'voice-2');
    await enter(field('配音文本'), '这是一段配音');
    await click(button('提交前核对…'));
    expect(api.startSpeechJob).not.toHaveBeenCalled();
    await click(button('确认上传并提交（可能计费）'));
    expect(api.startSpeechJob.mock.calls[0][0]).toEqual(
      expect.objectContaining({
        mode: 'synthesize',
        consent: true,
        synthesize: expect.objectContaining({
          modelId: 'eleven_multilingual_v2',
          voiceId: 'voice-2',
          text: '这是一段配音',
        }),
      }),
    );
    expect(api.importSpeechAudio).not.toHaveBeenCalled();
  });
  it('提交响应未明确时不自动重交，相同内容的再次明确确认沿用原编号', async () => {
    const api = fakeSpeechAPI();
    await render(panel(api));
    await enter(field('转写源素材'), speechSource.id);
    await click(button('提交前核对…'));
    await click(button('确认上传并提交（可能计费）'));
    expect(api.startSpeechJob).toHaveBeenCalledOnce();
    const id = api.startSpeechJob.mock.calls[0][0].id;
    expect(document.body.textContent).toContain('不会自动重交');
    await click(button('提交前核对…'));
    expect(document.querySelector('[role="dialog"]')?.textContent).toContain('沿用上次请求编号');
    expect(api.startSpeechJob).toHaveBeenCalledOnce();
    await click(button('确认上传并提交（可能计费）'));
    expect(api.startSpeechJob).toHaveBeenCalledTimes(2);
    expect(api.startSpeechJob.mock.calls[1][0].id).toBe(id);
  });
  it('音频导入能力缺失时在付费前禁用配音', async () => {
    const api = fakeSpeechAPI();
    api.getSpeechCatalog.mockResolvedValue({
      ...speechCatalog,
      audioImport: { available: false, reason: '媒体工具未就绪' },
    });
    await render(panel(api));
    await click(button('配音'));
    expect(document.body.textContent).toContain('媒体工具未就绪');
    expect(button('提交前核对…').disabled).toBe(true);
    expect(api.startSpeechJob).not.toHaveBeenCalled();
  });
  it('语音连接复用密钥Modal并采用独立模型选项，不冒充图片能力', async () => {
    const api = fakeSpeechAPI();
    api.saveConnection.mockResolvedValue(speechConnections[0]);
    await render(panel(api));
    await click(button('管理连接'));
    expect(field('服务商').value).toBe('elevenlabs');
    expect(
      Array.from(field('模型').querySelectorAll('option')).map((option) => option.value),
    ).toEqual(['scribe_v2', 'eleven_multilingual_v2']);
    await enter(field('连接名称'), '语音测试连接');
    await enter(field('API 密钥'), 'fixture-speech-secret');
    await click(button('保存连接'));
    expect(api.saveConnection).toHaveBeenCalledWith(
      expect.objectContaining({
        providerId: 'elevenlabs',
        modelId: 'scribe_v2',
        apiKey: 'fixture-speech-secret',
      }),
    );
    expect(field('API 密钥').value).toBe('');
    expect(document.body.textContent).not.toContain('fixture-speech-secret');
    expect(api.startSpeechJob).not.toHaveBeenCalled();
  });
  it('创建音色限定音频与self/authorized声明，核对不是身份验证', async () => {
    const review = vi.fn();
    await render(
      <SpeechCreationForm
        mode="clone"
        catalog={speechCatalog}
        connections={speechConnections}
        project={speechProject}
        voices={[]}
        busy={false}
        onReview={review}
      />,
    );
    expect(document.body.textContent).not.toContain('采访.mp4');
    await enter(field('音色名称'), '自定义声音');
    await click(checkbox('已登记口播.wav'));
    await enter(field('声音主体'), '本人');
    await click(button('提交前核对…'));
    expect(review).not.toHaveBeenCalled();
    await click(checkbox('我确认是本人或已获明确授权'));
    await click(button('提交前核对…'));
    expect(review).toHaveBeenCalledWith(
      expect.objectContaining({
        mode: 'clone',
        clone: expect.objectContaining({
          assetIds: [speechSource.id],
          consent: { confirmed: true, subject: '本人', authority: 'self' },
        }),
      }),
    );
    expect(document.body.textContent).toContain('仍为未验证音色');
  });
});
describe('语音结果与工程接受分离', () => {
  it('详情显式读取，缺失时间保留未提供且不允许接受成绑定字幕', async () => {
    const api = fakeSpeechAPI();
    api.listSpeechJobs.mockResolvedValue([speechSummary()]);
    const detail = speechJob();
    detail.transcription!.words[0] = {
      ...detail.transcription!.words[0],
      startUs: null,
      endUs: null,
      timingComplete: false,
    };
    detail.transcription!.timingComplete = false;
    api.getSpeechJob.mockResolvedValue(detail);
    await render(panel(api));
    expect(api.getSpeechJob).not.toHaveBeenCalled();
    await click(button('任务 1'));
    await click(button('查看结果与处理'));
    expect(api.getSpeechJob).toHaveBeenCalledWith('speech-job');
    expect(document.body.textContent).toContain('未提供 → 未提供');
    expect(button('接受源转写到工程').disabled).toBe(true);
    expect(api.acceptSpeechTranscript).not.toHaveBeenCalled();
  });
  it('完整普通词经明确接受才写工程，可显式生成绑定字幕', async () => {
    const api = fakeSpeechAPI();
    const onBootstrap = vi.fn();
    api.listSpeechJobs.mockResolvedValue([speechSummary()]);
    await render(panel(api, onBootstrap));
    await click(button('任务 1'));
    await click(button('查看结果与处理'));
    expect(api.acceptSpeechTranscript).not.toHaveBeenCalled();
    await click(checkbox('接受时生成跟随源剪辑'));
    await click(button('接受源转写到工程'));
    expect(api.acceptSpeechTranscript).toHaveBeenCalledWith('speech-job', {
      trackId: '',
      generateCaptions: true,
    });
    expect(onBootstrap).toHaveBeenCalledWith(speechBootstrap);
    expect(speechBootstrap.canUndo).toBe(true);
  });
  it('未知提交只人工核对，恢复仅调用本地落盘接口，均不重新收费', async () => {
    const api = fakeSpeechAPI();
    api.listSpeechJobs.mockResolvedValue([
      speechSummary({ status: 'unknown', canResumeLocal: false }),
    ]);
    await render(panel(api));
    await click(button('任务 1'));
    await click(button('已核对服务记录…'));
    expect(button('记录已核对').disabled).toBe(true);
    await click(checkbox('我已核对供应商受理'));
    await click(button('记录已核对'));
    expect(api.acknowledgeSpeechUnknown).toHaveBeenCalledWith('speech-job');
    api.listSpeechJobs.mockResolvedValue([
      speechSummary({ status: 'failed', canResumeLocal: true }),
    ]);
    await click(button('刷新语音任务'));
    await click(button('恢复本地保存'));
    expect(api.resumeSpeechLocal).toHaveBeenCalledWith('speech-job');
    expect(api.startSpeechJob).not.toHaveBeenCalled();
  });
  it('旧目标工程不能接受或导入结果', async () => {
    const api = fakeSpeechAPI();
    api.listSpeechJobs.mockResolvedValue([speechSummary()]);
    await render(panel(api, vi.fn(), { ...speechProject, id: 'another-project' }));
    await click(button('任务 1'));
    await click(button('查看结果与处理'));
    expect(button('接受源转写到工程').disabled).toBe(true);
    expect(document.body.textContent).toContain('不会添加到当前其他工程');
    expect(api.acceptSpeechTranscript).not.toHaveBeenCalled();
  });
  it('本地配音仅在明确导入时写素材/时间线，沿用实测时长与帧起点', async () => {
    const api = fakeSpeechAPI();
    const onBootstrap = vi.fn();
    const audio = {
      ...speechSource,
      id: 'tts-audio',
      name: '已保存配音.mp3',
      path: '/fixture/tts.mp3',
      durationUs: 1357246,
      url: 'http://127.0.0.1:43110/media/signed-test-only',
    };
    const summary = speechSummary({ mode: 'synthesize', audio, sourceAssetId: undefined });
    api.listSpeechJobs.mockResolvedValue([summary]);
    api.getSpeechJob.mockResolvedValue(speechJob({ ...summary, transcription: undefined }));
    await render(panel(api, onBootstrap));
    await click(button('任务 1'));
    await click(button('查看结果与处理'));
    const player = (await waitForElement('audio')) as HTMLAudioElement;
    expect(player?.getAttribute('src')).toBe(audio.url);
    expect(player?.preload).toBe('none');
    expect(document.body.textContent).toContain('1.357246秒');
    expect(api.importSpeechAudio).not.toHaveBeenCalled();
    await click(checkbox('同时加入时间线'));
    await enter(field('时间线起点 · 秒'), '3.000');
    await enter(field('音频轨'), 'audio-track');
    await click(button('导入配音并加入时间线'));
    expect(api.importSpeechAudio).toHaveBeenCalledWith('speech-job', {
      trackId: 'audio-track',
      startFrame: 90,
      addToTimeline: true,
    });
    expect(onBootstrap).toHaveBeenCalledWith(speechBootstrap);
    expect(api.startSpeechJob).not.toHaveBeenCalled();
  });
});
describe('源词纠错与解绑', () => {
  it('纠错只提交接受词，保留稳定ID/源微秒/speaker并携带工程与修订号', async () => {
    const correct = vi.fn().mockResolvedValue({
      ...sourceTranscript,
      revision: 3,
      acceptedWords: [{ ...sourceTranscript.acceptedWords[0], text: '已纠正' }],
    });
    const detach = vi.fn();
    await render(
      <SourceTranscriptEditor
        project={speechProject}
        busy={false}
        onCorrect={correct}
        onDetach={detach}
      />,
    );
    await enter(field('文本'), '已纠正');
    await click(button('保存源词纠错'));
    expect(correct).toHaveBeenCalledWith({
      projectId: speechProject.id,
      transcriptId: sourceTranscript.id,
      transcriptRevision: 2,
      words: [{ ...sourceTranscript.acceptedWords[0], text: '已纠正' }],
    });
    expect(sourceTranscript.rawWords[0].text).toBe('原始识别');
    expect(button('保存源词纠错').disabled).toBe(true);
  });
  it('接受版本变化时保留草稿并禁止保存，重新读取由明确按钮触发', async () => {
    const correct = vi.fn();
    const detach = vi.fn();
    const element = (project = speechProject) => (
      <SourceTranscriptEditor
        project={project}
        busy={false}
        onCorrect={correct}
        onDetach={detach}
      />
    );
    await render(element());
    await enter(field('文本'), '未提交草稿');
    await render(
      element({ ...speechProject, transcripts: [{ ...sourceTranscript, revision: 3 }] }),
    );
    expect(field('文本').value).toBe('未提交草稿');
    expect(button('保存源词纠错').disabled).toBe(true);
    expect(document.body.textContent).toContain('当前纠错草稿保留');
    await click(button('重新读取接受版本'));
    expect(field('文本').value).toBe('已接受文本');
    expect(correct).not.toHaveBeenCalled();
  });
  it('解绑需要明确动作，桥请求绑定工程且保留字幕内容', async () => {
    const api = fakeSpeechAPI();
    const onBootstrap = vi.fn();
    await render(panel(api, onBootstrap));
    await click(button('解绑此字幕'));
    expect(api.detachCaption).not.toHaveBeenCalled();
    await click(button('确认解绑字幕'));
    expect(api.detachCaption).toHaveBeenCalledWith({
      projectId: speechProject.id,
      clipId: 'bound-caption',
    });
    expect(onBootstrap).toHaveBeenCalledWith(speechBootstrap);
  });
});

it('连接在设置删除后刷新有效选择，保留语音草稿并禁止隐式提交', async () => {
  const api = fakeSpeechAPI(),
    open = vi.fn();
  const view = (version: number) => (
    <SpeechPanel
      project={speechProject}
      onBootstrap={vi.fn()}
      api={api}
      connectionsVersion={version}
      onOpenConnections={open}
    />
  );
  await render(view(0));
  await click(button('配音'));
  await enter(field('配音文本'), '保留语音草稿');
  await click(button('管理连接'));
  expect(open).toHaveBeenCalledOnce();
  api.getConnections.mockResolvedValue([]);
  await render(view(1));
  expect(field('语音模型连接').value).toBe('');
  expect(field('配音文本').value).toBe('保留语音草稿');
  expect(api.startSpeechJob).not.toHaveBeenCalled();
});
