import type { Bootstrap, ProjectScope } from './types';

export interface BatchRelinkInventory {
  missingCount: number;
  availableCount: number;
  suggestedOldRoot: string;
}
export interface BatchRelinkPreview {
  id: string;
  oldRoot: string;
  newRoot: string;
  matched: number;
  outside: number;
  available: number;
  readBytes: number;
  entries: {
    assetId: string;
    name: string;
    kind: string;
    originalPath: string;
    candidatePath: string;
    status: 'matched' | 'missing' | 'unproven' | 'different' | 'unreadable';
    reason: string;
  }[];
}
export interface BatchRelinkRef extends ProjectScope {
  id: string;
}
export interface BatchRelinkInput extends BatchRelinkRef {
  oldRoot: string;
  newRoot: string;
}
export interface BatchRelinkAPI {
  inspect: (input: ProjectScope) => Promise<BatchRelinkInventory>;
  preview: (input: BatchRelinkInput) => Promise<BatchRelinkPreview>;
  apply: (input: BatchRelinkRef) => Promise<Bootstrap>;
  cancel: (input: BatchRelinkRef) => Promise<void>;
  chooseDirectory: () => Promise<string>;
}
