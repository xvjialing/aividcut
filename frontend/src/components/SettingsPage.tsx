import { useEffect, useRef, type ReactNode } from 'react';
import {
  ArrowLeft,
  Check,
  Plug,
  SunMoon,
  Bot,
  SlidersHorizontal,
  Activity,
  Workflow,
  Database,
  Sparkles,
} from 'lucide-react';
import type { Appearance, useAppearance } from '../lib/appearance';
import { Button } from './ui';

const options: { value: Appearance; label: string }[] = [
  { value: 'light', label: '浅色模式' },
  { value: 'dark', label: '深色模式' },
  { value: 'system', label: '跟随系统' },
];

export const settingsNavigation = [
  { group: '偏好', items: [{ value: 'appearance', label: '外观', icon: SunMoon }] },
  {
    group: 'AI 与连接',
    items: [
      { value: 'models', label: '模型服务', icon: Sparkles },
      { value: 'local-ai', label: '本地 AI', icon: Bot },
      { value: 'mcp', label: 'MCP', icon: Plug },
    ],
  },
  {
    group: '应用',
    items: [
      { value: 'media', label: '媒体工具', icon: SlidersHorizontal },
      { value: 'diagnostics', label: '诊断', icon: Activity },
    ],
  },
  {
    group: '创作与资料',
    items: [
      { value: 'presets', label: '工作流预设', icon: Workflow },
      { value: 'storage', label: '存储与恢复', icon: Database },
    ],
  },
] as const;
export type SettingsSection = (typeof settingsNavigation)[number]['items'][number]['value'];
export const settingsHeading = (section: SettingsSection) =>
  settingsNavigation.flatMap((group) => [...group.items]).find((item) => item.value === section)
    ?.label || '设置';

export function SettingsPage({
  appearance,
  onReturn,
  section,
  onSectionChange,
  mcpContent,
  contents,
  busy = false,
}: {
  appearance: ReturnType<typeof useAppearance>;
  onReturn: () => void;
  section: SettingsSection;
  onSectionChange: (section: SettingsSection) => void;
  mcpContent?: ReactNode;
  contents?: Partial<Record<SettingsSection, ReactNode>>;
  busy?: boolean;
}) {
  const title = useRef<HTMLHeadingElement>(null);
  useEffect(() => title.current?.focus(), [section]);
  const heading = settingsHeading(section);

  return (
    <div className="settings-shell" data-settings-page>
      <div className="settings-drag-region" aria-hidden="true" />
      <aside className="settings-sidebar" aria-label="设置导航">
        <Button id="settings-return" variant="ghost" onClick={onReturn} disabled={busy}>
          <ArrowLeft size={17} aria-hidden="true" />
          返回应用
        </Button>
        <nav aria-label="设置分类">
          {settingsNavigation.map((group) => (
            <div key={group.group}>
              <p className="settings-nav-label">{group.group}</p>
              {group.items.map(({ value, label, icon: Icon }) => (
                <button
                  type="button"
                  key={value}
                  className="settings-nav-item"
                  aria-current={section === value ? 'page' : undefined}
                  disabled={busy}
                  onClick={() => onSectionChange(value)}
                >
                  <Icon size={18} aria-hidden="true" />
                  {label}
                </button>
              ))}
            </div>
          ))}
        </nav>
      </aside>
      <main className="settings-main" aria-labelledby="settings-page-title">
        <header className="settings-header">
          <h1 id="settings-page-title" tabIndex={-1} ref={title}>
            {heading}
          </h1>
        </header>
        <div className="settings-content" key={section} id={`settings-${section}`}>
          {section === 'appearance' ? (
            <section className="settings-section" aria-labelledby="appearance-section-title">
              <h2 id="appearance-section-title">主题</h2>
              <fieldset
                className="settings-group appearance-settings"
                aria-describedby="appearance-hint"
              >
                <legend className="sr-only">明暗模式</legend>
                <div className="settings-appearance-row">
                  <div className="settings-row-copy">
                    <h3>明暗模式</h3>
                    <p id="appearance-hint">选择浅色、深色或跟随系统，立即应用到整个界面。</p>
                  </div>
                  <div className="appearance-options">
                    {options.map(({ value, label }) => (
                      <label className="appearance-option" key={value}>
                        <input
                          className="sr-only"
                          type="radio"
                          name="appearance"
                          value={value}
                          checked={appearance.preference === value}
                          onChange={() => appearance.choose(value)}
                        />
                        <span
                          className={`appearance-preview appearance-preview-${value}`}
                          aria-hidden="true"
                        >
                          {(value === 'system' ? ['light', 'dark'] : [value]).map((mode) => (
                            <span
                              className={`appearance-preview-pane appearance-preview-${mode}-pane`}
                              key={mode}
                            >
                              <span />
                              <span />
                              <span />
                            </span>
                          ))}
                          <span className="appearance-preview-indicator">
                            {appearance.preference === value ? (
                              <Check size={11} strokeWidth={3} />
                            ) : null}
                          </span>
                        </span>
                        <span>{label}</span>
                      </label>
                    ))}
                  </div>
                </div>
              </fieldset>
              <p className="settings-save-status" role="status">
                {appearance.saveError ? '当前外观已应用，偏好尚未保存。' : '外观偏好自动保存。'}
                当前显示为{appearance.resolved === 'light' ? '浅色' : '深色'}模式。
              </p>
              {appearance.saveError ? (
                <div className="settings-save-error">
                  <p className="form-error" role="alert">
                    无法保存外观偏好，重启客户端后可能恢复原设置。
                  </p>
                  <Button onClick={() => appearance.choose(appearance.preference)}>重试保存</Button>
                </div>
              ) : null}
            </section>
          ) : (
            <section
              className={`settings-section settings-panel-section ${section === 'mcp' ? 'settings-mcp-section' : ''}`}
              aria-labelledby="mcp-section-title"
            >
              <h2 id="mcp-section-title">
                {
                  {
                    mcp: '外部工具连接',
                    models: '服务连接',
                    'local-ai': '本机工具',
                    media: '媒体运行时',
                    diagnostics: '性能与诊断记录',
                    presets: '参数预设管理',
                    storage: '版本与恢复资料',
                    appearance: '主题',
                  }[section]
                }
              </h2>
              {contents?.[section] || (section === 'mcp' ? mcpContent : null) || (
                <p className="field-hint">当前工程尚未就绪，请返回应用重新连接后再使用此项。</p>
              )}
            </section>
          )}
        </div>
      </main>
    </div>
  );
}
