import type { ReactNode } from "react";
import type { EpochRow, RecordedEpochRow } from "../../model/epochs";
import { splitCeiling, formatSpan } from "../../model/schedule";
import { formatDate, formatUtc } from "../../model/time";
import { formatInt, formatNorama } from "../../model/units";
import { Amount } from "../../ui/amount";
import { Card } from "../../ui/card";
import { Help } from "../../ui/help";
import { BlockLink } from "../../ui/links";
import { Stat } from "../../ui/stat";
import { splitOf } from "../rewards/logic";
import { epochSpanMs, sharePercent } from "./logic";

const CELL = "px-3 py-2";
const HEAD = "px-3 py-2 font-medium";

const TIP_SERVICE =
  "The chain keeps one running total for storage and relay mints together, so this page cannot split them. Both are ceilings: ORAMA is created only when a storage payment or a relay report claims it.";
const TIP_DEVELOPMENT = "Minted only when a governance-approved development spend claims it, never beyond the epoch's development share.";
const TIP_CLAIMED =
  "Storage, relay and development are minted when claimed, so these figures are what was claimed in this epoch's blocks. A claim can be for an earlier epoch's ceiling, which stays open for a limited window.";

function when(iso: string): ReactNode {
  return (
    <time dateTime={iso} title={formatUtc(iso)}>
      {formatDate(iso)}
    </time>
  );
}

/** "12:00:00 UTC": the time of day, since epochs on a test network are minutes long and the date alone would not tell them apart. */
function timeOfDay(iso: string): string {
  return `${iso.slice(11, 19)} UTC`;
}

export function EpochFacts({ row }: { row: EpochRow }) {
  return (
    <div className="grid gap-4 sm:grid-cols-3">
      <Stat
        label="Started"
        value={when(row.startTime)}
        detail={
          <>
            {timeOfDay(row.startTime)}, block <BlockLink height={row.startHeight} />
          </>
        }
      />
      <Stat
        label="Closed"
        value={when(row.endTime)}
        detail={
          <>
            {timeOfDay(row.endTime)}, block <BlockLink height={row.endHeight} />
          </>
        }
      />
      <Stat label="Length" value={formatSpan(epochSpanMs(row))} detail={`${formatInt(row.blocks)} blocks`} />
    </div>
  );
}

interface MintRow {
  name: ReactNode;
  ceiling: bigint | null;
  minted: string;
}

function mintRows(row: RecordedEpochRow): MintRow[] {
  const split = splitCeiling(BigInt(row.maxMintable));
  const rows: MintRow[] = [
    { name: "Validators and delegators", ceiling: split.validators, minted: row.mintedValidators },
    {
      name: (
        <>
          Storage and relay, combined
          <Help tip={TIP_SERVICE} />
        </>
      ),
      ceiling: split.storage + split.relay,
      minted: row.mintedService,
    },
    {
      name: (
        <>
          Development
          <Help tip={TIP_DEVELOPMENT} />
        </>
      ),
      ceiling: split.development,
      minted: row.mintedDevelopment,
    },
  ];
  if (BigInt(row.mintedFaucet) > 0n) rows.push({ name: "Test-network faucet", ceiling: null, minted: row.mintedFaucet });
  return rows;
}

export function MintedCard({ row }: { row: RecordedEpochRow }) {
  return (
    <Card title="Minted in this epoch">
      <div className="overflow-x-auto">
        <table className="w-full min-w-[480px] text-left text-sm">
          <caption className="sr-only">ORAMA minted in epoch {row.epoch}, by category, against the schedule's ceiling</caption>
          <thead className="text-xs text-muted">
            <tr>
              <th scope="col" className={HEAD}>Category</th>
              <th scope="col" className={`${HEAD} text-right`}>Ceiling at 60/25/10/5</th>
              <th scope="col" className={`${HEAD} text-right`}>Minted</th>
              <th scope="col" className={`${HEAD} text-right`}>Of ceiling</th>
            </tr>
          </thead>
          <tbody>
            {mintRows(row).map((r, i) => (
              <tr key={i} className="border-t border-border">
                <th scope="row" className={`${CELL} font-normal`}>{r.name}</th>
                <td className={`${CELL} text-right`}>{r.ceiling === null ? "none" : <Amount norama={r.ceiling.toString()} bare />}</td>
                <td className={`${CELL} text-right`}><Amount norama={r.minted} bare /></td>
                <td className={`${CELL} text-right font-mono tabular-nums text-muted`}>
                  {r.ceiling === null ? "" : (sharePercent(BigInt(r.minted), r.ceiling) ?? "")}
                </td>
              </tr>
            ))}
          </tbody>
          <tfoot>
            <tr className="border-t border-border font-medium">
              <th scope="row" className={`${CELL} text-left`}>Total</th>
              <td className={`${CELL} text-right`}><Amount norama={row.maxMintable} bare /></td>
              <td className={`${CELL} text-right`}><Amount norama={row.mintedTotal} bare /></td>
              <td className={`${CELL} text-right font-mono tabular-nums text-muted`}>
                {sharePercent(BigInt(row.mintedTotal), BigInt(row.maxMintable)) ?? ""}
              </td>
            </tr>
          </tfoot>
        </table>
      </div>
      <p className="mt-3 text-[13px] text-muted">
        All figures in ORAMA. The validator share is minted when the epoch closes. The others are minted when claimed
        <Help tip={TIP_CLAIMED} />, so they can be zero, or more than this epoch's share. The ceiling shown is the genesis split; a governance
        vote can move each share by up to 10 points, and the index does not record the split in force.
        {row.partial && " This epoch is partial: the index started inside it, so the minted figures cover only the blocks it saw."}
      </p>
    </Card>
  );
}

