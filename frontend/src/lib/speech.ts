import type { Asset } from '../types';
import type { ConnectionProfile } from '../generation-types';
import type { SpeechCatalog, SpeechDraftRequest, SpeechMode, SpeechStatus } from '../speech-types';
export const speechStatusNames: Record<SpeechStatus, string> = {
  queued: '排队中',
  preparing: '准备音轨',
  submitting: '正在提交',
  running: '处理中',
  saving: '保存结果',
  succeeded: '结果已保存',
  failed: '失败',
  unknown: '提交结果未知',
  cancelling: '停止本地等待中',
  cancelled: '已停止本地等待',
};

export const speechModeNames: Record<SpeechMode, string> = {
  transcribe: '源素材转写',
  synthesize: '文本配音',
  clone: '创建自定义音色',
};
export function speechOutputFormatLabel(value: string) {
  return value === 'mp3_44100_128' ? 'MP3 · 44.1kHz · 128kbps' : value;
}
export function speechSources(assets: Asset[], clone = false) {
  return assets.filter(
    (asset) =>
      !asset.missing &&
      (asset.kind === 'audio' || (!clone && asset.kind === 'video' && asset.hasAudio)),
  );
}
export function speechModelOptions(catalog: SpeechCatalog) {
  return [...new Set([...(catalog.asrModels || []), ...(catalog.ttsModels || [])])].map(
    (modelId) => ({ providerId: catalog.providerId, modelId }),
  );
}
export function speechConnections(
  profiles: ConnectionProfile[],
  catalog: SpeechCatalog,
  mode: SpeechMode,
) {
  const models =
    mode === 'transcribe'
      ? catalog.asrModels
      : mode === 'synthesize'
        ? catalog.ttsModels
        : [...catalog.asrModels, ...catalog.ttsModels];
  return profiles.filter(
    (profile) => profile.providerId === catalog.providerId && models.includes(profile.modelId),
  );
}
export function speechUnavailable(catalog: SpeechCatalog, mode: SpeechMode): string {
  const feature =
    mode === 'transcribe'
      ? catalog.transcription
      : mode === 'synthesize'
        ? catalog.speechSynthesis
        : catalog.instantCloning;
  if (!feature.supported) return feature.reason || '当前适配器未开放此能力。';
  if (mode === 'transcribe' && !catalog.audioPreparation.available)
    return catalog.audioPreparation.reason || '本机音轨准备能力未就绪。';
  if (mode === 'synthesize' && !catalog.audioImport.available)
    return catalog.audioImport.reason || '本机音频落盘与导入能力未就绪，暂不发起配音。';
  return '';
}
export function validateSpeechRequest(
  request: SpeechDraftRequest,
  catalog: SpeechCatalog,
  connection: ConnectionProfile | undefined,
  assets: Asset[],
): Record<string, string> {
  const errors: Record<string, string> = {};
  if (
    !connection?.configured ||
    !speechConnections(connection ? [connection] : [], catalog, request.mode).length
  )
    errors.connection = '请选择当前任务可用且已配置的连接。';
  const unavailable = speechUnavailable(catalog, request.mode);
  if (unavailable) errors.connection = unavailable;
  if (request.mode === 'transcribe') {
    const input = request.transcribe;
    if (!input || !speechSources(assets).some((asset) => asset.id === input.assetId))
      errors.source = '请选择已登记且有音轨的音视频素材。';
    if (input?.language && !/^[a-z]{2,3}$/i.test(input.language))
      errors.language = '语言使用 2–3 位字母代码，或留空自动识别。';
    if (input && !catalog.timestampGranularities.includes(input.granularity || 'word'))
      errors.granularity = '当前时间戳粒度不可用。';
  } else if (request.mode === 'synthesize') {
    const input = request.synthesize;
    if (input?.withTiming && !catalog.speechTiming?.supported)
      errors.withTiming = catalog.speechTiming?.reason || '此服务尚未提供配音时间戳。';
    if (!input?.voiceId) errors.voice = '请先查询并选择一个音色。';
    if (!input?.text.trim() || [...(input?.text || '')].length > catalog.maxTextCharacters)
      errors.text = `请填写 1–${catalog.maxTextCharacters} 个字符的配音文本。`;
    if (input && !catalog.outputFormats.includes(input.outputFormat || ''))
      errors.outputFormat = '请选择已声明的音频格式。';
  } else {
    const input = request.clone;
    if (!input?.name.trim() || [...input.name].length > 100)
      errors.name = '音色名称需要 1–100 个字符。';
    if ([...(input?.description || '')].length > 1000)
      errors.description = '描述不能超过 1000 个字符。';
    const sources = speechSources(assets, true);
    if (
      !input?.assetIds.length ||
      input.assetIds.length > catalog.maxCloneFiles ||
      input.assetIds.some((id) => !sources.some((asset) => asset.id === id))
    )
      errors.samples = `请选择 1–${catalog.maxCloneFiles} 个已登记音频样本。`;
    if (
      !input?.consent.confirmed ||
      !['self', 'authorized'].includes(input.consent.authority) ||
      !input.consent.subject.trim() ||
      [...input.consent.subject].length > 200
    )
      errors.consent = '请填写声音主体，并明确本人或获授权声明。';
  }
  return errors;
}
