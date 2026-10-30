export interface WaveformRequest {
  requestId: string;
  projectId: string;
  assetId: string;
  fingerprint: string;
  index: number;
}
export interface WaveformTile {
  index: number;
  sourceStartUs: number;
  sampleRate: number;
  channels: number;
  bucketSamples: number;
  sampleCount: number;
  peaks: { min: number; max: number; samples: number }[];
}
export interface WaveformAPI {
  read(request: WaveformRequest): Promise<WaveformTile>;
  cancel(requestId: string): Promise<unknown>;
}
