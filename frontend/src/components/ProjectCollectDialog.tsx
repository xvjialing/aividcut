import { useEffect, useId, useRef, useState } from 'react';
import type { Job, ProjectScope } from '../types';
import type { ProjectCollectAPI, ProjectCollectionSummary } from '../project-collect-types';
import { projectCollectAPI } from '../lib/bridge';
import {
  absoluteCollectionParent,
  collectionSize,
  defaultCollectionName,
  projectParentDirectory,
  validCollectionName,
} from '../lib/project-collect';
import { Button, Field, Input, Modal } from './ui';

export function ProjectCollectDialog({
  scope,
  projectName,
  projectPath,
  desktop,
  onStarted,
  onClose,
  onReturnFocus,
  api = projectCollectAPI,
}: {
  scope: ProjectScope;
  projectName: string;
  projectPath: string;
  desktop: boolean;
  onStarted: (job: Job) => void;
  onClose: () => void;
  onReturnFocus?: () => void;
  api?: ProjectCollectAPI;
}) {
  const [parent, setParent] = useState(projectParentDirectory(projectPath)),
    [name, setName] = useState(defaultCollectionName(projectName));
  const [summary, setSummary] = useState<ProjectCollectionSummary | null>(null),
    [busy, setBusy] = useState('核对'),
    [error, setError] = useState(''),
    [invalid, setInvalid] = useState<'parent' | 'name' | null>(null),
    [reload, setReload] = useState(0);
  const epoch = useRef(0),
    pending = useRef(false),
    parentRef = useRef<HTMLInputElement>(null),
    nameRef = useRef<HTMLInputElement>(null);
  const errorID = useId();
  useEffect(() => {
    const version = ++epoch.current;
    setBusy('核对');
    setError('');
    setSummary(null);
    void api
      .inspect(scope)
      .then((value) => {
        if (epoch.current === version) setSummary(value);
      })
      .catch((cause) => {
        if (epoch.current === version)
          setError(cause instanceof Error ? cause.message : '无法核对素材，请重新核对。');
      })
      .finally(() => {
        if (epoch.current === version) setBusy('');
      });
    return () => {
      epoch.current++;
    };
  }, [api, scope.projectId, scope.projectRevision, scope.sourceEpoch, reload]);
  async function choose() {
    if (pending.current || busy) return;
    pending.current = true;
    setBusy('选择');
    setError('');
    try {
      const value = await api.chooseDirectory();
      if (value) {
        setParent(value);
        setInvalid(null);
      }
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : '无法打开文件夹选择器，请直接输入路径。');
    } finally {
      pending.current = false;
      setBusy('');
    }
  }
  async function submit(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (pending.current || busy || !summary) return;
    const directory = parent.trim(),
      folder = name.trim();
    if (!absoluteCollectionParent(directory)) {
      setInvalid('parent');
      setError('请输入已存在的上级文件夹完整路径。');
      parentRef.current?.focus();
      return;
    }
    if (!validCollectionName(folder)) {
      setInvalid('name');
      setError('副本名称需为1到80个字符，不能包含路径、系统保留名称或以点结尾。');
      nameRef.current?.focus();
      return;
    }
    pending.current = true;
    setBusy('启动');
    setInvalid(null);
    setError('');
    try {
      const job = await api.start({ ...scope, parentDirectory: directory, folderName: folder });
      onStarted(job);
      onClose();
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : '未能启动收集，输入已保留，请重试。');
    } finally {
      pending.current = false;
      setBusy('');
    }
  }
  return (
    <Modal
      open
      title="收集工程副本"
      className="replacement-dialog project-collect-dialog"
      busy={Boolean(busy)}
      onOpenChange={(open) => {
        if (!open && !busy && !pending.current) onClose();
      }}
      onCloseAutoFocus={(event) => {
        if (onReturnFocus) {
          event.preventDefault();
          onReturnFocus();
        }
      }}
      description="把完整工程和全部登记素材复制到新文件夹，原工程与素材留在原处。"
    >
      <form noValidate className="project-collect-form" onSubmit={(event) => void submit(event)}>
        <div className="replacement-dialog-body">
          <p>
            工程：{projectName} · 修订 {scope.projectRevision}
          </p>
          {summary ? (
            <p role="status">
              {summary.assetCount} 个素材 · 源文件共 {collectionSize(summary.sourceBytes)}
              。请预留对应空间；相同内容会复用副本文件。
            </p>
          ) : busy === '核对' ? (
            <p role="status">正在核对登记素材…</p>
          ) : null}
          <Field label="上级文件夹" hint="在该位置新建副本文件夹；已有目标不会覆盖。">
            <Input
              ref={parentRef}
              value={parent}
              onChange={(event) => {
                setParent(event.target.value);
                setInvalid(null);
              }}
              disabled={Boolean(busy)}
              aria-invalid={invalid === 'parent' || undefined}
              aria-describedby={invalid === 'parent' ? errorID : undefined}
              placeholder={desktop ? '选择文件夹或输入完整路径' : '/完整路径/备份目录'}
            />
          </Field>
          <div className="speech-actions">
            {desktop ? (
              <Button disabled={Boolean(busy)} onClick={() => void choose()}>
                选择文件夹…
              </Button>
            ) : null}
            <Button disabled={Boolean(busy)} onClick={() => setReload((value) => value + 1)}>
              重新核对素材
            </Button>
          </div>
          <Field label="副本文件夹名称" hint="整个文件夹一起迁移，打开其中的 project.aividcut。">
            <Input
              ref={nameRef}
              value={name}
              onChange={(event) => {
                setName(event.target.value);
                setInvalid(null);
              }}
              disabled={Boolean(busy)}
              aria-invalid={invalid === 'name' || undefined}
              aria-describedby={invalid === 'name' ? errorID : undefined}
            />
          </Field>
          <p className="field-hint">
            包含视频、音频、图片、LUT、字幕和当前剪辑，包括尚未上轨的素材。生成历史、账户设置与工程版本库留在当前设备。目标设备的字体可能不同，迁移后请先预览核对文字。
          </p>
          {summary?.unprovenSourceCount ? (
            <p className="field-hint">
              {summary.unprovenSourceCount}{' '}
              个素材没有原始内容指纹，将核对本次源文件与副本，不据此判断导入以来是否改动。
            </p>
          ) : null}
          <p className="field-hint">
            启动后可继续剪辑；副本对应本次修订。任务面板显示实际进度，可取消未完成收集。
          </p>
        </div>
        <div className="replacement-dialog-status">
          {error ? (
            <p id={errorID} role="alert" className="form-error">
              {error}
            </p>
          ) : null}
        </div>
        <div className="dialog-actions replacement-dialog-actions">
          <Button disabled={Boolean(busy)} onClick={onClose}>
            取消收集
          </Button>
          <Button
            type="submit"
            variant="primary"
            busy={busy === '启动'}
            disabled={Boolean(busy) || !summary}
          >
            开始收集副本
          </Button>
        </div>
      </form>
    </Modal>
  );
}
