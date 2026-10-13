import { Card } from "../../ui/card";
import { Help } from "../../ui/help";
import { formatCompact, formatInt, formatPct } from "../../model/units";
import type { Handover } from "../../model/types";
import { gateValidators, handoverTerms } from "../validator/logic";
import { handoverSentence } from "./summary";
import type { ValidatorsSummary } from "./summary";

const LAMBDA_TIP =
  "λ (lambda) is how much of the network's voting power comes from staked ORAMA instead of the founding committee. It starts near 0 and rises as more ORAMA is staked, so control moves from the founders to the community. No vote and no admin switch is involved.";
/** Keeps the marker label inside the track at the extremes. */
const PIN_MIN_PCT = 6;
const PIN_MAX_PCT = 94;

function Progress({ label, value, children }: { label: string; value: number; children: React.ReactNode }) {
  return (
    <div>
      <div className="flex flex-wrap items-baseline justify-between gap-x-3 text-[13px]">
        <span className="font-medium">{label}</span>
        <span className="text-muted">{children}</span>
      </div>
      <div
        role="progressbar"
        aria-label={label}
        aria-valuemin={0}
        aria-valuemax={100}
        aria-valuenow={Math.round(value * 100)}
        aria-valuetext={formatPct(value)}
        className="mt-1 h-2 overflow-hidden rounded-full bg-surface-3"
      >
        <div className="h-full bg-fg/70" style={{ width: `${value * 100}%` }} />
      </div>
    </div>
  );
}

/** What drives λ: x/power exposes the two figures it is built from, not the terms. */
function Drivers({ handover }: { handover: Handover }) {
  const { stake, epochs, cap, preGateCap } = handover;
  const terms = handoverTerms(stake.bonded, stake.exit, epochs.current, epochs.deadline);
  return (
    <div className="mt-5 space-y-3 border-t border-border/60 pt-4">
      <p className="text-[13px] font-semibold text-muted">What moves λ</p>
      <Progress label="Stake" value={terms.stake}>
        {formatCompact(stake.bonded)} of {formatCompact(stake.exit)} ORAMA bonded ({formatPct(terms.stake)})
      </Progress>
      <Progress label="Time" value={terms.time}>
        epoch {formatInt(epochs.current)} of {formatInt(epochs.deadline)} ({formatPct(terms.time)})
      </Progress>
      <p className="max-w-[75ch] text-[13px] text-muted">
        λ is the larger of the two and never goes down, so it reaches 1 by the deadline however little is staked. Until the network has had at
        least {gateValidators(cap.current)} stake-backed validators once (twice the number a {formatPct(cap.current)} cap allows), λ is held
        at {formatPct(preGateCap)} or less. The chain does not say whether that has happened yet.
      </p>
    </div>
  );
}

export function HandoverCard({ summary, handover }: { summary: ValidatorsSummary; handover: Handover }) {
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
          λ = {handover.lambda.toFixed(2)}
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
      <Drivers handover={handover} />
    </Card>
  );
}
