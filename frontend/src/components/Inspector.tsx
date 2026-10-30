import { useEffect, useId, useState } from 'react';
import { Captions, Film, Info, Layers3, Plus, SlidersHorizontal, Type } from 'lucide-react';
import type { Asset, Bootstrap, Clip, Project } from '../types';
import { fps, framesToSeconds, timecode, trackNames } from '../lib/editor';
import { Button, Field, Input, Select } from './ui';
import { useInspectorDrafts } from '../lib/use-inspector-drafts';
import { AssetSourceBrowser } from './AssetSourceBrowser';
import { editedAudioFade } from '../lib/audio-fade';

function NumberField({
  label,
  draftKey,
  base,
  value,
  min,
  max,
  step = 1,
  disabled,
  onCommit,
}: {
  label: string;
  draftKey: string;
  base?: number;
  value: number;
  min?: number;
  max?: number;
  step?: number;
  disabled?: boolean;
  onCommit: (value: number) => void;
}) {
  const [draft, setDraft] = useState(String(Math.round(value * 1000) / 1000));
  const [invalid, setInvalid] = useState(false);
  const errorID = useId();
  const sameDisplay = draft.trim() !== '' && Number(draft) === Math.round(value * 1000) / 1000;
  useEffect(() => setDraft(String(Math.round(value * 1000) / 1000)), [value]);
  const commit = (input: HTMLInputElement) => {
    if (sameDisplay) {
      setInvalid(false);
      return;
    }
    const number = Number(draft);
    if (
      draft.trim() &&
      input.validity.valid &&
      Number.isFinite(number) &&
      (min === undefined || number >= min) &&
      (max === undefined || number <= max)
    ) {
      setInvalid(false);
      if (number !== value && draft !== String(Math.round(value * 1000) / 1000)) onCommit(number);
    } else setInvalid(true);
  };
  return (
    <Field label={label}>
      <Input
        type="number"
        data-inspector-key={draftKey}
        data-inspector-display={String(Math.round(value * 1000) / 1000)}
        data-inspector-base={JSON.stringify(base ?? value)}
        aria-label={label}
        required
        value={draft}
        data-editor-draft={!sameDisplay ? 'true' : undefined}
        min={min}
        max={sameDisplay && max !== undefined ? Math.max(max, value) : max}
        step={step}
        disabled={disabled}
        aria-invalid={invalid}
        aria-describedby={invalid ? errorID : undefined}
        onChange={(e) => setDraft(e.target.value)}
        onBlur={(event) => commit(event.currentTarget)}
        onKeyDown={(e) => {
          if (e.key === 'Enter' && !e.nativeEvent.isComposing) e.currentTarget.blur();
        }}
      />
      {invalid ? (
        <span id={errorID} className="form-error">
          请输入{min !== undefined ? `不小于 ${min}` : '有效'}
          {max !== undefined ? `、不大于 ${max}` : ''}的数字。
        </span>
      ) : null}
    </Field>
  );
}