const NOT_RECORDED_SPLIT =
  "The chain did not report who was paid in this epoch (it closed before the chain did), so how the validator share split between commission, operators and delegators is not known and is not shown.";

function PaidSplit({ row }: { row: EpochRow }) {
  const split = splitOf(row);
  if (split === null) return <p className="mt-3 text-[13px] text-muted">{NOT_RECORDED_SPLIT}</p>;
  const cells: [string, string, string | null][] = [
    ["Commission", split.commission, split.commissionPct],
    ["Operators' own cut", split.operatorsOwn, split.operatorsOwnPct],
    ["Delegators", split.delegators, split.delegatorsPct],
  ];
  return (
    <>
      <dl className="mt-3 grid gap-3 text-sm sm:grid-cols-3">
        {cells.map(([name, amount, pct]) => (
          <div key={name}>
            <dt className="text-muted">{name}</dt>
            <dd className="text-lg font-semibold tabular-nums">
              {formatNorama(amount)} ORAMA
              {pct !== null && <span className="ml-1.5 text-sm font-normal text-muted">{pct}</span>}
            </dd>
          </div>
        ))}
      </dl>
      <p className="mt-3 text-[13px] text-muted">
        Of the {formatNorama(split.total)} ORAMA paid to validators and their delegators, as the chain reported it when the epoch closed. The operators&apos; own
        cut is what an operator earned as a delegator of its own validator, and the flooring remainder; its commission is above. Each validator&apos;s share is
        below.
      </p>
    </>
  );
}

export function PaidCard({ row }: { row: RecordedEpochRow }) {
  return (
    <Card title="Who was paid">
      <dl className="grid gap-3 text-sm sm:grid-cols-2">
        <div>
          <dt className="text-muted">Credited to earnings accounts</dt>
          <dd className="text-lg font-semibold tabular-nums">{formatNorama(row.rewardsCredited)} ORAMA</dd>
          <dd className="mt-0.5 text-[13px] text-muted">The validator share paid to validators and their delegators when the epoch closed.</dd>
        </div>
        <div>
          <dt className="text-muted">Bonded straight into committee validators</dt>
          <dd className="text-lg font-semibold tabular-nums">{formatNorama(row.rewardsForceBonded)} ORAMA</dd>
          <dd className="mt-0.5 text-[13px] text-muted">The part of a founding-committee validator's reward that is staked for it instead of credited.</dd>
        </div>
      </dl>
      <PaidSplit row={row} />
      <p className="mt-3 text-[13px] text-muted">Storage, relay and development payouts are in the table above.</p>
    </Card>
  );
}

/**
 * Who was paid, for an epoch whose figures are not available: the indexer leaves out the credited
 * and bonded totals with the rest, but keeps the split of the validators' payout, which it reads
 * from the closing block's events.
 */
export function PaidSplitCard({ row }: { row: EpochRow }) {
  return (
    <Card title="Who was paid">
      <PaidSplit row={row} />
    </Card>
  );
}

export function FeesCard({ row }: { row: RecordedEpochRow }) {
  const cells: [string, string, string][] = [
    ["Fees collected", row.feesCollected, "Base fee plus tips paid by the epoch's transactions."],
    ["Fees burned", row.feesBurned, "The base-fee part, destroyed."],
    ["Fees distributed", row.feesDistributed, "The rest, credited to block proposers."],
    ["Burned, all causes", row.burned, "The chain's own count of all norama destroyed in the epoch, from any cause."],
  ];
  return (
    <Card title="Fees and burn">
      <dl className="grid gap-3 text-sm sm:grid-cols-2 lg:grid-cols-4">
        {cells.map(([name, amount, detail]) => (
          <div key={name}>
            <dt className="text-muted">{name}</dt>
            <dd className="text-lg font-semibold tabular-nums">{formatNorama(amount)} ORAMA</dd>
            <dd className="mt-0.5 text-[13px] text-muted">{detail}</dd>
          </div>
        ))}
      </dl>
    </Card>
  );
}
