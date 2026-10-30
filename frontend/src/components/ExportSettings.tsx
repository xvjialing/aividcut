import type { ExportDraft } from '../lib/export-options';
import { rendererNames } from '../lib/export-options';
import { Field, Input, Select } from './ui';

export function ExportSettings({
  value,
  onChange,
  duration,
  nativeAvailable,
  disabled,
  invalidField,
}: {
  value: ExportDraft;
  onChange: (value: ExportDraft) => void;
  duration: number;
  nativeAvailable: boolean;
  disabled?: boolean;
  invalidField?: 'from' | 'to';
}) {
  const update = <K extends keyof ExportDraft>(key: K, next: ExportDraft[K]) =>
    onChange({ ...value, [key]: next });
  return (
    <div className="export-settings">
      <Field
        label="合成方式"
        hint={
          value.renderer === 'auto'
            ? nativeAvailable
              ? '本次使用原生合成，沿用实时预览的文字与画面效果。'
              : '原生工具未配置，本次使用FFmpeg合成。'
            : value.renderer === 'native'
              ? '沿用实时预览；不支持的工程效果会在任务中说明原因。'
              : '文字和效果取决于本机FFmpeg能力，结果可能与原生预览有差异。'
        }
      >
        <Select
          value={value.renderer}
          disabled={disabled}
          onChange={(e) => update('renderer', e.target.value as ExportDraft['renderer'])}
        >
          <option value="auto">{rendererNames.auto}</option>
          <option value="native" disabled={!nativeAvailable}>
            {rendererNames.native}
            {!nativeAvailable ? ' · 未配置' : ''}
          </option>
          <option value="ffmpeg">{rendererNames.ffmpeg}</option>
        </Select>
      </Field>
      <div className="export-fields-row">
        <Field label="输出画幅">
          <Select
            value={value.preset}
            disabled={disabled}
            onChange={(e) => update('preset', e.target.value as ExportDraft['preset'])}
          >
            <option value="project">工程原画幅</option>
            <option value="720p">720p</option>
            <option value="1080p">1080p</option>
            <option value="4k">4K</option>
          </Select>
        </Field>
        <Field label="视频编码">
          <Select
            value={value.encoder}
            disabled={disabled}
            onChange={(e) =>
              onChange({
                ...value,
                encoder: e.target.value,
                resourceMode: e.target.value === 'libx264' ? value.resourceMode : 'standard',
              })
            }
          >
            <option value="libx264">H.264 软件编码</option>
            <option value="mpeg4">MPEG-4 软件编码</option>
            <option value="h264_videotoolbox">H.264 苹果硬件编码</option>
          </Select>
        </Field>
      </div>
      <p className="field-hint">
        画幅按长边等比缩放；编码器需本机媒体工具支持，硬件编码会在实际任务中验证。
      </p>
      <Field label="导出范围">
        <Select
          value={value.range}
          disabled={disabled}
          onChange={(e) =>
            onChange({
              ...value,
              range: e.target.value as ExportDraft['range'],
              to: value.to || String(duration),
            })
          }
        >
          <option value="all">整条时间线</option>
          <option value="custom">自定义帧区间</option>
        </Select>
      </Field>
      {value.range === 'custom' ? (
        <div className="export-fields-row">
          <Field label="起始帧（包含）">
            <Input
              type="number"
              data-export-field="from"
              aria-invalid={invalidField === 'from' || undefined}
              aria-describedby={invalidField === 'from' ? 'dialog-error' : undefined}
              required
              min={0}
              max={Math.max(0, duration - 1)}
              step={1}
              value={value.from}
              disabled={disabled}
              onChange={(e) => update('from', e.target.value)}
            />
          </Field>
          <Field label="结束帧（不包含）">
            <Input
              type="number"
              data-export-field="to"
              aria-invalid={invalidField === 'to' || undefined}
              aria-describedby={invalidField === 'to' ? 'dialog-error' : undefined}
              required
              min={Math.max(1, Number.isFinite(Number(value.from)) ? Number(value.from) + 1 : 1)}
              max={duration}
              step={1}
              value={value.to}
              disabled={disabled}
              onChange={(e) => update('to', e.target.value)}
            />
          </Field>
        </div>
      ) : null}
      <Field
        label="编码资源"
        hint={
          value.resourceMode === 'lower_memory'
            ? '减少编码缓存，导出可能更慢，文件大小和压缩画质可能变化；画幅、帧数和声音仍按原设置。此档位不是内存上限。'
            : value.encoder === 'libx264'
              ? '标准档位沿用当前编码设置。内存仍取决于画幅、内容和媒体工具。'
              : '当前编码器使用标准档位，较低内存仅适用于H.264软件编码。'
        }
      >
        <Select
          aria-label="编码资源"
          disabled={disabled || value.encoder !== 'libx264'}
          value={value.resourceMode || 'standard'}
          onChange={(e) => update('resourceMode', e.target.value as ExportDraft['resourceMode'])}
        >
          <option value="standard">标准</option>
          <option value="lower_memory">较低内存</option>
        </Select>
      </Field>
      <label className="checkbox-field">
        <input
          type="checkbox"
          checked={value.overwrite}
          disabled={disabled}
          onChange={(e) => update('overwrite', e.target.checked)}
        />
        允许替换同名输出文件
      </label>
    </div>
  );
}
