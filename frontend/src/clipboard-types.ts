import type { Bootstrap } from './types';
export interface ClipboardStatus {
  projectId: string;
  sourceEpoch: number;
  version: number;
  available: boolean;
  id?: string;
  clipCount: number;
  detachedCaptions: number;
  reason?: string;
}
export interface ClipboardSelectionInput {
  projectId: string;
  sourceEpoch: number;
  baseRevision: number;
  clipIds: string[];
  includeLinked?: boolean;
}
export interface ClipboardCutInput extends ClipboardSelectionInput {
  idempotencyKey: string;
}
export interface ClipboardPasteInput {
  projectId: string;
  sourceEpoch: number;
  baseRevision: number;
  clipboardId: string;
  idempotencyKey: string;
  atFrame: number;
  trackDelta?: number;
}
export interface ClipboardResult {
  bootstrap: Bootstrap;
  clipboard: ClipboardStatus;
  selectedClipIds?: string[];
}
