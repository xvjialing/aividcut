import { createHash } from 'node:crypto';
import { lstatSync, readFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { parseAllDocuments } from 'yaml';

export type LockedPackage = { resolution?: { integrity?: string } };

// pnpm 12 可能同时记录包管理器与应用两个 YAML 文档，只选择应用的 importer。
export function readPnpmLock(root: string) {
  const lockPath = path.join(root, 'pnpm-lock.yaml');
  const info = lstatSync(lockPath);
  if (!info.isFile() || info.size > 4 << 20) throw new Error('pnpm 锁文件须为 4MiB 以内普通文件');
  const raw = readFileSync(lockPath);
  const documents = parseAllDocuments(raw.toString('utf8'));
  if (documents.length > 4 || documents.some((document) => document.errors.length))
    throw new Error('pnpm 锁文件 YAML 无效');
  const candidates = documents
    .map((document) => document.toJS({ maxAliasCount: 0 }))
    .filter((document) => {
      const importer = document?.importers?.['.'];
      return (
        importer &&
        (importer.dependencies || importer.devDependencies || importer.optionalDependencies)
      );
    });
  if (candidates.length !== 1 || String(candidates[0].lockfileVersion) !== '9.0')
    throw new Error('需要唯一的 pnpm v9 应用锁文档');
  const packages = candidates[0].packages as Record<string, LockedPackage>;
  if (!packages || Array.isArray(packages) || Object.keys(packages).length > 4096)
    throw new Error('pnpm 锁定包记录无效');
  const manifest = JSON.parse(readFileSync(path.join(root, 'package.json'), 'utf8'));
  if (!/^pnpm@\d+\.\d+\.\d+$/.test(manifest.packageManager || ''))
    throw new Error('前端须固定 pnpm 精确版本');
  return {
    lockfileName: 'pnpm-lock.yaml',
    lockfileSHA256: createHash('sha256').update(raw).digest('hex'),
    packageManager: manifest.packageManager as string,
    packages,
  };
}

// 构建期 Python 收集器复用同一解析器，避免另加 Python YAML 环境。
if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  process.stdout.write(JSON.stringify(readPnpmLock(path.resolve(process.argv[2] || '.'))));
}
