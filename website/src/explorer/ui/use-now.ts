import { useSyncExternalStore } from "react";
import { createClockStore } from "./clock-store";

const NOW_INTERVAL_MS = 10_000;

const clock = createClockStore(NOW_INTERVAL_MS);

/** The current time in ms, refreshed on one shared interval so relative times stay true. */
export function useNow(): number {
  return useSyncExternalStore(clock.subscribe, clock.getSnapshot, clock.getSnapshot);
}
