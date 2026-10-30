import type { Bootstrap } from './types';
import type { WorkflowPreview } from './workflow-types';
export interface TimelineRange {
  from: number | null;
  to: number | null;
}
export interface RippleInput {
  projectId: string;
  projectRevision: number;
  sourceEpoch: number;
  fromFrame: number;
  toFrame: number;
}
export interface RippleAPI {
  preview(input: RippleInput): Promise<WorkflowPreview>;
  apply(id: string): Promise<Bootstrap>;
}
