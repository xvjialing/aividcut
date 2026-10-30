import type { NewProjectDraft, NewProjectErrors } from '../lib/new-project';
import {
  changeProjectLayout,
  newProjectDimensions,
  parseNewProject,
  projectFrameRates,
} from '../lib/new-project';
import { Field, Input, Select } from './ui';
export function NewProjectSettings({
  value,
  onChange,
  errors,
  disabled,
  context = 'project',
}: {
  value: NewProjectDraft;
  onChange: (d: NewProjectDraft) => void;
  errors: NewProjectErrors;
  disabled: boolean;
  context?: 'project' | 'sequence';
}) {
  const result = parseNewProject('摘要', value);
  const dimensions = newProjectDimensions(value);
  const numeric = (
    key: 'width' | 'height' | 'fpsNum' | 'fpsDen',
    label: string,
    min: number,
    max: number,
  ) => (
    <Field label={label}>
      <Input
        id={`new-project-${key}`}
        aria-label={label}
        type="number"
        min={min}
        max={max}
        step={key === 'width' || key === 'height' ? 2 : 1}
        required
        value={value[key]}
        disabled={disabled}
        onChange={(e) => onChange({ ...value, [key]: e.target.value })}
        aria-invalid={Boolean(errors[key])}
        aria-describedby={errors[key] ? `new-project-error-${key}` : undefined}
      />
      {errors[key] ? (
        <p id={`new-project-error-${key}`} className="form-error">
          {errors[key]}
        </p>
      ) : null}
    </Field>
  );
  return (
    <>
      <div className="field-pair">
        <Field label="画幅">
          <Select
            value={value.layout}
            disabled={disabled}
            onChange={(e) =>
              onChange(changeProjectLayout(value, e.target.value as NewProjectDraft['layout']))
            }
          >
            <option value="landscape">横屏 · 16:9</option>
            <option value="portrait">竖屏 · 9:16</option>
            <option value="square">方形 · 1:1</option>
            <option value="custom">自定义尺寸</option>
          </Select>
        </Field>
        <Field label="分辨率">
          <Select
            value={value.resolution}
            disabled={disabled || value.layout === 'custom'}
            onChange={(e) =>
              onChange({ ...value, resolution: e.target.value as NewProjectDraft['resolution'] })
            }
          >
            {['720', '1080', '2160'].map((n) => (
              <option key={n} value={n}>
                {value.layout === 'square'
                  ? `${n} × ${n}`
                  : n === '2160'
                    ? '4K · 短边2160'
                    : `${n}p`}
              </option>
            ))}
          </Select>
        </Field>
      </div>
      {value.layout === 'custom' ? (
        <div className="field-pair">
          {numeric('width', '宽度 · px', 16, 8192)}
          {numeric('height', '高度 · px', 16, 8192)}
        </div>
      ) : null}
      <Field label="时间线帧率">
        <Select
          value={value.frameRate}
          disabled={disabled}
          onChange={(e) => {
            const rate = e.target.value;
            const [n, den] =
              value.frameRate === 'custom'
                ? [value.fpsNum, value.fpsDen]
                : value.frameRate.split('/');
            onChange({
              ...value,
              frameRate: rate,
              ...(rate === 'custom' ? { fpsNum: n, fpsDen: den } : {}),
            });
          }}
        >
          {projectFrameRates.map(([id, label]) => (
            <option key={id} value={id}>
              {label}
            </option>
          ))}
        </Select>
      </Field>
      {value.frameRate === 'custom' ? (
        <div className="field-pair">
          {numeric('fpsNum', '帧率分子', 1, 240000)}
          {numeric('fpsDen', '帧率分母', 1, 120000)}
        </div>
      ) : null}
      <p className="field-hint" role="status">
        {result.settings
          ? `${dimensions[0]} × ${dimensions[1]} · ${result.settings.fpsNum}/${result.settings.fpsDen} fps · 48 kHz`
          : '请修正尺寸或帧率后创建。'}
      </p>
      <p className="field-hint">
        {context === 'sequence' ? '只创建新的空序列，共享当前工程素材库。' : '只创建新的空工程。'}
        当前媒体基线为4K像素、2–120fps、48kHz；时间码采用非丢帧计数，帧率按分数保存。
      </p>
    </>
  );
}
