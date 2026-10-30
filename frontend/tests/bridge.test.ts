import { installDesktop } from './desktop-fixture';
vi.mock('../src/lib/desktop', async () => (await import('./desktop-fixture')).desktopModule);
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  agentBridge,
  bridge,
  generationBridge,
  invoke,
  speechBridge,
  projectVersionsAPI,
  projectCollectAPI,
  batchRelinkAPI,
  sequenceAPI,
  recoveryAPI,
  snapshotStorageAPI,
} from '../src/lib/bridge';

beforeEach(() => {
  vi.stubGlobal('window', {});
  vi.stubEnv('DEV', true);
});
afterEach(() => {
  vi.unstubAllGlobals();
  vi.unstubAllEnvs();
});

describe('统一桌面桥接', () => {
  it('序列管理传递明确动作与原工程范围，不自动确认删除', async () => {
    const change = vi.fn().mockResolvedValue({});
    installDesktop({ ChangeSequence: change });
    const input = {
      projectId: 'p',
      projectRevision: 2,
      sourceEpoch: 3,
      action: 'switch' as const,
      sequenceId: 's',
      idempotencyKey: 'key',
    };
    await sequenceAPI.change(input);
    expect(change).toHaveBeenCalledWith(input);
    expect(change.mock.calls[0][0].confirmDelete).toBeUndefined();
  });
  it('批量修复保留工程范围，核对不调用应用，取消只发送原请求身份', async () => {
    const inspect = vi.fn().mockResolvedValue({ missingCount: 2 }),
      preview = vi.fn().mockResolvedValue({ matched: 2 }),
      apply = vi.fn().mockResolvedValue({}),
      cancel = vi.fn().mockResolvedValue(undefined);
    installDesktop({
      InspectBatchRelink: inspect,
      PreviewBatchRelink: preview,
      ApplyBatchRelink: apply,
      CancelBatchRelink: cancel,
    });
    const scope = { projectId: 'p', projectRevision: 3, sourceEpoch: 4 };
    await batchRelinkAPI.inspect(scope);
    await batchRelinkAPI.preview({ ...scope, id: 'request', oldRoot: '/old', newRoot: '/new' });
    expect(apply).not.toHaveBeenCalled();
    await batchRelinkAPI.cancel({ ...scope, id: 'request' });
    expect(cancel).toHaveBeenCalledWith({ ...scope, id: 'request' });
    await batchRelinkAPI.apply({ ...scope, id: 'request' });
    expect(apply).toHaveBeenCalledWith({ ...scope, id: 'request' });
  });
  it('资料管理保留类型/位置/范围，变更只发送不透明身份和明确永久确认', async () => {
    const list = vi.fn().mockResolvedValue({ items: [] }),
      change = vi.fn().mockResolvedValue({ alreadyApplied: false });
    installDesktop({ ListSnapshotStorage: list, ChangeSnapshotStorage: change });
    const scope = { projectId: 'project', projectRevision: 2, sourceEpoch: 3 };
    await snapshotStorageAPI.list({
      ...scope,
      kind: 'recovery',
      view: 'trash',
      offset: 0,
      limit: 20,
    });
    expect(change).not.toHaveBeenCalled();
    await snapshotStorageAPI.change({
      ...scope,
      token: 'opaque',
      action: 'purge',
      confirmPermanent: true,
    });
    expect(change).toHaveBeenCalledWith({
      ...scope,
      token: 'opaque',
      action: 'purge',
      confirmPermanent: true,
    });
  });
  it('恢复状态/列表不写入，保护与恢复使用明确独立方法和范围', async () => {
    const status = vi.fn().mockResolvedValue({ enabled: true }),
      list = vi.fn().mockResolvedValue({ records: [] }),
      checkpoint = vi.fn().mockResolvedValue({ enabled: true }),
      restore = vi.fn().mockResolvedValue({ project: {} });
    installDesktop({
      GetRecoveryStatus: status,
      ListProjectRecoveries: list,
      CheckpointRecovery: checkpoint,
      RestoreProjectRecovery: restore,
    });
    const input = {
      projectId: 'current',
      projectRevision: 3,
      sourceEpoch: 4,
      id: 'recovery-a',
      sha256: 'proof',
    };
    await recoveryAPI.status();
    await recoveryAPI.list({ offset: 0, limit: 20 });
    expect(checkpoint).not.toHaveBeenCalled();
    expect(restore).not.toHaveBeenCalled();
    await recoveryAPI.checkpoint();
    await recoveryAPI.restore(input);
    expect(list).toHaveBeenCalledWith({ offset: 0, limit: 20 });
    expect(restore).toHaveBeenCalledWith(input);
  });
  it('工程收集的核对、目录选择和任务启动保持独立真实调用', async () => {
    const inspect = vi.fn().mockResolvedValue({ assetCount: 1 }),
      choose = vi.fn().mockResolvedValue('/backup'),
      start = vi.fn().mockResolvedValue({ id: 'collect' });
    installDesktop({
      InspectProjectCollection: inspect,
      ChooseProjectCollectionDirectory: choose,
      CollectProject: start,
    });
    const scope = { projectId: 'project', projectRevision: 3, sourceEpoch: 4 };
    await projectCollectAPI.inspect(scope);
    await projectCollectAPI.chooseDirectory();
    await projectCollectAPI.start({ ...scope, parentDirectory: '/backup', folderName: 'copy' });
    expect(inspect).toHaveBeenCalledWith(scope);
    expect(choose).toHaveBeenCalledWith();
    expect(start).toHaveBeenCalledWith({
      ...scope,
      parentDirectory: '/backup',
      folderName: 'copy',
    });
  });
  it('工程版本使用三项独立桌面接口并原样传入实例和内容证明', async () => {
    const list = vi.fn().mockResolvedValue({ versions: [] }),
      create = vi.fn().mockResolvedValue({ id: 'version-1' }),
      restore = vi.fn().mockResolvedValue({ bootstrap: {} });
    installDesktop({
      ListProjectVersions: list,
      CreateProjectVersion: create,
      RestoreProjectVersion: restore,
    });
    const scope = { projectId: 'project', projectRevision: 5, sourceEpoch: 8 };
    await projectVersionsAPI.list({ ...scope, offset: 0, limit: 20 });
    await projectVersionsAPI.create({ ...scope, name: '粗剪' });
    await projectVersionsAPI.restore({ ...scope, id: 'version-1', sha256: 'proof' });
    expect(list).toHaveBeenCalledWith({ ...scope, offset: 0, limit: 20 });
    expect(create).toHaveBeenCalledWith({ ...scope, name: '粗剪' });
    expect(restore).toHaveBeenCalledWith({ ...scope, id: 'version-1', sha256: 'proof' });
  });
  it('已有桌面桥缺少新增方法时，不把请求误投开发HTTP工程', async () => {
    installDesktop({});
    const fetch = vi.fn();
    vi.stubGlobal('fetch', fetch);
    await expect(bridge.relinkAsset('asset-stable')).rejects.toThrow('桌面接口尚未就绪');
    expect(fetch).not.toHaveBeenCalled();
  });
  it('浏览器重新定位传递稳定素材ID和新路径，不走重新导入', async () => {
    const fetch = vi.fn().mockResolvedValue({
      ok: true,
      json: async () => ({ result: { project: { id: 'unchanged' } } }),
    });
    vi.stubGlobal('fetch', fetch);
    await bridge.relinkAssetPath('asset-stable', '/新位置/镜头.mp4');
    expect(fetch).toHaveBeenCalledWith(
      '/api/RelinkAssetPath',
      expect.objectContaining({
        body: JSON.stringify({ args: ['asset-stable', '/新位置/镜头.mp4'] }),
      }),
    );
  });
  it('原生生成接口缺失时明确失败，不转向其他HTTP工程或收费服务', async () => {
    installDesktop({ GetBootstrap: vi.fn() });
    const fetch = vi.fn();
    vi.stubGlobal('fetch', fetch);
    await expect(generationBridge.getGenerationCatalog()).rejects.toThrow('桌面接口尚未就绪');
    expect(fetch).not.toHaveBeenCalled();
  });
  it('候选导入经同一开发桥传递任务与明确素材ID，响应仍由服务提供', async () => {
    const snapshot = { project: { id: 'target-project' }, dirty: true };
    const fetch = vi.fn().mockResolvedValue({
      ok: true,
      json: async () => ({ result: snapshot }),
    });
    vi.stubGlobal('fetch', fetch);
    expect(await generationBridge.importGenerationAssets('job-1', ['candidate-b'])).toEqual(
      snapshot,
    );
    expect(fetch).toHaveBeenCalledWith(
      '/api/ImportGenerationAssets',
      expect.objectContaining({ body: JSON.stringify({ args: ['job-1', ['candidate-b']] }) }),
    );
  });
  it('已有原生桥缺少Agent接口时不降级到其他工程，不会间接启动CLI', async () => {
    installDesktop({ GetBootstrap: vi.fn() });
    const fetch = vi.fn();
    vi.stubGlobal('fetch', fetch);
    await expect(agentBridge.getAgentStatus()).rejects.toThrow('桌面接口尚未就绪');
    expect(fetch).not.toHaveBeenCalled();
  });
  it('纠错和解绑均携带原工程作用域，不仅传片段或转写ID', async () => {
    const fetch = vi.fn().mockResolvedValue({
      ok: true,
      json: async () => ({ result: { project: { id: 'original' } } }),
    });
    vi.stubGlobal('fetch', fetch);
    const correction = {
      projectId: 'original',
      transcriptId: 'transcript',
      transcriptRevision: 2,
      words: [{ id: 'word', startUs: 1, endUs: 1000, text: '词' }],
    };
    await speechBridge.correctSourceTranscript(correction);
    expect(fetch).toHaveBeenLastCalledWith(
      '/api/CorrectSourceTranscript',
      expect.objectContaining({ body: JSON.stringify({ args: [correction] }) }),
    );
    await speechBridge.detachCaption({ projectId: 'original', clipId: 'caption' });
    expect(fetch).toHaveBeenLastCalledWith(
      '/api/DetachCaption',
      expect.objectContaining({
        body: JSON.stringify({ args: [{ projectId: 'original', clipId: 'caption' }] }),
      }),
    );
  });
  it('字幕分句预览携带原工程/修订与显式新轨，应用只发送后端预览ID', async () => {
    const fetch = vi
      .fn()
      .mockResolvedValue({ ok: true, json: async () => ({ result: { id: 'layout-preview' } }) });
    vi.stubGlobal('fetch', fetch);
    const input = {
      projectId: 'project',
      projectRevision: 4,
      transcriptId: 'tts-source',
      transcriptRevision: 2,
      trackId: '',
      createNewTrack: true,
      layout: {
        mode: 'readable' as const,
        maxCharsPerLine: 18,
        maxLines: 2,
        maxDurationUs: 4000000,
        pauseThresholdUs: 700000,
      },
    };
    await speechBridge.previewCaptionLayout(input);
    expect(fetch).toHaveBeenLastCalledWith(
      '/api/PreviewCaptionLayout',
      expect.objectContaining({ body: JSON.stringify({ args: [input] }) }),
    );
    await speechBridge.applyCaptionLayout('layout-preview');
    expect(fetch).toHaveBeenLastCalledWith(
      '/api/ApplyCaptionLayout',
      expect.objectContaining({ body: JSON.stringify({ args: ['layout-preview'] }) }),
    );
  });
  it('Agent要求作为结构化数据传入本地桥，不拼成命令', async () => {
    const start = vi.fn().mockResolvedValue({ id: 'job' });
    const fetch = vi.fn();
    installDesktop({ StartAgentPlan: start });
    vi.stubGlobal('fetch', fetch);
    const input = {
      id: 'request-id',
      provider: 'codex' as const,
      instruction: '将标题移动；$(fixture-only)',
      workflow: 'manual-plan',
      selection: ['clip-id'],
      consent: true,
    };
    expect(await agentBridge.startAgentPlan(input)).toEqual({ id: 'job' });
    expect(start).toHaveBeenCalledWith(input);
    expect(fetch).not.toHaveBeenCalled();
  });
  it('字幕导入导出保持实际路径和明确轨道参数', async () => {
    const fetch = vi
      .fn()
      .mockResolvedValue({ ok: true, json: async () => ({ result: '/字幕.srt' }) });
    vi.stubGlobal('fetch', fetch);
    expect(await bridge.exportCaptionsTo('/字幕.srt')).toBe('/字幕.srt');
    expect(fetch).toHaveBeenLastCalledWith(
      '/api/ExportCaptionsTo',
      expect.objectContaining({ body: JSON.stringify({ args: ['/字幕.srt', ''] }) }),
    );
    await bridge.importCaptions('/原字幕.srt', 'caption-1');
    expect(fetch).toHaveBeenLastCalledWith(
      '/api/ImportCaptions',
      expect.objectContaining({ body: JSON.stringify({ args: ['/原字幕.srt', 'caption-1'] }) }),
    );
  });
  it('优先使用Wails方法，不把原生编辑请求重复发送HTTP', async () => {
    const method = vi.fn().mockResolvedValue({ revision: 4 });
    const fetch = vi.fn();
    installDesktop({ ApplyEdit: method });
    vi.stubGlobal('fetch', fetch);
    const request = { baseRevision: 3 };
    expect(await invoke('ApplyEdit', request)).toEqual({ revision: 4 });
    expect(method).toHaveBeenCalledWith(request);
    expect(fetch).not.toHaveBeenCalled();
  });
  it('开发HTTP保持方法、参数和真实结果，不创建本地假工程', async () => {
    const fetch = vi.fn().mockResolvedValue({
      ok: true,
      json: async () => ({ result: { project: { id: 'real-project' } } }),
    });
    vi.stubGlobal('fetch', fetch);
    expect(await invoke('OpenProjectPath', '/project.aividcut')).toEqual({
      project: { id: 'real-project' },
    });
    expect(fetch).toHaveBeenCalledWith(
      '/api/OpenProjectPath',
      expect.objectContaining({
        method: 'POST',
        body: JSON.stringify({ args: ['/project.aividcut'] }),
      }),
    );
  });
  it('原生失败保留错误，不回退HTTP重复非幂等操作', async () => {
    installDesktop({ ImportMedia: vi.fn().mockRejectedValue(new Error('文件不可读取')) });
    const fetch = vi.fn();
    vi.stubGlobal('fetch', fetch);
    await expect(invoke('ImportMedia')).rejects.toThrow('文件不可读取');
    expect(fetch).not.toHaveBeenCalled();
  });
  it('拒绝不包含结果的服务响应，失败信息用于恢复', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ ok: true, json: async () => ({}) }));
    await expect(invoke('GetBootstrap')).rejects.toThrow('本地服务未返回有效结果');
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue({
        ok: false,
        json: async () => ({ error: { message: '工程版本已改变' } }),
      }),
    );
    await expect(invoke('ApplyEdit', {})).rejects.toThrow('工程版本已改变');
  });
  it('正式版没有Wails桥时停止，不使用开发HTTP服务', async () => {
    vi.stubEnv('DEV', false);
    const fetch = vi.fn();
    vi.stubGlobal('fetch', fetch);
    await expect(invoke('NewProject', '影片')).rejects.toThrow('桌面连接尚未就绪');
    expect(fetch).not.toHaveBeenCalled();
  });
});
