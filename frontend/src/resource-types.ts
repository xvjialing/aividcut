import type { ProjectScope } from './types';
export type ResourcePhase = 'idle' | 'preview' | 'editing' | 'export' | 'mixed';
export type ResourceProcess = {
  pid: number;
  ppid: number;
  name: string;
  identity: string;
  role: string;
  association: string;
  rssBytes: number | null;
  privateBytes: number | null;
  privateKind?: string;
  cpuTimeNs: number | null;
  cpuPercent: number | null;
  issue?: string;
};
export type ResourceSample = {
  snapshot: {
    at: string;
    queryMs: number;
    windowMs: number | null;
    rootPid: number;
    processes: ResourceProcess[];
    knownRssBytes: number;
    rssBytes: number | null;
    cpuPercent: number | null;
    missingRss: number;
    missingCpu: number;
    partial: boolean;
    issues: string[];
    uncovered: string[];
    os: string;
    architecture: string;
    logicalCpus: number;
  };
  project: {
    projectId: string;
    revision: number;
    sourceEpoch: number;
    width: number;
    height: number;
    fpsNum: number;
    fpsDen: number;
    clips: number;
    assets: number;
    tracks: number;
  };
};
export type ResourceSummary = {
  id: string;
  phase: ResourcePhase;
  status: 'running' | 'stopped' | 'complete' | 'failed';
  startedAt: string;
  finishedAt: string | null;
  samples: number;
  sampledPeakRss: number | null;
  sampledPeakCpu: number | null;
  error?: string;
};
export type ResourceState = {
  latest: ResourceSample | null;
  active: ResourceSummary | null;
  reports: ResourceSummary[];
  error?: string;
};
export type ResourceAPI = {
  state(): Promise<ResourceState>;
  status(): Promise<ResourceState>;
  start(input: ProjectScope & { phase: ResourcePhase }): Promise<ResourceSummary>;
  stop(id: string): Promise<ResourceSummary>;
  choosePath(): Promise<string>;
  export(input: { id: string; path: string; overwrite: boolean }): Promise<string>;
};
