import { Link } from "react-router";
import type { EpochRow } from "../../model/epochs";
import { useExplorerPaths } from "../../network/active";
import { formatInt } from "../../model/units";
import { Amount } from "../../ui/amount";
import { Help } from "../../ui/help";
import { RelTime } from "../../ui/rel-time";
import { NotAvailable, UnavailableBadge } from "../epochs/unavailable";
import { splitOf } from "./logic";

const AMOUNT_FRACTION = 2;
const TH = "px-3 py-2 font-medium";
const TD = "px-3 py-2";

export const NOT_RECORDED = "not recorded";

const TIP_COMMISSION = "The validators' commission rate applied to what each was paid, added up. The part of a reward its operator takes before sharing the rest with everyone staked on it.";
const TIP_OWN = "What operators earned as delegators of their own validator, plus the flooring remainder of the split. An operator's whole share is this plus the commission.";
const TIP_BONDED = "A founding-committee validator has part of its reward staked for it instead of credited to earnings, up to a ceiling. It is included in the operators' own cut.";

function Cell({ row, pick, pct }: { row: EpochRow; pick: "commission" | "operatorsOwn" | "delegators"; pct: "commissionPct" | "operatorsOwnPct" | "delegatorsPct" }) {
  const split = splitOf(row);
  if (split === null) return <span className="text-muted">{NOT_RECORDED}</span>;
  return (
    <>
      <Amount norama={split[pick]} bare maxFraction={AMOUNT_FRACTION} />
      {split[pct] !== null && <span className="ml-1.5 text-xs text-muted">{split[pct]}</span>}
    </>
  );
}

/**
 * What each closed epoch paid validators and delegators, split into commission, the operators' own cut
 * and the delegators'. An epoch whose payout the chain did not report is "not recorded", never zero.
 */
export function RewardsTable({ rows }: { rows: readonly EpochRow[] }) {
  const paths = useExplorerPaths();
  return (
    <div className="overflow-x-auto">
      <table className="w-full min-w-[760px] text-left text-sm">
        <caption className="sr-only">What each closed epoch paid validators and delegators, newest first</caption>
        <thead className="text-xs text-muted">
          <tr>
            <th scope="col" className={TH}>Epoch</th>
            <th scope="col" className={TH}>Closed</th>
            <th scope="col" className={`${TH} text-right`}>Paid (ORAMA)</th>
            <th scope="col" className={`${TH} text-right`}>
              Commission
              <Help tip={TIP_COMMISSION} />
            </th>
            <th scope="col" className={`${TH} text-right`}>
              Operators&apos; own
              <Help tip={TIP_OWN} />
            </th>
            <th scope="col" className={`${TH} text-right`}>Delegators</th>
            <th scope="col" className={`${TH} text-right`}>
              Staked for committee
              <Help tip={TIP_BONDED} />
            </th>
          </tr>
        </thead>
        <tbody>
          {rows.map((row) => {
            const split = splitOf(row);
            return (
              <tr key={row.epoch} className="border-t border-border hover:bg-surface-2/60">
                <td className={TD}>
                  <Link to={paths.epoch(row.epoch)} className="font-mono font-medium underline decoration-dotted underline-offset-4">
                    {formatInt(row.epoch)}
                  </Link>
                  {row.unavailable && <UnavailableBadge row={row} className="ml-2" />}
                </td>
                <td className={`${TD} text-muted`}>
                  <RelTime iso={row.endTime} />
                </td>
                <td className={`${TD} text-right`}>
                  {split === null ? <span className="text-muted">{NOT_RECORDED}</span> : <Amount norama={split.total} bare maxFraction={AMOUNT_FRACTION} />}
                </td>
                <td className={`${TD} text-right`}><Cell row={row} pick="commission" pct="commissionPct" /></td>
                <td className={`${TD} text-right`}><Cell row={row} pick="operatorsOwn" pct="operatorsOwnPct" /></td>
                <td className={`${TD} text-right`}><Cell row={row} pick="delegators" pct="delegatorsPct" /></td>
                <td className={`${TD} text-right`}>
                  {row.unavailable ? (
                    <NotAvailable row={row} />
                  ) : split === null ? (
                    <span className="text-muted">{NOT_RECORDED}</span>
                  ) : (
                    <Amount norama={row.rewardsForceBonded} bare maxFraction={AMOUNT_FRACTION} />
                  )}
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}
