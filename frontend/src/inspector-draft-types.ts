import type { Bootstrap, ProjectScope } from './types';
export type DraftField = { key: string; raw: string; base: string | number };
export type InspectorDraft = {
  id: string;
  projectId: string;
  name: string;
  target: string;
  originalPath: string;
  baseRevision: number;
  updatedAt: string;
  baseSha256: string;
  sha256: string;
  fields: DraftField[];
};
export type DraftRef = ProjectScope & { id: string; sha256: string };
export type DraftReview = DraftRef & { values: Record<string, string>; recheckCurrent: boolean };
export type DraftPreview = {
  id: string;
  canApply: boolean;
  error?: string;
  rows: {
    key: string;
    label: string;
    raw: string;
    base: string | number;
    current: string | number;
    conflict: boolean;
    error?: string;
  }[];
};
export type InspectorDraftAPI = {
  save(
    input: ProjectScope & { target: string; fields: DraftField[] },
  ): Promise<InspectorDraft | null>;
  list(): Promise<{ records: InspectorDraft[]; unreadable: number }>;
  remove(input: DraftRef): Promise<void>;
  preview(input: DraftReview): Promise<DraftPreview>;
  apply(id: string): Promise<Bootstrap>;
  restoreBase(input: DraftRef): Promise<Bootstrap>;
};
