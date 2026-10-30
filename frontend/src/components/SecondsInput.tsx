import { useEffect, useRef, useState } from 'react';
import { formatSourceSeconds, sourceSecondsToMicroseconds } from '../lib/action-review';
import { Input } from './ui';
export function SecondsInput({
  microseconds,
  onChange,
  disabled,
  label,
}: {
  microseconds: number;
  onChange: (value: number) => void;
  disabled?: boolean;
  label?: string;
}) {
  const [draft, setDraft] = useState(
    Number.isFinite(microseconds) ? formatSourceSeconds(microseconds) : '',
  );
  const focused = useRef(false);
  const emitted = useRef(microseconds);
  useEffect(() => {
    if (!focused.current || !Object.is(microseconds, emitted.current))
      setDraft(Number.isFinite(microseconds) ? formatSourceSeconds(microseconds) : '');
  }, [microseconds]);
  const invalid = sourceSecondsToMicroseconds(draft) === null;
  return (
    <Input
      type="text"
      inputMode="decimal"
      value={draft}
      disabled={disabled}
      aria-label={label}
      aria-invalid={invalid}
      onFocus={() => {
        focused.current = true;
      }}
      onChange={(event) => {
        const text = event.target.value;
        setDraft(text);
        const value = sourceSecondsToMicroseconds(text) ?? NaN;
        emitted.current = value;
        onChange(value);
      }}
      onBlur={() => {
        focused.current = false;
        const value = sourceSecondsToMicroseconds(draft);
        if (value !== null) setDraft(formatSourceSeconds(value));
      }}
    />
  );
}
