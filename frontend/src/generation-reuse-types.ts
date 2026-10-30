import type { GenerationRequest } from './generation-types';
import type { Asset } from './types';
export interface GenerationReuseInput {
  jobId: string;
  candidateId?: string;
  projectId: string;
  sourceEpoch: number;
}
export interface GenerationReusePlan {
  input: GenerationReuseInput;
  projectRevision: number;
  connectionId: string;
  request: GenerationRequest;
  referenceFingerprints: Record<string, string>;
  warnings: string[];
  candidate?: Asset;
  needsImport: boolean;
}
export interface GenerationReuseAPI {
  prepare(input: GenerationReuseInput): Promise<GenerationReusePlan>;
}
