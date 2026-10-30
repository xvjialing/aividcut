import type { SourceFrameEvidence } from './source-frame-types';
export interface VisualNote {
  id: string;
  kind: 'observation' | 'inference' | 'suggestion';
  text: string;
  tags: string[];
  evidenceIds: string[];
}
export interface VisualAnalysis {
  id: string;
  assetId: string;
  sourceKind: 'image' | 'video';
  sourceFingerprint: string;
  sourceDurationUs: number;
  producer: 'codex' | 'claude' | 'external-mcp' | 'apple-vision' | 'windows-media-ocr';
  executionId?: string;
  methodVersion: string;
  coverage: 'sampled_frames';
  frames: SourceFrameEvidence[];
  notes: VisualNote[];
  acceptedAt: string;
  stale?: boolean;
}
export const visualNoteKind = {
  observation: '画面观察',
  inference: '未确认推断',
  suggestion: '剪辑建议',
};
export const analysisProducer = {
  codex: 'Codex CLI提交',
  claude: 'Claude Code提交',
  'external-mcp': '外部MCP提交',
  'apple-vision': '本机Apple Vision文字识别',
  'windows-media-ocr': '本机Windows系统文字识别',
};
