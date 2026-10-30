import { describe, expect, it } from 'vitest';
import { defaultExportDraft, exportOptions, ExportRangeError } from '../src/lib/export-options';

describe('视频导出范围与方式', () => {
  it('整条时间线不固化旧结束帧，保留明确合成、画幅和编码选择', () => {
    expect(
      exportOptions(
        {
          ...defaultExportDraft,
          renderer: 'native',
          preset: '4k',
          encoder: 'mpeg4',
          from: '9',
          to: '10',
        },
        300,
      ),
    ).toEqual({ renderer: 'native', preset: '4k', videoEncoder: 'mpeg4', overwrite: false });
  });
  it('自定义范围使用包含/不包含的整数帧，不按近似秒数转换', () => {
    expect(
      exportOptions(
        {
          ...defaultExportDraft,
          renderer: 'ffmpeg',
          range: 'custom',
          from: '3',
          to: '17',
          overwrite: true,
        },
        30,
      ),
    ).toMatchObject({ renderer: 'ffmpeg', fromFrame: 3, toFrame: 17, overwrite: true });
  });
  it('非法或已超出当前时间线的范围拒绝并指向需要修正的字段', () => {
    for (const [from, to, field] of [
      ['', '10', 'from'],
      ['-1', '10', 'from'],
      ['1.5', '10', 'from'],
      ['3', '3', 'to'],
      ['3', '31', 'to'],
      ['3', '', 'to'],
      ['30', '31', 'from'],
    ]) {
      try {
        exportOptions({ ...defaultExportDraft, range: 'custom', from, to }, 30);
        throw new Error('不应成功');
      } catch (error) {
        expect(error).toBeInstanceOf(ExportRangeError);
        expect((error as ExportRangeError).field).toBe(field);
      }
    }
  });
});
