import type { VisualAnalysis } from '../src/visual-analysis-types';
export const visualAnalysis: VisualAnalysis = {
  id: 'prepared-action',
  assetId: 'visual-source',
  sourceKind: 'image',
  sourceFingerprint: 'sha256:fixture',
  sourceDurationUs: 0,
  producer: 'external-mcp',
  methodVersion: 'visual-notes-v1',
  coverage: 'sampled_frames',
  acceptedAt: '',
  frames: [
    {
      id: 'a'.repeat(64),
      assetId: 'visual-source',
      assetName: '画面源',
      sourceFingerprint: 'sha256:fixture',
      requestedPositionUs: 0,
      positionUs: 0,
      imageSha256: 'b'.repeat(64),
      width: 320,
      height: 180,
      profile: 'overview',
      recipeVersion: 'overview-jpeg-v1',
    },
  ],
  notes: [
    {
      id: 'visual-note',
      kind: 'inference',
      text: '可能是海边场景（DOM夹具）',
      tags: ['海边'],
      evidenceIds: ['a'.repeat(64)],
    },
  ],
};
