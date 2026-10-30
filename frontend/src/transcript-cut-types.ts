import type { Bootstrap } from './types';
import type { WorkflowPreview } from './workflow-types';

export interface TranscriptCutInput {
  projectId: string;
  projectRevision: number;
  sourceEpoch: number;
  transcriptId: string;
  transcriptRevision: number;
  clipId: string;
  wordIds: string[];
  closeGap: boolean;
  rippleAll?: boolean;
}
export interface TranscriptWordSelection {
  projectId: string;
  projectRevision: number;
  transcriptId: string;
  transcriptRevision: number;
  clipId: string;
  assetId: string;
  wordIds: string[];
  text: string;
  sourceStartUs: number;
  sourceEndUs: number;
  startFrame: number;
  endFrame: number;
  warnings: string[];
}
export interface TranscriptCutAPI {
  locate(input: TranscriptCutInput): Promise<TranscriptWordSelection>;
  preview(input: TranscriptCutInput): Promise<WorkflowPreview>;
  apply(id: string): Promise<Bootstrap>;
}
