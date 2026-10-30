import { lazy, Suspense, useCallback, useEffect, useRef, useState } from 'react';
import { onDesktopEvent } from './lib/desktop';
import { ProjectMenu } from './components/ProjectMenu';
import { SourceThumbnail } from './components/SourceThumbnail';
const ContentSearchPanel = lazy(() =>
  import('./components/ContentSearchPanel').then((module) => ({
    default: module.ContentSearchPanel,
  })),
);
import { SelectionInspector } from './components/SelectionInspector';
import {
  emptySelection,
  selectClip,
  linkedSelection,
  pruneClipSelection,
} from './lib/clip-selection';
import { ExportSettings } from './components/ExportSettings';
import { NewProjectSettings } from './components/NewProjectSettings';
import { TrackSettingsDialog } from './components/TrackSettingsDialog';
import { SettingsPage, settingsHeading, type SettingsSection } from './components/SettingsPage';
import { LocalAISettings } from './components/LocalAISettings';
import { ModelServicesSettings, type ModelServiceKind } from './components/ModelServicesSettings';
import { connectionsChangedEvent } from './lib/settings';
import { useAppearance } from './lib/appearance';
import { defaultNewProjectDraft, parseNewProject } from './lib/new-project';
import { currentSequenceID, currentSequenceName } from './lib/sequences';
import { sequenceAPI } from './lib/bridge';
import { SequenceTabs } from './components/SequenceTabs';
import type { NewProjectErrors } from './lib/new-project';
import { defaultExportDraft, exportOptions, ExportRangeError } from './lib/export-options';
import {
  ArrowDownToLine,
  ArrowLeftToLine,
  ArrowRightToLine,
  AudioLines,
  Captions,
  ChevronLeft,
  ChevronRight,
  Clapperboard,
  Download,
  Film,
  FolderOpen,
  Image as ImageIcon,
  LayoutPanelLeft,
  LoaderCircle,
  Plus,
  Redo2,
  Save,
  Scissors,
  Search,
  Settings2,
  Square,
  StepBack,
  StepForward,
  Type,
  Undo2,
  Upload,
  X,
} from 'lucide-react';
import type { Asset, Bootstrap, Clip, Job, PreviewFrame, Track, TrackKind } from './types';
import { bridge, isDesktop, projectVersionsAPI, resourceAPI } from './lib/bridge';
import { recoveryAPI } from './lib/bridge';
import { useRecoveryStatus } from './lib/use-recovery-status';
const MediaToolsDialog = lazy(() =>
  import('./components/MediaToolsDialog').then((m) => ({ default: m.MediaToolsDialog })),
);
const ResourceMonitorDialog = lazy(() =>
  import('./components/ResourceMonitorDialog').then((m) => ({ default: m.ResourceMonitorDialog })),
);
const ProjectVersionsDialog = lazy(() =>
  import('./components/ProjectVersionsDialog').then((module) => ({
    default: module.ProjectVersionsDialog,
  })),
);
import {
  canPlace,
  defaultClip,
  fps,
  framesToSeconds,
  filterCaptions,
  projectEnd,
  timecode,
  trackNames,
} from './lib/editor';
import { useEditor } from './lib/use-editor';
import {
  useTimelineClipboard,
  timelineClipboardShortcut,
  type TimelineClipboardAction,
} from './lib/use-timeline-clipboard';
import type { ClipboardResult } from './clipboard-types';
import { commitActiveDraft, prepareToClose, settleProjectState } from './lib/close-protection';
import { hasEditorDraft, hasTimelineGesture } from './lib/close-protection';
import {
  Button,
  Field,
  IconButton,
  Input,
  Modal,
  PanelTabs,
  Select,
  TooltipProvider,
} from './components/ui';
import { Preview } from './components/Preview';
import { Timeline } from './components/Timeline';
import { Inspector } from './components/Inspector';
import { SubtitleLibrary } from './components/SubtitleLibrary';
import { GenerationPanel } from './components/GenerationPanel';
import { AgentPanel } from './components/AgentPanel';
import { SpeechPanel } from './components/SpeechPanel';
import { WorkflowPanel, type WorkflowPresetDraft } from './components/WorkflowPanel';
import { MCPPanel } from './components/MCPPanel';
const AgentExecutionPanel = lazy(() =>
  import('./components/AgentExecutionPanel').then((module) => ({
    default: module.AgentExecutionPanel,
  })),
);

const ProjectCollectDialog = lazy(() =>
  import('./components/ProjectCollectDialog').then((module) => ({
    default: module.ProjectCollectDialog,
  })),
);

const BatchRelinkDialog = lazy(() =>
  import('./components/BatchRelinkDialog').then((module) => ({
    default: module.BatchRelinkDialog,
  })),
);

const SequenceManagerDialog = lazy(() =>
  import('./components/SequenceManagerDialog').then((module) => ({
    default: module.SequenceManagerDialog,
  })),
);

const SnapshotStorageDialog = lazy(() =>
  import('./components/SnapshotStorageDialog').then((module) => ({
    default: module.SnapshotStorageDialog,
  })),
);
const InspectorDraftsDialog = lazy(() =>
  import('./components/InspectorDraftsDialog').then((m) => ({ default: m.InspectorDraftsDialog })),
);
const RecoveryCenterDialog = lazy(() =>
  import('./components/RecoveryCenterDialog').then((module) => ({
    default: module.RecoveryCenterDialog,
  })),
);

type DialogKind =
  | 'new'
  | 'open-path'
  | 'save-path'
  | 'import-font'
  | 'import-paths'
  | 'import-subtitles'
  | 'export-subtitles'
  | 'relink-asset'
  | 'export'
  | 'track'
  | 'confirm'
  | 'close'
  | 'external-refresh'
  | null;

function ClockDisplay({
  editor,
  className = '',
}: {
  editor: ReturnType<typeof useEditor>;
  className?: string;
}) {
  const ref = useRef<HTMLSpanElement>(null);
  useEffect(() => {
    const sync = () => {
      if (ref.current && editor.bootstrap)
        ref.current.textContent = timecode(
          editor.clock.getFrame(),
          editor.bootstrap.project.settings,
        );
    };
    sync();
    return editor.clock.subscribe(sync);
  }, [editor.clock, editor.bootstrap?.project.settings]);
  return (
    <span ref={ref} className={`timecode ${className}`}>
      00:00:00:00
    </span>
  );
}

function PlayControl({ editor }: { editor: ReturnType<typeof useEditor> }) {
  const [playing, setPlaying] = useState(false);
  useEffect(
    () => editor.clock.subscribe(() => setPlaying(editor.clock.isPlaying())),
    [editor.clock],
  );
  return (
    <IconButton
      label={playing ? '暂停 · Space' : '播放 · Space'}
      active={playing}
      onClick={() => editor.clock.toggle()}
      disabled={!editor.bootstrap?.project.clips.length}
    >
      {playing ? <span className="pause-icon" /> : <span className="play-icon" />}
    </IconButton>
  );
}

const kindIcon = (kind: string, size = 16) =>
  kind === 'audio' ? (
    <AudioLines size={size} />
  ) : kind === 'image' ? (
    <ImageIcon size={size} />
  ) : kind === 'text' ? (
    <Type size={size} />
  ) : kind === 'caption' ? (
    <Captions size={size} />
  ) : kind === 'font' ? (
    <Type size={size} />
  ) : kind === 'lut' ? (
    <Settings2 size={size} />
  ) : (
    <Film size={size} />
  );
const statusLabels: Record<Job['status'], string> = {
  queued: '等待中',
  running: '进行中',
  succeeded: '已完成',
  failed: '失败',
  cancelled: '已取消',
};

