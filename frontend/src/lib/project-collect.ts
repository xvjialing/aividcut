export function defaultCollectionName(name: string) {
  const clean = name
    .replace(/[\\/:*?"<>|\u0000-\u001f]/g, '')
    .replace(/[.\s]+$/g, '')
    .trim();
  const candidate = `${Array.from(clean || '工程')
    .slice(0, 76)
    .join('')} 副本`;
  return validCollectionName(candidate)
    ? candidate
    : `工程 ${Array.from(clean).slice(0, 72).join('')} 副本`;
}
export function projectParentDirectory(path: string) {
  const position = Math.max(path.lastIndexOf('/'), path.lastIndexOf('\\'));
  return position === 0
    ? '/'
    : position === 2 && path[1] === ':'
      ? path.slice(0, 3)
      : position > 0
        ? path.slice(0, position)
        : '';
}
export function validCollectionName(name: string) {
  const value = name.trim();
  return Boolean(
    value &&
    Array.from(value).length <= 80 &&
    !/[\\/:*?"<>|\u0000-\u001f]/.test(value) &&
    !/[.]$/.test(value) &&
    !/^(CON|PRN|AUX|NUL|COM[1-9]|LPT[1-9])(?:\.|$)/i.test(value),
  );
}
export function absoluteCollectionParent(path: string) {
  return path.startsWith('/') || /^[a-z]:[\\/]/i.test(path) || path.startsWith('\\\\');
}
export function collectionSize(bytes: number) {
  const units = ['字节', 'KiB', 'MiB', 'GiB', 'TiB'];
  let value = bytes,
    index = 0;
  while (value >= 1024 && index < units.length - 1) {
    value /= 1024;
    index++;
  }
  return `${new Intl.NumberFormat('zh-CN', { maximumFractionDigits: index ? 1 : 0 }).format(value)} ${units[index]}`;
}
