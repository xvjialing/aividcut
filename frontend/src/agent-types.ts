import type { Bootstrap, EditRequest } from './types';

export type AgentProvider = 'codex' | 'claude';
export interface AgentIsolation {
  supported: boolean;
  status: string;
  reason: string;
  checks: Array<{ id: string; status: string; reason: string; nextAction?: string }>;
  missingFlags: string[];
  evidence: string[];
  limitations: string[];
}
export interface AgentInstallation {
  provider: AgentProvider;
  path: string;
  installed: boolean;
  version: string;
  authState: string;
  authReason: string;
  profileDirectory?: string;
  isolation: AgentIsolation;
}
export interface AgentLoginCommand {
  executable: string;
  args: string[];
  environment: Record<string, string>;
  notice: string;
}
export interface AgentWorkflow {
  id: string;
  name: string;
  description: string;
  available: boolean;
  reason: string;
}
export interface AgentPlanInput {
  id: string;
  provider: AgentProvider;
  instruction: string;
  workflow: string;
  selection: string[];
  consent: boolean;
}
export interface AgentJob {
  id: string;
  provider: AgentProvider;
  projectId: string;
  projectRevision: number;
  status: 'queued' | 'running' | 'succeeded' | 'failed' | 'cancelling' | 'cancelled';
  message: string;
  createdAt: string;
  updatedAt: string;
  result?: {
    provider: AgentProvider;
    request: EditRequest;
    coreValidated: boolean;
    warnings: string[];
  };
  applied: boolean;
}
export interface AgentAPI {
  getAgentStatus(): Promise<AgentInstallation[]>;
  getAgentLoginInstructions(provider: AgentProvider): Promise<AgentLoginCommand>;
  getAgentWorkflows(): Promise<AgentWorkflow[]>;
  startAgentPlan(input: AgentPlanInput): Promise<AgentJob>;
  listAgentPlans(): Promise<AgentJob[]>;
  cancelAgentPlan(id: string): Promise<AgentJob>;
  applyAgentPlan(id: string): Promise<Bootstrap>;
}
