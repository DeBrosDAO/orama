import type { EpochClock } from "../../model/epochs";
import { earliestStartMs, formatSpan, maxMintableAt, nextCeilingChange, TAIL_CEILING_ORAMA } from "../../model/schedule";
import { formatDate, formatUtc } from "../../model/time";
import { formatInt } from "../../model/units";
import { Amount } from "../../ui/amount";
import { Card } from "../../ui/card";
import { Help } from "../../ui/help";
import { useNow } from "../../ui/use-now";

const TIP_EARLIEST =
  "Each epoch lasts at least its minimum time and also waits for its minimum block count, and a halt does not make up missed epochs. So this is the earliest the next ceiling can apply; it can be later.";

/** How far the epoch in progress is from the next drop in the per-epoch ceiling. */
export function HalvingCard({ clock }: { clock: EpochClock }) {
  const now = useNow();
  const change = nextCeilingChange(clock.epoch);
  if (change === null) {
    return (
      <Card title="Halving countdown">
        <div className="text-2xl font-semibold tracking-tight">No more halvings</div>
        <p className="mt-1 text-[13px] text-muted">
          The flat tail applies: every epoch has a ceiling of {TAIL_CEILING_ORAMA.toString()} ORAMA, forever.
        </p>
      </Card>
    );
  }
  const earliest = earliestStartMs(clock, change.epochsLeft);
  const iso = new Date(earliest).toISOString();
  return (
    <Card title={change.halving ? "Halving countdown" : "Final step down"}>
      <div className="text-2xl font-semibold tracking-tight tabular-nums">
        {formatInt(change.epochsLeft)} epoch{change.epochsLeft === 1 ? "" : "s"}
      </div>
      <p className="mt-1 text-[13px] text-muted">
        until epoch {formatInt(change.firstEpoch)}, where the ceiling {change.halving ? "halves" : "drops"} from <Amount norama={change.from.toString()} bare /> to{" "}
        <Amount norama={change.to.toString()} bare /> ORAMA per epoch. The count includes the epoch in progress.
      </p>
      <p className="mt-2 text-[13px] text-muted">
        No sooner than{" "}
        <time dateTime={iso} title={formatUtc(iso)} className="text-fg">
          {formatDate(iso)}
        </time>{" "}
        (in {formatSpan(earliest - now)})
        <Help tip={TIP_EARLIEST} />.
      </p>
      <p className="mt-2 text-[13px] text-muted">
        This epoch's ceiling is <Amount norama={maxMintableAt(clock.epoch).toString()} bare /> ORAMA.
      </p>
    </Card>
  );
}
