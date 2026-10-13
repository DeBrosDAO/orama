import { useSyncExternalStore } from "react";
import { createClockStore } from "./clock-store";

const SECOND_INTERVAL_MS = 1000;

const clock = createClockStore(SECOND_INTERVAL_MS);

/** The current time in ms, refreshed every second, for a countdown that shows seconds. */
export function useSecond(): number {
  return useSyncExternalStore(clock.subscribe, clock.getSnapshot, clock.getSnapshot);
}
