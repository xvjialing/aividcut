export interface SourceContentQuery {
  sourceEpoch: number;
  projectId: string;
  projectRevision: number;
  assetId?: string;
  text: string;
  origin?: string;
  offset: number;
  limit: number;
}
export interface SourceContentHit {
  kind?: 'transcript' | 'visual';
  analysisId?: string;
  noteId?: string;
  noteKind?: 'observation' | 'inference' | 'suggestion';
  producer?: 'codex' | 'claude' | 'external-mcp' | 'apple-vision' | 'windows-media-ocr';
  tags?: string[];
  frames?: import('./source-frame-types').SourceFrameEvidence[];
  id: string;
  assetId: string;
  assetName: string;
  transcriptId: string;
  transcriptRevision: number;
  sourceFingerprint: string;
  origin: string;
  startUs: number;
  endUs: number;
  wordIds: string[];
  text: string;
}
export interface SourceContentResult {
  sourceEpoch: number;
  projectId: string;
  projectRevision: number;
  assetId?: string;
  version: string;
  query: string;
  origin: string;
  offset: number;
  total: number;
  hits: SourceContentHit[];
  stats: {
    analyses?: number;
    visualNotes?: number;
    transcripts: number;
    words: number;
    skippedStale: number;
    skippedMissing: number;
  };
}
export interface ContentAPI {
  removeAnalysis?(input: {
    sourceEpoch: number;
    projectId: string;
    projectRevision: number;
    id: string;
    operationId: string;
  }): Promise<import('./types').Bootstrap>;
  search(input: SourceContentQuery): Promise<SourceContentResult>;
}
