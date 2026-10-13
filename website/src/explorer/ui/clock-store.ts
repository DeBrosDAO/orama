type Listener = () => void;
type TimerId = ReturnType<typeof setInterval>;

export interface ClockTimers {
  start: (tick: () => void, ms: number) => TimerId;
  stop: (id: TimerId) => void;
}

export interface ClockStore {
  subscribe: (listener: Listener) => () => void;
  getSnapshot: () => number;
}

const defaultTimers: ClockTimers = {
  start: (tick, ms) => setInterval(tick, ms),
  stop: (id) => clearInterval(id),
};

/**
 * One shared clock for every relative time on the page. A single interval
 * runs while anyone is subscribed and stops when the last one leaves, so a
 * long list of "14 s ago" labels costs one timer, not one each. The snapshot
 * only changes on a tick (or when it is older than one interval on the first
 * read after being idle), which keeps it stable for useSyncExternalStore.
 */
export function createClockStore(intervalMs: number, now: () => number = Date.now, timers: ClockTimers = defaultTimers): ClockStore {
  const listeners = new Set<Listener>();
  let current = now();
  let timer: TimerId | null = null;

  function refresh() {
    current = now();
  }

  function tick() {
    refresh();
    listeners.forEach((l) => l());
  }

  function subscribe(listener: Listener): () => void {
    if (listeners.size === 0) {
      refresh();
      timer = timers.start(tick, intervalMs);
    }
    listeners.add(listener);
    return () => {
      listeners.delete(listener);
      if (listeners.size === 0 && timer !== null) {
        timers.stop(timer);
        timer = null;
      }
    };
  }

  function getSnapshot(): number {
    if (listeners.size === 0 && now() - current >= intervalMs) refresh();
    return current;
  }

  return { subscribe, getSnapshot };
}
