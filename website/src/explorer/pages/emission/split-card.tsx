import type { ReactNode } from "react";
import { useQuery } from "../../data/use-query";
import { hasFigures } from "../../model/epochs";
import type { EpochRow, SupplyPoint } from "../../model/epochs";
import { cumulativeCeiling, maxMintableAt, splitCeiling } from "../../model/schedule";
import { formatCompact, formatInt, formatNorama } from "../../model/units";
import { Amount } from "../../ui/amount";
import { Card } from "../../ui/card";
import { Help } from "../../ui/help";
import { IndexQuery } from "../../ui/index-query";
import { Skeleton } from "../../ui/query-states";
import { SeriesChart } from "../../ui/series-chart";
import { ceilingWindow, sharePercent } from "../epochs/logic";
import type { CeilingWindow, Tally } from "../epochs/logic";
import { NOT_AVAILABLE } from "../epochs/unavailable";

/** The newest closed epochs the page compares with the schedule. */
const WINDOW_EPOCHS = 30;
const TH = "px-3 py-2 font-medium";
const TD = "px-3 py-2";

const TIP_SERVICE =
  "Storage 25% and relay 10% are one figure here: the chain keeps a single running total for what both have minted. Both are ceilings, minted only when a storage payment or a relay report claims them.";
const TIP_DEV = "Minted only when a governance-approved spend claims it, and never above the epoch's development share. Until then it is a ceiling, not supply.";

