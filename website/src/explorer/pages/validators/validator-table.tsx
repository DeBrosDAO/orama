import { Link } from "react-router";
import type { QueryState } from "../../data/use-query";
import { useExplorerPaths } from "../../network/active";
import type { Validator, ValidatorSigning } from "../../model/types";
import { Badge } from "../../ui/badge";
import { Card } from "../../ui/card";
import { Help } from "../../ui/help";
import { Identicon } from "../../ui/identicon";
import { cn } from "../../../lib/utils";
import { formatCompact, formatPct } from "../../model/units";
import { statusWord } from "../validator/logic";
import { rankValidators } from "./summary";

const HEAD_CELL = "pb-2 pr-3 font-medium";
const CELL = "py-3 pr-3 align-middle";
const UPTIME_TIP =
  "The share of blocks the validator signed in the newest closed epoch, from the chain indexer. A block it did not sign (offline, or voted nil) counts as missed.";

type SigningState = QueryState<ValidatorSigning[]>;

/** The uptime of one validator, or why there is none. */
function UptimeCell({ signing, operator }: { signing: SigningState; operator: string }) {
  if (signing.status === "loading") return <span className="text-muted">…</span>;
  if (signing.status === "error") return <span className="text-muted" title={signing.error.message}>n/a</span>;
  const row = signing.data.find((s) => s.operator === operator);
  if (row === undefined || row.uptime === null) return <span className="text-muted" title="No closed epoch carries a commit for this validator yet">–</span>;
  return (
    <span className={cn("font-mono text-sm tabular-nums", row.uptime < 0.95 && "text-loss")} title={`${row.missedBlocks} blocks missed in epoch ${row.epoch}`}>
      {formatPct(row.uptime)}
    </span>
  );
}

function Row({ v, rank, signing }: { v: Validator; rank: number; signing: SigningState }) {
  const paths = useExplorerPaths();
  return (
    <tr className="relative border-t border-border/60 first:border-t-0 hover:bg-surface-2">
      <td className={cn(CELL, "pl-2 font-mono text-muted")}>{rank}</td>
      <th scope="row" className={cn(CELL, "min-w-0 text-left font-normal")}>
        <span className="flex min-w-0 items-center gap-2">
          <Identicon seed={v.ref.operator} size={22} />
          <Link to={paths.validator(v.ref.operator)} className="truncate font-medium after:absolute after:inset-0">
            {v.ref.moniker}
          </Link>
          {v.jailed && <Badge tone="bad">jailed</Badge>}
          {!v.jailed && v.status !== "bonded" && <Badge>{statusWord(v.status)}</Badge>}
        </span>
      </th>
      <td className={cn(CELL, "hidden lg:table-cell")}>
        <Badge tone={v.type === "committee" ? "mute" : "ok"}>{v.type === "committee" ? "Founding committee" : "Community stake"}</Badge>
      </td>
      <td className={cn(CELL, "w-[90px] md:w-auto")}>
        <span className="font-mono text-sm tabular-nums">{formatPct(v.power)}</span>
        <span className="mt-1 block h-[5px] overflow-hidden rounded-[3px] bg-surface-3">
          <span className="block h-full bg-fg/60" style={{ width: `${Math.min(100, v.power * 100)}%` }} />
        </span>
      </td>
      <td className={cn(CELL, "hidden font-mono text-sm tabular-nums sm:table-cell")}>{formatPct(v.commission)}</td>
      <td className={cn(CELL, "hidden font-mono text-sm tabular-nums md:table-cell")}>{formatCompact(v.tokens)}</td>
      <td className={cn(CELL, "hidden sm:table-cell")}>
        <UptimeCell signing={signing} operator={v.ref.operator} />
      </td>
    </tr>
  );
}

export function ValidatorTable({ validators, signing }: { validators: readonly Validator[]; signing: SigningState }) {
  return (
    <Card title="Validators">
      <table className="w-full border-collapse text-left">
        <caption className="sr-only">Validators by voting power: commission, ORAMA staked, uptime in the newest closed epoch</caption>
        <thead>
          <tr className="text-[11px] uppercase tracking-[0.07em] text-muted">
            <th scope="col" className={cn(HEAD_CELL, "w-8 pl-2")}>#</th>
            <th scope="col" className={HEAD_CELL}>Validator</th>
            <th scope="col" className={cn(HEAD_CELL, "hidden lg:table-cell")}>Type</th>
            <th scope="col" className={HEAD_CELL}>Voting power</th>
            <th scope="col" className={cn(HEAD_CELL, "hidden sm:table-cell")}>Commission</th>
            <th scope="col" className={cn(HEAD_CELL, "hidden md:table-cell")}>Staked (ORAMA)</th>
            <th scope="col" className={cn(HEAD_CELL, "hidden sm:table-cell")}>
              Uptime
              <Help tip={UPTIME_TIP} />
            </th>
          </tr>
        </thead>
        <tbody>
          {rankValidators(validators).map((v, i) => (
            <Row key={v.ref.operator} v={v} rank={i + 1} signing={signing} />
          ))}
        </tbody>
      </table>
      {signing.status === "error" && (
        <p role="note" className="mt-3 text-[13px] text-muted">
          Uptime comes from the chain indexer and could not be read: {signing.error.message}
        </p>
      )}
    </Card>
  );
}
