import type { QueryState } from "../../data/use-query";
import type { NetworkSnapshot } from "../../model/types";
import { Skeleton } from "../../ui/query-states";
import { cn } from "../../../lib/utils";
import { healthSentence, TONE_ANNOUNCEMENT } from "./logic";
import type { HealthTone } from "./logic";

const DOT_TONE: Record<HealthTone, string> = {
  healthy: "bg-gain text-gain",
  degraded: "bg-signal text-signal",
};

/**
 * The one-line verdict. Its block height ticks every block, so the visible
 * line is not a live region; a separate hidden one speaks only when the tone
 * (healthy or degraded) changes. While loading it holds its footprint; on error it
 * stays silent, because the stat tiles below already show the failure with a
 * retry and two identical error boxes would only add noise.
 */
export function HealthPill({ state }: { state: QueryState<NetworkSnapshot> }) {
  if (state.status === "error") return null;
  const announcement = state.status === "ready" ? TONE_ANNOUNCEMENT[healthSentence(state.data).tone] : "";
  return (
    <div className="mt-4 flex justify-center">
      <span role="status" className="sr-only">
        {announcement}
      </span>
      {state.status === "loading" ? (
        <Skeleton className="h-[34px] w-full max-w-[520px] rounded-full" />
      ) : (
        <Verdict network={state.data} />
      )}
    </div>
  );
}

function Verdict({ network }: { network: NetworkSnapshot }) {
  const { tone, sentence } = healthSentence(network);
  return (
    <p className="inline-flex items-center gap-2.5 rounded-full border border-border bg-surface px-3.5 py-1.5 text-left text-[13px] text-muted">
      <span aria-hidden="true" className={cn("h-1.5 w-1.5 shrink-0 animate-pulse-dot rounded-full", DOT_TONE[tone])} />
      <span>{sentence}</span>
    </p>
  );
}
