export interface ThumbnailRequest {
  sourceEpoch: number;
  requestId: string;
  projectId: string;
  assetId: string;
  fingerprint: string;
  positionUs: number;
}
export interface Thumbnail {
  assetId: string;
  positionUs: number;
  url: string;
  width: number;
  height: number;
}
export interface ThumbnailAPI {
  read(request: ThumbnailRequest): Promise<Thumbnail>;
  cancel(requestId: string): Promise<unknown>;
}
