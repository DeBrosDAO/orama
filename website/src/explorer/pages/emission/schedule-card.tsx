import { brackets, cumulativeCeiling, EPOCHS_PER_BRACKET } from "../../model/schedule";
import { formatInt } from "../../model/units";
import { Amount } from "../../ui/amount";
import { Badge } from "../../ui/badge";
import { Card } from "../../ui/card";

const TH = "px-3 py-2 font-medium";
const TD = "px-3 py-2";

/** The whole schedule: five brackets that halve, then the flat tail. The bracket the chain is in is marked. */
export function ScheduleCard({ currentEpoch }: { currentEpoch: number | null }) {
  return (
    <Card title="The schedule">
      <div className="overflow-x-auto">
        <table className="w-full min-w-[560px] text-left text-sm">
          <caption className="sr-only">Per-epoch emission ceiling by epoch range</caption>
          <thead className="text-xs text-muted">
            <tr>
              <th scope="col" className={TH}>Epochs</th>
              <th scope="col" className={`${TH} text-right`}>Ceiling per epoch</th>
              <th scope="col" className={`${TH} text-right`}>Bracket total</th>
              <th scope="col" className={`${TH} text-right`}>Cumulative ceiling</th>
            </tr>
          </thead>
          <tbody>
            {brackets().map((b) => {
              const here = currentEpoch !== null && currentEpoch >= b.firstEpoch && (b.lastEpoch === null || currentEpoch <= b.lastEpoch);
              return (
                <tr key={b.number} aria-current={here ? "true" : undefined} className={here ? "border-t border-border bg-surface-2" : "border-t border-border"}>
                  <td className={TD}>
                    {b.lastEpoch === null
                      ? `${formatInt(b.firstEpoch)} onward`
                      : `${formatInt(b.firstEpoch)} to ${formatInt(b.lastEpoch)}`}
                    {here && (
                      <Badge tone="signal" className="ml-2">
                        now
                      </Badge>
                    )}
                  </td>
                  <td className={`${TD} text-right`}>
                    <Amount norama={b.ceiling.toString()} bare />
                  </td>
                  <td className={`${TD} text-right`}>
                    {b.lastEpoch === null ? "no end" : <Amount norama={(b.ceiling * BigInt(EPOCHS_PER_BRACKET)).toString()} bare />}
                  </td>
                  <td className={`${TD} text-right`}>
                    {b.lastEpoch === null ? "no cap" : <Amount norama={cumulativeCeiling(b.lastEpoch).toString()} bare />}
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
      <p className="mt-3 text-[13px] text-muted">
        Amounts in ORAMA. The ceiling halves for each of the first four steps. After epoch 3,650 it steps down once more, to a flat 274 per epoch,
        and stays there: there is no terminal supply cap. Epochs, not calendar days, are counted, and the table is fixed in the chain's code. The
        ceiling is a maximum; what is minted can be less.
      </p>
    </Card>
  );
}
