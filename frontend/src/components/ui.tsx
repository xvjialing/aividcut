import { useEffect, useRef, useState } from 'react';
import * as Dialog from '@radix-ui/react-dialog';
import * as Tooltip from '@radix-ui/react-tooltip';
import { LoaderCircle, X } from 'lucide-react';
import type { ComponentPropsWithRef, ReactNode, SelectHTMLAttributes } from 'react';

type ButtonProps = ComponentPropsWithRef<'button'> & {
  variant?: 'primary' | 'ghost' | 'outline' | 'danger';
  busy?: boolean;
};
export function Button({
  className = '',
  variant = 'outline',
  busy,
  children,
  disabled,
  ...props
}: ButtonProps) {
  return (
    <button
      type="button"
      className={`button button-${variant} ${className}`}
      aria-busy={busy || undefined}
      disabled={disabled || busy}
      {...props}
    >
      {busy ? <LoaderCircle size={15} className="spin" aria-hidden="true" /> : null}
      {children}
    </button>
  );
}

export function IconButton({
  label,
  children,
  active,
  onClick,
  disabled,
  ...props
}: ButtonProps & { label: string; active?: boolean }) {
  return (
    <Tooltip.Root>
      <Tooltip.Trigger asChild>
        <span className="icon-button-wrap">
          <Button
            aria-label={label}
            className={`icon-button ${active ? 'active' : ''}`}
            variant="ghost"
            aria-pressed={active === undefined ? undefined : active}
            onClick={onClick}
            disabled={disabled}
            {...props}
          >
            {children}
          </Button>
        </span>
      </Tooltip.Trigger>
      <Tooltip.Portal>
        <Tooltip.Content className="tooltip" sideOffset={6}>
          {label}
          <Tooltip.Arrow />
        </Tooltip.Content>
      </Tooltip.Portal>
    </Tooltip.Root>
  );
}

export function TooltipProvider({ children }: { children: ReactNode }) {
  return <Tooltip.Provider delayDuration={350}>{children}</Tooltip.Provider>;
}

export function Field({
  label,
  hint,
  children,
}: {
  label: string;
  hint?: string;
  children: ReactNode;
}) {
  return (
    <label className="field">
      <span className="field-label">{label}</span>
      {children}
      {hint ? <span className="field-hint">{hint}</span> : null}
    </label>
  );
}

export function Input(props: ComponentPropsWithRef<'input'>) {
  return <input className="input" {...props} />;
}

// 原生选择器作为明确的平台控件，打开后的系统弹出样式由操作系统负责。
export function Select(props: SelectHTMLAttributes<HTMLSelectElement>) {
  return <select className="input select" {...props} />;
}

export function PanelTabs<T extends string>({
  id,
  label,
  value,
  items,
  onChange,
  children,
  activationMode = 'automatic',
  disabled = false,
}: {
  id: string;
  label: string;
  value: T;
  items: { value: T; label: string }[];
  onChange: (value: T) => void;
  children?: ReactNode;
  activationMode?: 'automatic' | 'manual';
  disabled?: boolean;
}) {
  const list = useRef<HTMLDivElement>(null);
  const [focused, setFocused] = useState(value);
  useEffect(() => setFocused(value), [value]);
  useEffect(() => {
    const parent = list.current,
      button = document.getElementById(`${id}-${value}`);
    if (!parent || !button || parent.scrollWidth <= parent.clientWidth) return;
    const bounds = parent.getBoundingClientRect(),
      item = button.getBoundingClientRect();
    if (item.left < bounds.left) parent.scrollLeft += item.left - bounds.left;
    else if (item.right > bounds.right) parent.scrollLeft += item.right - bounds.right;
  }, [id, value, items.length]);
  return (
    <div className="panel-tabs">
      <div ref={list} role="tablist" aria-label={label} className="tab-buttons">
        {items.map((item, index) => (
          <button
            type="button"
            role="tab"
            key={item.value}
            id={`${id}-${item.value}`}
            aria-controls={`${id}-panel`}
            title={item.label}
            aria-selected={value === item.value}
            tabIndex={(activationMode === 'manual' ? focused : value) === item.value ? 0 : -1}
            disabled={disabled}
            onFocus={() => setFocused(item.value)}
            onClick={() => {
              if (!disabled && (activationMode === 'automatic' || item.value !== value))
                onChange(item.value);
            }}
            onKeyDown={(event) => {
              if (disabled) return;
              if (activationMode === 'manual' && (event.key === 'Enter' || event.key === ' '))
                event.stopPropagation();
              let next: number | undefined;
              if (event.key === 'ArrowRight') next = (index + 1) % items.length;
              if (event.key === 'ArrowLeft') next = (index + items.length - 1) % items.length;
              if (event.key === 'Home') next = 0;
              if (event.key === 'End') next = items.length - 1;
              if (next !== undefined) {
                event.preventDefault();
                event.stopPropagation();
                setFocused(items[next].value);
                if (activationMode === 'automatic') onChange(items[next].value);
                document.getElementById(`${id}-${items[next].value}`)?.focus();
              }
            }}
          >
            {item.label}
          </button>
        ))}
      </div>
      {children ? (
        <>
          <span className="grow" />
          {children}
        </>
      ) : null}
    </div>
  );
}

export function Modal({
  open,
  title,
  description,
  children,
  onOpenChange,
  busy = false,
  className = '',
  onCloseAutoFocus,
}: {
  open: boolean;
  title: string;
  description?: string;
  children: ReactNode;
  onOpenChange: (open: boolean) => void;
  busy?: boolean;
  className?: string;
  onCloseAutoFocus?: (event: Event) => void;
}) {
  return (
    <Dialog.Root open={open} onOpenChange={onOpenChange}>
      <Dialog.Portal>
        <Dialog.Overlay className="dialog-overlay" />
        <Dialog.Content
          className={`dialog-content ${className}`}
          onCloseAutoFocus={onCloseAutoFocus}
          aria-describedby={description ? undefined : undefined}
        >
          <div className="dialog-title-row">
            <Dialog.Title>{title}</Dialog.Title>
            <Button
              className="icon-button"
              variant="ghost"
              aria-label="关闭对话框"
              disabled={busy}
              onClick={() => onOpenChange(false)}
            >
              <X size={18} />
            </Button>
          </div>
          {description ? (
            <Dialog.Description className="dialog-description">{description}</Dialog.Description>
          ) : (
            <Dialog.Description className="sr-only">{title}</Dialog.Description>
          )}
          {children}
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
