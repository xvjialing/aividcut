import type { ProjectScope } from './types';
export type StorageKind = 'version' | 'recovery';
export type StorageView = 'active' | 'trash';
export type StorageAction = 'trash' | 'restore' | 'purge';
export interface StorageItem {
  id: string;
  name: string;
  projectId?: string;
  revision: number;
  time: string;
  bytes: number;
  readable: boolean;
  canPurge: boolean;
  reason?: string;
  locked: boolean;
  token: string;
}
export interface StoragePage {
  items: StorageItem[];
  total: number;
  totalBytes: number;
  offset: number;
  limit: number;
}
export interface SnapshotStorageAPI {
  list: (
    input: ProjectScope & { kind: StorageKind; view: StorageView; offset: number; limit: number },
  ) => Promise<StoragePage>;
  change: (
    input: ProjectScope & { token: string; action: StorageAction; confirmPermanent: boolean },
  ) => Promise<{ kind: StorageKind; id: string; action: StorageAction; alreadyApplied: boolean }>;
}
