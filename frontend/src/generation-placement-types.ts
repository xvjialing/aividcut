import type { Asset, Bootstrap, Clip, Track } from './types';
export interface GenerationPlacementInput {
  jobId: string;
  candidateId: string;
  projectId: string;
  projectRevision: number;
  sourceEpoch: number;
  startFrame: number;
  durationFrames: number;
  autoDuration: boolean;
  sourceInUs: number;
  trackId: string;
  audioMode: 'muted' | 'embedded';
}
export interface GenerationPlacementPreview {
  id: string;
  projectId: string;
  projectRevision: number;
  sourceEpoch: number;
  createdAt: string;
  asset: Asset;
  clip: Clip;
  track: Track;
  importRequired: boolean;
  newTrack: boolean;
  captionCount: number;
  audioMode: 'muted' | 'embedded';
  summary: string;
  warnings: string[];
  applied: boolean;
}
export interface GenerationPlacementAPI {
  preview(input: GenerationPlacementInput): Promise<GenerationPlacementPreview>;
  apply(id: string): Promise<Bootstrap>;
}
