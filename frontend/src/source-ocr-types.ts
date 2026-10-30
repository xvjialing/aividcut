import type { Bootstrap } from './types';
import type { SourceFrameRequest, SourceFrameEvidence } from './source-frame-types';
export interface SourceOCRDraft {
  id: string;
  projectId: string;
  projectRevision: number;
  sourceEpoch: number;
  frame: { evidence: SourceFrameEvidence; url: string };
  result: {
    version: number;
    engine: string;
    revision: number;
    languages: string[];
    lines: { text: string; confidence: number | null }[];
    truncated: boolean;
  };
}
export interface SourceOCRAPI {
  capability(): Promise<{ available: boolean; engine: string; reason: string }>;
  prepare(
    input: Omit<SourceFrameRequest, 'profile'> & { projectRevision: number },
  ): Promise<SourceOCRDraft>;
  accept(input: {
    id: string;
    projectId: string;
    projectRevision: number;
    sourceEpoch: number;
    lines: string[];
  }): Promise<Bootstrap>;
  cancel(id: string): Promise<unknown>;
}
