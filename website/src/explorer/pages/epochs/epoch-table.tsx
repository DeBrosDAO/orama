import { Link } from "react-router";
import type { EpochRow } from "../../model/epochs";
import { useExplorerPaths } from "../../network/active";
import { formatInt } from "../../model/units";
import { Amount } from "../../ui/amount";
import { Badge } from "../../ui/badge";
import { RelTime } from "../../ui/rel-time";
import { NotAvailable } from "./unavailable";

const AMOUNT_FRACTION = 2;
/** Minted, burned and fees: the columns an unavailable epoch has no figure for. */
const FIGURE_COLUMNS = 3;
const TH = "px-3 py-2 font-medium";
const TD = "px-3 py-2";

/** Closed epochs, one row each, newest first. */
export function EpochTable({ rows }: { rows: readonly EpochRow[] }) {
  const explorerPaths = useExplorerPaths();
  return (
    <div className="overflow-x-auto">
      <table className="w-full min-w-[640px] text-left text-sm">
        <caption className="sr-only">Closed emission epochs, newest first</caption>
        <thead className="text-xs text-muted">
          <tr>
            <th scope="col" className={TH}>Epoch</th>
            <th scope="col" className={TH}>Closed</th>
            <th scope="col" className={`${TH} text-right`}>Blocks</th>
            <th scope="col" className={`${TH} text-right`}>Minted (ORAMA)</th>
            <th scope="col" className={`${TH} text-right`}>Burned (ORAMA)</th>
            <th scope="col" className={`${TH} text-right`}>Fees (ORAMA)</th>
          </tr>
        </thead>
        <tbody>
          {rows.map((row) => (
            <tr key={row.epoch} className="border-t border-border hover:bg-surface-2/60">
              <td className={TD}>
                <Link to={explorerPaths.epoch(row.epoch)} className="font-mono font-medium underline decoration-dotted underline-offset-4">
                  {formatInt(row.epoch)}
                </Link>
                {row.partial && (
                  <Badge tone="signal" className="ml-2">
                    partial
                  </Badge>
                )}
              </td>
              <td className={`${TD} text-muted`}>
                <RelTime iso={row.endTime} />
              </td>
              <td className={`${TD} text-right font-mono tabular-nums`}>{formatInt(row.blocks)}</td>
              {row.unavailable ? (
                <td colSpan={FIGURE_COLUMNS} className={`${TD} text-right`}>
                  <NotAvailable row={row} />
                </td>
              ) : (
                <>
                  <td className={`${TD} text-right`}>
                    <Amount norama={row.mintedTotal} bare maxFraction={AMOUNT_FRACTION} />
                  </td>
                  <td className={`${TD} text-right`}>
                    <Amount norama={row.burned} bare maxFraction={AMOUNT_FRACTION} />
                  </td>
                  <td className={`${TD} text-right`}>
                    <Amount norama={row.feesCollected} bare maxFraction={AMOUNT_FRACTION} />
                  </td>
                </>
              )}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
