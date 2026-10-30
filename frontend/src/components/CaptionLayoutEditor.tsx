import { useId } from 'react';
import type { CaptionLayoutDraft } from '../lib/caption-layout';
import { Field, Input, Select } from './ui';
export function CaptionLayoutEditor({
  value,
  onChange,
  errors = {},
  disabled = false,
}: {
  value: CaptionLayoutDraft;
  onChange: (value: CaptionLayoutDraft) => void;
  errors?: Record<string, string>;
  disabled?: boolean;
}) {
  const prefix = useId();
  const labels: Array<{
    id: keyof Omit<CaptionLayoutDraft, 'mode'>;
    name: string;
    kind: 'number' | 'text';
    min?: number;
    max?: number;
  }> = [
    { id: 'maxCharsPerLine', name: '每行最多字数', kind: 'number', min: 2, max: 120 },
    { id: 'maxLines', name: '每条最多行数', kind: 'number', min: 1, max: 4 },
    { id: 'maxDurationSeconds', name: '单条最长 · 秒', kind: 'text' },
    { id: 'pauseThresholdSeconds', name: '停顿分界 · 秒', kind: 'text' },
  ];
  return (
    <div className="caption-layout-editor">
      <Field label="字幕呈现">
        <Select
          value={value.mode}
          disabled={disabled}
          onChange={(event) =>
            onChange({ ...value, mode: event.target.value as CaptionLayoutDraft['mode'] })
          }
        >
          <option value="word">逐词显示</option>
          <option value="readable">阅读分句</option>
        </Select>
      </Field>
      {value.mode === 'readable' ? (
        <>
          <div className="field-pair">
            {labels.map((field) => (
              <Field key={field.id} label={field.name}>
                <Input
                  type={field.kind}
                  inputMode={field.kind === 'text' ? 'decimal' : 'numeric'}
                  min={field.min}
                  max={field.max}
                  step={field.kind === 'number' ? 1 : undefined}
                  value={value[field.id]}
                  disabled={disabled}
                  aria-invalid={Boolean(errors[field.id])}
                  aria-describedby={errors[field.id] ? `${prefix}-${field.id}` : undefined}
                  onChange={(event) => onChange({ ...value, [field.id]: event.target.value })}
                />
                {errors[field.id] ? (
                  <span className="form-error" id={`${prefix}-${field.id}`}>
                    {errors[field.id]}
                  </span>
                ) : null}
              </Field>
            ))}
          </div>
          <p className="field-hint">
            依据真实词时间分句与换行；不会按字数猜时间。人工修改会保留，冲突需先核对。
          </p>
        </>
      ) : null}
    </div>
  );
}
