import { formatUtc } from "../../model/time";
import { formatInt } from "../../model/units";
import type { Block } from "../../model/types";
import { Amount } from "../../ui/amount";
import { Card } from "../../ui/card";
import { ValidatorLink } from "../../ui/links";
import { RelTime } from "../../ui/rel-time";
import { Stat } from "../../ui/stat";

const PERCENT = 100;

function signedShare({ signed, total }: Block["signatures"]): number {
  return total > 0 ? Math.min(PERCENT, (signed / total) * PERCENT) : 0;
}

/** How many of the validators vouched for this block, as a sentence and a bar. */
function Signatures({ signatures }: { signatures: Block["signatures"] }) {
  const { signed, total } = signatures;
  return (
    <Card>
      <div className="flex flex-wrap items-center gap-x-4 gap-y-2">
        <p className="text-sm">
          <b className="font-semibold tabular-nums">{formatInt(signed)}</b> of{" "}
          <b className="font-semibold tabular-nums">{formatInt(total)}</b> validators signed this block
        </p>
        <div aria-hidden="true" className="h-1.5 min-w-32 flex-1 overflow-hidden rounded-full bg-surface-3">
          <div className="h-full rounded-full bg-gain" style={{ width: `${signedShare(signatures)}%` }} />
        </div>
      </div>
    </Card>
  );
}

/** Time, proposer and transaction totals, then the signatures. */
export function BlockStats({ block }: { block: Block }) {
  return (
    <div className="space-y-4">
      <div className="grid gap-4 sm:grid-cols-3">
        <Stat label="Time" value={<RelTime iso={block.time} />} detail={formatUtc(block.time)} />
        <Stat label="Proposer" value={<ValidatorLink validator={block.proposer} className="text-lg" />} detail="Built this block" />
        <Stat
          label="Transactions"
          value={formatInt(block.txCount)}
          detail={
            <>
              {formatInt(block.gasUsed)} gas used · <Amount norama={block.burned} /> burned
            </>
          }
        />
      </div>
      <Signatures signatures={block.signatures} />
    </div>
  );
}
