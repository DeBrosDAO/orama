import { ROLE_INFO, bondOf, minBondOf, roleBonded } from "../../model/nodes";
import type { NodeDetail } from "../../model/nodes";
import { formatUtc } from "../../model/time";
import { Amount } from "../../ui/amount";
import { Badge } from "../../ui/badge";
import { Card } from "../../ui/card";
import { Help } from "../../ui/help";
import { cn } from "../../../lib/utils";

const HEAD_CELL = "pb-2 pr-3 font-medium";
const CELL = "py-2 pr-3 align-middle";
const DAY_SECONDS = 86_400;
const UNBONDING_TIP = "Bond that was taken out of a role. It stays locked in escrow until the unbonding time has passed, and then goes back to the operator.";

function unbondingDays(seconds: number): string {
  const days = seconds / DAY_SECONDS;
  return Number.isInteger(days) ? `${days} day${days === 1 ? "" : "s"}` : `${seconds} seconds`;
}

/** What each role has bonded against the minimum that keeps it active, and the bond on its way out. */
export function NodeBonds({ detail }: { detail: NodeDetail }) {
  const { node, params, unbondings } = detail;
  return (
    <Card title="Bonds">
      {node.roles.length === 0 ? (
        <p className="text-sm text-muted">This node holds no role, so it has nothing to bond.</p>
      ) : (
        <table className="w-full border-collapse text-left">
          <caption className="sr-only">ORAMA bonded on each role of the node against the minimum for that role</caption>
          <thead>
            <tr className="text-[11px] uppercase tracking-[0.07em] text-muted">
              <th scope="col" className={HEAD_CELL}>Role</th>
              <th scope="col" className={HEAD_CELL}>Bonded</th>
              <th scope="col" className={HEAD_CELL}>Minimum</th>
              <th scope="col" className={HEAD_CELL}>State</th>
            </tr>
          </thead>
          <tbody>
            {node.roles.map((role) => {
              const bonded = roleBonded(node, params, role);
              return (
                <tr key={role} className="border-t border-border/60 first:border-t-0">
                  <th scope="row" className={cn(CELL, "text-left font-normal")}>{ROLE_INFO[role].label}</th>
                  <td className={CELL}><Amount norama={bondOf(node, role)} bare maxFraction={2} /></td>
                  <td className={CELL}><Amount norama={minBondOf(params, role)} bare maxFraction={2} /></td>
                  <td className={CELL}>
                    <Badge tone={bonded ? "ok" : "bad"}>{bonded ? "active" : "below minimum"}</Badge>
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      )}
      <h3 className="mt-4 text-[11px] font-semibold uppercase tracking-[0.09em] text-muted">
        Unbonding
        <Help tip={UNBONDING_TIP} />
      </h3>
      {unbondings.length === 0 ? (
        <p className="mt-1 text-sm text-muted">Nothing is unbonding.</p>
      ) : (
        <ul className="mt-1 text-sm">
          {unbondings.map((u) => (
            <li key={u.id} className="flex flex-wrap justify-between gap-x-4 border-t border-border/60 py-2 first:border-t-0">
              <span>
                <Amount norama={u.amount} maxFraction={2} /> from {ROLE_INFO[u.role].label}
              </span>
              <span className="text-muted">released {formatUtc(u.completesAt)}</span>
            </li>
          ))}
        </ul>
      )}
      <p className="mt-3 text-[13px] text-muted">Bond stays locked {unbondingDays(params.unbondingSeconds)} after it is taken out of a role.</p>
    </Card>
  );
}
