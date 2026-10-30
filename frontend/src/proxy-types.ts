import type { Job } from './types';
export type ProxyProfile = '' | '720p' | '540p';
export interface PreviewProxyInput {
  clearAll?: boolean;
  projectId: string;
  sourceEpoch: number;
  assetId: string;
  fingerprint: string;
  profile: Exclude<ProxyProfile, ''>;
}
export interface PreviewProxyView {
  cacheVideos?: number;
  cacheBytes?: number;
  status: string;
  reason?: string;
  projectId: string;
  sourceEpoch: number;
  assetId: string;
  profile: string;
  job?: Job;
  proof?: {
    originUs: number;
    frames: number;
    width: number;
    height: number;
    bytes: number;
    timelineSha256: string;
    proxyFingerprint: string;
  };
}
export interface PreviewProxyAPI {
  get(input: PreviewProxyInput): Promise<PreviewProxyView>;
  start(input: PreviewProxyInput): Promise<PreviewProxyView>;
  clear(input: PreviewProxyInput): Promise<unknown>;
  cancel(id: string): Promise<unknown>;
}