function App() {
  const appearance = useAppearance();
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [settingsSection, setSettingsSection] = useState<SettingsSection>('appearance');
  const [settingsBusy, setSettingsBusy] = useState(false);
  const [settingsFormEpoch, setSettingsFormEpoch] = useState(0);
  const [settingsDirty, setSettingsDirty] = useState(false);
  const [settingsDestination, setSettingsDestination] = useState<
    SettingsSection | 'editor' | 'close-app' | null
  >(null);
  const [presetSettingsDraft, setPresetSettingsDraft] = useState<WorkflowPresetDraft | null>(null);
  const [modelSettingsKind, setModelSettingsKind] = useState<ModelServiceKind>('generation');
  const [connectionsVersion, setConnectionsVersion] = useState(0);
  useEffect(() => {
    const changed = () => setConnectionsVersion((value) => value + 1);
    window.addEventListener(connectionsChangedEvent, changed);
    return () => window.removeEventListener(connectionsChangedEvent, changed);
  }, []);
  const settingsFormRef = useRef({ busy: settingsBusy, dirty: settingsDirty });
  settingsFormRef.current = { busy: settingsBusy, dirty: settingsDirty };
  const [settingsCloseBlocked, setSettingsCloseBlocked] = useState(false);
  function applySettingsNavigation(next: SettingsSection | 'editor' | 'close-app') {
    setSettingsDirty(false);
    settingsFormRef.current.dirty = false;
    if (next === 'close-app') {
      // 用户明确放弃后立即清理配置表单，后续工程关闭核对仍可取消。
      setSettingsFormEpoch((value) => value + 1);
      window.dispatchEvent(new Event('settings:resume-close'));
      return;
    }
    if (next === 'editor') setSettingsOpen(false);
    else setSettingsSection(next);
  }
  function navigateSettings(next: SettingsSection | 'editor') {
    if (settingsBusy || next === settingsSection) return;
    if (settingsDirty) setSettingsDestination(next);
    else applySettingsNavigation(next);
  }
  const editor = useEditor();
  const {
    bootstrap,
    error,
    setError,
    busy: editorBusy,
    notice,
    setNotice,
    clock,
    edit,
    run,
    jobs,
    waitForIdle,
    acceptBootstrap,
  } = editor;
  const recovery = useRecoveryStatus(Boolean(bootstrap));
  const [checkingClose, setCheckingClose] = useState(false);
  const [checkingTransition, setCheckingTransition] = useState(false);
  const transitioning = useRef(false);
  const [closeInvalid, setCloseInvalid] = useState(false);
  const closing = useRef(false);
  const busy = editorBusy || (checkingClose || checkingTransition ? '核对未保存的更改' : '');
  const transitionDisabled =
    checkingClose ||
    checkingTransition ||
    (Boolean(editorBusy) && !['更新片段', '修改工程名称'].includes(editorBusy));
  const settingsReturnFocus = useRef<HTMLElement | null>(null);
  const enterSettings = useCallback(() => {
    const visibleDialog = document.querySelector<HTMLElement>(
      '[role="dialog"], [role="alertdialog"]',
    );
    if (visibleDialog) {
      visibleDialog.focus();
      return false;
    }
    if (document.querySelector('[data-settings-page]')) {
      document.getElementById('settings-page-title')?.focus();
      return true;
    }
    settingsReturnFocus.current =
      document.activeElement instanceof HTMLElement ? document.activeElement : null;
    clock.pause();
    document.querySelectorAll<HTMLDetailsElement>('.project-menu[open]').forEach((menu) => {
      menu.open = false;
    });
    document
      .querySelectorAll<HTMLMediaElement>('.editor-shell video, .editor-shell audio')
      .forEach((media) => media.pause());
    setSettingsOpen(true);
    return true;
  }, [clock]);
  function openSettingsSection(section: SettingsSection, kind?: ModelServiceKind) {
    if (!enterSettings()) return;
    if (kind) setModelSettingsKind(kind);
    setSettingsSection(section);
  }

  useEffect(() => {
    if (!isDesktop()) return;
    let disposed = false;
    let stop: (() => void) | undefined;
    void onDesktopEvent('app:open-settings', enterSettings)
      .then((unsubscribe) => {
        if (disposed) unsubscribe();
        else stop = unsubscribe;
      })
      .catch((cause) =>
        setError(
          cause instanceof Error ? cause.message : '设置菜单连接失败，请使用快捷键打开设置。',
        ),
      );
    return () => {
      disposed = true;
      stop?.();
    };
  }, [enterSettings, setError]);
  useEffect(() => {
    document.documentElement.dataset.appView = settingsOpen ? 'settings' : 'editor';
    document.title = settingsOpen
      ? `${settingsHeading(settingsSection)} · 设置 · AIVidCut`
      : bootstrap
        ? `${bootstrap.project.name}${bootstrap.dirty ? ' •' : ''} · AIVidCut`
        : 'AIVidCut';
    if (!settingsOpen && settingsReturnFocus.current) {
      const target = settingsReturnFocus.current;
      settingsReturnFocus.current = null;
      if (target.isConnected && target !== document.body) target.focus();
      else document.getElementById('new-project-trigger')?.focus();
    }
  }, [settingsOpen, settingsSection, bootstrap?.project.name, bootstrap?.dirty]);
  useEffect(
    () => () => {
      delete document.documentElement.dataset.appView;
    },
    [],
  );
  const selectionContext = `${bootstrap?.project.id || ''}:${bootstrap?.sourceEpoch || 0}`;
  const selectionContextRef = useRef(selectionContext);
  selectionContextRef.current = selectionContext;
  const [storedSelection, storeSelection] = useState({ ...emptySelection, contextKey: '' });
  const clipSelection =
    storedSelection.contextKey === selectionContext ? storedSelection : emptySelection;
  const setClipSelection = useCallback(
    (action: React.SetStateAction<typeof emptySelection>) =>
      storeSelection((previous) => {
        if (selectionContextRef.current !== selectionContext) return previous;
        const current = previous.contextKey === selectionContext ? previous : emptySelection;
        const next = typeof action === 'function' ? action(current) : action;
        return { ...next, contextKey: selectionContext };
      }),
    [selectionContext],
  );
  const selectedIds = clipSelection.projectId === bootstrap?.project.id ? clipSelection.ids : [];
  const selectedId =
    clipSelection.projectId === bootstrap?.project.id ? clipSelection.primary : null;
  const setSelectedId = useCallback(
    (id: string | null) =>
      setClipSelection({
        projectId: bootstrap?.project.id || '',
        ids: id ? [id] : [],
        primary: id,
      }),
    [bootstrap?.project.id, setClipSelection],
  );
  const [assetId, setAssetId] = useState<string | null>(null);
  const [versionsOpen, setVersionsOpen] = useState(false);
  const [trackSettings, setTrackSettings] = useState<{ snapshot: Bootstrap; track: Track } | null>(
    null,
  );
  const [resourceRecording, setResourceRecording] = useState(false);
  const resourceReady = Boolean(bootstrap);
  useEffect(() => {
    if (!resourceReady || (settingsOpen && settingsSection === 'diagnostics')) return;
    let alive = true,
      reading = false;
    const read = async () => {
      if (reading) return;
      reading = true;
      try {
        const state = await resourceAPI.status();
        if (alive) setResourceRecording(Boolean(state.active));
      } catch {
        /* 旧桥不影响编辑，诊断窗口保留具体错误。 */
      } finally {
        reading = false;
      }
    };
    void read();
    const timer = resourceRecording ? window.setInterval(() => void read(), 1000) : undefined;
    return () => {
      alive = false;
      if (timer) window.clearInterval(timer);
    };
  }, [resourceReady, resourceRecording, settingsOpen, settingsSection]);
  const [collectionOpen, setCollectionOpen] = useState(false);
  const [batchRelinkOpen, setBatchRelinkOpen] = useState(false);
  const [sequenceDialog, setSequenceDialog] = useState<import('./types').Bootstrap | null>(null);
  const [storageKind, setStorageKind] = useState<
    import('./snapshot-storage-types').StorageKind | null
  >(null);
  const [recoveryOpen, setRecoveryOpen] = useState(false);
  const [draftsOpen, setDraftsOpen] = useState(false);
  const recoveryPrompted = useRef(false),
    initialRecoveryScope = useRef<string | null>(null);
  const [captureIntent, setCaptureIntent] = useState<
    import('./frame-capture-types').CaptureIntent | null
  >(null);
  const markedKey = `${bootstrap?.project.id || ''}:${bootstrap?.sourceEpoch ?? 0}`;
  const [markedScope, setMarkedScope] = useState<{
    key: string;
    range: import('./ripple-types').TimelineRange;
  }>({ key: '', range: { from: null, to: null } });
  const markedRange = markedScope.key === markedKey ? markedScope.range : { from: null, to: null };
  const [query, setQuery] = useState('');
  const [assetLimit, setAssetLimit] = useState(40);
  useEffect(() => setAssetLimit(40), [query]);
  const [assetTab, setAssetTab] = useState<'media' | 'lut' | 'font' | 'captions' | 'content'>(
    'media',
  );
  const [sideTab, setSideTab] = useState<
    'inspector' | 'assistant' | 'jobs' | 'generation' | 'speech'
  >('inspector');
  const [sideOpen, setSideOpen] = useState(true);
  const [assistantView, setAssistantView] = useState<'cli' | 'execution' | 'workflows'>('cli');
  const [leftOpen, setLeftOpen] = useState(true);
  const [dialog, setDialog] = useState<DialogKind>(null);
  const [dialogValue, setDialogValue] = useState('');
  const [newProjectDraft, setNewProjectDraft] = useState(defaultNewProjectDraft);
  const [newProjectErrors, setNewProjectErrors] = useState<NewProjectErrors>({});
  const newProjectScope = useRef<{
    projectId: string;
    projectRevision: number;
    sourceEpoch: number;
  } | null>(null);
  const returnToNewTrigger = useRef(false);
  const newProjectComposing = useRef(false);
  const [dialogError, setDialogError] = useState('');
  const [trackKind, setTrackKind] = useState<TrackKind>('video');
  const [composed, setComposed] = useState<PreviewFrame | null>(null);
  const [previewBusy, setPreviewBusy] = useState(false);
  const [exportBusy, setExportBusy] = useState(false);
  const exportSubmitting = useRef(false);
  const [exportDraft, setExportDraft] = useState(defaultExportDraft);
  const [exportInvalid, setExportInvalid] = useState<'from' | 'to'>();
  const [subtitleExportPath, setSubtitleExportPath] = useState('');
  const [relinkTarget, setRelinkTarget] = useState<Asset | null>(null);
  const nextAction = useRef<(() => void) | null>(null);
  const savedAction = useRef<(() => void) | null>(null);
  const searchRef = useRef<HTMLInputElement | null>(null);
  const searchPending = useRef(false);
  const setSearchInput = useCallback((node: HTMLInputElement | null) => {
    searchRef.current = node;
    if (node && searchPending.current) {
      searchPending.current = false;
      node.focus();
      node.select();
    }
  }, []);
  const project = bootstrap?.project;
  const recoveryScopeMatches = Boolean(
    bootstrap &&
    recovery.status?.scope.projectId === bootstrap.project.id &&
    recovery.status.scope.sourceEpoch === (bootstrap.sourceEpoch ?? 0),
  );
  useEffect(() => {
    if (!bootstrap) return;
    const key = `${bootstrap.project.id}:${bootstrap.sourceEpoch ?? 0}`;
    initialRecoveryScope.current ??= key;
    if (recoveryPrompted.current || !recovery.status?.enabled || !recoveryScopeMatches) return;
    recoveryPrompted.current = true;
    const active = document.activeElement;
    const editing =
      active instanceof Element &&
      Boolean(active.closest('input,textarea,[contenteditable="true"]'));
    if (
      key === initialRecoveryScope.current &&
      !bootstrap.dirty &&
      !bootstrap.projectPath &&
      !bootstrap.project.assets.length &&
      !bootstrap.project.clips.length &&
      !dialog &&
      !versionsOpen &&
      !collectionOpen &&
      !batchRelinkOpen &&
      !sequenceDialog &&
      !draftsOpen &&
      !settingsOpen &&
      !editing &&
      !hasEditorDraft() &&
      !hasTimelineGesture() &&
      (recovery.status.startupPending > 0 ||
        recovery.status.unreadable > 0 ||
        (recovery.status.inputDrafts || 0) > 0)
    ) {
      clock.pause();
      setRecoveryOpen(true);
    }
  }, [
    bootstrap,
    recovery.status,
    recoveryScopeMatches,
    dialog,
    versionsOpen,
    collectionOpen,
    batchRelinkOpen,
    sequenceDialog,
    draftsOpen,
    settingsOpen,
    clock,
  ]);
  useEffect(() => {
    if (bootstrap) void recovery.refresh();
  }, [bootstrap?.project.id, bootstrap?.sourceEpoch, recovery.refresh]);
  const clipboard = useTimelineClipboard(project?.id, bootstrap?.sourceEpoch || 0);
  const [clipboardFocusVersion, setClipboardFocusVersion] = useState(0);
  const selected = project?.clips.find((c) => c.id === selectedId);
  const selectedSet = new Set(selectedIds);
  const selectedClips = project?.clips.filter((c) => selectedSet.has(c.id)) || [];
  const asset = project?.assets.find((a) => a.id === assetId);
  const selectedTrack = project?.tracks.find((t) => t.id === selected?.trackId);
  const selectedLocked = project
    ? linkedSelection(project, selectedIds).some(
        (c) => project.tracks.find((t) => t.id === c.trackId)?.locked,
      )
    : Boolean(selectedTrack?.locked);
  useEffect(() => setSubtitleExportPath(''), [project?.id]);

  useEffect(() => {
    if (!project) return;
    setClipSelection((old) =>
      pruneClipSelection(old, project, editor.getCurrentBootstrap()?.project),
    );
  }, [project, editor.getCurrentBootstrap]);
  useEffect(() => {
    setComposed(null);
  }, [project?.revision]);
  const readyForClose = Boolean(bootstrap);
  useEffect(() => {
    if (!readyForClose || !isDesktop()) return;
    let disposed = false;
    let unsubscribe: (() => void) | undefined;
    const checkClose = () => {
      if (settingsFormRef.current.busy) {
        setSettingsCloseBlocked(true);
        return;
      }
      if (settingsFormRef.current.dirty) {
        setSettingsDestination('close-app');
        return;
      }
      if (closing.current) return;
      closing.current = true;
      clock.pause();
      setCloseInvalid(false);
      setDialogError('');
      setCheckingClose(true);
      void prepareToClose({
        commitDraft: commitActiveDraft,
        waitForIdle,
        read: bridge.bootstrap,
        accept: acceptBootstrap,
        discard: () => bridge.confirmClose('discard'),
      })
        .then((result) => {
          if (result !== 'closed') setDialog('close');
          if (result === 'invalid') {
            setCloseInvalid(true);
            setDialogError('部分输入尚未成功提交。请继续编辑修正，或明确放弃这些更改。');
          }
        })
        .catch((cause) => {
          setCloseInvalid(true);
          setDialogError(
            cause instanceof Error ? cause.message : '无法核对工程状态，请继续编辑并保存。',
          );
          setDialog('close');
        })
        .finally(() => setCheckingClose(false));
    };
    window.addEventListener('settings:resume-close', checkClose);
    void onDesktopEvent('project:close-request', checkClose)
      .then((stop) => {
        if (disposed) {
          stop();
          return;
        }
        unsubscribe = stop;
        return bridge.frontendReady();
      })
      .catch((cause) =>
        setError(cause instanceof Error ? cause.message : '关闭保护连接未完成，请先保存工程。'),
      );
    return () => {
      window.removeEventListener('settings:resume-close', checkClose);
      disposed = true;
      unsubscribe?.();
    };
  }, [readyForClose, waitForIdle, acceptBootstrap, clock, setError]);

  const showDialog = (kind: DialogKind, value = '') => {
    if (kind === 'new') {
      newProjectComposing.current = false;
      const b = editor.getCurrentBootstrap();
      newProjectScope.current = b
        ? {
            projectId: b.project.id,
            projectRevision: b.project.revision,
            sourceEpoch: b.sourceEpoch ?? 0,
          }
        : null;
      setNewProjectDraft(defaultNewProjectDraft());
      setNewProjectErrors({});
      returnToNewTrigger.current = true;
    }
    setError('');
    setDialogError('');
    setDialogValue(value);
    setDialog(kind);
  };
  const guarded = async (action: () => void) => {
    if (transitioning.current || closing.current) return;
    transitioning.current = true;
    setCheckingTransition(true);
    clock.pause();
    try {
      const result = await settleProjectState({
        commitDraft: commitActiveDraft,
        waitForIdle,
        read: bridge.bootstrap,
        accept: acceptBootstrap,
      });
      if (result.kind === 'invalid') {
        setError('部分输入尚未成功提交，请先修正，再新建或打开其他工程。');
        return;
      }
      if (result.snapshot.dirty === false) action();
      else {
        nextAction.current = action;
        showDialog('confirm');
      }
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : '无法核对工程状态，已保留当前工程。');
    } finally {
      transitioning.current = false;
      setCheckingTransition(false);
    }
  };
  const chooseClip = (id: string, additive = false) => {
    void navigateEditor(() => {
      setClipSelection((current) => selectClip(current, project?.id || '', id, additive));
      setAssetId(null);
      setSideTab('inspector');
    });
  };
  const navigating = useRef(false);
  async function navigateEditor(action: () => void) {
    if (navigating.current) return;
    navigating.current = true;
    try {
      const valid = commitActiveDraft();
      if (!valid) editor.protectDraft();
      const settled = await waitForIdle();
      if (!valid || !settled) {
        setError('属性草稿或工程冲突尚未处理，已保留当前属性面板。请先修正或明确放弃重读。');
        return;
      }
      action();
    } finally {
      navigating.current = false;
    }
  }
  const updateClip = useCallback(
    (clip: Clip) => {
      void edit('更新片段', [{ type: 'update_clip', clip }]);
    },
    [edit],
  );
  const prepareSelectionEdit = useCallback(
    async (pause = true) => {
      try {
        const current = await settleProjectState({
          commitDraft: commitActiveDraft,
          waitForIdle,
          read: bridge.bootstrap,
          accept: acceptBootstrap,
        });
        if (current.kind === 'invalid') {
          setError('请先处理属性草稿、外部冲突或当前拖动，再编辑选区。');
          return null;
        }
        if (
          current.snapshot.project.id !== bootstrap?.project.id ||
          current.snapshot.sourceEpoch !== bootstrap?.sourceEpoch
        ) {
          setError('工程已切换或重开，已取消旧选区操作，请在当前工程重新选择。');
          return null;
        }
        if (pause) clock.pause();
        return current.snapshot;
      } catch (cause) {
        setError(cause instanceof Error ? cause.message : '无法核对工程，当前选择保留。');
        return null;
      }
    },
    [waitForIdle, acceptBootstrap, clock, setError, bootstrap?.project.id, bootstrap?.sourceEpoch],
  );
  const split = useCallback(async () => {
    if (!selectedIds.length || selectedLocked || !(await prepareSelectionEdit())) return;
    await edit('分割选中片段', [
      { type: 'split_clips', clipIds: selectedIds, atFrame: clock.getFrame() },
    ]);
  }, [selectedIds, selectedLocked, clock, edit, prepareSelectionEdit]);
  const trimSelection = useCallback(
    async (ids: string[], edge: 'start' | 'end', frameDelta: number) => {
      if (!ids.length || !frameDelta || !(await prepareSelectionEdit())) return null;
      return edit('修剪选中片段', [{ type: 'trim_clips', clipIds: ids, edge, frameDelta }]);
    },
    [prepareSelectionEdit, edit],
  );
  const duplicate = useCallback(async () => {
    if (!selectedIds.length || selectedLocked) return;
    const current = await prepareSelectionEdit();
    if (!current) return;
    const before = new Set(current.project.clips.map((c) => c.id));
    const copied = await edit('复制到播放头', [
      { type: 'duplicate_clips', clipIds: selectedIds, atFrame: clock.getFrame() },
    ]);
    if (copied) {
      const ids = copied.project.clips.filter((c) => !before.has(c.id)).map((c) => c.id);
      setClipSelection({ projectId: copied.project.id, ids, primary: ids[0] || null });
      setAssetId(null);
      setSideTab('inspector');
    }
  }, [selectedIds, selectedLocked, clock, edit, prepareSelectionEdit]);
  const clipboardAction = useCallback(
    async (action: TimelineClipboardAction) => {
      if (action !== 'paste' && !selectedIds.length) return;
      if (action === 'cut' && selectedLocked) return;
      const clipboardID = clipboard.status?.id;
      if (action === 'paste' && (!clipboard.status?.available || !clipboardID)) {
        setError(clipboard.status?.reason || clipboard.error || '先在时间线复制或剪切片段。');
        return;
      }
      const atFrame = clock.getFrame();
      if (action !== 'copy') clock.pause();
      const current = await prepareSelectionEdit(action !== 'copy');
      if (!current) return;
      const scope = {
        projectId: current.project.id,
        sourceEpoch: current.sourceEpoch || 0,
        baseRevision: current.project.revision,
      };
      let result: ClipboardResult | undefined;
      const labels = { copy: '复制片段', cut: '剪切片段', paste: '粘贴片段' };
      const accepted = await run(labels[action], async () => {
        result =
          action === 'copy'
            ? await bridge.copySelection({ ...scope, clipIds: selectedIds })
            : action === 'cut'
              ? await bridge.cutSelection({
                  ...scope,
                  clipIds: selectedIds,
                  idempotencyKey: crypto.randomUUID(),
                })
              : await bridge.pasteSelection({
                  ...scope,
                  clipboardId: clipboardID!,
                  idempotencyKey: crypto.randomUUID(),
                  atFrame,
                });
        clipboard.accept(result.clipboard);
        return result.bootstrap;
      });
      if (!accepted || !result) {
        await clipboard.refresh();
        return;
      }
      if (action === 'paste') {
        const ids = result.selectedClipIds || [];
        setClipSelection({ projectId: accepted.project.id, ids, primary: ids[0] || null });
        setAssetId(null);
        setSideTab('inspector');
      }
      setClipboardFocusVersion((v) => v + 1);
      setNotice(
        `${labels[action]}完成，工程剪贴板含 ${result.clipboard.clipCount} 个片段${result.clipboard.detachedCaptions ? '；单独字幕粘贴为独立字幕' : ''}。`,
      );
    },
    [selectedIds, selectedLocked, clipboard, prepareSelectionEdit, run, clock, setError, setNotice],
  );
  const remove = useCallback(() => {
    if (selectedIds.length && !selectedLocked)
      void edit('删除选中片段', [{ type: 'delete_clips', clipIds: selectedIds }]);
  }, [selectedIds, selectedLocked, edit]);
  const moveSelection = useCallback(
    (frameDelta: number, trackDelta = 0) => {
      if (!selectedIds.length || selectedLocked) return Promise.resolve(null);
      return edit('移动选中片段', [
        { type: 'move_clips', clipIds: selectedIds, frameDelta, trackDelta },
      ]);
    },
    [selectedIds, selectedLocked, edit],
  );

  const save = useCallback(async () => {
    if (!bootstrap) return null;
    if (!(await waitForIdle())) return null;
    if (isDesktop()) return run('保存工程', bridge.saveProject);
    showDialog('save-path', bootstrap.projectPath || '');
    return null;
  }, [bootstrap, run, waitForIdle]);

  const importMedia = () => {
    if (isDesktop()) void run('导入素材', bridge.importMedia);
    else showDialog('import-paths');
  };
  const importFont = () => {
    clock.pause();
    if (isDesktop()) void run('导入工程字体', bridge.importFont);
    else showDialog('import-font');
  };
  const importSubtitles = () => {
    clock.pause();
    setQuery('');
    setAssetTab('captions');
    setAssetLimit(40);
    if (isDesktop()) void run('导入 SRT 字幕', bridge.importSubtitle);
    else showDialog('import-subtitles');
  };
  const exportSubtitles = async () => {
    if (!(await waitForIdle())) return;
    if (!isDesktop()) {
      showDialog('export-subtitles');
      return;
    }
    setExportBusy(true);
    setError('');
    try {
      const path = await bridge.exportSubtitles();
      if (path) {
        setSubtitleExportPath(path);
        setNotice(`SRT 已导出：${path}`);
      }
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : '字幕导出未完成，请重试。');
    } finally {
      setExportBusy(false);
    }
  };
  const openProject = () =>
    void guarded(() => {
      if (isDesktop()) void run('打开工程', bridge.openProject);
      else showDialog('open-path');
    });
  const newProject = () => {
    void guarded(() => showDialog('new', '未命名工程'));
  };
  const relinkAsset = async (target: Asset) => {
    clock.pause();
    try {
      const current = await settleProjectState({
        commitDraft: commitActiveDraft,
        waitForIdle,
        read: bridge.bootstrap,
        accept: acceptBootstrap,
      });
      if (current.kind === 'invalid') {
        setError('请先修正尚未提交的输入，再重新定位素材。');
        return;
      }
      const source = current.snapshot.project.assets.find((item) => item.id === target.id);
      if (!source) {
        setError('当前工程中已找不到此素材，请重新选择。');
        return;
      }
      if (isDesktop()) await run('重新定位素材', () => bridge.relinkAsset(source.id));
      else {
        setRelinkTarget(source);
        showDialog('relink-asset', source.path);
      }
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : '重新定位未完成，请重试。');
    }
  };

  function addClip(kind: Clip['kind'], source?: Asset) {
    if (!project) return;
    const frame = clock.getFrame();
    if (kind === 'audio' && source) {
      const clipId = crypto.randomUUID();
      const expected = { id: project.id, revision: project.revision };
      clock.pause();
      void (async () => {
        try {
          const current = await settleProjectState({
            commitDraft: commitActiveDraft,
            waitForIdle,
            read: bridge.bootstrap,
            accept: acceptBootstrap,
          });
          if (current.kind === 'invalid') {
            setError('请先处理未提交的属性或工程冲突，再添加音频。');
            return;
          }
          if (
            current.snapshot.project.id !== expected.id ||
            current.snapshot.project.revision !== expected.revision
          ) {
            setError('工程已变化，请核对后重新添加音频。');
            return;
          }
          const value = await run('添加完整音频到时间线', () =>
            bridge.addAudioToTimeline({
              projectId: expected.id,
              projectRevision: expected.revision,
              assetId: source.id,
              clipId,
              startFrame: frame,
            }),
          );
          if (value) {
            chooseClip(clipId);
            setNotice(`已添加 ${source.name}`);
          }
        } catch (cause) {
          setError(cause instanceof Error ? cause.message : '音频添加未完成，请核对源素材。');
        }
      })();
      return;
    }
    const clip = defaultClip(kind, '', frame, project.settings, source);
    const compatible = project.tracks.filter((t) => canPlace(t, kind) && !t.locked);
    let target = compatible.find(
      (t) =>
        !project.clips.some(
          (c) =>
            c.trackId === t.id &&
            c.startFrame < frame + clip.durationFrames &&
            c.startFrame + c.durationFrames > frame,
        ),
    );
    const commands: Parameters<typeof edit>[1] = [];
    if (!target) {
      const targetKind: TrackKind = kind === 'image' ? 'video' : kind;
      target = {
        id: crypto.randomUUID(),
        name: `${trackNames[targetKind]} ${project.tracks.filter((t) => t.kind === targetKind).length + 1}`,
        kind: targetKind,
        locked: false,
        muted: false,
        hidden: false,
      };
      commands.push({ type: 'add_track', track: target });
    }
    clip.trackId = target.id;
    commands.push({ type: 'add_clip', clip });
    void edit('添加到时间线', commands).then((value) => {
      if (value) {
        chooseClip(clip.id);
        setNotice(`已添加 ${clip.name}`);
      }
    });
  }

  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (event.defaultPrevented || event.isComposing || dialog) return;
      const target = event.target as HTMLElement;
      if (target.closest('[role="dialog"], [role="alertdialog"]')) return;
      const command = event.metaKey || event.ctrlKey;
      if (command && !event.altKey && !event.shiftKey && event.key === ',') {
        event.preventDefault();
        enterSettings();
        return;
      }
      if (settingsOpen) return;
      if (command && event.key.toLowerCase() === 's') {
        event.preventDefault();
        if (target.closest('input, textarea, select, [contenteditable="true"]')) target.blur();
        void save();
        return;
      }
      if (command && !event.altKey && !event.shiftKey && event.key.toLowerCase() === 'f') {
        event.preventDefault();
        setLeftOpen(true);
        searchPending.current = true;
        if (searchRef.current) {
          searchRef.current.focus();
          searchRef.current.select();
          searchPending.current = false;
        }
        return;
      }
      if (target.closest('summary, .project-menu[open]')) return;
      if (busy || target.closest('input, textarea, select, [contenteditable="true"]')) return;
      const clipboardCommand = timelineClipboardShortcut(event);
      if (clipboardCommand) {
        event.preventDefault();
        void clipboardAction(clipboardCommand);
        return;
      }
      if (command && event.key.toLowerCase() === 'z') {
        event.preventDefault();
        if (event.shiftKey && bootstrap?.canRedo) void run('重做编辑', bridge.redo);
        else if (!event.shiftKey && bootstrap?.canUndo) void run('撤销编辑', bridge.undo);
      } else if (event.code === 'Space') {
        if (target.closest('button, a, [role="tab"]')) return;
        event.preventDefault();
        clock.toggle();
      } else if (command && event.key.toLowerCase() === 'd') {
        event.preventDefault();
        void duplicate();
      } else if (!command && event.key.toLowerCase() === 'c') {
        event.preventDefault();
        split();
      } else if (event.key === 'Delete' || event.key === 'Backspace') {
        if (selected) {
          event.preventDefault();
          remove();
        }
      } else if (event.key === 'ArrowLeft' || event.key === 'ArrowRight') {
        event.preventDefault();
        if (event.altKey && selectedIds.length) {
          void moveSelection((event.key === 'ArrowRight' ? 1 : -1) * (event.shiftKey ? 10 : 1));
          return;
        }
        clock.seek(
          clock.getFrame() + (event.key === 'ArrowRight' ? 1 : -1) * (event.shiftKey ? 10 : 1),
        );
      }
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [
    settingsOpen,
    enterSettings,
    dialog,
    busy,
    save,
    run,
    bootstrap?.canUndo,
    bootstrap?.canRedo,
    clock,
    split,
    duplicate,
    clipboardAction,
    remove,
    selected,
    selectedIds,
    moveSelection,
  ]);

  async function submitDialog(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (dialog === 'new' && newProjectComposing.current) return;
    const form = event.currentTarget;
    setExportInvalid(undefined);
    setDialogError('');
    const value = dialogValue.trim();
    if (!value && dialog !== 'new' && (dialog !== 'export' || !isDesktop())) {
      setDialogError(
        dialog === 'import-paths' || dialog === 'import-font'
          ? '请输入至少一个素材文件的完整路径。'
          : '请填写此项。',
      );
      return;
    }
    let result: Bootstrap | null = null;
    if (dialog === 'new') {
      const v = parseNewProject(dialogValue, newProjectDraft);
      setNewProjectErrors(v.errors);
      if (!v.settings) {
        const key = Object.keys(v.errors)[0];
        form.querySelector<HTMLElement>(`#new-project-${key}`)?.focus();
        return;
      }
      if (!newProjectScope.current) {
        setDialogError('原工程状态尚未准备好，请关闭并重新打开新建窗口。');
        return;
      }
      const settings = v.settings;
      const scope = { ...newProjectScope.current };
      result = await run('新建工程', () =>
        bridge.newProjectWithSettings({ ...scope, name: v.name, settings }),
      );
    }
    if (dialog === 'open-path') result = await run('打开工程', () => bridge.openProjectPath(value));
    if (dialog === 'save-path') result = await run('保存工程', () => bridge.saveProjectTo(value));
    if (dialog === 'relink-asset') {
      if (!relinkTarget) {
        setDialogError('请重新选择需要定位的素材。');
        return;
      }
      result = await run('重新定位素材', () => bridge.relinkAssetPath(relinkTarget.id, value));
    }
    if (
      dialog === 'import-font' &&
      value
        .split('\n')
        .filter((p) => p.trim())
        .some((p) => !/\.(ttf|otf)$/i.test(p.trim()))
    ) {
      setDialogError('请选择TTF或OTF单字体文件，每行一个完整路径。');
      return;
    }
    if (dialog === 'import-paths' || dialog === 'import-font')
      result = await run('导入素材', () =>
        bridge.importPaths(
          value
            .split('\n')
            .map((path) => path.trim())
            .filter(Boolean),
        ),
      );
    if (dialog === 'import-subtitles')
      result = await run('导入 SRT 字幕', () => bridge.importCaptions(value));
    if (dialog === 'export-subtitles') {
      setExportBusy(true);
      try {
        const path = await bridge.exportCaptionsTo(value);
        if (path) {
          setSubtitleExportPath(path);
          setNotice(`SRT 已导出：${path}`);
          setDialog(null);
        }
      } catch (cause) {
        setDialogError(cause instanceof Error ? cause.message : '字幕导出未完成，请重试。');
      } finally {
        setExportBusy(false);
      }
      return;
    }
    if (dialog === 'track')
      result = await edit('添加轨道', [
        {
          type: 'add_track',
          track: {
            id: crypto.randomUUID(),
            name: value,
            kind: trackKind,
            locked: false,
            muted: false,
            hidden: false,
          },
        },
      ]);
    if (dialog === 'export') {
      if (exportSubmitting.current) return;
      exportSubmitting.current = true;
      setExportBusy(true);
      try {
        const options = exportOptions(exportDraft, projectEnd(bootstrap!.project));
        const job = await bridge.exportVideo({ ...options, path: value || undefined });
        editor.setJobs((list) => [job, ...list.filter((item) => item.id !== job.id)]);
        setDialog(null);
        setSideOpen(true);
        setSideTab('jobs');
      } catch (cause) {
        setDialogError(cause instanceof Error ? cause.message : '无法开始导出，请重试。');
        if (cause instanceof ExportRangeError) {
          setExportInvalid(cause.field);
          form.querySelector<HTMLInputElement>(`[data-export-field="${cause.field}"]`)?.focus();
        }
      } finally {
        exportSubmitting.current = false;
        setExportBusy(false);
      }
      return;
    }
    if (result) {
      setDialog(null);
      if (dialog === 'new' || dialog === 'open-path') {
        setSelectedId(null);
        setAssetId(null);
        clock.seek(0);
      }
      if (dialog === 'save-path' && savedAction.current) {
        const action = savedAction.current;
        savedAction.current = null;
        action();
      }
    }
  }

  async function saveBeforeContinue() {
    const action = nextAction.current;
    if (!isDesktop() && !bootstrap?.projectPath) {
      savedAction.current = action;
      showDialog('save-path');
      return;
    }
    const result = await run('保存工程', bridge.saveProject);
    if (result && !result.dirty) {
      setDialog(null);
      nextAction.current = null;
      action?.();
    }
  }

  async function checkFrame() {
    setPreviewBusy(true);
    setError('');
    clock.pause();
    setAssetId(null);
    try {
      const frame = await bridge.previewFrame(clock.getFrame());
      setComposed(frame);
      setNotice(`已完成第 ${frame.frame} 帧合成检查`);
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : '无法生成合成帧。');
    } finally {
      setPreviewBusy(false);
    }
  }

  const settingsLeaveDialog = (
    <>
      <Modal
        open={settingsDestination !== null}
        title="放弃未保存的设置？"
        description="离开此分类会清空尚未保存的输入。已保存设置与当前工程不受影响。"
        onOpenChange={(open) => {
          if (!open) setSettingsDestination(null);
        }}
      >
        <div className="dialog-actions">
          <Button onClick={() => setSettingsDestination(null)}>继续编辑</Button>
          <Button
            variant="danger"
            onClick={() => {
              if (settingsDestination) applySettingsNavigation(settingsDestination);
              setSettingsDestination(null);
            }}
          >
            放弃并离开
          </Button>
        </div>
      </Modal>
      <Modal
        open={settingsCloseBlocked}
        title="设置操作尚未完成"
        description="请等待当前操作结束后再退出，已提交的工程保持不变。"
        onOpenChange={setSettingsCloseBlocked}
      >
        <div className="dialog-actions">
          <Button onClick={() => setSettingsCloseBlocked(false)}>继续等待</Button>
        </div>
      </Modal>
    </>
  );
  if (!bootstrap)
    return (
      <TooltipProvider>
        <div className="connection-screen" hidden={settingsOpen} inert={settingsOpen}>
          <div className="brand-mark large">
            <Clapperboard size={27} />
          </div>
          <h1>AIVidCut</h1>
          <p>{error ? '暂时无法连接剪辑工作台' : '正在打开剪辑工作台'}</p>
          {error ? (
            <>
              <div className="connection-error" role="alert">
                {error}
              </div>
              <Button variant="primary" onClick={() => void editor.refresh()} busy={Boolean(busy)}>
                重新连接
              </Button>
              <span className="field-hint">开发环境请同时启动本地 Go 服务。</span>
            </>
          ) : (
            <LoaderCircle className="spin" size={22} />
          )}
        </div>
        {settingsOpen ? (
          <SettingsPage
            key={settingsFormEpoch}
            appearance={appearance}
            section={settingsSection}
            onSectionChange={navigateSettings}
            onReturn={() => navigateSettings('editor')}
            busy={settingsBusy}
            contents={{
              'local-ai': (
                <LocalAISettings onBusyChange={setSettingsBusy} onDirtyChange={setSettingsDirty} />
              ),
              media: (
                <Suspense fallback={<p role="status">正在读取媒体工具…</p>}>
                  <MediaToolsDialog
                    inline
                    onClose={() => navigateSettings('editor')}
                    onReturnFocus={() => {}}
                    onBusyChange={setSettingsBusy}
                    onDirtyChange={setSettingsDirty}
                  />
                </Suspense>
              ),
              models: (
                <ModelServicesSettings
                  key={modelSettingsKind}
                  initialKind={modelSettingsKind}
                  onBusyChange={setSettingsBusy}
                  onDirtyChange={setSettingsDirty}
                />
              ),
            }}
          />
        ) : null}
        {settingsLeaveDialog}
      </TooltipProvider>
    );

  const duration = projectEnd(bootstrap.project);
  const filteredAssets = bootstrap.project.assets.filter(
    (item) =>
      (assetTab === 'lut'
        ? item.kind === 'lut'
        : assetTab === 'font'
          ? item.kind === 'font'
          : item.kind !== 'lut' && item.kind !== 'font') &&
      item.name.toLocaleLowerCase().includes(query.toLocaleLowerCase()),
  );
  const filteredCaptionCount = filterCaptions(bootstrap.project, query).length;
  const exportCapability = bootstrap.capabilities.exportVideo;
  const frameCapability = bootstrap.capabilities.previewFrame;
  const activeJobs = jobs.filter(
    (job) => job.status === 'queued' || job.status === 'running',
  ).length;
  const dialogTitles = {
    new: '新建工程',
    'open-path': '打开工程',
    'save-path': '保存工程',
    'import-paths': '导入本地素材',
    'import-font': '导入工程字体',
    'import-subtitles': '导入 SRT 字幕',
    'export-subtitles': '导出 SRT 字幕',
    'relink-asset': '重新定位素材',
    export: '导出视频',
    track: '添加轨道',
    confirm: '保留当前工程的更改？',
    close: '关闭前保存工程？',
    'external-refresh': '重新读取工程状态',
  };

  return (
    <TooltipProvider>
      <div
        className={`editor-shell ${leftOpen ? '' : 'left-collapsed'} ${sideOpen ? '' : 'right-collapsed'}`}
        hidden={settingsOpen}
        inert={settingsOpen}
      >
        <header className="app-header">
          <div className="brand">
            <span className="brand-mark">
              <Clapperboard size={19} />
            </span>
            <strong>AIVidCut</strong>
            <span className="build-badge">PREVIEW</span>
          </div>
          <ProjectMenu
            name={bootstrap.project.name}
            dirty={bootstrap.dirty}
            details={`${bootstrap.project.settings.width} × ${bootstrap.project.settings.height} · ${fps(bootstrap.project.settings).toFixed(2)} fps · ${bootstrap.project.settings.sampleRate / 1000} kHz`}
            recoveries={
              recoveryScopeMatches
                ? (recovery.status?.pending || 0) + (recovery.status?.inputDrafts || 0)
                : 0
            }
            recoveryError={Boolean(recovery.status?.error)}
            disabled={transitionDisabled}
            onVersions={() =>
              void navigateEditor(() => {
                clock.pause();
                setVersionsOpen(true);
              })
            }
            onCollect={() =>
              void navigateEditor(() => {
                clock.pause();
                setCollectionOpen(true);
              })
            }
            onRecovery={() =>
              void navigateEditor(() => {
                clock.pause();
                setRecoveryOpen(true);
              })
            }
          />
          <div className="app-header-actions">
            <IconButton label="打开工程" disabled={transitionDisabled} onClick={openProject}>
              <FolderOpen size={17} />
            </IconButton>
            <IconButton label="保存工程 · ⌘/Ctrl S" onClick={() => void save()}>
              <Save size={16} />
            </IconButton>
            <Button
              variant="primary"
              disabled={Boolean(busy) || !duration || !exportCapability?.available}
              title={exportCapability?.reason || '添加片段后导出'}
              onClick={() => showDialog('export')}
            >
              <Download size={15} />
              导出视频
            </Button>
          </div>
        </header>
        <div className="workspace-toolbar">
          <div className="toolbar-group">
            <IconButton
              label={leftOpen ? '收起素材面板' : '展开素材面板'}
              active={leftOpen}
              onClick={() => setLeftOpen((v) => !v)}
            >
              <LayoutPanelLeft size={16} />
            </IconButton>
            <Button
              id="new-project-trigger"
              variant="ghost"
              onClick={newProject}
              disabled={transitionDisabled}
            >
              <Plus size={14} />
              新建
            </Button>
            <span className="toolbar-divider" />
            <IconButton
              label="撤销 · ⌘/Ctrl Z"
              disabled={!bootstrap.canUndo || Boolean(busy)}
              onClick={() => void run('撤销编辑', bridge.undo)}
            >
              <Undo2 size={16} />
            </IconButton>
            <IconButton
              label="重做 · ⌘/Ctrl Shift Z"
              disabled={!bootstrap.canRedo || Boolean(busy)}
              onClick={() => void run('重做编辑', bridge.redo)}
            >
              <Redo2 size={16} />
            </IconButton>
          </div>
          <div className="workspace-name">
            <span>剪辑</span>
            <span className="workspace-line" />
          </div>
          <div className="toolbar-group">
            {activeJobs > 0 ? (
              <>
                <Button
                  variant={sideOpen && sideTab === 'jobs' ? 'outline' : 'ghost'}
                  onClick={() => {
                    void navigateEditor(() => {
                      setSideOpen(true);
                      setSideTab('jobs');
                    });
                  }}
                >
                  <ArrowDownToLine size={14} />
                  任务{activeJobs ? <span className="count-badge">{activeJobs}</span> : null}
                </Button>
              </>
            ) : null}
            <IconButton
              label={sideOpen ? '收起右侧面板' : '展开右侧面板'}
              onClick={() => void navigateEditor(() => setSideOpen((v) => !v))}
            >
              {sideOpen ? <ChevronRight size={16} /> : <ChevronLeft size={16} />}
            </IconButton>
          </div>
        </div>
        {error || (recovery.status?.enabled && recovery.status.error) ? (
          <div className="global-alert" role="alert">
            <InfoIcon />
            <span>{error || `自动恢复：${recovery.status?.error}`}</span>
            {error ? (
              <Button
                variant="ghost"
                className="icon-button"
                aria-label="关闭错误信息"
                onClick={() => setError('')}
              >
                <X size={14} />
              </Button>
            ) : (
              <Button
                variant="ghost"
                onClick={() => void navigateEditor(() => setRecoveryOpen(true))}
              >
                查看恢复中心
              </Button>
            )}
          </div>
        ) : null}
        {bootstrap.warnings?.length ? (
          <div className="global-alert global-notice" role="status" aria-label="本次操作提示">
            <InfoIcon />
            <details className="operation-notices">
              <summary>本次操作有 {new Set(bootstrap.warnings).size} 条提示 · 查看原因</summary>
              {Array.from(new Set(bootstrap.warnings)).map((warning) => (
                <p key={warning}>{warning}</p>
              ))}
            </details>
          </div>
        ) : null}
        {editor.externalChange || editor.draftFailure ? (
          <div className="global-alert" role="status">
            <InfoIcon />
            <span>
              {editor.externalChange
                ? `${editor.externalChange.reason} 外部版本 ${editor.externalChange.revision}`
                : '属性提交尚未成功，当前输入已保留。'}
              ；重新读取前可复制保留的输入。
            </span>
            <Button onClick={() => showDialog('external-refresh')}>
              {editor.externalChange ? '重新读取外部更改…' : '放弃未提交输入并读取…'}
            </Button>
          </div>
        ) : null}
        <main className="workspace-main">
          {leftOpen ? (
            <aside className="asset-panel" aria-label="素材库">
              <PanelTabs
                id="asset-tabs"
                label="素材分类"
                value={assetTab}
                items={[
                  { value: 'media', label: '素材' },
                  { value: 'content', label: '内容' },
                  { value: 'captions', label: '字幕' },
                  { value: 'lut', label: 'LUT' },
                  { value: 'font', label: '字体' },
                ]}
                onChange={(value) => {
                  setAssetTab(value);
                  setAssetLimit(40);
                }}
              >
                <IconButton
                  label={
                    assetTab === 'font'
                      ? '导入工程字体'
                      : assetTab === 'captions'
                        ? '导入 SRT 字幕'
                        : assetTab === 'lut'
                          ? '导入 LUT'
                          : '导入素材'
                  }
                  disabled={Boolean(busy)}
                  onClick={() => {
                    if (assetTab === 'font') importFont();
                    else if (assetTab === 'captions') importSubtitles();
                    else if (assetTab === 'lut' && isDesktop())
                      void run('导入 LUT', bridge.importLUT);
                    else importMedia();
                  }}
                >
                  <Plus size={16} />
                </IconButton>
              </PanelTabs>
              <div className="asset-repair-entry">
                <Button
                  id="batch-relink-trigger"
                  variant="ghost"
                  disabled={transitionDisabled || !project?.assets.length}
                  onClick={() =>
                    void navigateEditor(() => {
                      clock.pause();
                      setBatchRelinkOpen(true);
                    })
                  }
                >
                  批量修复素材链接
                </Button>
              </div>
              {assetTab !== 'content' ? (
                <div className="asset-tools">
                  <div className="search-box">
                    <Search size={14} />
                    <input
                      ref={setSearchInput}
                      aria-label={assetTab === 'captions' ? '搜索字幕' : '搜索素材'}
                      value={query}
                      placeholder={assetTab === 'captions' ? '搜索字幕内容' : '搜索素材名称'}
                      onChange={(e) => setQuery(e.target.value)}
                    />
                    {query ? (
                      <button
                        type="button"
                        aria-label="清空素材搜索"
                        onClick={() => {
                          setQuery('');
                          searchRef.current?.focus();
                        }}
                      >
                        <X size={13} />
                      </button>
                    ) : null}
                  </div>
                  <div className="asset-summary">
                    <span>
                      {assetTab === 'captions'
                        ? `${filteredCaptionCount} 条字幕`
                        : `${filteredAssets.length} 个${assetTab === 'lut' ? ' LUT' : assetTab === 'font' ? '字体' : '素材'}`}
                    </span>
                    <span>
                      {assetTab === 'captions'
                        ? '点击定位'
                        : assetTab === 'font'
                          ? '选择后用于文字'
                          : '双击添加'}
                    </span>
                  </div>
                </div>
              ) : null}
              <div
                id="asset-tabs-panel"
                role="tabpanel"
                aria-labelledby={`asset-tabs-${assetTab}`}
                className="asset-list"
                aria-label={
                  assetTab === 'captions'
                    ? '工程字幕'
                    : assetTab === 'lut'
                      ? 'LUT 列表'
                      : '项目素材'
                }
              >
                {assetTab === 'content' ? (
                  <Suspense fallback={<p role="status">正在打开内容检索…</p>}>
                    <ContentSearchPanel
                      applyAnalysis={(action) => run('移除画面分析', action)}
                      searchRef={setSearchInput}
                      project={bootstrap.project}
                      sourceEpoch={bootstrap.sourceEpoch}
                    />
                  </Suspense>
                ) : assetTab === 'captions' ? (
                  <SubtitleLibrary
                    project={bootstrap.project}
                    query={query}
                    limit={assetLimit}
                    busy={Boolean(busy) || exportBusy}
                    exportPath={subtitleExportPath}
                    onMore={() => setAssetLimit((count) => count + 40)}
                    onSelect={(clip) => {
                      clock.pause();
                      clock.seek(clip.startFrame);
                      chooseClip(clip.id);
                      setSideOpen(true);
                    }}
                    onImport={importSubtitles}
                    onExport={() => void exportSubtitles()}
                    onAdd={() => addClip('caption')}
                  />
                ) : (
                  <>
                    {filteredAssets.slice(0, assetLimit).map((item) => (
                      <div className="asset-entry" key={item.id}>
                        <button
                          type="button"
                          className={`asset-item ${assetId === item.id ? 'selected' : ''}`}
                          onClick={() => {
                            void navigateEditor(() => {
                              clock.pause();
                              setAssetId(item.id);
                              setSelectedId(null);
                              setSideTab('inspector');
                            });
                          }}
                          onDoubleClick={() => {
                            if (item.kind !== 'lut' && item.kind !== 'font')
                              addClip(item.kind as Clip['kind'], item);
                          }}
                          disabled={Boolean(busy)}
                          aria-label={`选择素材 ${item.name}`}
                          draggable={false}
                        >
                          <span className={`asset-thumb thumb-${item.kind}`}>
                            <SourceThumbnail
                              projectId={project?.id || ''}
                              sourceEpoch={bootstrap?.sourceEpoch}
                              asset={item}
                              fallback={kindIcon(item.kind, 23)}
                            />
                            <span className="asset-kind-badge">
                              {trackNames[item.kind] || item.kind}
                            </span>
                          </span>
                          <span className="asset-item-label">
                            <strong title={item.name}>{item.name}</strong>
                            <span>
                              {item.missing
                                ? '源文件缺失'
                                : item.durationUs
                                  ? `${(item.durationUs / 1e6).toFixed(1)} 秒`
                                  : item.kind === 'font'
                                    ? `${item.font?.family || item.name} · ${item.font?.style || '常规'}`
                                    : item.kind === 'lut'
                                      ? '色彩查找表'
                                      : `${item.width} × ${item.height}`}
                              {item.hasAudio ? <AudioLines size={11} /> : null}
                            </span>
                          </span>
                        </button>
                        {item.missing ? (
                          <div className="asset-relink">
                            <Button
                              variant="ghost"
                              disabled={Boolean(busy)}
                              aria-label={`重新定位素材 ${item.name}`}
                              onClick={() => void relinkAsset(item)}
                            >
                              重新定位素材
                            </Button>
                          </div>
                        ) : null}
                      </div>
                    ))}
                    {filteredAssets.length > assetLimit ? (
                      <div className="load-more">
                        <span>
                          已显示 {assetLimit} / {filteredAssets.length}
                        </span>
                        <Button
                          variant="ghost"
                          onClick={() => setAssetLimit((count) => count + 40)}
                        >
                          再显示 40 个
                        </Button>
                      </div>
                    ) : null}
                    {!filteredAssets.length ? (
                      <div className="asset-empty">
                        {query ? (
                          <>
                            <Search size={25} />
                            <p>没有找到匹配素材</p>
                            <Button variant="ghost" onClick={() => setQuery('')}>
                              清空搜索
                            </Button>
                          </>
                        ) : (
                          <>
                            <FolderOpen size={27} />
                            <p>
                              {assetTab === 'font'
                                ? '为字幕准备工程字体'
                                : assetTab === 'lut'
                                  ? '保存你的色彩风格'
                                  : '素材就位，创作开始'}
                            </p>
                            <span>
                              {assetTab === 'font'
                                ? '导入32MiB以内的TTF/OTF单字体，随工程保存引用。'
                                : assetTab === 'lut'
                                  ? '导入 .cube 文件，应用到视频或图片。'
                                  : '视频、图片和音频都放在这里。'}
                            </span>
                            <Button
                              onClick={assetTab === 'font' ? importFont : importMedia}
                              disabled={Boolean(busy)}
                            >
                              <Upload size={14} />
                              {assetTab === 'font'
                                ? '导入工程字体'
                                : assetTab === 'lut'
                                  ? '导入 LUT 文件'
                                  : '导入素材'}
                            </Button>
                          </>
                        )}
                      </div>
                    ) : null}
                  </>
                )}
              </div>
              <div className="asset-panel-footer">
                <span className="status-dot" />
                源素材保留在本机
              </div>
            </aside>
          ) : null}
          <div className="center-workspace">
            <Preview
              onOpenMediaSettings={() => openSettingsSection('media')}
              onUseCurrentFrame={(intent) => {
                setCaptureIntent({
                  ...intent,
                  id: crypto.randomUUID(),
                  projectId: bootstrap.project.id,
                  sourceEpoch: bootstrap.sourceEpoch || 0,
                });
                setSideTab('generation');
              }}
              sourceEpoch={bootstrap.sourceEpoch}
              project={bootstrap.project}
              clock={clock}
              asset={asset?.kind !== 'lut' && asset?.kind !== 'font' ? asset : undefined}
              composed={composed}
              onImport={importMedia}
              prepareProject={async () => {
                const current = await settleProjectState({
                  commitDraft: commitActiveDraft,
                  waitForIdle,
                  read: bridge.bootstrap,
                  accept: acceptBootstrap,
                });
                if (current.kind === 'invalid') {
                  setError('请先处理属性草稿、工程冲突或时间线手势，再准备实时预览。');
                  return null;
                }
                return current.snapshot.project;
              }}
            />
            <div className="transport">
              <div className="transport-left">
                <ClockDisplay editor={editor} />
                <span className="transport-slash">/</span>
                <span className="timecode timecode-muted">
                  {timecode(duration, bootstrap.project.settings)}
                </span>
              </div>
              <div className="transport-buttons">
                <IconButton
                  label="回到开头"
                  onClick={() => {
                    setAssetId(null);
                    clock.seek(0);
                  }}
                >
                  <ArrowLeftToLine size={15} />
                </IconButton>
                <IconButton
                  label="上一帧 · ←"
                  onClick={() => {
                    setAssetId(null);
                    clock.seek(clock.getFrame() - 1);
                  }}
                >
                  <StepBack size={15} />
                </IconButton>
                <PlayControl editor={editor} />
                <IconButton
                  label="下一帧 · →"
                  onClick={() => {
                    setAssetId(null);
                    clock.seek(clock.getFrame() + 1);
                  }}
                >
                  <StepForward size={15} />
                </IconButton>
                <IconButton
                  label="跳到结尾"
                  onClick={() => {
                    setAssetId(null);
                    clock.seek(duration);
                  }}
                >
                  <ArrowRightToLine size={15} />
                </IconButton>
              </div>
              <div className="transport-right">
                <Button
                  variant="ghost"
                  disabled={previewBusy || !frameCapability?.available || !duration}
                  busy={previewBusy}
                  title={frameCapability?.reason || '从后端核对当前合成画面'}
                  onClick={() => void checkFrame()}
                >
                  <LayersIcon />
                  合成检查
                </Button>
                <span className="quiet-badge">
                  {fps(bootstrap.project.settings).toFixed(2)} fps
                </span>
              </div>
            </div>
            <div className="selection-strip">
              <span className="selection-label">当前选区</span>
              {selected ? (
                <>
                  <span className={`mini-kind kind-${selected.kind}`}>
                    {kindIcon(selected.kind, 12)}
                  </span>
                  <strong>
                    {selectedIds.length > 1 ? `已选择 ${selectedIds.length} 个片段` : selected.name}
                  </strong>
                  <span>
                    {framesToSeconds(selected.durationFrames, bootstrap.project.settings).toFixed(
                      2,
                    )}{' '}
                    秒
                  </span>
                  {selectedLocked ? <span className="quiet-badge">轨道已锁定</span> : null}
                  <span className="grow" />
                  <Button
                    variant="ghost"
                    disabled={Boolean(busy) || selectedLocked}
                    onClick={split}
                  >
                    <Scissors size={13} />
                    分割
                  </Button>
                </>
              ) : (
                <span className="muted">在时间线上选择一个片段，继续精修</span>
              )}
            </div>
          </div>
          {sideOpen ? (
            <aside className="right-panel" aria-label="属性与任务">
              <PanelTabs
                id="side-tabs"
                label="右侧面板"
                value={sideTab}
                items={[
                  { value: 'inspector', label: '属性' },
                  { value: 'generation', label: '生成' },
                  { value: 'speech', label: '语音' },
                  { value: 'assistant', label: 'AI 助手' },
                  { value: 'jobs', label: activeJobs ? `任务 ${activeJobs}` : '任务' },
                ]}
                onChange={(value) => void navigateEditor(() => setSideTab(value))}
              />
              <div
                className="right-panel-body"
                id="side-tabs-panel"
                role="tabpanel"
                aria-labelledby={`side-tabs-${sideTab}`}
              >
                {sideTab === 'inspector' ? (
                  selectedClips.length > 1 ? (
                    <SelectionInspector
                      key={selectedIds.join(':')}
                      project={bootstrap.project}
                      clips={selectedClips}
                      busy={Boolean(busy)}
                      onMove={async (delta) => Boolean(await moveSelection(delta))}
                      onTrim={async (edge, delta) =>
                        Boolean(await trimSelection(selectedIds, edge, delta))
                      }
                      onDelete={remove}
                    />
                  ) : (
                    <Inspector
                      applyAnalysis={(action) => run('接纳画面文字', action)}
                      key={`${bootstrap.project.id}:${selected?.id || asset?.id || 'project'}:${editor.draftEpoch}`}
                      project={bootstrap.project}
                      sourceEpoch={bootstrap.sourceEpoch}
                      clip={selected}
                      asset={asset}
                      busy={Boolean(busy)}
                      onUpdate={updateClip}
                      onAddAsset={(item) => addClip(item.kind as Clip['kind'], item)}
                      onImportFont={() =>
                        void navigateEditor(() => {
                          importFont();
                        })
                      }
                      onProjectName={(name) =>
                        void edit('修改工程名称', [{ type: 'update_project', name }])
                      }
                      onRelinkAsset={(item) => void relinkAsset(item)}
                    />
                  )
                ) : sideTab === 'generation' ? (
                  <GenerationPanel
                    connectionsVersion={connectionsVersion}
                    onOpenConnections={() => openSettingsSection('models', 'generation')}
                    timeRange={markedRange}
                    applyPlacement={async (action) => {
                      if (!(await waitForIdle())) return null;
                      return run('添加生成候选到时间线', action);
                    }}
                    onPlaced={(projectId, clip) => {
                      clock.pause();
                      clock.seek(clip.startFrame);
                      setClipSelection({ projectId, ids: [clip.id], primary: clip.id });
                      setAssetId(null);
                      setSideTab('inspector');
                    }}
                    sourceEpoch={bootstrap.sourceEpoch}
                    currentFrame={() => clock.getFrame()}
                    pausePlayback={() => clock.pause()}
                    captureIntent={captureIntent}
                    project={bootstrap.project}
                    selectedClipId={selected?.id}
                    onBootstrap={acceptBootstrap}
                    prepareProject={async () => {
                      const result = await settleProjectState({
                        commitDraft: commitActiveDraft,
                        waitForIdle,
                        read: bridge.bootstrap,
                        accept: acceptBootstrap,
                      });
                      if (result.kind === 'invalid') {
                        setError('请先处理属性草稿、工程冲突或时间线手势，再预览替换。');
                        return null;
                      }
                      return result.snapshot.project;
                    }}
                    applyReplacement={async (action) => {
                      if (!(await waitForIdle())) return null;
                      return run('替换当前镜头', action);
                    }}
                    importToProject={async (action) => {
                      if (!(await waitForIdle())) return null;
                      return run('加入生成素材', action);
                    }}
                  />
                ) : sideTab === 'speech' ? (
                  <SpeechPanel
                    connectionsVersion={connectionsVersion}
                    onOpenConnections={() => openSettingsSection('models', 'speech')}
                    project={bootstrap.project}
                    sourceEpoch={bootstrap.sourceEpoch}
                    onLocateWords={(value) => {
                      if (
                        value.projectId !== bootstrap.project.id ||
                        value.projectRevision !== bootstrap.project.revision
                      )
                        return;
                      clock.pause();
                      clock.seek(value.startFrame);
                      setClipSelection({
                        projectId: value.projectId,
                        ids: [value.clipId],
                        primary: value.clipId,
                      });
                      setAssetId(null);
                    }}
                    onBootstrap={acceptBootstrap}
                    prepareProject={async () => {
                      const current = await settleProjectState({
                        commitDraft: commitActiveDraft,
                        waitForIdle,
                        read: bridge.bootstrap,
                        accept: acceptBootstrap,
                      });
                      if (current.kind === 'invalid') {
                        setError('请先修正尚未提交的工程属性，再提交语音任务。');
                        return null;
                      }
                      return current.snapshot.project;
                    }}
                    applyToProject={async (label, action) => {
                      if (!(await waitForIdle())) return null;
                      return run(label, action);
                    }}
                  />
                ) : sideTab === 'jobs' ? (
                  <div className="job-list">
                    {resourceRecording ? (
                      <Button onClick={() => openSettingsSection('diagnostics')}>
                        资源诊断记录中 · 查看
                      </Button>
                    ) : null}
                    {jobs.length ? (
                      jobs.map((job) => (
                        <div className={`job-item job-${job.status}`} key={job.id}>
                          <div className="job-heading">
                            <strong>
                              {job.type === 'project-collect'
                                ? '工程素材收集'
                                : job.type === 'export'
                                  ? '视频导出'
                                  : job.type === 'preview-proxy'
                                    ? '本地画面代理'
                                    : job.type === 'shot-index'
                                      ? '本地镜头检测'
                                      : job.type}
                            </strong>
                            <span>{statusLabels[job.status]}</span>
                          </div>
                          {job.progress !== undefined && job.status === 'running' ? (
                            <progress max="1" value={job.progress} aria-label="任务进度" />
                          ) : job.status === 'running' ? (
                            <div className="job-pending">
                              <LoaderCircle size={13} className="spin" />
                              正在处理
                            </div>
                          ) : null}
                          <p>{job.message}</p>
                          {job.outputPath ? <p className="file-path">{job.outputPath}</p> : null}
                          {job.type === 'project-collect' &&
                          job.status === 'succeeded' &&
                          job.outputPath ? (
                            <Button
                              disabled={transitionDisabled}
                              onClick={() =>
                                void guarded(() => {
                                  void run('打开工程副本', () =>
                                    bridge.openProjectPath(job.outputPath!),
                                  ).then((value) => {
                                    if (value) {
                                      setSelectedId(null);
                                      setAssetId(null);
                                      clock.seek(0);
                                    }
                                  });
                                })
                              }
                            >
                              打开工程副本
                            </Button>
                          ) : null}
                          {job.status === 'running' || job.status === 'queued' ? (
                            <Button
                              variant="ghost"
                              onClick={() =>
                                void bridge
                                  .cancelJob(job.id)
                                  .then((value) =>
                                    editor.setJobs((list) =>
                                      list.map((item) => (item.id === value.id ? value : item)),
                                    ),
                                  )
                                  .catch((cause) => setError(cause.message))
                              }
                            >
                              取消任务
                            </Button>
                          ) : null}
                        </div>
                      ))
                    ) : (
                      <div className="inspector-empty">
                        <ArrowDownToLine size={28} />
                        <p>当前没有任务</p>
                        <span>导出和后台处理进度会显示在这里。</span>
                      </div>
                    )}
                  </div>
                ) : (
                  <div className="assistant-workspace">
                    <PanelTabs
                      id="assistant-tabs"
                      label="助手功能"
                      value={assistantView}
                      items={[
                        { value: 'cli', label: '规划' },
                        { value: 'execution', label: '执行' },
                        { value: 'workflows', label: '工作流' },
                      ]}
                      onChange={setAssistantView}
                    >
                      <Button variant="ghost" onClick={() => openSettingsSection('mcp')}>
                        连接设置
                      </Button>
                    </PanelTabs>
                    <div
                      className="assistant-workspace-body"
                      id="assistant-tabs-panel"
                      role="tabpanel"
                      aria-labelledby={`assistant-tabs-${assistantView}`}
                    >
                      {assistantView === 'execution' ? (
                        <Suspense
                          fallback={
                            <div className="inspector-empty" role="status">
                              正在加载执行面板…
                            </div>
                          }
                        >
                          <AgentExecutionPanel
                            onOpenSettings={() => openSettingsSection('local-ai')}
                            sourceEpoch={bootstrap.sourceEpoch}
                            project={bootstrap.project}
                            selection={selectedIds}
                            mcpStatus={editor.mcpStatus}
                            onOpenMCP={() => openSettingsSection('mcp')}
                            onProgress={(job, force) =>
                              editor.syncExternalRevision(job.projectId, job.endRevision, force)
                            }
                            prepareProject={async () => {
                              const result = await settleProjectState({
                                commitDraft: commitActiveDraft,
                                waitForIdle,
                                read: bridge.bootstrap,
                                accept: acceptBootstrap,
                              });
                              if (result.kind === 'invalid') {
                                setError('请先处理属性草稿或时间线手势，再核对直接执行。');
                                return null;
                              }
                              return result.snapshot.project;
                            }}
                          />
                        </Suspense>
                      ) : assistantView === 'workflows' ? (
                        <WorkflowPanel
                          onManagePresets={(draft) => {
                            setPresetSettingsDraft(draft);
                            openSettingsSection('presets');
                          }}
                          key={`${bootstrap.project.id}:${bootstrap.sourceEpoch ?? 0}`}
                          project={bootstrap.project}
                          selection={selectedIds}
                          onBootstrap={acceptBootstrap}
                          prepareProject={async () => {
                            const current = await settleProjectState({
                              commitDraft: commitActiveDraft,
                              waitForIdle,
                              read: bridge.bootstrap,
                              accept: acceptBootstrap,
                            });
                            if (current.kind === 'invalid') {
                              setError('请先处理尚未提交的属性草稿，再预览工作流。');
                              return null;
                            }
                            return current.snapshot.project;
                          }}
                          applyToProject={async (action) => {
                            if (!(await waitForIdle())) return null;
                            return run('应用工作流', action);
                          }}
                        />
                      ) : (
                        <AgentPanel
                          onOpenSettings={() => openSettingsSection('local-ai')}
                          project={bootstrap.project}
                          selection={selectedIds}
                          onBootstrap={acceptBootstrap}
                          onOpenGeneration={() => setSideTab('generation')}
                          prepareProject={async () => {
                            const current = await settleProjectState({
                              commitDraft: commitActiveDraft,
                              waitForIdle,
                              read: bridge.bootstrap,
                              accept: acceptBootstrap,
                            });
                            if (current.kind === 'invalid') {
                              setError('请先修正尚未提交的工程属性，再生成剪辑计划。');
                              return null;
                            }
                            return current.snapshot.project;
                          }}
                          applyToProject={async (action) => {
                            if (!(await waitForIdle())) return null;
                            return run('应用剪辑计划', action);
                          }}
                        />
                      )}
                    </div>
                  </div>
                )}
              </div>
            </aside>
          ) : null}
        </main>
        <SequenceTabs
          snapshot={bootstrap}
          disabled={transitionDisabled}
          onSwitch={(target) =>
            void navigateEditor(async () => {
              clock.pause();
              const current = editor.getCurrentBootstrap();
              if (!current) return;
              await run('切换序列', () =>
                sequenceAPI.change({
                  projectId: current.project.id,
                  projectRevision: current.project.revision,
                  sourceEpoch: current.sourceEpoch || 0,
                  action: 'switch',
                  sequenceId: target,
                  idempotencyKey: crypto.randomUUID(),
                }),
              );
            })
          }
          onManage={() =>
            void navigateEditor(() => {
              clock.pause();
              setSequenceDialog(editor.getCurrentBootstrap());
            })
          }
        />
        <Timeline
          sequencePanel={{
            id: 'timeline-sequences-panel',
            labelledBy: `timeline-sequences-${currentSequenceID(bootstrap)}`,
          }}
          markedRange={markedRange}
          onMarkedRangeChange={(range) => setMarkedScope({ key: markedKey, range })}
          sourceEpoch={bootstrap.sourceEpoch}
          prepareProject={async () => {
            const result = await settleProjectState({
              commitDraft: commitActiveDraft,
              waitForIdle,
              read: bridge.bootstrap,
              accept: acceptBootstrap,
            });
            if (result.kind === 'invalid') {
              setError('请先处理工程草稿、冲突或手势，再预览全轨删除。');
              return null;
            }
            return result.snapshot.project;
          }}
          onRippleApply={async (action) => {
            if (!(await waitForIdle())) return null;
            return run('全轨波纹删除', action);
          }}
          project={bootstrap.project}
          clock={clock}
          selected={selectedId}
          selectedIds={selectedIds}
          onSelectMany={(ids) =>
            void navigateEditor(() => {
              setClipSelection({
                projectId: bootstrap.project.id,
                ids,
                primary: ids.includes(selectedId || '') ? selectedId : ids[0] || null,
              });
              setAssetId(null);
              setSideTab('inspector');
            })
          }
          onMoveSelection={(frameDelta, trackDelta) => void moveSelection(frameDelta, trackDelta)}
          onTrimSelection={(ids, edge, delta) => void trimSelection(ids, edge, delta)}
          busy={Boolean(busy) || Boolean(editor.externalChange) || editor.draftFailure}
          canBeginGesture={editor.canBeginInteraction}
          onSelect={chooseClip}
          onUpdateClip={updateClip}
          onUpdateTrack={(track) => void edit('更新轨道', [{ type: 'update_track', track }])}
          onManageTrack={(track) =>
            void navigateEditor(() => {
              const snapshot = editor.getCurrentBootstrap();
              const current = snapshot?.project.tracks.find((t) => t.id === track.id);
              if (snapshot && current) {
                clock.pause();
                setTrackSettings({ snapshot, track: current });
              }
            })
          }
          onSplit={split}
          onDuplicate={() => void duplicate()}
          clipboard={clipboard.status}
          clipboardError={clipboard.error}
          clipboardFocusVersion={clipboardFocusVersion}
          onCopy={() => void clipboardAction('copy')}
          onCut={() => void clipboardAction('cut')}
          onPaste={() => void clipboardAction('paste')}
          onDelete={remove}
          onText={() => addClip('text')}
          onAddTrack={() => showDialog('track', '新轨道')}
        />
        <footer className="status-bar">
          <div className="status-message" role="status" aria-live="polite">
            {busy ? (
              <LoaderCircle size={12} className="spin" />
            ) : (
              <span className={`status-dot ${bootstrap.dirty ? 'pending' : ''}`} />
            )}
            <span>{busy || (bootstrap.dirty ? '有未保存的更改' : notice || '工程已就绪')}</span>
          </div>
          <div className="status-meta">
            {editor.mcpStatus?.running ? (
              <Button
                variant="ghost"
                onClick={() => {
                  if (editor.mcpStatus?.executionId) {
                    void navigateEditor(() => {
                      setSideOpen(true);
                      setSideTab('assistant');
                      setAssistantView('execution');
                    });
                  } else openSettingsSection('mcp');
                }}
              >
                {editor.mcpStatus.executionId
                  ? 'AI 执行中'
                  : !editor.mcpStatus.scopeValid
                    ? 'MCP 待重连'
                    : editor.mcpStatus.allowEdits
                      ? 'MCP 可编辑'
                      : 'MCP 只读'}
              </Button>
            ) : null}
            <span>{bootstrap.project.clips.length} 个片段</span>
            <span>
              {bootstrap.project.settings.width} × {bootstrap.project.settings.height}
            </span>
            <span>{bootstrap.project.settings.sampleRate / 1000} kHz</span>
            {resourceRecording ? (
              <Button variant="ghost" onClick={() => openSettingsSection('diagnostics')}>
                诊断记录中
              </Button>
            ) : null}
          </div>
        </footer>

        {recoveryOpen ? (
          <Suspense fallback={<p role="status">正在打开恢复中心…</p>}>
            <RecoveryCenterDialog
              key={`${bootstrap.project.id}:${bootstrap.sourceEpoch ?? 0}`}
              scope={{
                projectId: bootstrap.project.id,
                projectRevision: bootstrap.project.revision,
                sourceEpoch: bootstrap.sourceEpoch ?? 0,
              }}
              dirty={bootstrap.dirty}
              onClose={() => {
                setRecoveryOpen(false);
                void recovery.refresh();
              }}
              onReturnFocus={() => document.getElementById('project-menu-trigger')?.focus()}
              onDrafts={() => {
                setRecoveryOpen(false);
                setDraftsOpen(true);
              }}
              onManage={() => {
                setRecoveryOpen(false);
                setStorageKind('recovery');
                setSettingsSection('storage');
                setSettingsOpen(true);
                settingsReturnFocus.current = document.getElementById('project-menu-trigger');
              }}
              onRestore={async (input) => {
                if (!(await waitForIdle())) throw new Error('请先处理当前草稿或工程冲突，再恢复。');
                let failure: unknown;
                const result = await run('恢复工程副本', async () => {
                  try {
                    return await recoveryAPI.restore(input);
                  } catch (cause) {
                    failure = cause;
                    throw cause;
                  }
                });
                if (!result) throw failure || new Error('当前操作尚未结束，请稍后重试。');
                clock.pause();
                clock.seek(0);
                setSelectedId(null);
                setAssetId(null);
                void recovery.refresh();
                setNotice('已恢复为未保存工程；请另选位置保存，原文件未覆盖。');
              }}
            />
          </Suspense>
        ) : null}
        {draftsOpen ? (
          <Suspense fallback={<p role="status">正在打开输入草稿…</p>}>
            <InspectorDraftsDialog
              scope={{
                projectId: bootstrap.project.id,
                projectRevision: bootstrap.project.revision,
                sourceEpoch: bootstrap.sourceEpoch ?? 0,
              }}
              dirty={bootstrap.dirty}
              onClose={() => setDraftsOpen(false)}
              onReturnFocus={() => document.getElementById('project-menu-trigger')?.focus()}
              onChange={async (action) => {
                if (!(await waitForIdle())) throw Error('请先处理当前工程冲突。');
                let failure: unknown;
                const result = await run('恢复属性输入', async () => {
                  try {
                    return await action();
                  } catch (e) {
                    failure = e;
                    throw e;
                  }
                });
                if (!result) throw failure || Error('操作未完成，请重试。');
                clock.pause();
                setSelectedId(null);
                setAssetId(null);
                void recovery.refresh();
              }}
            />
          </Suspense>
        ) : null}
        {trackSettings ? (
          <TrackSettingsDialog
            snapshot={trackSettings.snapshot}
            track={trackSettings.track}
            onClose={() => setTrackSettings(null)}
            onReturnFocus={() => {
              (
                document.getElementById(`track-settings-${trackSettings.track.id}`) ||
                document.querySelector<HTMLElement>('.timeline-grid')
              )?.focus();
            }}
            onApply={(commands, label) =>
              run(label, () =>
                bridge.applyScoped(
                  {
                    projectId: trackSettings.snapshot.project.id,
                    projectRevision: trackSettings.snapshot.project.revision,
                    sourceEpoch: trackSettings.snapshot.sourceEpoch || 0,
                  },
                  {
                    baseRevision: trackSettings.snapshot.project.revision,
                    idempotencyKey: crypto.randomUUID(),
                    label,
                    commands,
                  },
                ),
              )
            }
          />
        ) : null}
        {sequenceDialog ? (
          <Suspense fallback={<p role="status">正在打开序列管理…</p>}>
            <SequenceManagerDialog
              snapshot={sequenceDialog}
              onApply={(operation, label) => run(label, operation)}
              onClose={() => setSequenceDialog(null)}
              onReturnFocus={() => document.getElementById('sequence-manager-trigger')?.focus()}
            />
          </Suspense>
        ) : null}
        {batchRelinkOpen ? (
          <Suspense fallback={<p role="status">正在打开素材修复…</p>}>
            <BatchRelinkDialog
              key={`${bootstrap.project.id}:${bootstrap.sourceEpoch ?? 0}`}
              scope={{
                projectId: bootstrap.project.id,
                projectRevision: bootstrap.project.revision,
                sourceEpoch: bootstrap.sourceEpoch ?? 0,
              }}
              desktop={isDesktop()}
              onApply={(operation, label) => run(label, operation)}
              onClose={() => setBatchRelinkOpen(false)}
              onReturnFocus={() => document.getElementById('batch-relink-trigger')?.focus()}
            />
          </Suspense>
        ) : null}
        {collectionOpen ? (
          <Suspense fallback={<p role="status">正在打开工程收集…</p>}>
            <ProjectCollectDialog
              key={`${bootstrap.project.id}:${bootstrap.sourceEpoch ?? 0}`}
              scope={{
                projectId: bootstrap.project.id,
                projectRevision: bootstrap.project.revision,
                sourceEpoch: bootstrap.sourceEpoch ?? 0,
              }}
              projectName={bootstrap.project.name}
              projectPath={bootstrap.projectPath}
              desktop={isDesktop()}
              onStarted={(job) => {
                editor.setJobs((list) => [job, ...list.filter((item) => item.id !== job.id)]);
                setSideOpen(true);
                setSideTab('jobs');
              }}
              onClose={() => setCollectionOpen(false)}
              onReturnFocus={() => document.getElementById('project-menu-trigger')?.focus()}
            />
          </Suspense>
        ) : null}
        {versionsOpen ? (
          <Suspense fallback={<p role="status">正在打开工程版本…</p>}>
            <ProjectVersionsDialog
              key={`${bootstrap.project.id}:${bootstrap.sourceEpoch ?? 0}`}
              scope={{
                projectId: bootstrap.project.id,
                projectRevision: bootstrap.project.revision,
                sourceEpoch: bootstrap.sourceEpoch ?? 0,
              }}
              dirty={bootstrap.dirty}
              onManage={() => {
                setVersionsOpen(false);
                setStorageKind('version');
                setSettingsSection('storage');
                setSettingsOpen(true);
                settingsReturnFocus.current = document.getElementById('project-menu-trigger');
              }}
              onClose={() => setVersionsOpen(false)}
              onReturnFocus={() => document.getElementById('project-menu-trigger')?.focus()}
              onRestore={async (input) => {
                if (!(await waitForIdle()))
                  throw new Error('请先处理当前草稿或工程冲突，再恢复版本。');
                let failure: unknown;
                const result = await run('恢复工程版本', async () => {
                  try {
                    return (await projectVersionsAPI.restore(input)).bootstrap;
                  } catch (cause) {
                    failure = cause;
                    throw cause;
                  }
                });
                if (!result) throw failure || new Error('当前工程操作尚未结束，请稍后重试。');
                clock.pause();
                clock.seek(0);
                setSelectedId(null);
                setAssetId(null);
                setNotice('已恢复工程版本；恢复前状态已保留，原工程文件未覆盖。');
              }}
            />
          </Suspense>
        ) : null}
        <Modal
          open={dialog !== null}
          className={dialog === 'new' ? 'new-project-dialog' : ''}
          onCloseAutoFocus={(event) => {
            if (settingsOpen) {
              event.preventDefault();
              document.getElementById('settings-return')?.focus();
              return;
            }
            if (returnToNewTrigger.current) {
              event.preventDefault();
              returnToNewTrigger.current = false;
              document.getElementById('new-project-trigger')?.focus();
            }
          }}
          title={dialog ? dialogTitles[dialog] : ''}
          busy={Boolean(busy) || exportBusy}
          description={
            dialog === 'confirm' || dialog === 'close'
              ? '当前工程有未保存的更改，保存后可以继续编辑。'
              : dialog === 'import-paths' || dialog === 'import-font'
                ? '输入本机文件的完整路径，每行一个。文件会导入当前工程。'
                : dialog === 'export'
                  ? '导出使用当前工程快照，你可以在任务面板查看实际进度。'
                  : dialog === 'import-subtitles'
                    ? '导入静态 SRT 字幕并按工程时间放入字幕轨，重叠字幕会自动分轨。此操作不进行语音转写。'
                    : dialog === 'export-subtitles'
                      ? '将全部字幕轨（包含隐藏轨）导出为一个 SRT 文件，不包含普通文字标题。'
                      : dialog === 'relink-asset'
                        ? `请选择“${relinkTarget?.name || '原素材'}”在本机的新位置。需要是同一份素材文件，当前片段与时间安排会保留。`
                        : undefined
          }
          onOpenChange={(open) => {
            if (!open && !busy && !exportBusy) {
              if (dialog === 'close') {
                closing.current = false;
                void bridge.confirmClose('cancel');
              }
              setDialog(null);
              savedAction.current = null;
            }
          }}
        >
          {dialog === 'confirm' ? (
            <div className="dialog-actions">
              <Button
                onClick={() => {
                  setDialog(null);
                  nextAction.current = null;
                }}
              >
                继续编辑
              </Button>
              <Button
                variant="ghost"
                onClick={() => {
                  const action = nextAction.current;
                  nextAction.current = null;
                  setDialog(null);
                  action?.();
                }}
              >
                放弃更改并继续
              </Button>
              <Button
                variant="primary"
                busy={Boolean(busy)}
                onClick={() => void saveBeforeContinue()}
              >
                先保存
              </Button>
            </div>
          ) : dialog === 'external-refresh' ? (
            <div>
              <p className="dialog-description">
                重新读取将放弃尚未提交的本地属性草稿，保留后端已经提交的工程编辑。需要的文字或数值请先复制。
              </p>
              <div className="dialog-actions">
                <Button onClick={() => setDialog(null)}>保留草稿</Button>
                <Button
                  variant="primary"
                  onClick={() =>
                    void editor.reloadExternal().then((success) => {
                      if (success) setDialog(null);
                    })
                  }
                >
                  放弃本地草稿并读取
                </Button>
              </div>
            </div>
          ) : dialog === 'close' ? (
            <div className="dialog-actions">
              <Button
                onClick={() => {
                  setDialog(null);
                  closing.current = false;
                  void bridge.confirmClose('cancel');
                }}
              >
                继续编辑
              </Button>
              <Button variant="ghost" onClick={() => void bridge.confirmClose('discard')}>
                放弃并关闭
              </Button>
              <Button
                variant="primary"
                disabled={closeInvalid}
                onClick={() =>
                  void bridge.confirmClose('save').catch((cause) => setDialogError(cause.message))
                }
              >
                保存并关闭
              </Button>
            </div>
          ) : (
            <form
              noValidate
              onSubmit={submitDialog}
              onCompositionStartCapture={() => {
                newProjectComposing.current = true;
              }}
              onCompositionEndCapture={() => {
                newProjectComposing.current = false;
              }}
            >
              {dialog === 'track' ? (
                <Field label="轨道类型">
                  <Select
                    value={trackKind}
                    onChange={(e) => setTrackKind(e.target.value as TrackKind)}
                  >
                    {['video', 'audio', 'text', 'caption'].map((kind) => (
                      <option value={kind} key={kind}>
                        {trackNames[kind]}轨
                      </option>
                    ))}
                  </Select>
                </Field>
              ) : null}
              <Field
                label={
                  dialog === 'new'
                    ? '工程名称'
                    : dialog === 'track'
                      ? '轨道名称'
                      : dialog === 'import-paths' || dialog === 'import-font'
                        ? '素材路径'
                        : dialog === 'import-subtitles' || dialog === 'export-subtitles'
                          ? 'SRT 文件路径'
                          : dialog === 'relink-asset'
                            ? '原素材的新位置'
                            : dialog === 'export'
                              ? '导出路径'
                              : '工程文件路径'
                }
                hint={
                  dialog === 'export' && isDesktop()
                    ? '留空后将打开系统保存对话框。'
                    : dialog === 'save-path'
                      ? '例如 /Users/你的用户名/Documents/我的影片.aividcut'
                      : undefined
                }
              >
                {dialog === 'import-paths' || dialog === 'import-font' ? (
                  <textarea
                    className="input path-input resize-none"
                    rows={5}
                    value={dialogValue}
                    onChange={(e) => setDialogValue(e.target.value)}
                    autoFocus
                    aria-invalid={Boolean(dialogError)}
                    aria-describedby={dialogError ? 'dialog-error' : undefined}
                  />
                ) : (
                  <Input
                    id={dialog === 'new' ? 'new-project-name' : undefined}
                    maxLength={dialog === 'new' ? 120 : undefined}
                    disabled={Boolean(busy)}
                    autoFocus
                    value={dialogValue}
                    onChange={(e) => {
                      setDialogValue(e.target.value);
                      if (dialog === 'new') setNewProjectErrors((e) => ({ ...e, name: undefined }));
                    }}
                    placeholder={
                      dialog === 'export'
                        ? '/完整路径/我的影片.mp4'
                        : dialog === 'import-subtitles' || dialog === 'export-subtitles'
                          ? '/完整路径/我的字幕.srt'
                          : undefined
                    }
                    aria-invalid={
                      dialog === 'new'
                        ? Boolean(newProjectErrors.name)
                        : Boolean(dialogError) && !(dialog === 'export' && exportInvalid)
                    }
                    aria-describedby={
                      dialog === 'new'
                        ? newProjectErrors.name
                          ? 'new-project-error-name'
                          : undefined
                        : dialogError && !(dialog === 'export' && exportInvalid)
                          ? 'dialog-error'
                          : undefined
                    }
                  />
                )}
              </Field>
              {dialog === 'new' ? (
                <>
                  {newProjectErrors.name ? (
                    <p className="form-error" id="new-project-error-name">
                      {newProjectErrors.name}
                    </p>
                  ) : null}
                  <NewProjectSettings
                    value={newProjectDraft}
                    errors={newProjectErrors}
                    disabled={Boolean(busy)}
                    onChange={(v) => {
                      setNewProjectDraft(v);
                      setNewProjectErrors({});
                      setDialogError('');
                    }}
                  />
                </>
              ) : null}
              {dialog === 'export' ? (
                <>
                  <p className="muted">
                    导出当前序列：{currentSequenceName(bootstrap)}。任务使用启动时的固定快照。
                  </p>
                  <ExportSettings
                    value={exportDraft}
                    onChange={(value) => {
                      setExportDraft(value);
                      setExportInvalid(undefined);
                      setDialogError('');
                    }}
                    invalidField={exportInvalid}
                    duration={projectEnd(bootstrap.project)}
                    nativeAvailable={Boolean(bootstrap.capabilities.nativeExport?.available)}
                    disabled={Boolean(busy) || exportBusy}
                  />
                </>
              ) : null}
              {dialogError || error ? (
                <p className="form-error" id="dialog-error" role="alert">
                  {dialogError || error}
                </p>
              ) : null}
              <div className="dialog-actions">
                <Button
                  onClick={() => {
                    setDialog(null);
                    savedAction.current = null;
                    nextAction.current = null;
                  }}
                  disabled={Boolean(busy) || exportBusy}
                >
                  取消
                </Button>
                <Button type="submit" variant="primary" busy={Boolean(busy) || exportBusy}>
                  {dialog === 'export'
                    ? '开始导出'
                    : dialog === 'new'
                      ? '创建工程'
                      : dialog === 'track'
                        ? '添加轨道'
                        : dialog === 'import-paths' || dialog === 'import-font'
                          ? '导入素材'
                          : dialog === 'import-subtitles'
                            ? '导入 SRT'
                            : dialog === 'export-subtitles'
                              ? '导出 SRT'
                              : dialog === 'relink-asset'
                                ? '重新定位素材'
                                : dialog === 'open-path'
                                  ? '打开工程'
                                  : '保存工程'}
                </Button>
              </div>
            </form>
          )}
          {dialogError && dialog === 'close' ? (
            <p className="form-error" role="alert">
              {dialogError}
            </p>
          ) : null}
        </Modal>
      </div>
      {settingsOpen ? (
        <SettingsPage
          key={settingsFormEpoch}
          appearance={appearance}
          section={settingsSection}
          onSectionChange={navigateSettings}
          onReturn={() => navigateSettings('editor')}
          busy={settingsBusy}
          contents={{
            presets: (
              <WorkflowPanel
                presetManagement
                project={bootstrap.project}
                selection={[]}
                onBootstrap={acceptBootstrap}
                initialDraft={presetSettingsDraft}
                onBusyChange={setSettingsBusy}
                onDirtyChange={setSettingsDirty}
              />
            ),
            diagnostics: (
              <Suspense fallback={<p role="status">正在读取诊断…</p>}>
                <ResourceMonitorDialog
                  inline
                  scope={{
                    projectId: bootstrap.project.id,
                    projectRevision: bootstrap.project.revision,
                    sourceEpoch: bootstrap.sourceEpoch ?? 0,
                  }}
                  onClose={() => navigateSettings('editor')}
                  onReturnFocus={() => {}}
                  onRecording={setResourceRecording}
                  onBusyChange={setSettingsBusy}
                />
              </Suspense>
            ),
            storage: (
              <Suspense fallback={<p role="status">正在读取资料…</p>}>
                <SnapshotStorageDialog
                  inline
                  scope={{
                    projectId: bootstrap.project.id,
                    projectRevision: bootstrap.project.revision,
                    sourceEpoch: bootstrap.sourceEpoch ?? 0,
                  }}
                  initialKind={storageKind || 'version'}
                  onClose={() => navigateSettings('editor')}
                  onBusyChange={setSettingsBusy}
                />
              </Suspense>
            ),
            'local-ai': (
              <LocalAISettings onBusyChange={setSettingsBusy} onDirtyChange={setSettingsDirty} />
            ),
            media: (
              <Suspense fallback={<p role="status">正在读取媒体工具…</p>}>
                <MediaToolsDialog
                  inline
                  onClose={() => navigateSettings('editor')}
                  onReturnFocus={() => {}}
                  onBusyChange={setSettingsBusy}
                  onDirtyChange={setSettingsDirty}
                />
              </Suspense>
            ),
            models: (
              <ModelServicesSettings
                key={modelSettingsKind}
                initialKind={modelSettingsKind}
                onBusyChange={setSettingsBusy}
                onDirtyChange={setSettingsDirty}
              />
            ),
          }}
          mcpContent={
            <MCPPanel
              key={`${bootstrap.project.id}:${bootstrap.sourceEpoch ?? 0}`}
              sourceEpoch={bootstrap.sourceEpoch}
              onBusyChange={setSettingsBusy}
              applyAnalysis={(action) => run('接纳画面分析', action)}
              onOpenExecution={() => {
                setSettingsOpen(false);
                setSideTab('assistant');
                setAssistantView('execution');
              }}
              project={bootstrap.project}
              status={editor.mcpStatus}
              onStatus={editor.acceptMCPStatus}
              onOpenJobs={(kind) => {
                setSettingsOpen(false);
                setSideTab(
                  kind === 'generation' ? 'generation' : kind === 'speech' ? 'speech' : 'jobs',
                );
              }}
              prepareProject={async () => {
                const current = await settleProjectState({
                  commitDraft: commitActiveDraft,
                  waitForIdle,
                  read: bridge.bootstrap,
                  accept: acceptBootstrap,
                });
                if (current.kind === 'invalid') {
                  throw new Error('请返回应用处理尚未提交的属性草稿，再启动工具连接。');
                }
                return current.snapshot.project;
              }}
            />
          }
        />
      ) : null}
      {settingsLeaveDialog}
    </TooltipProvider>
  );
}

function InfoIcon() {
  return <span className="error-icon">!</span>;
}
function LayersIcon() {
  return <Square size={13} />;
}

export default App;
