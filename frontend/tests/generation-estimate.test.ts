import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';
import type {
  GenerationCostEstimate,
  GenerationEstimateInput,
  GenerationRequest,
} from '../src/generation-types';
import { calculateCostEstimate } from '../src/lib/generation-estimate';

const cases = JSON.parse(
  readFileSync(
    new URL('../../internal/generationjobs/testdata/estimate-cases.json', import.meta.url),
    'utf8',
  ),
) as {
  name: string;
  request: GenerationRequest;
  input?: GenerationEstimateInput;
  expected?: GenerationCostEstimate;
  error?: boolean;
}[];
describe('跨 Go/TypeScript 的费用参考契约', () => {
  for (const c of cases) {
    it(c.name, () => {
      if (c.error) expect(() => calculateCostEstimate(c.request, c.input || undefined)).toThrow();
      else
        expect(calculateCostEstimate(c.request, c.input || undefined) || null).toEqual(c.expected);
    });
  }
});
