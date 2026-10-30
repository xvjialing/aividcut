import type { Asset, Bootstrap } from './types';
export interface CaptureIntent {
  id: string;
  projectId: string;
  sourceEpoch: number;
  kind: 'timeline' | 'source';
  frame: number;
  assetId?: string;
  fingerprint?: string;
  positionUs?: number;
}
export interface FrameCaptureInput extends CaptureIntent {
  projectRevision: number;
}
export interface FrameCaptureOrigin {
  version: string;
  input: FrameCaptureInput;
  signature: string;
  projectHash: string;
  sources: Array<{ assetId: string; fingerprint: string }>;
  asset: Asset;
  renderer: string;
  approximate: boolean;
  applied: boolean;
}
export interface FrameCaptureResult {
  bootstrap: Bootstrap;
  assetId: string;
  imported: boolean;
  origin: FrameCaptureOrigin;
  duplicate: boolean;
}
export interface FrameCaptureAPI {
  capture(input: FrameCaptureInput): Promise<FrameCaptureResult>;
  cancel(input: { id: string; projectId: string; sourceEpoch: number }): Promise<unknown>;
}
