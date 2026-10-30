import { useEffect, useRef } from 'react';
import { Archive, ChevronDown, History, LifeBuoy } from 'lucide-react';
import { Button } from './ui';

export function ProjectMenu({
  name,
  dirty,
  details,
  recoveries,
  recoveryError,
  disabled,
  onVersions,
  onCollect,
  onRecovery,
}: {
  name: string;
  dirty: boolean;
  details: string;
  recoveries: number;
  recoveryError: boolean;
  disabled: boolean;
  onVersions: () => void;
  onCollect: () => void;
  onRecovery: () => void;
}) {
  const root = useRef<HTMLDetailsElement>(null),
    trigger = useRef<HTMLElement>(null);
  useEffect(() => {
    const outside = (event: PointerEvent) => {
      if (event.target instanceof Node && !root.current?.contains(event.target) && root.current)
        root.current.open = false;
    };
    const escape = (event: KeyboardEvent) => {
      if (event.key === 'Escape' && root.current?.open) {
        event.preventDefault();
        event.stopPropagation();
        root.current.open = false;
        trigger.current?.focus();
      }
    };
    document.addEventListener('pointerdown', outside);
    document.addEventListener('keydown', escape, true);
    return () => {
      document.removeEventListener('pointerdown', outside);
      document.removeEventListener('keydown', escape, true);
    };
  }, []);
  function run(action: () => void) {
    if (root.current) root.current.open = false;
    action();
  }
  return (
    <details className="project-menu" ref={root}>
      <summary
        className="project-heading"
        id="project-menu-trigger"
        ref={trigger}
        aria-label={`工程菜单：${name}`}
      >
        <span className={`save-dot ${dirty ? 'dirty' : ''}`} />
        <span>{name}</span>
        <ChevronDown size={12} aria-hidden="true" />
        {recoveries || recoveryError ? (
          <span
            className="count-badge"
            aria-label={recoveryError ? '恢复资料有错误' : `${recoveries}份恢复资料`}
          >
            {recoveryError ? '!' : recoveries}
          </span>
        ) : null}
      </summary>
      <nav className="project-menu-content" aria-label="当前工程操作">
        <p className="field-hint">{details}</p>
        <Button
          variant="ghost"
          id="project-versions-trigger"
          aria-label="工程版本"
          disabled={disabled}
          onClick={() => run(onVersions)}
        >
          <History size={16} />
          工程版本
        </Button>
        <Button
          variant="ghost"
          id="project-collect-trigger"
          aria-label="收集工程副本"
          disabled={disabled}
          onClick={() => run(onCollect)}
        >
          <Archive size={16} />
          收集工程副本
        </Button>
        <Button
          variant="ghost"
          id="recovery-center-trigger"
          aria-label="恢复中心"
          disabled={disabled}
          onClick={() => run(onRecovery)}
        >
          <LifeBuoy size={16} />
          恢复中心{recoveries ? ` · ${recoveries}` : ''}
        </Button>
      </nav>
    </details>
  );
}
