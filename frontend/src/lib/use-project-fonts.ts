import { useEffect, useRef, useState } from 'react';
import type { Asset, Project } from '../types';
const MAX_FILE = 32 * 1024 * 1024,
  MAX_TOTAL = 128 * 1024 * 1024;
export const projectFontFamily = (sha: string) => `aividcut-font-${sha.replace(/^sha256:/, '')}`;
type Entry = {
  key: string;
  asset: Asset;
  controller: AbortController;
  status: 'pending' | 'loading' | 'ready' | 'error';
  bytes: number;
  face?: FontFace;
  error?: string;
};
// 按实际引用复用字体；单次只读取一份，切换标题不会重载无关字幕字体。
export function useProjectFonts(project: Project, sourceEpoch: number, enabled: boolean) {
  const [retry, setRetry] = useState(0),
    [state, setState] = useState<{ ready: Set<string>; errors: Map<string, string> }>({
      ready: new Set(),
      errors: new Map(),
    });
  const used = new Set(project.clips.map((c) => c.textStyle.fontAssetId).filter(Boolean));
  const assets = enabled ? project.assets.filter((a) => used.has(a.id)) : [];
  const descriptors = assets.map((asset) => ({
    asset,
    key: JSON.stringify([
      project.id,
      sourceEpoch,
      asset.id,
      asset.fingerprint,
      asset.url,
      asset.missing,
      asset.font?.weight,
      asset.font?.italic,
    ]),
  }));
  const viewKey = JSON.stringify(descriptors.map((d) => d.key));
  const manager = useRef({ mounted: true, running: false, entries: new Map<string, Entry>() });
  function publish() {
    const m = manager.current;
    if (m.mounted)
      setState({
        ready: new Set(
          [...m.entries.values()].filter((e) => e.status === 'ready').map((e) => e.key),
        ),
        errors: new Map(
          [...m.entries.values()].filter((e) => e.error).map((e) => [e.key, e.error!]),
        ),
      });
  }
  async function load(entry: Entry) {
    const asset = entry.asset;
    if (typeof FontFace === 'undefined' || !document.fonts || !crypto.subtle)
      throw Error('当前WebView无法加载并核对工程字体，请使用原生合成预览。');
    if (asset.missing || !asset.url || !asset.fingerprint)
      throw Error(`字体“${asset.name}”缺失，请重新定位原字体。`);
    let response: Response;
    try {
      response = await fetch(asset.url, {
        signal: entry.controller.signal,
        referrerPolicy: 'no-referrer',
      });
    } catch {
      throw Error(`字体“${asset.name}”读取失败，请重试或检查本机素材服务。`);
    }
    if (!response.ok || !response.body)
      throw Error(`字体“${asset.name}”读取失败，请重试或重新定位。`);
    const reader = response.body.getReader(),
      chunks: Uint8Array[] = [];
    let size = 0;
    const retained = [...manager.current.entries.values()]
      .filter((e) => e !== entry)
      .reduce((sum, e) => sum + e.bytes, 0);
    try {
      while (true) {
        const chunk = await reader.read();
        if (chunk.done) break;
        size += chunk.value.length;
        if (size > MAX_FILE || retained + size > MAX_TOTAL)
          throw Error('工程字体超过32MiB单文件或128MiB总预算');
        chunks.push(chunk.value);
      }
    } finally {
      await reader.cancel();
      reader.releaseLock();
    }
    if (entry.controller.signal.aborted) return;
    entry.bytes = size;
    const bytes = new Uint8Array(size);
    let offset = 0;
    for (const chunk of chunks) {
      bytes.set(chunk, offset);
      offset += chunk.length;
    }
    const hash = [...new Uint8Array(await crypto.subtle.digest('SHA-256', bytes))]
      .map((b) => b.toString(16).padStart(2, '0'))
      .join('');
    if (hash !== asset.fingerprint.replace(/^sha256:/, ''))
      throw Error(`字体“${asset.name}”内容已变化，请重新导入或定位原文件。`);
    if (entry.controller.signal.aborted) return;
    const face = new FontFace(projectFontFamily(hash), bytes, {
      weight: String(asset.font?.weight || 400),
      style: asset.font?.italic ? 'italic' : 'normal',
    });
    try {
      await face.load();
    } catch {
      throw Error(`当前WebView无法解析字体“${asset.name}”，请使用原生合成预览。`);
    }
    if (!manager.current.mounted || manager.current.entries.get(entry.key) !== entry) return;
    document.fonts.add(face);
    entry.face = face;
    entry.status = 'ready';
  }
  async function pump() {
    const m = manager.current;
    if (m.running) return;
    m.running = true;
    try {
      while (m.mounted) {
        const entry = [...m.entries.values()].find((e) => e.status === 'pending');
        if (!entry) break;
        entry.status = 'loading';
        try {
          await load(entry);
        } catch (cause) {
          if (m.entries.get(entry.key) === entry) {
            entry.status = 'error';
            entry.bytes = 0;
            entry.error = cause instanceof Error ? cause.message : '工程字体加载失败，请重新定位。';
          }
        }
        publish();
      }
    } finally {
      m.running = false;
    }
  }
  useEffect(() => {
    const m = manager.current;
    m.mounted = true;
    return () => {
      m.mounted = false;
      for (const e of m.entries.values()) {
        e.controller.abort();
        if (e.face) document.fonts.delete(e.face);
      }
      m.entries.clear();
    };
  }, []);
  useEffect(() => {
    const m = manager.current,
      desired = new Set(descriptors.slice(0, 32).map((d) => d.key));
    for (const [key, entry] of m.entries) {
      if (!desired.has(key) || entry.status === 'error') {
        entry.controller.abort();
        if (entry.face) document.fonts.delete(entry.face);
        m.entries.delete(key);
      }
    }
    for (const d of descriptors.slice(0, 32)) {
      if (!m.entries.has(d.key))
        m.entries.set(d.key, {
          ...d,
          controller: new AbortController(),
          status: 'pending',
          bytes: 0,
        });
    }
    publish();
    void pump();
  }, [viewKey, retry]);
  return {
    ready: new Set(descriptors.filter((d) => state.ready.has(d.key)).map((d) => d.asset.id)),
    error:
      assets.length > 32
        ? '当前工程最多加载32份字体'
        : descriptors.map((d) => state.errors.get(d.key)).find(Boolean) || '',
    retry: () => setRetry((v) => v + 1),
  };
}
