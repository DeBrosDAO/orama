import type { BalanceChange } from "../../model/types";
import { cn } from "../../../lib/utils";
import { Card } from "../../ui/card";
import { Help } from "../../ui/help";
import { WalletLink } from "../../ui/links";
import { balanceText, changeKey, changeText, changeTone, deltasSumToZero } from "./balance-format";
import type { ChangeTone } from "./balance-format";
import { systemPartyLabel } from "./tx-parties";

const BALANCES_TIP = "This is the whole effect of the transaction on every account it touched.";
const SUMS_TO_ZERO_TIP = `${BALANCES_TIP} Amounts add up to zero.`;
const NOT_ZERO_NOTE = "These rows do not sum to zero";
const NO_CHANGES_TEXT = "This transaction changed no balances.";

const TONE_CLASS: Record<ChangeTone, string> = {
  gain: "text-gain",
  signal: "text-signal",
  plain: "",
};

const HEAD_CELL = "pb-2 pr-2 text-[11.5px] font-medium uppercase tracking-[0.07em] text-muted";
const NUM_CELL = "py-2.5 pr-2 text-right font-mono tabular-nums";

function AccountCell({ change }: { change: BalanceChange }) {
  const { party } = change;
  return party.kind === "wallet" ? <WalletLink wallet={party.ref} /> : <span>{systemPartyLabel(party.name)}</span>;
}

/** "Balances before -> after": every account the transaction touched. */
export function BalancesCard({ changes }: { changes: BalanceChange[] }) {
  if (changes.length === 0) {
    return (
      <Card title={<>Balances before → after<Help tip={BALANCES_TIP} /></>}>
        <p className="py-2 text-sm text-muted">{NO_CHANGES_TEXT}</p>
      </Card>
    );
  }
  const balanced = deltasSumToZero(changes);
  return (
    <Card title={<>Balances before → after<Help tip={balanced ? SUMS_TO_ZERO_TIP : BALANCES_TIP} /></>}>
      <table className="w-full border-collapse text-[13px]">
        <caption className="sr-only">Balance of each account before and after this transaction</caption>
        <thead>
          <tr>
            <th scope="col" className={cn(HEAD_CELL, "text-left")}>Account</th>
            <th scope="col" className={cn(HEAD_CELL, "hidden text-right sm:table-cell")}>Before</th>
            <th scope="col" className={cn(HEAD_CELL, "hidden text-right sm:table-cell")}>After</th>
            <th scope="col" className={cn(HEAD_CELL, "pr-0 text-right")}>Change</th>
          </tr>
        </thead>
        <tbody>
          {changes.map((c) => (
            <tr key={changeKey(c)} className="border-t border-border">
              <td className="py-2.5 pr-2"><AccountCell change={c} /></td>
              <td className={cn(NUM_CELL, "hidden sm:table-cell")}>{balanceText(c.before)}</td>
              <td className={cn(NUM_CELL, "hidden sm:table-cell")}>{balanceText(c.after)}</td>
              <td className={cn(NUM_CELL, "pr-0", TONE_CLASS[changeTone(c)])} title={`${c.delta} norama`}>
                {changeText(c)}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
      {!balanced && <p className="mt-2 text-xs text-muted">{NOT_ZERO_NOTE}</p>}
    </Card>
  );
}
