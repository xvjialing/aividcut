import type { Job } from './types';
export interface ShotIndexInput {
  projectId: string;
  sourceEpoch: number;
  assetId: string;
  fingerprint: string;
  threshold: number;
  minDurationUs: number;
  force?: boolean;
}
export interface Shot {
  id: string;
  startUs: number;
  endUs: number;
  sampleUs: number;
}
export interface ShotIndex {
  version: string;
  key: string;
  sourceFingerprint: string;
  sourceDurationUs: number;
  detectorVersion: string;
  config: { threshold: number; minDurationUs: number };
  coverageStartUs: number;
  coverageEndUs: number;
  decodedFrames: number;
  rawCutCount: number;
  shots: Shot[];
}
export interface ShotIndexView {
  status: 'missing' | 'cached' | 'running' | 'ready' | 'invalid' | 'failed' | 'cancelled';
  reason?: string;
  projectId: string;
  sourceEpoch: number;
  assetId: string;
  key?: string;
  job?: Job;
  index?: ShotIndex;
  cached: boolean;
}
export interface ShotIndexAPI {
  get(input: ShotIndexInput): Promise<ShotIndexView>;
  start(input: ShotIndexInput): Promise<ShotIndexView>;
  cancel(id: string): Promise<unknown>;
}
