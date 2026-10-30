import type { WorkflowDefinition } from '../workflow-types';
export function workflowParameterInactive(
  definition: WorkflowDefinition,
  draft: Record<string, unknown>,
  id: string,
) {
  return (
    definition.id === 'audio-fades' &&
    draft.mode === 'clear' &&
    ['inMs', 'outMs', 'existingPolicy', 'shortPolicy'].includes(id)
  );
}
export function workflowDefaults(definition: WorkflowDefinition): Record<string, unknown> {
  return Object.fromEntries(
    definition.parameters.map((parameter) => [
      parameter.id,
      definition.defaults[parameter.id] ??
        parameter.default ??
        (parameter.type === 'boolean' ? false : ''),
    ]),
  );
}
export function validateWorkflowParameters(
  definition: WorkflowDefinition,
  draft: Record<string, unknown>,
) {
  const errors: Record<string, string> = {};
  const values: Record<string, unknown> = {};
  for (const parameter of definition.parameters) {
    if (workflowParameterInactive(definition, draft, parameter.id)) continue;
    const value = draft[parameter.id];
    if (value === '' || value === undefined || value === null) {
      if (parameter.required) errors[parameter.id] = '请填写此参数。';
      continue;
    }
    if (parameter.type === 'integer' || parameter.type === 'number') {
      const number = Number(value);
      if (
        !Number.isFinite(number) ||
        (parameter.type === 'integer' && !Number.isSafeInteger(number)) ||
        (parameter.min !== undefined && number < parameter.min) ||
        (parameter.max !== undefined && number > parameter.max)
      )
        errors[parameter.id] = '请填写范围内的有效数值。';
      else if (
        parameter.step &&
        Math.abs(
          (number - (parameter.min || 0)) / parameter.step -
            Math.round((number - (parameter.min || 0)) / parameter.step),
        ) > 1e-6
      )
        errors[parameter.id] = '数值不符合参数步长。';
      else values[parameter.id] = number;
    } else if (parameter.type === 'boolean') {
      if (typeof value !== 'boolean') errors[parameter.id] = '请明确选择开关状态。';
      else values[parameter.id] = value;
    } else {
      if (typeof value !== 'string' || (parameter.required && !value.trim()))
        errors[parameter.id] = '请填写有效文字或选择项。';
      else if (
        parameter.options?.length &&
        !parameter.options.some((option) => option.value === value)
      )
        errors[parameter.id] = '此选项已不在当前工作流目录中。';
      else values[parameter.id] = value;
    }
  }
  return { errors, values };
}
