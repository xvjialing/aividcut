import type { Bootstrap, ProjectScope } from './types';

export type ProjectVersionScope = ProjectScope;
export interface ProjectVersion {
  sequences?: number;
  sequenceName?: string;
  id: string;
  projectId: string;
  name: string;
  createdAt: string;
  revision: number;
  assets: number;
  tracks: number;
  clips: number;
  sha256: string;
  beforeRestore: boolean;
}
export interface ProjectVersionPage {
  versions: ProjectVersion[];
  total: number;
  unreadable: number;
  offset: number;
  limit: number;
}
export interface RestoreProjectVersionInput extends ProjectVersionScope {
  id: string;
  sha256: string;
}
export interface ProjectVersionsAPI {
  list: (
    input: ProjectVersionScope & { offset: number; limit: number },
  ) => Promise<ProjectVersionPage>;
  create: (input: ProjectVersionScope & { name: string }) => Promise<ProjectVersion>;
  restore: (
    input: RestoreProjectVersionInput,
  ) => Promise<{ bootstrap: Bootstrap; previousVersion: ProjectVersion }>;
}
