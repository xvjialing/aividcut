import { useEffect, useId, useRef, useState } from 'react';
import type { Bootstrap, Project } from '../types';
import type {
  WorkflowAPI,
  WorkflowDefinition,
  WorkflowPreset,
  WorkflowPreview,
} from '../workflow-types';
import { workflowBridge } from '../lib/bridge';
import {
  validateWorkflowParameters,
  workflowDefaults,
  workflowParameterInactive,
} from '../lib/workflows';
import { useSettingsFormState, type SettingsFormEvents } from '../lib/settings';
import { Button, Field, Input, Modal, Select } from './ui';

export type WorkflowPresetDraft = {
  workflowId: string;
  parameters: Record<string, unknown>;
  presetId?: string;
  presetVersion?: number;
};
export const workflowPresetsChangedEvent = 'settings:workflow-presets-changed';

export function WorkflowPanel({
  project,
  selection,
  onBootstrap,
  prepareProject,
  applyToProject,
  api = workflowBridge,
  presetManagement = false,
  initialDraft,
  onManagePresets,
  onBusyChange,
  onDirtyChange,
}: {
  project: Project;
  selection: string[];
  onBootstrap: (value: Bootstrap) => void;
  prepareProject?: () => Promise<Project | null>;
  applyToProject?: (action: () => Promise<Bootstrap>) => Promise<Bootstrap | null>;
  api?: WorkflowAPI;
  presetManagement?: boolean;
  initialDraft?: WorkflowPresetDraft | null;
  onManagePresets?: (draft: WorkflowPresetDraft) => void;
} & SettingsFormEvents) {
  const [dirty, setDirty] = useState(false);
  const [definitions, setDefinitions] = useState<WorkflowDefinition[]>([]);
  const [presets, setPresets] = useState<WorkflowPreset[]>([]);
  const [definitionID, setDefinitionID] = useState('');
  const [parameters, setParameters] = useState<Record<string, unknown>>({});
  const [scope, setScope] = useState<'project' | 'selection'>(
    selection.length ? 'selection' : 'project',
  );
  const [presetID, setPresetID] = useState('');
  const [presetBaseVersion, setPresetBaseVersion] = useState<number | null>(null);
  const [presetName, setPresetName] = useState('');
  const [presetDialog, setPresetDialog] = useState<'new' | 'update' | 'delete' | null>(null);
  const [preview, setPreview] = useState<WorkflowPreview | null>(null);
  const [loaded, setLoaded] = useState(false);
  const [busy, setBusy] = useState('');
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [effectLimit, setEffectLimit] = useState(20);
  useSettingsFormState(Boolean(busy), presetManagement && dirty, { onBusyChange, onDirtyChange });
  const pending = useRef(false);
  const alive = useRef(false);
  const reads = useRef(0);
  const projectRef = useRef(project);
  projectRef.current = project;
  const form = useRef<HTMLFormElement>(null);
  const prefix = useId();
  const definition = definitions.find((value) => value.id === definitionID);
  const preset = presets.find((value) => value.id === presetID);
  const activeSelection = selection.filter((id) => project.clips.some((clip) => clip.id === id));
  const selectionKey = scope === 'selection' ? [...activeSelection].sort().join('\0') : '';
  const rangeKey = `${scope}:${selectionKey}`;
  const rangeRef = useRef(rangeKey);
  rangeRef.current = rangeKey;
  useEffect(() => setPreview(null), [rangeKey]);
  const presetMatches = Boolean(
    preset &&
    definition &&
    preset.workflowId === definition.id &&
    preset.workflowVersion === definition.version,
  );
  const previewMatches = Boolean(
    preview &&
    !preview.applied &&
    preview.projectId === project.id &&
    preview.projectRevision === project.revision &&
    preview.request.baseRevision === project.revision,
  );
  async function load() {
    const token = ++reads.current;
    try {
      const [catalog, values] = await Promise.all([
        api.getWorkflowCatalog(),
        api.getWorkflowPresets(),
      ]);
      if (!alive.current || token !== reads.current) return;
      setDefinitions(catalog || []);
      setPresets(values || []);
      setPresetID((current) => (values?.some((value) => value.id === current) ? current : ''));
      setDefinitionID((current) =>
        catalog?.some((value) => value.id === (initialDraft?.workflowId || current))
          ? initialDraft?.workflowId || current
          : catalog?.[0]?.id || '',
      );
      setLoaded(true);
    } catch (cause) {
      if (alive.current) setError(cause instanceof Error ? cause.message : '工作流读取失败。');
    }
  }
  useEffect(() => {
    alive.current = true;
    void load();
    return () => {
      alive.current = false;
      reads.current++;
    };
  }, [api]);
  useEffect(() => {
    if (loaded) void load();
  }, [project.id, project.revision]);
  useEffect(() => {
    if (definition)
      setParameters(
        initialDraft?.workflowId === definition.id
          ? { ...workflowDefaults(definition), ...initialDraft.parameters }
          : workflowDefaults(definition),
      );
    setDirty(false);
    if (definition?.id === 'audio-fades') setScope('selection');
    setPreview(null);
    setErrors({});
    setPresetID(
      initialDraft && initialDraft.workflowId === definition?.id ? initialDraft.presetId || '' : '',
    );
    setPresetBaseVersion(initialDraft?.presetVersion ?? null);
    setPresetDialog(null);
  }, [definition?.id, definition?.version, project.id]);
  function validated() {
    if (!definition) return null;
    const result = validateWorkflowParameters(definition, parameters);
    setErrors(result.errors);
    if (Object.keys(result.errors).length) {
      setTimeout(
        () => form.current?.querySelector<HTMLElement>('[aria-invalid="true"]')?.focus(),
        0,
      );
      return null;
    }
    return result.values;
  }
  async function generatePreview(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (presetManagement || pending.current || !definition?.available) return;
    const values = validated();
    if (!values) return;
    if (scope === 'selection' && !activeSelection.length) {
      setError('请先选择片段，或明确改为整个当前工程。');
      return;
    }
    pending.current = true;
    const requestedRange = rangeRef.current;
    setBusy('预览影响');
    setError('');
    try {
      const current = prepareProject ? await prepareProject() : projectRef.current;
      if (!current) return;
      if (current.id !== project.id || current.revision !== project.revision) {
        setError('工程已变化，请核对参数后重新预览。');
        return;
      }
      const value = await api.previewWorkflow({
        workflowId: definition.id,
        scope,
        selection: scope === 'selection' ? activeSelection : [],
        parameters: values,
      });
      if (
        alive.current &&
        requestedRange === rangeRef.current &&
        value.projectId === projectRef.current.id &&
        value.projectRevision === projectRef.current.revision
      ) {
        setPreview(value);
        setEffectLimit(20);
        setNotice('预览已生成，工程尚未修改。');
      } else if (alive.current) {
        setNotice('选区、范围或工程已变化，请重新预览；旧结果未载入。');
      }
    } catch (cause) {
      if (alive.current)
        setError(cause instanceof Error ? cause.message : '预览失败，工程未修改。');
    } finally {
      pending.current = false;
      if (alive.current) setBusy('');
    }
  }
  async function apply() {
    if (pending.current || !preview || !previewMatches) return;
    pending.current = true;
    setBusy('应用工作流');
    setError('');
    try {
      const action = () => api.applyWorkflowPreview(preview.id);
      const value = await (applyToProject ? applyToProject(action) : action());
      if (value) {
        if (!applyToProject) onBootstrap(value);
        setPreview(null);
        setNotice('工作流已应用，可使用撤销恢复。');
      }
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : '应用失败，请重新核对工程版本。');
    } finally {
      pending.current = false;
      if (alive.current) setBusy('');
    }
  }
  async function savePreset() {
    if (pending.current || !definition || !presetName.trim()) {
      setError('请填写预设名称。');
      return;
    }
    const values = validated();
    if (!values) return;
    pending.current = true;
    setBusy('保存预设');
    setError('');
    try {
      const value = await api.saveWorkflowPreset({
        name: presetName.trim(),
        workflowId: definition.id,
        parameters: values,
        ...(presetDialog === 'update' && preset
          ? { id: preset.id, baseVersion: presetBaseVersion ?? preset.version }
          : {}),
      });
      setPresets((current) => [...current.filter((item) => item.id !== value.id), value]);
      setPresetID(value.id);
      setPresetBaseVersion(value.version);
      setPresetDialog(null);
      setDirty(false);
      window.dispatchEvent(new Event(workflowPresetsChangedEvent));
      setNotice('参数预设已保存，没有应用到工程。');
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : '预设保存失败，输入保留。');
    } finally {
      pending.current = false;
      if (alive.current) setBusy('');
    }
  }
  async function deletePreset() {
    if (!preset || pending.current) return;
    pending.current = true;
    setBusy('删除预设');
    setError('');
    try {
      await api.deleteWorkflowPreset(preset.id, presetBaseVersion ?? preset.version);
      setPresets((current) => current.filter((value) => value.id !== preset.id));
      setPresetID('');
      setPresetDialog(null);
      setDirty(false);
      window.dispatchEvent(new Event(workflowPresetsChangedEvent));
      setNotice('预设已删除，当前参数与工程保留。');
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : '预设删除失败。');
    } finally {
      pending.current = false;
      if (alive.current) setBusy('');
    }
  }
  useEffect(() => {
    const changed = () => {
      if (!presetManagement) void load();
    };
    window.addEventListener(workflowPresetsChangedEvent, changed);
    return () => window.removeEventListener(workflowPresetsChangedEvent, changed);
  }, [api, presetManagement]);
  return (
    <div className="agent-panel workflow-panel">
      <p className="field-hint">
        {presetManagement
          ? '管理可复用参数，保存预设不会执行工作流或修改工程；应用时再检查素材与轨道条件。'
          : '工作流将参数与已有证据编译为可撤销编辑；预览不调用生成模型。'}
      </p>
      {error ? (
        <p className="form-error" role="alert">
          {error}
        </p>
      ) : null}
      {notice ? (
        <p className="agent-notice" role="status">
          {notice}
        </p>
      ) : null}
      {!loaded ? (
        <div className="inspector-empty">
          <p>工作流目录尚未就绪</p>
          <Button onClick={() => void load()}>重新读取工作流</Button>
        </div>
      ) : (
        <>
          <Field label="工作流">
            <Select
              value={definitionID}
              disabled={Boolean(busy)}
              onChange={(event) => setDefinitionID(event.target.value)}
            >
              {definitions.map((value) => (
                <option key={value.id} value={value.id}>
                  {value.name}
                  {presetManagement || value.available ? '' : '（前置条件未满足）'}
                </option>
              ))}
            </Select>
          </Field>
          {definition ? (
            <>
              <p className="field-hint">
                {definition.description} · 版本 {definition.version}
              </p>
              {!definition.available && !presetManagement ? (
                <p className="agent-warning">
                  {definition.reason || '当前工程没有此工作流所需的素材或分析证据。'}
                </p>
              ) : null}
              <Field label="参数预设">
                <Select
                  value={presetID}
                  disabled={Boolean(busy)}
                  onChange={(event) => {
                    setPresetID(event.target.value);
                    setPresetBaseVersion(
                      presets.find((value) => value.id === event.target.value)?.version ?? null,
                    );
                  }}
                >
                  <option value="">选择预设，不自动载入</option>
                  {presets
                    .filter((value) => value.workflowId === definition.id)
                    .map((value) => (
                      <option value={value.id} key={value.id}>
                        {value.name} · 预设修订 {value.version}
                        {value.workflowVersion !== definition.version ? '（工作流版本不同）' : ''}
                      </option>
                    ))}
                </Select>
              </Field>
              {onManagePresets ? (
                <Button
                  variant="ghost"
                  disabled={Boolean(busy)}
                  onClick={() =>
                    onManagePresets({
                      workflowId: definition.id,
                      parameters: { ...parameters },
                      presetId: presetID,
                      presetVersion: presetBaseVersion ?? preset?.version,
                    })
                  }
                >
                  管理预设
                </Button>
              ) : null}
              {preset ? (
                <div className="speech-actions">
                  <Button
                    disabled={Boolean(busy) || !presetMatches}
                    onClick={() => {
                      setParameters({ ...workflowDefaults(definition), ...preset.parameters });
                      setPreview(null);
                      setErrors({});
                    }}
                  >
                    载入预设参数
                  </Button>
                  {!onManagePresets ? (
                    <Button
                      variant="ghost"
                      disabled={Boolean(busy)}
                      onClick={() => {
                        setError('');
                        setPresetDialog('delete');
                      }}
                    >
                      删除此预设…
                    </Button>
                  ) : null}
                </div>
              ) : null}
              {preset && !presetMatches ? (
                <p className="agent-warning">
                  预设与当前工作流版本不匹配，未自动转换或载入。可核对后用当前参数另存。
                </p>
              ) : null}
              <form
                ref={form}
                noValidate
                className="agent-form"
                onChange={() => {
                  if (presetManagement) setDirty(true);
                }}
                onSubmit={(event) => {
                  if (presetManagement) {
                    event.preventDefault();
                    return;
                  }
                  void generatePreview(event);
                }}
              >
                {!presetManagement ? (
                  <>
                    <Field label={definition.id === 'talking-head' ? '停顿候选来源' : '操作范围'}>
                      <Select
                        value={scope}
                        disabled={Boolean(busy)}
                        onChange={(event) => {
                          setScope(event.target.value as 'project' | 'selection');
                          setPreview(null);
                        }}
                      >
                        <option value="project">整个当前工程</option>
                        <option value="selection">仅当前选中片段</option>
                      </Select>
                    </Field>
                    {scope === 'selection' ? (
                      <p className="field-hint">
                        {activeSelection.length
                          ? activeSelection
                              .map((id) => project.clips.find((clip) => clip.id === id)?.name || id)
                              .join('、')
                          : '尚未选择片段，不会扩大为整个工程。'}
                      </p>
                    ) : null}
                  </>
                ) : null}
                {definition.parameters.map((parameter) => {
                  const inactive = workflowParameterInactive(definition, parameters, parameter.id);
                  const value = parameters[parameter.id];
                  const invalid = {
                    'aria-invalid': !inactive && Boolean(errors[parameter.id]),
                    'aria-describedby': errors[parameter.id]
                      ? `${prefix}-${parameter.id}`
                      : undefined,
                  };
                  const options = parameter.options || [];
                  const change = (next: unknown) => {
                    setParameters((current) => ({ ...current, [parameter.id]: next }));
                    setErrors((current) =>
                      Object.fromEntries(
                        Object.entries(current).filter(
                          ([key]) =>
                            key !== parameter.id &&
                            !workflowParameterInactive(
                              definition,
                              { ...parameters, [parameter.id]: next },
                              key,
                            ),
                        ),
                      ),
                    );
                    setPreview(null);
                  };
                  return (
                    <Field
                      key={parameter.id}
                      label={parameter.name}
                      hint={inactive ? '清除已有淡化时不使用本项。' : parameter.description}
                    >
                      {parameter.type === 'boolean' ? (
                        <input
                          type="checkbox"
                          checked={value === true}
                          disabled={Boolean(busy) || inactive}
                          onChange={(event) => change(event.target.checked)}
                          {...invalid}
                        />
                      ) : ['select', 'asset', 'track'].includes(parameter.type) ? (
                        <Select
                          value={typeof value === 'string' ? value : ''}
                          disabled={Boolean(busy) || inactive}
                          onChange={(event) => change(event.target.value)}
                          {...invalid}
                        >
                          <option value="">请选择</option>
                          {options.map((option) => (
                            <option value={option.value} key={option.value}>
                              {option.name}
                            </option>
                          ))}
                        </Select>
                      ) : (
                        <Input
                          type={['number', 'integer'].includes(parameter.type) ? 'number' : 'text'}
                          value={
                            typeof value === 'string' || typeof value === 'number' ? value : ''
                          }
                          min={parameter.min}
                          max={parameter.max}
                          step={parameter.step || (parameter.type === 'integer' ? 1 : 'any')}
                          required={parameter.required}
                          disabled={Boolean(busy) || inactive}
                          onChange={(event) => change(event.target.value)}
                          {...invalid}
                        />
                      )}
                      {!inactive && errors[parameter.id] ? (
                        <span className="form-error" id={`${prefix}-${parameter.id}`}>
                          {errors[parameter.id]}
                        </span>
                      ) : null}
                    </Field>
                  );
                })}
                <div className="speech-actions">
                  {!presetManagement ? (
                    <>
                      <Button
                        type="submit"
                        variant="primary"
                        busy={Boolean(busy)}
                        disabled={
                          !definition.available ||
                          (scope === 'selection' && !activeSelection.length)
                        }
                      >
                        预览影响
                      </Button>
                    </>
                  ) : null}
                  {!onManagePresets ? (
                    <>
                      <Button
                        disabled={Boolean(busy)}
                        onClick={() => {
                          setPresetName(definition.name);
                          setError('');
                          setPresetDialog('new');
                        }}
                      >
                        保存为新预设…
                      </Button>
                      {presetMatches ? (
                        <Button
                          disabled={Boolean(busy)}
                          onClick={() => {
                            setPresetName(preset!.name);
                            setError('');
                            setPresetDialog('update');
                          }}
                        >
                          更新此预设…
                        </Button>
                      ) : null}
                    </>
                  ) : null}
                </div>
              </form>
            </>
          ) : null}
          {preview ? (
            <section className="agent-plan">
              <h3>预览影响</h3>
              <p>{preview.summary}</p>
              <p className="field-hint">
                {preview.speechRippleSummary ? (
                  <>
                    变更原片段 {preview.speechRippleSummary.changedOriginalClips} 个，新增存续片段{' '}
                    {preview.speechRippleSummary.newClips} 个；
                  </>
                ) : (
                  <>
                    影响{' '}
                    {preview.rangeSummary?.totalAffectedClips ??
                      preview.affectedClipIds?.length ??
                      0}{' '}
                    个片段；
                  </>
                )}
                成片缩短 {preview.savedFrames} 帧。这不是人工工时估算。
              </p>
              {(preview.warnings || []).map((warning, index) => (
                <p className="agent-warning" key={index}>
                  {warning}
                </p>
              ))}
              <ol className="agent-command-list">
                {(preview.effects || []).slice(0, effectLimit).map((effect, index) => (
                  <li key={index}>
                    <strong>{effect.description}</strong>
                    <span>
                      {effect.startFrame} → {effect.endFrame} 帧
                      {effect.clipId ? ` · ${effect.clipId}` : ''}
                    </span>
                  </li>
                ))}
              </ol>
              {(preview.effects || []).length > effectLimit ? (
                <Button onClick={() => setEffectLimit((value) => value + 20)}>显示更多影响</Button>
              ) : null}
              <details className="agent-details">
                <summary>证据与结构化计划</summary>
                {(preview.evidence || []).slice(0, effectLimit).map((evidence, index) => (
                  <p key={index}>
                    {evidence.description} · 源 {evidence.sourceStartUs}–{evidence.sourceEndUs} 微秒
                    · 工程 {evidence.startFrame}–{evidence.endFrame} 帧
                    {evidence.transcriptId ? ` · 转写 ${evidence.transcriptId}` : ''}
                  </p>
                ))}
                {(preview.evidence || []).length > effectLimit ? (
                  <Button onClick={() => setEffectLimit((value) => value + 20)}>
                    显示更多证据
                  </Button>
                ) : null}
                <pre>{JSON.stringify(preview.request, null, 2)}</pre>
              </details>
              {!previewMatches ? (
                <p className="agent-warning">原工程或版本已变化，请重新预览；不会套用旧计划。</p>
              ) : null}
              <Button
                variant="primary"
                disabled={Boolean(busy) || !previewMatches}
                onClick={() => void apply()}
              >
                应用预览并保留撤销
              </Button>
            </section>
          ) : null}
        </>
      )}
      <Modal
        open={Boolean(presetDialog)}
        title={
          presetDialog === 'delete'
            ? '删除此参数预设？'
            : presetDialog === 'update'
              ? '更新参数预设'
              : '保存参数预设'
        }
        description={
          presetDialog === 'delete'
            ? '只删除预设，不删除素材或改变当前工程。'
            : '保存当前工作流参数，不会应用编辑或发起模型调用。'
        }
        busy={Boolean(busy)}
        onOpenChange={(value) => {
          if (!value && !busy) setPresetDialog(null);
        }}
      >
        {presetDialog !== 'delete' ? (
          <Field label="预设名称">
            <Input
              value={presetName}
              disabled={Boolean(busy)}
              onChange={(event) => {
                setPresetName(event.target.value);
                if (presetManagement) setDirty(true);
              }}
            />
          </Field>
        ) : (
          <p>
            {preset?.name} · 修订 {preset?.version}
          </p>
        )}
        {error ? (
          <p className="form-error" role="alert">
            {error}
          </p>
        ) : null}
        <div className="dialog-actions">
          <Button disabled={Boolean(busy)} onClick={() => setPresetDialog(null)}>
            取消
          </Button>
          <Button
            variant={presetDialog === 'delete' ? 'danger' : 'primary'}
            busy={Boolean(busy)}
            onClick={() => void (presetDialog === 'delete' ? deletePreset() : savePreset())}
          >
            {presetDialog === 'delete' ? '确认删除预设' : '保存预设'}
          </Button>
        </div>
      </Modal>
    </div>
  );
}
