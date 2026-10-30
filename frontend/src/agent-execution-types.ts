import type { AgentInstallation, AgentProvider } from './agent-types';
import type { MCPAudit } from './mcp-types';
export interface AgentExecutionInput {
  id: string;
  provider: AgentProvider;
  projectId: string;
  projectRevision: number;
  instruction: string;
  workflow: string;
  selection: string[];
  allowEdits: boolean;
  allowSourceFrames?: boolean;
  consent: boolean;
}
export interface AgentExecution {
  id: string;
  provider: AgentProvider;
  projectId: string;
  projectRevision: number;
  endRevision: number;
  allowEdits: boolean;
  allowSourceFrames?: boolean;
  selection: string[];
  workflow: string;
  status:
    'queued' | 'running' | 'cancelling' | 'succeeded' | 'failed' | 'cancelled' | 'interrupted';
  stage: string;
  message: string;
  summary?: string;
  frameEvidence?: import('./source-frame-types').SourceFrameEvidence[];
  createdAt: string;
  updatedAt: string;
  audit?: MCPAudit[];
  warnings: string[];
}
export interface AgentExecutionAPI {
  getAgentExecutionStatus(): Promise<AgentInstallation[]>;
  startAgentExecution(input: AgentExecutionInput): Promise<AgentExecution>;
  listAgentExecutions(): Promise<AgentExecution[]>;
  getAgentExecution(id: string): Promise<AgentExecution>;
  cancelAgentExecution(id: string): Promise<AgentExecution>;
}