export function Inspector({
  project,
  clip,
  asset,
  busy,
  onUpdate,
  onAddAsset,
  onProjectName,
  onRelinkAsset,
  onImportFont,
  sourceEpoch,
  applyAnalysis,
}: {
  project: Project;
  clip?: Clip;
  asset?: Asset;
  busy: boolean;
  onUpdate: (clip: Clip) => void;
  onAddAsset: (asset: Asset) => void;
  onProjectName: (name: string) => void;
  onRelinkAsset?: (asset: Asset) => void;
  onImportFont?: () => void;
  sourceEpoch?: number;
  applyAnalysis?: (action: () => Promise<Bootstrap>) => Promise<Bootstrap | null>;
}) {
  const drafts = useInspectorDrafts(
    { projectId: project.id, projectRevision: project.revision, sourceEpoch: sourceEpoch ?? 0 },
    clip?.id || '',
  );
  const [text, setText] = useState(clip?.text || '');
  const [name, setName] = useState(project.name);
  const [clipName, setClipName] = useState(clip?.name || '');
  useEffect(() => {
    setText(clip?.text || '');
    setClipName(clip?.name || '');
  }, [clip?.id, clip?.text, clip?.name]);
  useEffect(() => setName(project.name), [project.name]);
  const track = project.tracks.find((t) => t.id === clip?.trackId);
  const disabled = busy || Boolean(track?.locked);
  const luts = project.assets.filter((a) => a.kind === 'lut');
  const fonts = project.assets.filter((a) => a.kind === 'font');

  if (asset && !clip)
    return (
      <div className="inspector-content" data-editor-draft-scope {...drafts.events}>
        <div className="inspector-section">
          <div className="inspector-caption">素材信息</div>
          <h3 className="asset-detail-name">{asset.name}</h3>
          <span className="quiet-badge">{trackNames[asset.kind] || asset.kind}</span>
        </div>
        {asset.kind !== 'font' ? (
          <AssetSourceBrowser
            projectId={project.id}
            projectRevision={project.revision}
            asset={asset}
            sourceEpoch={sourceEpoch}
            applyAnalysis={applyAnalysis}
          />
        ) : (
          <p className="field-hint">
            {asset.font?.family} · {asset.font?.style}。选择文字或字幕片段，在字体选项中使用。
          </p>
        )}
        {asset.kind === 'font' ? (
          <div className="inspector-section metadata-list">
            <div>
              <span>字体家族</span>
              <strong>{asset.font?.family || '未提供'}</strong>
            </div>
            <div>
              <span>样式</span>
              <strong>{asset.font?.style || '未提供'}</strong>
            </div>
            <div>
              <span>字重</span>
              <strong>{asset.font?.weight || '未提供'}</strong>
            </div>
          </div>
        ) : (
          <div className="inspector-section metadata-list">
            <div>
              <span>分辨率</span>
              <strong>
                {asset.width && asset.height ? `${asset.width} × ${asset.height}` : '—'}
              </strong>
            </div>
            <div>
              <span>时长</span>
              <strong>
                {asset.durationUs ? `${(asset.durationUs / 1e6).toFixed(2)} 秒` : '静态素材'}
              </strong>
            </div>
            <div>
              <span>帧率</span>
              <strong>
                {asset.fpsNum ? `${(asset.fpsNum / asset.fpsDen).toFixed(2)} fps` : '—'}
              </strong>
            </div>
            <div>
              <span>声音</span>
              <strong>{asset.hasAudio ? '包含音频' : '无'}</strong>
            </div>
          </div>
        )}
        <div className="inspector-section">
          <p className="field-hint file-path">{asset.path}</p>
          {onRelinkAsset ? (
            <Button variant="ghost" disabled={busy} onClick={() => onRelinkAsset(asset)}>
              重新定位素材
            </Button>
          ) : null}
          {asset.kind === 'font' &&
          (asset.font?.copyright || asset.font?.license || asset.font?.licenseUrl) ? (
            <details>
              <summary>查看字体文件内的声明</summary>
              <p className="field-hint font-notice">{asset.font.copyright}</p>
              <p className="field-hint font-notice">{asset.font.license}</p>
              <p className="field-hint file-path">{asset.font.licenseUrl}</p>
            </details>
          ) : null}
          {asset.kind !== 'lut' && asset.kind !== 'font' ? (
            <Button
              variant="primary"
              disabled={busy || asset.missing}
              onClick={() => onAddAsset(asset)}
            >
              <Plus size={14} />
              添加到时间线
            </Button>
          ) : (
            <p className="field-hint">
              {asset.kind === 'font'
                ? '工程副本会收集此字体文件。'
                : '选择视频或图片片段后，在色彩选项中应用此 LUT。'}
            </p>
          )}
        </div>
      </div>
    );

  if (!clip)
    return (
      <div className="inspector-content" data-editor-draft-scope {...drafts.events}>
        <div className="inspector-section">
          <div className="inspector-caption">工程设置</div>
          {drafts.status ? (
            <p role="status" className="field-hint">
              {drafts.status}
            </p>
          ) : null}
          <Field label="工程名称">
            <Input
              value={name}
              data-inspector-key="projectName"
              data-inspector-base={JSON.stringify(project.name)}
              data-editor-draft={name !== project.name ? 'true' : undefined}
              onChange={(e) => setName(e.target.value)}
              onBlur={() => {
                if (name.trim() && name !== project.name) onProjectName(name.trim());
              }}
              onKeyDown={(e) => {
                if (e.key === 'Enter') e.currentTarget.blur();
              }}
              disabled={busy}
            />
          </Field>
          <div className="metadata-list">
            <div>
              <span>画布</span>
              <strong>
                {project.settings.width} × {project.settings.height}
              </strong>
            </div>
            <div>
              <span>时间线帧率</span>
              <strong>{fps(project.settings).toFixed(2)} fps</strong>
            </div>
            <div>
              <span>音频采样率</span>
              <strong>{project.settings.sampleRate / 1000} kHz</strong>
            </div>
          </div>
        </div>
        <div className="inspector-empty">
          <SlidersHorizontal size={26} />
          <p>选择片段，调整细节</p>
          <span>
            位置、修剪、文字与声音
            <br />
            都从这里继续精修。
          </span>
        </div>
        <div className="inspector-tip">
          <Info size={14} />
          <span>双击素材可添加到播放头；选中片段后按 C 分割。</span>
        </div>
      </div>
    );

  const source = project.assets.find((a) => a.id === clip.assetId);
  return (
    <div className="inspector-content" key={clip.id} data-editor-draft-scope {...drafts.events}>
      <div className="inspector-section">
        {drafts.status ? (
          <p role="status" className="field-hint">
            {drafts.status}
          </p>
        ) : null}
        <div className="inspector-caption">
          {trackNames[clip.kind]}片段
          {track?.locked ? <span className="quiet-badge">轨道已锁定</span> : null}
        </div>
        <Field label="片段名称">
          <Input
            value={clipName}
            data-inspector-key="name"
            data-inspector-base={JSON.stringify(clip.name)}
            data-editor-draft={clipName !== clip.name ? 'true' : undefined}
            onChange={(e) => setClipName(e.target.value)}
            onBlur={() => {
              if (clipName.trim() && clipName !== clip.name)
                onUpdate({ ...clip, name: clipName.trim() });
            }}
            disabled={disabled}
          />
        </Field>
      </div>
      <div className="inspector-section">
        <h3>
          <Film size={14} />
          时间
        </h3>
        <div className="field-pair">
          <NumberField
            label="起点 · 帧"
            draftKey="startFrame"
            value={clip.startFrame}
            min={0}
            disabled={disabled}
            onCommit={(value) => onUpdate({ ...clip, startFrame: Math.round(value) })}
          />
          <NumberField
            label="长度 · 帧"
            draftKey="durationFrames"
            value={clip.durationFrames}
            min={1}
            disabled={disabled}
            onCommit={(value) => onUpdate({ ...clip, durationFrames: Math.round(value) })}
          />
        </div>
        <p className="field-hint mono">
          {timecode(clip.startFrame, project.settings)} —{' '}
          {timecode(clip.startFrame + clip.durationFrames, project.settings)}
        </p>
        {clip.assetId && clip.kind !== 'image' ? (
          <>
            <NumberField
              label="素材入点 · 秒"
              draftKey="sourceInUs"
              base={clip.sourceInUs}
              value={clip.sourceInUs / 1e6}
              min={0}
              step={0.001}
              disabled={disabled}
              onCommit={(value) => onUpdate({ ...clip, sourceInUs: Math.round(value * 1e6) })}
            />
            <Field label="播放速度">
              <Select
                value={`${clip.speedNum}/${clip.speedDen}`}
                disabled={disabled}
                onChange={(e) => {
                  const [num, den] = e.target.value.split('/').map(Number);
                  onUpdate({
                    ...clip,
                    speedNum: num,
                    speedDen: den,
                    durationFrames: Math.max(
                      1,
                      Math.floor(
                        (((clip.durationFrames * clip.speedNum) / clip.speedDen) * den) / num,
                      ),
                    ),
                  });
                }}
              >
                {[
                  ['1/2', '0.5×'],
                  ['1/1', '1×'],
                  ['5/4', '1.25×'],
                  ['3/2', '1.5×'],
                  ['2/1', '2×'],
                ].map(([value, label]) => (
                  <option value={value} key={value}>
                    {label}
                  </option>
                ))}
              </Select>
            </Field>
          </>
        ) : null}
        <p className="field-hint">
          使用时长 {framesToSeconds(clip.durationFrames, project.settings).toFixed(2)} 秒
        </p>
      </div>
      {clip.kind === 'text' || clip.kind === 'caption' ? (
        <div className="inspector-section">
          <h3>{clip.kind === 'caption' ? <Captions size={14} /> : <Type size={14} />}文字</h3>
          <Field label="字体">
            <Select
              aria-label="字体"
              disabled={disabled}
              value={clip.textStyle.fontAssetId || ''}
              onChange={(e) =>
                onUpdate({
                  ...clip,
                  textStyle: { ...clip.textStyle, fontAssetId: e.target.value || undefined },
                })
              }
            >
              <option value="">系统默认（未随工程固定）</option>
              {fonts.map((font) => (
                <option key={font.id} value={font.id}>
                  {font.font?.family || font.name} · {font.font?.style || '常规'}
                  {font.missing ? ' · 缺失' : ''}
                </option>
              ))}
            </Select>
            {onImportFont ? (
              <Button variant="ghost" disabled={busy} onClick={onImportFont}>
                导入工程字体
              </Button>
            ) : null}
            <p className="field-hint">
              静态TTF/OTF，单文件32MiB；随工程收集。缺失或缺字时先修复，最终用原生合成核对。
            </p>
          </Field>
          <Field label="显示内容">
            <textarea
              className="input text-content resize-none"
              rows={4}
              value={text}
              data-inspector-key="text"
              data-inspector-base={JSON.stringify(clip.text || '')}
              data-editor-draft={text !== (clip.text || '') ? 'true' : undefined}
              disabled={disabled}
              onChange={(e) => setText(e.target.value)}
              onBlur={() => {
                if (text !== clip.text) onUpdate({ ...clip, text });
              }}
            />
          </Field>
          <div className="field-pair">
            <NumberField
              label="字号 · px"
              draftKey="fontSize"
              value={clip.textStyle.fontSize || 48}
              min={8}
              max={500}
              disabled={disabled}
              onCommit={(value) =>
                onUpdate({ ...clip, textStyle: { ...clip.textStyle, fontSize: Math.round(value) } })
              }
            />
            <Field label="文字颜色">
              <Input
                type="color"
                aria-label="文字颜色"
                value={clip.textStyle.color || '#ffffff'}
                disabled={disabled}
                onChange={(e) =>
                  onUpdate({ ...clip, textStyle: { ...clip.textStyle, color: e.target.value } })
                }
              />
            </Field>
          </div>
        </div>
      ) : null}
      {clip.kind !== 'audio' ? (
        <div className="inspector-section">
          <h3>
            <Layers3 size={14} />
            画面变换
          </h3>
          <div className="field-pair">
            <NumberField
              label="水平位移 · px"
              draftKey="x"
              value={clip.transform.x}
              disabled={disabled}
              onCommit={(value) =>
                onUpdate({ ...clip, transform: { ...clip.transform, x: value } })
              }
            />
            <NumberField
              label="垂直位移 · px"
              draftKey="y"
              value={clip.transform.y}
              disabled={disabled}
              onCommit={(value) =>
                onUpdate({ ...clip, transform: { ...clip.transform, y: value } })
              }
            />
          </div>
          <div className="field-pair">
            <NumberField
              label="缩放"
              draftKey="scale"
              value={clip.transform.scale}
              min={0.01}
              max={10}
              step={0.01}
              disabled={disabled}
              onCommit={(value) =>
                onUpdate({ ...clip, transform: { ...clip.transform, scale: value } })
              }
            />
            <NumberField
              label="旋转 · °"
              draftKey="rotation"
              value={clip.transform.rotation}
              min={-360}
              max={360}
              step={1}
              disabled={disabled}
              onCommit={(value) =>
                onUpdate({ ...clip, transform: { ...clip.transform, rotation: value } })
              }
            />
          </div>
        </div>
      ) : null}
      {clip.kind === 'video' || clip.kind === 'audio' ? (
        <div className="inspector-section">
          <h3>声音</h3>
          <NumberField
            label="音量 · %"
            draftKey="volume"
            base={clip.volume}
            value={Math.round(clip.volume * 100)}
            min={0}
            max={100}
            disabled={disabled}
            onCommit={(value) => onUpdate({ ...clip, volume: value / 100 })}
          />
          <label className="checkbox-field">
            <input
              type="checkbox"
              checked={clip.muted}
              disabled={disabled}
              onChange={(e) => onUpdate({ ...clip, muted: e.target.checked })}
            />
            静音此片段
          </label>
          {project.assets.find((a) => a.id === clip.assetId)?.hasAudio ? (
            <>
              <div className="field-pair">
                <NumberField
                  label="淡入 · 秒"
                  draftKey="fadeInUs"
                  base={clip.audioFade?.inUs || 0}
                  value={(clip.audioFade?.inUs || 0) / 1e6}
                  min={0}
                  max={framesToSeconds(clip.durationFrames, project.settings)}
                  step={0.001}
                  disabled={disabled}
                  onCommit={(value) =>
                    onUpdate({
                      ...clip,
                      audioFade: editedAudioFade(clip, project.settings, 'in', value),
                    })
                  }
                />
                <NumberField
                  label="淡出 · 秒"
                  draftKey="fadeOutUs"
                  base={clip.audioFade?.outUs || 0}
                  value={(clip.audioFade?.outUs || 0) / 1e6}
                  min={0}
                  max={framesToSeconds(clip.durationFrames, project.settings)}
                  step={0.001}
                  disabled={disabled}
                  onCommit={(value) =>
                    onUpdate({
                      ...clip,
                      audioFade: editedAudioFade(clip, project.settings, 'out', value),
                    })
                  }
                />
              </div>
              <p className="field-hint">
                按片段输出时间线性淡化，可撤销；两端总时长不超过片段长度；另一端过长时会缩短。工作区声音为近似预览。
              </p>
              {clip.audioFade &&
              (clip.audioFade.offsetFrames > 0 ||
                clip.audioFade.spanFrames !== clip.durationFrames) ? (
                <p className="field-hint">此段沿用分割前的淡化曲线；修改数值会按当前片段重设。</p>
              ) : null}
              {clip.audioFade ? (
                <Button
                  disabled={disabled}
                  onClick={() => onUpdate({ ...clip, audioFade: undefined })}
                >
                  清除淡入淡出
                </Button>
              ) : null}
            </>
          ) : null}
        </div>
      ) : null}
      {clip.kind === 'video' || clip.kind === 'image' ? (
        <div className="inspector-section">
          <h3>色彩与 LUT</h3>
          <Field label="LUT">
            <Select
              value={clip.lut?.assetId || ''}
              disabled={disabled}
              onChange={(e) =>
                onUpdate({
                  ...clip,
                  lut: e.target.value ? { assetId: e.target.value, intensity: 1 } : undefined,
                })
              }
            >
              <option value="">不使用 LUT</option>
              {luts.map((lut) => (
                <option key={lut.id} value={lut.id}>
                  {lut.name}
                </option>
              ))}
            </Select>
          </Field>
          {clip.lut ? (
            <NumberField
              label="LUT 强度 · %"
              draftKey="lutIntensity"
              base={clip.lut?.intensity}
              value={Math.round(clip.lut.intensity * 100)}
              min={0}
              max={100}
              disabled={disabled}
              onCommit={(value) =>
                onUpdate({ ...clip, lut: { ...clip.lut!, intensity: value / 100 } })
              }
            />
          ) : null}
          <p className="field-hint">工作区画面未应用 LUT；使用合成检查核对输出。</p>
        </div>
      ) : null}
      {source ? (
        <div className="inspector-section">
          <div className="inspector-caption">源素材</div>
          <p className="truncate" title={source.name}>
            {source.name}
          </p>
          <p className="field-hint file-path">{source.path}</p>
          {source.missing ? (
            <p className="field-hint">源文件缺失。请选择同一素材的新位置，保留当前片段与编排。</p>
          ) : null}
          {onRelinkAsset ? (
            <Button variant="ghost" disabled={busy} onClick={() => onRelinkAsset(source)}>
              重新定位素材
            </Button>
          ) : null}
        </div>
      ) : null}
    </div>
  );
}
