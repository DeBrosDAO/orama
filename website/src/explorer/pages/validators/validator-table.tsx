import { Link } from "react-router";
import { explorerPaths } from "../../model/routes";
import type { Validator } from "../../model/types";
import { Badge } from "../../ui/badge";
import { Card } from "../../ui/card";
import { Identicon } from "../../ui/identicon";
import { cn } from "../../../lib/utils";
import { formatPct } from "../../model/units";
import { rankValidators } from "./summary";

const HEAD_CELL = "pb-2 pr-3 font-medium";
const CELL = "py-3 pr-3 align-middle";

function Row({ v, rank }: { v: Validator; rank: number }) {
  return (
    <tr className="relative border-t border-border/60 first:border-t-0 hover:bg-surface-2">
      <td className={cn(CELL, "pl-2 font-mono text-muted")}>{rank}</td>
      <th scope="row" className={cn(CELL, "min-w-0 text-left font-normal")}>
        <span className="flex min-w-0 items-center gap-2">
          <Identicon seed={v.ref.operator} size={22} />
          <Link
            to={explorerPaths.wallet(v.ref.operator)}
            className="truncate font-medium after:absolute after:inset-0"
          >
            {v.ref.moniker}
          </Link>
          {v.jailed && <Badge tone="bad">jailed</Badge>}
        </span>
      </th>
      <td className={cn(CELL, "hidden md:table-cell")}>
        <Badge tone={v.type === "committee" ? "mute" : "ok"}>{v.type === "committee" ? "Founding committee" : "Community stake"}</Badge>
      </td>
      <td className={cn(CELL, "w-[90px] md:w-auto")}>
        <span className="font-mono text-sm tabular-nums">{formatPct(v.power)}</span>
        <span className="mt-1 block h-[5px] overflow-hidden rounded-[3px] bg-surface-3">
          <span className="block h-full bg-fg/60" style={{ width: `${Math.min(100, v.power * 100)}%` }} />
        </span>
      </td>
    </tr>
  );
}

export function ValidatorTable({ validators }: { validators: readonly Validator[] }) {
  return (
    <Card title="Validators">
      <table className="w-full border-collapse text-left">
        <thead>
          <tr className="text-[11px] uppercase tracking-[0.07em] text-muted">
            <th scope="col" className={cn(HEAD_CELL, "w-8 pl-2")}>#</th>
            <th scope="col" className={HEAD_CELL}>Validator</th>
            <th scope="col" className={cn(HEAD_CELL, "hidden md:table-cell")}>Type</th>
            <th scope="col" className={HEAD_CELL}>Voting power</th>
          </tr>
        </thead>
        <tbody>
          {rankValidators(validators).map((v, i) => (
            <Row key={v.ref.operator} v={v} rank={i + 1} />
          ))}
        </tbody>
      </table>
    </Card>
  );
}
