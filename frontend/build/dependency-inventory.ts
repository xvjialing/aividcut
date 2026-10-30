import { createHash } from 'node:crypto';
import { readFileSync, realpathSync, statSync } from 'node:fs';
import path from 'node:path';
import { createRequire } from 'node:module';
import type { Plugin } from 'vite';
import { readPnpmLock } from './pnpm-lock.ts';

function sha256(value: string | Uint8Array) {
  return createHash('sha256').update(value).digest('hex');
}

// 只输出实际产物的包名/版本和相对chunk，不暴露开发机路径或自有源码模块名。
export function dependencyInventory(): Plugin {
  let root = '';
  return {
    name: 'aividcut-dependency-inventory',
    enforce: 'post',
    apply: 'build',
    configResolved(config) {
      root = realpathSync(config.root);
    },
    generateBundle: {
      order: 'post',
      handler(_options, bundle) {
        const lock = readPnpmLock(root);
        const require = createRequire(path.join(root, 'package.json'));
        const packages = new Map<
          string,
          {
            name: string;
            version: string;
            lockPath: string;
            integrity?: string;
            declaredLicense?: string;
            modules: number;
            renderedBytes: number;
          }
        >();
        function recordPackage(key: string, renderedBytes: number) {
          key = key.split(path.sep).join('/');
          const directory = path.join(root, key);
          const real = path.relative(path.join(root, 'node_modules'), realpathSync(directory));
          if (real.startsWith('..') || path.isAbsolute(real))
            throw new Error('实际构建包指向node_modules之外');
          const metadataPath = path.join(directory, 'package.json');
          if (statSync(metadataPath).size > 1 << 20) throw new Error('依赖元数据过大');
          const metadata = JSON.parse(readFileSync(metadataPath, 'utf8')) as {
            name: string;
            version: string;
            license?: string;
          };
          const entry = lock.packages[`${metadata.name}@${metadata.version}`];
          if (!metadata.name || !metadata.version || !entry?.resolution?.integrity)
            throw new Error(`实际构建版本与锁不一致：${key}`);
          let record = packages.get(key);
          if (!record) {
            record = {
              name: metadata.name,
              version: metadata.version,
              lockPath: key,
              integrity: entry.resolution.integrity,
              declaredLicense: metadata.license,
              modules: 0,
              renderedBytes: 0,
            };
            packages.set(key, record);
          }
          record.modules++;
          record.renderedBytes += renderedBytes;
        }
        const chunks: { file: string; bytes: number; sha256: string }[] = [];
        let applicationModules = 0;
        let generatedModules = 0;
        let unknownGeneratedModules = 0;
        const generatedModuleOwners = new Set<string>();
        for (const chunk of Object.values(bundle)) {
          if (chunk.type !== 'chunk') {
            if (chunk.fileName.endsWith('.css')) {
              const data =
                typeof chunk.source === 'string' ? Buffer.from(chunk.source) : chunk.source;
              chunks.push({ file: chunk.fileName, bytes: data.byteLength, sha256: sha256(data) });
            }
            continue;
          }
          chunks.push({
            file: chunk.fileName,
            bytes: Buffer.byteLength(chunk.code),
            sha256: sha256(chunk.code),
          });
          for (const [id, module] of Object.entries(chunk.modules)) {
            if (module.renderedLength === 0) continue;
            const clean = id.split('?')[0];
            if (clean.includes('\0') || !path.isAbsolute(clean)) {
              generatedModules++;
              const owner = clean.startsWith('\0vite/')
                ? 'vite'
                : clean === '\0rolldown/runtime.js'
                  ? 'rolldown'
                  : undefined;
              if (owner) {
                generatedModuleOwners.add(owner);
                const viteManifest = require.resolve('vite/package.json');
                const ownerManifest =
                  owner === 'vite'
                    ? viteManifest
                    : createRequire(viteManifest).resolve('rolldown/package.json');
                recordPackage(
                  path.relative(root, path.dirname(ownerManifest)),
                  module.renderedLength,
                );
              } else unknownGeneratedModules++;
              continue;
            }
            const relative = path.relative(root, clean).split(path.sep).join('/');
            if (!relative.startsWith('node_modules/')) {
              if (relative.startsWith('../') || path.isAbsolute(relative))
                throw new Error('构建模块位于工程范围之外');
              applicationModules++;
              continue;
            }
            const pieces = relative.split('/');
            const marker = pieces.lastIndexOf('node_modules');
            const nameParts = pieces[marker + 1]?.startsWith('@') ? 2 : 1;
            const key = pieces.slice(0, marker + 1 + nameParts).join('/');
            recordPackage(key, module.renderedLength);
          }
        }
        this.emitFile({
          type: 'asset',
          fileName: 'dependency-inventory.json',
          source:
            JSON.stringify(
              {
                schemaVersion: 2,
                lockfileName: lock.lockfileName,
                lockfileSHA256: lock.lockfileSHA256,
                packageManager: lock.packageManager,
                packages: [...packages.values()].sort((a, b) =>
                  a.lockPath.localeCompare(b.lockPath),
                ),
                chunks: chunks.sort((a, b) => a.file.localeCompare(b.file)),
                applicationModules,
                generatedModules,
                generatedModuleOwners: [...generatedModuleOwners].sort(),
                unknownGeneratedModules,
                licenseApproval: false,
              },
              null,
              2,
            ) + '\n',
        });
      },
    },
  };
}
