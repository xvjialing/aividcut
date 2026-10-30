import type { Job, ProjectScope } from './types';

export interface ProjectCollectionSummary {
  assetCount: number;
  sourceBytes: number;
  unprovenSourceCount: number;
}
export interface CollectProjectInput extends ProjectScope {
  parentDirectory: string;
  folderName: string;
}
export interface ProjectCollectAPI {
  inspect: (scope: ProjectScope) => Promise<ProjectCollectionSummary>;
  chooseDirectory: () => Promise<string>;
  start: (input: CollectProjectInput) => Promise<Job>;
}
