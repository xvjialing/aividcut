import type { Bootstrap, ProjectScope } from './types';
export interface RecoveryStatus {
  scope: ProjectScope;
  enabled: boolean;
  intervalSeconds: number;
  pending: number;
  inputDrafts?: number;
  startupPending: number;
  unreadable: number;
  protectedAt?: string;
  protectedRevision: number;
  error?: string;
}
export interface RecoveryRecord {
  sequences?: number;
  sequenceName?: string;
  formatVersion: number;
  id: string;
  projectId: string;
  name: string;
  projectPath: string;
  revision: number;
  updatedAt: string;
  assets: number;
  clips: number;
  sha256: string;
  beforeRestore: boolean;
}
export interface RecoveryPage {
  records: RecoveryRecord[];
  total: number;
  unreadable: number;
  offset: number;
  limit: number;
}
export interface RestoreRecoveryInput extends ProjectScope {
  id: string;
  sha256: string;
}
export interface RecoveryAPI {
  status: () => Promise<RecoveryStatus>;
  list: (input: { offset: number; limit: number }) => Promise<RecoveryPage>;
  checkpoint: () => Promise<RecoveryStatus>;
  restore: (input: RestoreRecoveryInput) => Promise<Bootstrap>;
}
