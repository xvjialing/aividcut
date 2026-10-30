import type { GenerationRequest } from './generation-types';
import type { StartSpeechInput } from './speech-types';
export interface MCPAudit {
  id: string;
  tool: string;
  success: boolean;
  revision: number;
  createdAt: string;
}

export interface MCPStatus {
  running: boolean;
  projectId?: string;
  projectRevision: number;
  allowEdits: boolean;
  allowSourceFrames?: boolean;
  scopeValid: boolean;
  reason?: string;
  address?: string;
  startedAt?: string;
  executionId?: string;
  audit: MCPAudit[];
}
export interface MCPConnection {
  url: string;
  transport: string;
  httpHeaders: Record<string, string>;
}
export interface MCPAction {
  id: string;
  projectId: string;
  projectRevision: number;
  kind: 'generation' | 'speech' | 'export' | 'analysis';
  status: 'pending' | 'submitting' | 'started' | 'failed' | 'rejected';
  message: string;
  connectionId?: string;
  analysis?: import('./visual-analysis-types').VisualAnalysis;
  analysisPresent?: boolean;
  generation?: GenerationRequest;
  speech?: StartSpeechInput;
  export?: {
    resourceMode?: 'standard' | 'lower_memory';
    renderer?: 'auto' | 'native' | 'ffmpeg';
    preset?: string;
    fromFrame?: number;
    toFrame?: number;
    videoEncoder?: string;
  };
  jobId?: string;
  createdAt: string;
  updatedAt: string;
  executionId?: string;
  canApprove: boolean;
  approvalReason?: string;
}
export interface MCPConfirmation {
  cloneConsent?: { confirmed: boolean; subject: string; authority: 'self' | 'authorized' };
  exportPath?: string;
  overwrite: boolean;
}
export interface MCPAPI {
  acceptVisualAnalysis?(input: {
    id: string;
    sourceEpoch: number;
    projectId: string;
    projectRevision: number;
  }): Promise<import('./types').Bootstrap>;
  getMCPStatus(): Promise<MCPStatus>;
  startMCP(input: {
    projectId: string;
    allowEdits: boolean;
    allowSourceFrames?: boolean;
  }): Promise<MCPStatus>;
  stopMCP(): Promise<MCPStatus>;
  getMCPConnection(): Promise<MCPConnection>;
  listMCPRequests(): Promise<MCPAction[]>;
  rejectMCPRequest(id: string): Promise<MCPAction>;
  approveMCPRequest(id: string, confirmation: MCPConfirmation): Promise<MCPAction>;
}