/** The 60/25/10/5 split of the epoch in progress's ceiling, from the schedule alone. */
export function SplitCard({ epoch }: { epoch: number }) {
  const split = splitCeiling(maxMintableAt(epoch));
  const rows: { name: ReactNode; percent: string; ceiling: bigint; note: string }[] = [
    { name: "Validators and delegators", percent: "60%", ceiling: split.validators, note: "Minted when the epoch closes." },
    {
      name: (
        <>
          Storage and relay, combined
          <Help tip={TIP_SERVICE} />
        </>
      ),
      percent: "25% + 10%",
      ceiling: split.storage + split.relay,
      note: "Minted when claimed.",
    },
    {
      name: (
        <>
          Development
          <Help tip={TIP_DEV} />
        </>
      ),
      percent: "5%",
      ceiling: split.development,
      note: "Minted when claimed.",
    },
  ];
  return (
    <Card title={`How epoch ${formatInt(epoch)}'s ceiling is split`}>
      <div className="overflow-x-auto">
        <table className="w-full min-w-[480px] text-left text-sm">
          <caption className="sr-only">The current epoch's emission ceiling by recipient</caption>
          <thead className="text-xs text-muted">
            <tr>
              <th scope="col" className={TH}>Share</th>
              <th scope="col" className={`${TH} text-right`}>Percent</th>
              <th scope="col" className={`${TH} text-right`}>Ceiling (ORAMA)</th>
              <th scope="col" className={TH}>Created</th>
            </tr>
          </thead>
          <tbody>
            {rows.map((r, i) => (
              <tr key={i} className="border-t border-border">
                <th scope="row" className={`${TD} font-normal`}>{r.name}</th>
                <td className={`${TD} text-right font-mono tabular-nums`}>{r.percent}</td>
                <td className={`${TD} text-right`}><Amount norama={r.ceiling.toString()} bare /></td>
                <td className={`${TD} text-muted`}>{r.note}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <p className="mt-3 text-[13px] text-muted">
        This is the genesis split. A governance vote can move each share by up to 10 points as long as the four still sum to 100, and the explorer does
        not read the split in force. The validator share takes the rounding remainder.
      </p>
    </Card>
  );
}

function TallyRow({ name, tally }: { name: ReactNode; tally: Tally }) {
  return (
    <tr className="border-t border-border">
      <th scope="row" className={`${TD} font-normal`}>{name}</th>
      <td className={`${TD} text-right`}><Amount norama={tally.ceiling.toString()} bare /></td>
      <td className={`${TD} text-right`}><Amount norama={tally.minted.toString()} bare /></td>
      <td className={`${TD} text-right font-mono tabular-nums text-muted`}>{sharePercent(tally.minted, tally.ceiling) ?? ""}</td>
    </tr>
  );
}

function WindowTable({ win }: { win: CeilingWindow }) {
  return (
    <div className="overflow-x-auto">
      <table className="w-full min-w-[480px] text-left text-sm">
        <caption className="sr-only">Minted against the ceiling over the newest closed epochs</caption>
        <thead className="text-xs text-muted">
          <tr>
            <th scope="col" className={TH}>Share</th>
            <th scope="col" className={`${TH} text-right`}>Ceiling</th>
            <th scope="col" className={`${TH} text-right`}>Minted</th>
            <th scope="col" className={`${TH} text-right`}>Of ceiling</th>
          </tr>
        </thead>
        <tbody>
          <TallyRow name="Validators and delegators" tally={win.validators} />
          <TallyRow name="Storage and relay, combined" tally={win.service} />
          <TallyRow name="Development" tally={win.development} />
        </tbody>
        <tfoot className="font-medium">
          <TallyRow name="Total" tally={win.total} />
        </tfoot>
      </table>
    </div>
  );
}

function ceilingChart(rows: readonly EpochRow[]) {
  const oldestFirst = [...rows].reverse();
  const minted = oldestFirst.map((r) => (hasFigures(r) ? BigInt(r.mintedTotal) : null));
  const ceilings = oldestFirst.map((r) => (hasFigures(r) ? BigInt(r.maxMintable) : null));
  return (
    <SeriesChart
      label={`ORAMA minted per epoch against the epoch's ceiling, newest ${oldestFirst.length} closed epochs`}
      kind="bars"
      values={minted}
      reference={ceilings}
      format={(v) => formatCompact(v.toString())}
      from={`epoch ${formatInt(oldestFirst[0]!.epoch)}`}
      to={`epoch ${formatInt(oldestFirst[oldestFirst.length - 1]!.epoch)}`}
      tone="gain"
      table={{
        head: ["Epoch", "Minted (ORAMA)", "Ceiling (ORAMA)", "Partial"],
        rows: rows.map((r) =>
          hasFigures(r)
            ? [formatInt(r.epoch), formatNorama(r.mintedTotal), formatNorama(r.maxMintable), r.partial ? "yes" : ""]
            : [formatInt(r.epoch), NOT_AVAILABLE, NOT_AVAILABLE, r.partial ? "yes" : ""],
        ),
      }}
    />
  );
}

function ToDate({ point }: { point: SupplyPoint | undefined }) {
  if (point === undefined) return null;
  const allowed = cumulativeCeiling(point.epoch);
  return (
    <p className="mt-3 text-[13px] text-muted">
      Since the chain began, through epoch {formatInt(point.epoch)}: <Amount norama={point.mintedToDate} bare /> ORAMA minted of the{" "}
      <Amount norama={allowed.toString()} bare /> the schedule allowed ({sharePercent(BigInt(point.mintedToDate), allowed) ?? "no ceiling yet"}).
      Minted to date counts every category, and the test-network faucet, which no ceiling covers.
    </p>
  );
}

export function WindowBody({ rows, latest }: { rows: readonly EpochRow[]; latest: SupplyPoint | undefined }) {
  if (rows.length === 0) {
    return <p className="py-4 text-center text-sm text-muted">No epoch has closed in this index yet, so there is nothing minted to compare.</p>;
  }
  const win = ceilingWindow(rows);
  return (
    <div className="space-y-4">
      <p className="text-[13px] text-muted">
        The newest {formatInt(rows.length)} closed epoch{rows.length === 1 ? "" : "s"}
        {win.partialSkipped > 0 && `, leaving out ${win.partialSkipped} partial one${win.partialSkipped === 1 ? "" : "s"} (the index saw only part of it)`}
        {win.unavailableSkipped > 0 &&
          `, leaving out ${win.unavailableSkipped} that ${win.unavailableSkipped === 1 ? "is" : "are"} not available (the index could not read ${win.unavailableSkipped === 1 ? "its" : "their"} figures, and a missing figure is not zero)`}
        . The ceiling is the chain's own figure for each epoch, split 60/25/10/5.
      </p>
      {win.epochs > 0 && <WindowTable win={win} />}
      {win.faucet > 0n && (
        <p className="text-[13px] text-muted">
          Test-network faucet, outside every ceiling: <Amount norama={win.faucet.toString()} bare /> ORAMA, included in the total.
        </p>
      )}
      {ceilingChart(rows)}
      <ToDate point={latest} />
    </div>
  );
}

/** What was actually minted in recent epochs, against what the schedule allowed. Needs the index. */
export function MintedAgainstCeilingCard() {
  const epochs = useQuery((s) => s.getEpochs({ page: 1, limit: WINDOW_EPOCHS }), []);
  const supply = useQuery((s) => s.getSupplyHistory(1), []);
  return (
    <Card title="Minted against the ceiling">
      <IndexQuery state={epochs.state} onRetry={epochs.refetch} loading={<Skeleton className="h-64 w-full" />}>
        {(page) => <WindowBody rows={page.items} latest={supply.state.status === "ready" ? supply.state.data[0] : undefined} />}
      </IndexQuery>
    </Card>
  );
}
