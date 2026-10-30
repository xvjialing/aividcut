import { useEffect, useId, useRef } from 'react';
import type {
  GenerationCostEstimate,
  GenerationEstimateInput,
  GenerationRequest,
} from '../generation-types';
import { calculateCostEstimate, estimateUnitNames } from '../lib/generation-estimate';
import { Field, Input, Select } from './ui';

export function GenerationEstimateSummary({
  estimate,
  submitted = false,
}: {
  estimate?: GenerationCostEstimate;
  submitted?: boolean;
}) {
  return (
    <div className="generation-estimate-summary">
      <p className="field-hint">
        {estimate
          ? `${submitted ? '提交时估价' : '本次估价'}：${estimate.currency} ${estimate.amount}（${estimateUnitNames[estimate.unit]} ${estimate.unitPrice} × ${estimate.quantity}）`
          : '未填写费用参考'}
      </p>
      <p className="field-hint">手动参考价，实际扣费及退款以服务商账单为准。</p>
    </div>
  );
}

export function GenerationEstimateFields({
  request,
  value,
  error,
  disabled,
  locked,
  onChange,
}: {
  request: GenerationRequest;
  value: GenerationEstimateInput;
  error?: string;
  disabled?: boolean;
  locked?: boolean;
  onChange: (value: GenerationEstimateInput) => void;
}) {
  const id = useId();
  const input = useRef<HTMLInputElement>(null);
  useEffect(() => {
    if (error) input.current?.focus();
  }, [error]);
  let estimate: GenerationCostEstimate | undefined;
  try {
    if (value.unitPrice !== '') estimate = calculateCostEstimate(request, value);
  } catch {
    // 允许用户继续输入；只有明确提交才展示字段错误。
  }
  return (
    <fieldset className="generation-reference-fieldset" disabled={disabled || locked}>
      <legend>费用参考（选填）</legend>
      <p className="field-hint">
        请按本次模型、尺寸、质量、参考素材和声音核对单价。估价不是账单或费用上限。
      </p>
      {locked ? <p className="field-hint">沿用上次提交的费用参考，继续核对同一个请求。</p> : null}
      <div className="field-pair">
        <Field label="估价币种">
          <Select
            value={value.currency}
            onChange={(event) =>
              onChange({
                ...value,
                currency: event.target.value as GenerationEstimateInput['currency'],
              })
            }
          >
            <option value="CNY">CNY · 人民币</option>
            <option value="USD">USD · 美元</option>
          </Select>
        </Field>
        <Field label="计价单位">
          <Select
            value={value.unit}
            onChange={(event) =>
              onChange({ ...value, unit: event.target.value as GenerationEstimateInput['unit'] })
            }
          >
            <option value="request">每次请求</option>
            {request.mode.startsWith('image.') ? (
              <option value="image">每张图片</option>
            ) : (
              <option value="video_second">每秒视频</option>
            )}
          </Select>
        </Field>
      </div>
      <Field label="参考单价">
        <Input
          ref={input}
          type="text"
          inputMode="decimal"
          maxLength={16}
          placeholder="留空表示未知，例如 0.10"
          value={value.unitPrice}
          aria-invalid={Boolean(error)}
          aria-describedby={`${id}-hint${error ? ` ${id}-error` : ''}`}
          onChange={(event) => onChange({ ...value, unitPrice: event.target.value })}
        />
        <span id={`${id}-hint`} className="field-hint">
          最多六位小数，仅用于本次请求。
        </span>
        {error ? (
          <span id={`${id}-error`} className="form-error">
            {error}
          </span>
        ) : null}
      </Field>
      {value.unitPrice !== '' && !estimate ? (
        <p className="field-hint">填写有效单价和计价参数后计算估价。</p>
      ) : (
        <GenerationEstimateSummary estimate={estimate} />
      )}
    </fieldset>
  );
}
