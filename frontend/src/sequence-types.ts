import type { Bootstrap, ProjectScope, Settings } from './types';
export interface SequenceSummary {
  id: string;
  name: string;
  settings: Settings;
  clipCount: number;
  trackCount: number;
  durationFrames: number;
  locked: boolean;
  current: boolean;
}
export interface SequenceChangeInput extends ProjectScope {
  action: 'switch' | 'create' | 'duplicate' | 'rename' | 'delete';
  sequenceId: string;
  name?: string;
  settings?: Settings;
  idempotencyKey: string;
  confirmDelete?: boolean;
}
export interface SequenceAPI {
  change: (input: SequenceChangeInput) => Promise<Bootstrap>;
}
