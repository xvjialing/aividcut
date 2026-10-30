import type { Bootstrap } from './types';

export interface ConnectionProfile {
  id: string;
  providerId: string;
  name: string;
  modelId: string;
  configured: boolean;
  createdAt: string;
  updatedAt: string;
  warning?: string;
}
export interface SaveConnectionInput {
  id?: string;
  providerId: string;
  name: string;
  modelId: string;
  apiKey?: string;
}
export interface GenerationCapability {
  providerId: string;
  family: string;
  modelId: string;
  mediaKind: string;
  modes: string[];
  sizes?: string[];
  defaultSize?: string;
  modeAspectRatios?: Record<string, string[]>;
  requiresFirstFrame?: boolean;
  supportsAudioGeneration?: boolean;
  defaultGenerateAudio?: boolean;
  aspectRatios?: string[];
  qualities?: string[];
  outputFormats?: string[];
  maxReferences: number;
  maxRequestBytes?: number;
  maxCount: number;
  minDurationSeconds?: number;
  maxDurationSeconds?: number;
  async: boolean;
  supportsCancel: boolean;
  needsModelConfiguration: boolean;
  accountVerified: boolean;
  note: string;
  sourceUrl: string;
}
export interface GenerationReference {
  assetId: string;
  role: string;
}
export interface GenerationRequest {
  id: string;
  providerId: string;
  modelId: string;
  mode: string;
  prompt: string;
  size?: string;
  aspectRatio?: string;
  durationSeconds?: number;
  generateAudio?: boolean;
  quality?: string;
  outputFormat?: string;
  background?: string;
  count?: number;
  references?: GenerationReference[];
  parentRecipeId?: string;
}
export interface SubmitGenerationInput {
  connectionId: string;
  request: GenerationRequest;
  consent: boolean;
  estimate?: GenerationEstimateInput;
  context?: { projectId: string; revision: number; sourceEpoch?: number };
}
export interface GenerationOutputAsset {
  id: string;
  kind: string;
  name: string;
  path: string;
  url?: string;
  mime: string;
  sha256: string;
  bytes: number;
  recipeId: string;
}
export interface GenerationEstimateInput {
  currency: 'CNY' | 'USD';
  unit: 'request' | 'image' | 'video_second';
  unitPrice: string;
}
export interface GenerationCostEstimate extends GenerationEstimateInput {
  basis: 'manual-unit-v1';
  quantity: number;
  amount: string;
}
export interface GenerationJob {
  id: string;
  connectionId: string;
  projectId: string;
  projectRevision: number;
  request: GenerationRequest;
  status:
    | 'queued'
    | 'submitting'
    | 'running'
    | 'waiting'
    | 'importing'
    | 'succeeded'
    | 'failed'
    | 'unknown'
    | 'cancelled';
  message: string;
  remoteId?: string;
  createdAt: string;
  updatedAt: string;
  canResume: boolean;
  assets: GenerationOutputAsset[];
  imported: boolean;
  importedAssetIds?: string[];
  acknowledged?: boolean;
  estimate?: GenerationCostEstimate;
}
export interface ConnectionAPI {
  getConnections(): Promise<ConnectionProfile[]>;
  saveConnection(input: SaveConnectionInput): Promise<ConnectionProfile>;
  deleteConnection(id: string): Promise<{ warning?: string }>;
  getConnectionHealth(): Promise<{ pendingCleanup: number; storage: string }>;
  cleanupCredentials(): Promise<{ pendingCleanup: number }>;
}
export interface GenerationAPI extends ConnectionAPI {
  getGenerationCatalog(): Promise<GenerationCapability[]>;
  submitGeneration(input: SubmitGenerationInput): Promise<GenerationJob>;
  listGenerationJobs(): Promise<GenerationJob[]>;
  syncGenerationJobs(input: { fingerprint: string }): Promise<GenerationSync>;
  listArchivedGenerationJobs(input: {
    offset: number;
    limit: number;
  }): Promise<GenerationArchivePage>;
  archiveGenerationJob(id: string): Promise<GenerationArchiveRecord>;
  restoreArchivedGenerationJob(id: string): Promise<GenerationJob>;
  stopGenerationWaiting(id: string): Promise<GenerationJob>;
  resumeGeneration(id: string): Promise<GenerationJob>;
  importGenerationResult(id: string): Promise<Bootstrap>;
  importGenerationAssets(id: string, assetIds: string[]): Promise<Bootstrap>;
  acknowledgeGenerationUnknown(id: string): Promise<GenerationJob>;
}
export type GenerationSync =
  | { fingerprint: string; changed: false }
  | { fingerprint: string; changed: true; jobs: GenerationJob[] };

export interface GenerationArchiveRecord {
  id: string;
  archivedAt: string;
  job?: GenerationJob;
  canRestore: boolean;
  issue?: string;
}
export interface GenerationArchivePage {
  items: GenerationArchiveRecord[];
  total: number;
  active: number;
  offset: number;
  limit: number;
}
