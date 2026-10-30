import { Button } from './ui';
export interface ProjectSnapshotRow {
  id: string;
  name: string;
  time: string;
  revision?: number;
  details: string;
  beforeRestore?: boolean;
}
// 具名版本与自动恢复共用同一可读、可滚动的历史条目，不另造列表控件。
export function ProjectSnapshotList({
  label,
  rows,
  busy,
  onSelect,
  actionLabel = '核对恢复',
}: {
  label: string;
  rows: ProjectSnapshotRow[];
  busy: boolean;
  onSelect: (id: string) => void;
  actionLabel?: string;
}) {
  return (
    <ul className="project-version-list" aria-label={label}>
      {rows.map((row) => (
        <li key={row.id}>
          <div>
            <strong>{row.name}</strong>
            {row.beforeRestore ? <span className="quiet-badge">恢复前</span> : null}
            <p>
              <time dateTime={row.time}>{new Date(row.time).toLocaleString('zh-CN')}</time> · 修订{' '}
              {row.revision ?? '未提供'}
            </p>
            <p className="field-hint">{row.details}</p>
          </div>
          <Button disabled={busy} title={row.name} onClick={() => onSelect(row.id)}>
            {actionLabel} · {row.name}
          </Button>
        </li>
      ))}
    </ul>
  );
}
