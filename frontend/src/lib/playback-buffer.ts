import type { PlaybackFrame } from '../playback-types';

export class PlaybackBundleBuffer {
  private values: Array<{ frame: PlaybackFrame; consumed: boolean; released: boolean }> = [];
  private latest: PlaybackFrame | null = null;
  private seen = -1;
  epoch = 0;
  reset(epoch: number) {
    this.epoch = epoch;
    this.values = [];
    this.latest = null;
    this.seen = -1;
  }
  push(frame: PlaybackFrame) {
    if (frame.header.epoch !== this.epoch) return false;
    if (frame.header.sequence !== (this.seen < 0 ? 1 : this.seen + 1) || this.values.length >= 3)
      throw new Error('实时预览队列超过限制或顺序无效，已停止。');
    this.seen = frame.header.sequence;
    this.values.push({ frame, consumed: !frame.header.playing, released: false });
    return true;
  }
  consumed(sequence: number) {
    const value = this.values.find((item) => item.frame.header.sequence === sequence);
    if (value) value.consumed = true;
  }
  release(sequence: number) {
    const value = this.values.find((item) => item.frame.header.sequence === sequence);
    if (value) value.released = true;
  }
  candidate(position: number): PlaybackFrame | null {
    const available = this.values.filter((item) => item.frame.header.position <= position);
    const selected =
      available.at(-1)?.frame ||
      (this.latest && this.latest.header.position <= position ? this.latest : null);
    for (const item of available) {
      if (item.frame !== selected) item.released = true;
      if (item.frame === selected) this.latest = item.frame;
    }
    return selected;
  }
  acknowledge(): PlaybackFrame[] {
    const values: PlaybackFrame[] = [];
    while (this.values[0]?.consumed && this.values[0].released) {
      const entry = this.values.shift()!;
      values.push(entry.frame);
    }
    return values;
  }
  get length() {
    return this.values.length;
  }
}
