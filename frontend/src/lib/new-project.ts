import type { Settings } from '../types';
export type NewProjectDraft = {
  layout: 'landscape' | 'portrait' | 'square' | 'custom';
  resolution: '720' | '1080' | '2160';
  width: string;
  height: string;
  frameRate: string;
  fpsNum: string;
  fpsDen: string;
};
export type NewProjectErrors = Partial<
  Record<'name' | 'width' | 'height' | 'fpsNum' | 'fpsDen', string>
>;
export const defaultNewProjectDraft = (): NewProjectDraft => ({
  layout: 'landscape',
  resolution: '1080',
  width: '1920',
  height: '1080',
  frameRate: '30/1',
  fpsNum: '30',
  fpsDen: '1',
});
export const projectFrameRates = [
  ['24000/1001', '23.976（24000/1001）'],
  ['24/1', '24'],
  ['25/1', '25'],
  ['30000/1001', '29.97（30000/1001）'],
  ['30/1', '30'],
  ['50/1', '50'],
  ['60000/1001', '59.94（60000/1001）'],
  ['60/1', '60'],
  ['120/1', '120'],
  ['custom', '自定义有理帧率'],
] as const;
export function newProjectDimensions(d: NewProjectDraft) {
  if (d.layout === 'custom') return [d.width, d.height];
  const short = Number(d.resolution),
    long = (short * 16) / 9;
  return (
    d.layout === 'square' ? [short, short] : d.layout === 'portrait' ? [short, long] : [long, short]
  ).map(String);
}
export function changeProjectLayout(
  d: NewProjectDraft,
  layout: NewProjectDraft['layout'],
): NewProjectDraft {
  const [width, height] = newProjectDimensions(d);
  return { ...d, layout, ...(layout === 'custom' ? { width, height } : {}) };
}
export function parseNewProject(
  name: string,
  d: NewProjectDraft,
): { name: string; settings: Settings | null; errors: NewProjectErrors } {
  const errors: NewProjectErrors = {};
  name = name.trim();
  if (!name || [...name].length > 120 || /[\u0000-\u001f\u007f]/.test(name))
    errors.name = '工程名称需要1–120个字符，不能包含控制字符。';
  const [widthText, heightText] = newProjectDimensions(d);
  const width = Number(widthText),
    height = Number(heightText);
  for (const [key, raw, value] of [
    ['width', widthText, width],
    ['height', heightText, height],
  ] as const) {
    if (
      !raw.trim() ||
      !Number.isSafeInteger(value) ||
      value < 16 ||
      value > 8192 ||
      value % 2 !== 0
    )
      errors[key] = '请输入16–8192之间的偶数像素。';
  }
  if (!errors.width && !errors.height && width * height > 3840 * 2160)
    errors.width = '总像素不能超过3840×2160，当前媒体基线为4K。';
  const [n, den] = d.frameRate === 'custom' ? [d.fpsNum, d.fpsDen] : d.frameRate.split('/');
  let fpsNum = Number(n),
    fpsDen = Number(den);
  if (!n?.trim() || !Number.isSafeInteger(fpsNum) || fpsNum < 1 || fpsNum > 240000)
    errors.fpsNum = '帧率分子需要1–240000整数。';
  if (!den?.trim() || !Number.isSafeInteger(fpsDen) || fpsDen < 1 || fpsDen > 120000)
    errors.fpsDen = '帧率分母需要1–120000整数。';
  if (!errors.fpsNum && !errors.fpsDen && (fpsNum / fpsDen < 2 || fpsNum / fpsDen > 120))
    errors.fpsNum = '当前帧率范围为2–120fps。';
  if (Object.keys(errors).length) return { name, settings: null, errors };
  let a = fpsNum,
    b = fpsDen;
  while (b) {
    [a, b] = [b, a % b];
  }
  fpsNum /= a;
  fpsDen /= a;
  return { name, settings: { width, height, fpsNum, fpsDen, sampleRate: 48000 }, errors };
}
