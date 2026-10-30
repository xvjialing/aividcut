import type { Asset, Bootstrap, Clip } from './types';
export interface GenerationReplacementInput {
  jobId: string;
  candidateId: string;
  clipId: string;
  projectId: string;
  projectRevision: number;
  sourceInUs: number;
  audioMode: 'keep_original' | 'use_candidate' | 'mute';
  lutMode: 'keep' | 'clear';
  speedMode: 'preserve' | 'normal';
}
export interface GenerationReplacementPreview {
  id: string;
  projectId: string;
  projectRevision: number;
  jobId: string;
  candidateId: string;
  clipId: string;
  createdAt: string;
  applied: boolean;
  summary: string;
  effects: Array<{
    kind: string;
    clipId?: string;
    trackId?: string;
    assetId?: string;
    description: string;
  }>;
  warnings: string[];
  affectedClipIds: string[];
  originalClip: Clip;
  originalAsset: Asset;
  candidateAsset: Asset;
  replacementClip: Clip;
  audioMode: GenerationReplacementInput['audioMode'];
  lutMode: GenerationReplacementInput['lutMode'];
  speedMode: GenerationReplacementInput['speedMode'];
}
export interface ReplacementAPI {
  previewGenerationReplacement(
    input: GenerationReplacementInput,
  ): Promise<GenerationReplacementPreview>;
  applyGenerationReplacement(previewId: string): Promise<Bootstrap>;
}
