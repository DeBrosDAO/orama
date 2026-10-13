import { describe, expect, it, vi } from "vitest";
import { createClockStore } from "./clock-store";
import type { ClockTimers } from "./clock-store";

const INTERVAL = 10_000;

function harness() {
  let time = 1_000;
  const ticks: Array<() => void> = [];
  const timers: ClockTimers = {
    start: vi.fn((tick) => {
      ticks.push(tick);
      return ticks.length as unknown as ReturnType<typeof setInterval>;
    }),
    stop: vi.fn(),
  };
  const store = createClockStore(INTERVAL, () => time, timers);
  return {
    store,
    timers,
    ticks,
    advance(ms: number) {
      time += ms;
    },
  };
}

describe("createClockStore", () => {
  it("starts a single interval for many subscribers", () => {
    const h = harness();
    const stops = [h.store.subscribe(() => {}), h.store.subscribe(() => {}), h.store.subscribe(() => {})];
    expect(h.timers.start).toHaveBeenCalledTimes(1);
    stops.forEach((s) => s());
  });

  it("stops the interval only when the last subscriber leaves", () => {
    const h = harness();
    const a = h.store.subscribe(() => {});
    const b = h.store.subscribe(() => {});
    a();
    expect(h.timers.stop).not.toHaveBeenCalled();
    b();
    expect(h.timers.stop).toHaveBeenCalledTimes(1);
  });

  it("notifies every subscriber with a new snapshot on each tick", () => {
    const h = harness();
    const first = vi.fn();
    const second = vi.fn();
    h.store.subscribe(first);
    h.store.subscribe(second);
    const before = h.store.getSnapshot();
    h.advance(INTERVAL);
    h.ticks[0]?.();
    expect(first).toHaveBeenCalledTimes(1);
    expect(second).toHaveBeenCalledTimes(1);
    expect(h.store.getSnapshot()).toBe(before + INTERVAL);
  });

  it("returns a stable snapshot between ticks", () => {
    const h = harness();
    h.store.subscribe(() => {});
    const a = h.store.getSnapshot();
    h.advance(3_000);
    expect(h.store.getSnapshot()).toBe(a);
  });

  it("does not notify a subscriber that has left", () => {
    const h = harness();
    const gone = vi.fn();
    const stay = vi.fn();
    const leave = h.store.subscribe(gone);
    h.store.subscribe(stay);
    leave();
    h.ticks[0]?.();
    expect(gone).not.toHaveBeenCalled();
    expect(stay).toHaveBeenCalledTimes(1);
  });

  it("restarts the interval for a later subscriber and refreshes a stale snapshot", () => {
    const h = harness();
    h.store.subscribe(() => {})();
    const stale = h.store.getSnapshot();
    h.advance(60_000);
    expect(h.store.getSnapshot()).toBe(stale + 60_000);
    h.store.subscribe(() => {});
    expect(h.timers.start).toHaveBeenCalledTimes(2);
  });

  it("keeps an idle snapshot stable within one interval", () => {
    const h = harness();
    const a = h.store.getSnapshot();
    h.advance(INTERVAL - 1);
    expect(h.store.getSnapshot()).toBe(a);
  });
});
