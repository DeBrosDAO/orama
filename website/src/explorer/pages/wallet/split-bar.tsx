import type { WalletBalance } from "../../model/types";
import { Amount } from "../../ui/amount";
import { Help } from "../../ui/help";
import { cn } from "../../../lib/utils";
import { splitSegments } from "./balance-split";
import type { SplitId } from "./balance-split";
import { BALANCE_FRACTION } from "./constants";

const COLOURS: Record<SplitId, string> = {
  available: "bg-fg",
  staked: "bg-muted",
  unbonding: "bg-signal",
};

const TIPS: Partial<Record<SplitId, string>> = {
  staked: "Staked ORAMA is locked to a validator and earns rewards. Unstaking takes a waiting period.",
  unbonding: "ORAMA that is being unstaked. It cannot be spent until the waiting period ends.",
};

/** Available, staked and unbonding as one bar and a legend. */
export function SplitBar({ balance }: { balance: WalletBalance }) {
  const segments = splitSegments(balance);
  return (
    <div className="mt-3">
      <div className="flex h-2 overflow-hidden rounded-full bg-surface-3" role="img" aria-label={segments.map((s) => `${s.label} ${s.percent}%`).join(", ")}>
        {segments
          .filter((s) => s.width > 0)
          .map((s) => (
            <i key={s.id} className={cn("block h-full min-w-[3px]", COLOURS[s.id])} style={{ flex: `0 1 ${s.width}%` }} />
          ))}
      </div>
      <ul className="mt-2.5 flex flex-wrap gap-x-4 gap-y-1 text-[13px] text-muted">
        {segments.map((s) => (
          <li key={s.id} className="flex items-center gap-1.5">
            <i className={cn("inline-block h-2 w-2 rounded-sm", COLOURS[s.id])} aria-hidden="true" />
            {s.label}
            <b className="font-semibold text-fg">
              <Amount norama={s.norama} bare maxFraction={BALANCE_FRACTION} />
            </b>
            {TIPS[s.id] && <Help tip={TIPS[s.id] as string} className="-ml-1" />}
          </li>
        ))}
      </ul>
    </div>
  );
}
