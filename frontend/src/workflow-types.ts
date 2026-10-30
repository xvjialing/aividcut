import type { Bootstrap, EditRequest } from './types';
export interface WorkflowParameter {
  id: string;
  name: string;
  type: 'integer' | 'number' | 'boolean' | 'string' | 'select' | 'asset' | 'track';
  description: string;
  required: boolean;
  min?: number;
  max?: number;
  step?: number;
  options?: Array<{ value: string; name: string }>;
  default?: unknown;
}
export interface WorkflowDefinition {
  id: string;
  name: string;
  description: string;
  version: string;
  builtin: boolean;
  available: boolean;
  reason?: string;
  parameters: WorkflowParameter[];
  defaults: Record<string, unknown>;
}
export interface WorkflowInput {
  scope?: 'selection' | 'project';
  workflowId: string;
  selection: string[];
  parameters: Record<string, unknown>;
}
export interface WorkflowPreview {
  audioFadeSummary?: {
    scope: string;
    mode: string;
    examined: number;
    changed: number;
    preserved: number;
    skippedMuted: number;
    skippedType: number;
    unchanged: number;
    scaled: number;
  };
  speechRippleSummary?: {
    cutCount: number;
    removedFrames: number;
    beforeFrames: number;
    afterFrames: number;
    affectedTracks: number;
    totalAffectedClips: number;
    changedOriginalClips: number;
    newClips: number;
  };
  rangeSummary?: {
    fromFrame: number;
    toFrame: number;
    beforeFrames: number;
    afterFrames: number;
    deleted: number;
    split: number;
    trimmed: number;
    moved: number;
    captionChanges: number;
    affectedTracks: number;
    totalAffectedClips: number;
  };
  id: string;
  workflowId: string;
  workflowVersion: string;
  projectId: string;
  projectRevision: number;
  createdAt: string;
  applied: boolean;
  request: EditRequest;
  summary: string;
  warnings: string[];
  affectedClipIds: string[];
  savedFrames: number;
  effects: Array<{
    kind: string;
    clipId?: string;
    trackId?: string;
    startFrame: number;
    endFrame: number;
    description: string;
  }>;
  evidence: Array<{
    kind: string;
    clipId?: string;
    assetId?: string;
    transcriptId?: string;
    sourceFingerprint?: string;
    wordIds?: string[];
    sourceStartUs: number;
    sourceEndUs: number;
    startFrame: number;
    endFrame: number;
    description: string;
  }>;
}
export interface WorkflowPreset {
  id: string;
  name: string;
  workflowId: string;
  workflowVersion: string;
  version: number;
  parameters: Record<string, unknown>;
  createdAt: string;
  updatedAt: string;
}
export interface WorkflowSave {
  id?: string;
  name: string;
  workflowId: string;
  parameters: Record<string, unknown>;
  baseVersion?: number;
}
export interface WorkflowAPI {
  getWorkflowCatalog(): Promise<WorkflowDefinition[]>;
  getWorkflowPresets(): Promise<WorkflowPreset[]>;
  saveWorkflowPreset(input: WorkflowSave): Promise<WorkflowPreset>;
  deleteWorkflowPreset(id: string, baseVersion: number): Promise<unknown>;
  previewWorkflow(input: WorkflowInput): Promise<WorkflowPreview>;
  applyWorkflowPreview(id: string): Promise<Bootstrap>;
}
