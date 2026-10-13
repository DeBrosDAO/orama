import type { QueryState } from "../../data/use-query";
import { bondOf, capacityTotals, hasRole, isLive, utilisation } from "../../model/nodes";
import type { ChainNode, NodeSet } from "../../model/nodes";
import { formatBytes, formatInt, formatPct } from "../../model/units";
import { cn } from "../../../lib/utils";
import { Amount } from "../../ui/amount";
import { Card } from "../../ui/card";
import { NodeLink, WalletLink } from "../../ui/links";
import { CutNotice } from "../../ui/not-on-chain";
import { Query, Skeleton } from "../../ui/query-states";
import { StatusBadge } from "../nodes/badges";

const HEAD_CELL = "pb-2 pr-3 font-medium";
const CELL = "py-2.5 pr-3 align-middle";

/** The live nodes that run the storage role, largest declared capacity first. */
export function providersOf(nodes: readonly ChainNode[]): ChainNode[] {
  return nodes
    .filter((n) => isLive(n) && hasRole(n, "storage"))
    .sort((a, b) => {
      const x = BigInt(a.declaredCapacityBytes);
      const y = BigInt(b.declaredCapacityBytes);
      return x === y ? a.id.localeCompare(b.id) : x > y ? -1 : 1;
    });
}

function ProviderRow({ node }: { node: ChainNode }) {
  const used = utilisation(node);
  return (
    <tr className="border-t border-border/60 first:border-t-0 hover:bg-surface-2">
      <th scope="row" className={cn(CELL, "pl-2 text-left font-normal")}>
        <NodeLink id={node.id} />
      </th>
      <td className={cn(CELL, "hidden md:table-cell")}>
        <WalletLink wallet={{ address: node.operator }} bare />
      </td>
      <td className={cn(CELL, "text-sm")}>{formatBytes(node.declaredCapacityBytes)}</td>
      <td className={cn(CELL, "w-[130px] text-sm")}>
        {used === null ? (
          <span className="text-muted">–</span>
        ) : (
          <>
            <span className="font-mono tabular-nums">{formatPct(used)}</span>
            <span className="mt-1 block h-[5px] overflow-hidden rounded-[3px] bg-surface-3">
              <span className="block h-full bg-fg/60" style={{ width: `${Math.min(100, used * 100)}%` }} />
            </span>
          </>
        )}
      </td>
      <td className={cn(CELL, "hidden text-sm sm:table-cell")}>
        <Amount norama={bondOf(node, "storage")} bare maxFraction={1} />
      </td>
      <td className={cn(CELL, "hidden md:table-cell")}>
        <StatusBadge status={node.status} />
      </td>
    </tr>
  );
}

function ProvidersBody({ set }: { set: NodeSet }) {
  const providers = providersOf(set.nodes);
  const totals = capacityTotals(set.nodes);
  return (
    <>
      {set.truncated && <CutNotice shown={set.nodes.length} what="nodes" />}
      {providers.length === 0 ? (
        <p className="py-4 text-center text-sm text-muted">No node runs the storage role yet.</p>
      ) : (
        <>
          <p className="mb-3 text-sm text-muted">
            {formatInt(providers.length)} provider{providers.length === 1 ? "" : "s"} declare {formatBytes(totals.declared)} between them, and deals have
            reserved {formatBytes(totals.reserved)} of it. Providers state their capacity themselves; it is backed by their bond but not measured by the chain.
          </p>
          <table className="w-full border-collapse text-left">
            <caption className="sr-only">Storage providers: declared capacity, the share reserved for deals, storage bond in ORAMA and status</caption>
            <thead>
              <tr className="text-[11px] uppercase tracking-[0.07em] text-muted">
                <th scope="col" className={cn(HEAD_CELL, "pl-2")}>Provider</th>
                <th scope="col" className={cn(HEAD_CELL, "hidden md:table-cell")}>Operator</th>
                <th scope="col" className={HEAD_CELL}>Declared</th>
                <th scope="col" className={HEAD_CELL}>Reserved</th>
                <th scope="col" className={cn(HEAD_CELL, "hidden sm:table-cell")}>Bond (ORAMA)</th>
                <th scope="col" className={cn(HEAD_CELL, "hidden md:table-cell")}>Status</th>
              </tr>
            </thead>
            <tbody>
              {providers.map((node) => (
                <ProviderRow key={node.id} node={node} />
              ))}
            </tbody>
          </table>
        </>
      )}
    </>
  );
}

/** The storage providers and the capacity they declare. */
export function ProvidersCard({ state, onRetry }: { state: QueryState<NodeSet>; onRetry: () => void }) {
  return (
    <Card title="Providers">
      <Query state={state} onRetry={onRetry} loading={<Skeleton className="h-40 w-full" />}>
        {(set) => <ProvidersBody set={set} />}
      </Query>
    </Card>
  );
}
