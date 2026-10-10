import { Card } from "../../ui/card";
import { Help } from "../../ui/help";
import { formatPct } from "../../model/units";
import { handoverSentence } from "./summary";
import type { ValidatorsSummary } from "./summary";

const LAMBDA_TIP =
  "λ (lambda) is how much of the network's voting power comes from staked ORAMA instead of the founding committee. It starts near 0 and rises as more ORAMA is staked, so control moves from the founders to the community. No vote and no admin switch is involved.";
/** Keeps the marker label inside the track at the extremes. */
const PIN_MIN_PCT = 6;
const PIN_MAX_PCT = 94;

export function HandoverCard({ summary, lambda }: { summary: ValidatorsSummary; lambda: number }) {
  const committeePct = summary.committeeShare * 100;
  const pin = Math.min(PIN_MAX_PCT, Math.max(PIN_MIN_PCT, committeePct));
  return (
    <Card
      title={
        <>
          Power hand-over
          <Help tip={LAMBDA_TIP} />
        </>
      }
    >
      <p className="max-w-[70ch] text-base">
        <b>{handoverSentence(summary)}</b> As more ORAMA is staked, that power shifts to stakers automatically.
      </p>
      <div className="relative mb-2 mt-9">
        <span
          className="absolute -top-7 -translate-x-1/2 whitespace-nowrap rounded-md bg-signal px-2 py-0.5 text-xs font-bold text-black after:absolute after:left-1/2 after:top-full after:-translate-x-1/2 after:border-[6px] after:border-x-transparent after:border-b-0 after:border-t-signal after:content-['']"
          style={{ left: `${pin}%` }}
        >
          λ = {lambda.toFixed(2)}
        </span>
        <div
          className="flex h-4 overflow-hidden rounded-full bg-surface-3"
          role="img"
          aria-label={`Founding committee ${formatPct(summary.committeeShare)}, community stake ${formatPct(summary.communityShare)}`}
        >
          <div className="h-full bg-fg/25" style={{ width: `${committeePct}%` }} />
          <div className="h-full flex-1 bg-fg/80" />
        </div>
      </div>
      <div className="flex justify-between text-[13px] text-muted">
        <span>← Founding committee {formatPct(summary.committeeShare)}</span>
        <span>Community stake {formatPct(summary.communityShare)} →</span>
      </div>
    </Card>
  );
}
