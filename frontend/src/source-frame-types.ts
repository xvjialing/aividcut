export interface SourceFrameEvidence {
  id: string;
  assetId: string;
  assetName: string;
  sourceFingerprint: string;
  requestedPositionUs: number;
  positionUs: number;
  imageSha256: string;
  width: number;
  height: number;
  profile: 'overview' | 'detail';
  recipeVersion: string;
}
export interface SourceFrameRequest {
  sourceEpoch: number;
  requestId: string;
  projectId: string;
  assetId: string;
  fingerprint: string;
  positionUs: number;
  profile: 'overview' | 'detail';
}
export interface SourceFrameAPI {
  read(input: SourceFrameRequest): Promise<{ evidence: SourceFrameEvidence; url: string }>;
  cancel(id: string): Promise<unknown>;
}
