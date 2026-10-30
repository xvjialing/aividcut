import type {
  GenerationCostEstimate,
  GenerationEstimateInput,
  GenerationRequest,
} from '../generation-types';

export const estimateUnitNames = {
  request: '每次请求',
  image: '每张图片',
  video_second: '每秒视频',
};

function money(value: bigint): string {
  const decimal = (value % 1_000_000n).toString().padStart(6, '0').replace(/0+$/, '');
  return `${value / 1_000_000n}.${decimal.padEnd(2, '0')}`;
}

// 与 Go 使用相同十进制契约；估价只记录人为参考，不参与支付或供应商参数。
export function calculateCostEstimate(
  request: GenerationRequest,
  input?: GenerationEstimateInput,
): GenerationCostEstimate | undefined {
  if (!input) return undefined;
  if (!['CNY', 'USD'].includes(input.currency))
    throw new Error('费用参考仅支持 CNY 或 USD，不自动换算币种');
  if (input.unitPrice.length > 16 || !/^(0|[1-9][0-9]{0,8})(\.[0-9]{1,6})?$/.test(input.unitPrice))
    throw new Error('参考单价需要非负十进制数，最多九位整数和六位小数');
  const [whole, decimal = ''] = input.unitPrice.split('.');
  const micro = BigInt(whole) * 1_000_000n + BigInt(decimal.padEnd(6, '0'));
  const count = request.count === undefined || request.count === 0 ? 1 : request.count;
  if (!Number.isSafeInteger(count) || count < 1 || count > 1_000_000)
    throw new Error('费用参考的请求数量超出范围');
  let quantity = 1;
  if (input.unit === 'image') {
    if (!['image.generate', 'image.edit'].includes(request.mode))
      throw new Error('每张图片参考价仅用于图片生成或修改');
    quantity = count;
  } else if (input.unit === 'video_second') {
    if (!['video.text', 'video.image', 'video.reference'].includes(request.mode))
      throw new Error('每秒视频参考价仅用于视频生成');
    const seconds = request.durationSeconds;
    if (
      !seconds ||
      !Number.isSafeInteger(seconds) ||
      seconds < 1 ||
      seconds > 1_000_000 ||
      count > Math.floor(1_000_000 / seconds)
    )
      throw new Error('每秒视频参考价需要显式时长，且计价数量不超过一百万');
    quantity = count * seconds;
  } else if (input.unit !== 'request') throw new Error('费用参考单位无效');
  if (micro > 9_223_372_036_854_775_807n / BigInt(quantity))
    throw new Error('费用参考金额超出当前计算范围');
  return {
    basis: 'manual-unit-v1',
    currency: input.currency,
    unit: input.unit,
    unitPrice: money(micro),
    quantity,
    amount: money(micro * BigInt(quantity)),
  };
}

export function estimateInput(value?: GenerationCostEstimate): GenerationEstimateInput | undefined {
  return value && { currency: value.currency, unit: value.unit, unitPrice: value.unitPrice };
}
