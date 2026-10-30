import type { GenerationJob, GenerationOutputAsset } from '../generation-types';

export interface CandidateComparisonItem {
  key: string;
  job: GenerationJob;
  asset: GenerationOutputAsset;
}

// 只保留两个身份，任务轮询后从当前读模型解析，访问地址不参与持久身份。
export function comparisonKey(job: GenerationJob, asset: GenerationOutputAsset): string {
  return JSON.stringify([job.projectId, job.id, asset.id, asset.sha256, asset.kind]);
}

export function comparisonItems(jobs: GenerationJob[], projectID: string) {
  const items = new Map<string, CandidateComparisonItem>();
  for (const job of jobs) {
    if (job.projectId !== projectID || job.status !== 'succeeded') continue;
    for (const asset of job.assets) {
      if (!asset.url || !['image', 'video'].includes(asset.kind)) continue;
      const key = comparisonKey(job, asset);
      items.set(key, { key, job, asset });
    }
  }
  return items;
}

export function candidateIsImported(item: CandidateComparisonItem) {
  return item.job.imported || !!item.job.importedAssetIds?.includes(item.asset.id);
}
