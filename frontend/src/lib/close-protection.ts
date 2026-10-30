import type { Bootstrap } from '../types';

export interface CloseProtectionPorts {
  commitDraft: () => boolean;
  waitForIdle: () => Promise<boolean>;
  read: () => Promise<Bootstrap>;
  accept: (snapshot: Bootstrap) => void;
  discard: () => Promise<unknown>;
}

export function hasEditorDraft(documentRef: Document = document): boolean {
  return Boolean(
    documentRef.querySelector(
      '[data-editor-draft-scope] [data-editor-draft="true"], [data-editor-draft-scope] [aria-invalid="true"]',
    ),
  );
}

// 只核对工程属性草稿，生成/连接表单的错误不属于待保存工程。
export function hasTimelineGesture(documentRef: Document = document): boolean {
  return Boolean(documentRef.querySelector('[data-editor-gesture="true"]'));
}
export function commitActiveDraft(documentRef: Document = document): boolean {
  const active = documentRef.activeElement;
  const editorDraft = active?.closest('[data-editor-draft-scope]');
  const control =
    active instanceof HTMLInputElement ||
    active instanceof HTMLTextAreaElement ||
    active instanceof HTMLSelectElement;
  const activeValid = !editorDraft || !control || active.validity.valid;
  const otherInvalid = Array.from(
    documentRef.querySelectorAll('[data-editor-draft-scope] [aria-invalid="true"]'),
  ).some((element) => element !== active);
  if (control) active.blur();
  return activeValid && !otherInvalid;
}

export async function settleProjectState(
  ports: Omit<CloseProtectionPorts, 'discard'>,
): Promise<{ kind: 'invalid' } | { kind: 'ready'; snapshot: Bootstrap }> {
  const draftValid = ports.commitDraft();
  const submitted = await ports.waitForIdle();
  if (!draftValid || !submitted) return { kind: 'invalid' };
  const current = await ports.read();
  ports.accept(current);
  return { kind: 'ready', snapshot: current };
}

// 先提交界面草稿，再读取权威 dirty；不能用发起关闭时的旧快照判断。
export async function prepareToClose(
  ports: CloseProtectionPorts,
): Promise<'closed' | 'dirty' | 'invalid'> {
  const result = await settleProjectState(ports);
  if (result.kind === 'invalid') return 'invalid';
  const current = result.snapshot;
  if (current.dirty === false) {
    await ports.discard();
    return 'closed';
  }
  return 'dirty';
}
