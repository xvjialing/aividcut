import { describe, expect, it, vi } from 'vitest';
import { prepareToClose } from '../src/lib/close-protection';
import type { Bootstrap } from '../src/types';

const snapshot = (dirty: boolean): Bootstrap => ({
  dirty,
  projectPath: '',
  canUndo: false,
  canRedo: false,
  capabilities: {},
  project: {
    id: 'project',
    name: '影片',
    schemaVersion: 1,
    revision: dirty ? 1 : 0,
    settings: { width: 1920, height: 1080, fpsNum: 30, fpsDen: 1, sampleRate: 48000 },
    assets: [],
    tracks: [],
    clips: [],
  },
});

describe('窗口关闭前的草稿保护', () => {
  it('先blur提交并等待，再读取最新dirty，不能根据旧干净工程直接关闭', async () => {
    const order: string[] = [];
    let latest = snapshot(false);
    let settle!: (value: boolean) => void;
    const waiting = new Promise<boolean>((resolve) => {
      settle = resolve;
    });
    const discard = vi.fn();
    const result = prepareToClose({
      commitDraft: () => {
        order.push('blur');
        latest = snapshot(true);
        return true;
      },
      waitForIdle: () => {
        order.push('wait');
        return waiting;
      },
      read: async () => {
        order.push('read');
        return latest;
      },
      accept: () => {
        order.push('accept');
      },
      discard,
    });
    expect(order).toEqual(['blur', 'wait']);
    expect(discard).not.toHaveBeenCalled();
    settle(true);
    expect(await result).toBe('dirty');
    expect(order).toEqual(['blur', 'wait', 'read', 'accept']);
    expect(discard).not.toHaveBeenCalled();
  });
  it('最新权威工程确实干净时才自动确认关闭', async () => {
    const discard = vi.fn().mockResolvedValue(undefined);
    const accept = vi.fn();
    const current = snapshot(false);
    expect(
      await prepareToClose({
        commitDraft: () => true,
        waitForIdle: async () => true,
        read: async () => current,
        accept,
        discard,
      }),
    ).toBe('closed');
    expect(accept).toHaveBeenCalledWith(current);
    expect(discard).toHaveBeenCalledOnce();
  });
  it('输入无效或提交失败时不能跳过草稿而自动关闭', async () => {
    for (const [valid, submitted] of [
      [false, true],
      [true, false],
    ]) {
      const discard = vi.fn();
      const read = vi.fn();
      expect(
        await prepareToClose({
          commitDraft: () => valid,
          waitForIdle: async () => submitted,
          read,
          accept: vi.fn(),
          discard,
        }),
      ).toBe('invalid');
      expect(read).not.toHaveBeenCalled();
      expect(discard).not.toHaveBeenCalled();
    }
  });
  it('状态核对失败时保留窗口，不能伪造安全关闭', async () => {
    const discard = vi.fn();
    await expect(
      prepareToClose({
        commitDraft: () => true,
        waitForIdle: async () => true,
        read: async () => {
          throw new Error('服务暂时不可用');
        },
        accept: vi.fn(),
        discard,
      }),
    ).rejects.toThrow('服务暂时不可用');
    expect(discard).not.toHaveBeenCalled();
  });
});
