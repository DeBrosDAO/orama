import { useState } from "react";
import { NODE_ROLES, ROLE_INFO, hasRole, isLive, totalBond } from "../../model/nodes";
import type { ChainNode, NodeRole } from "../../model/nodes";
import { cn } from "../../../lib/utils";
import { Amount } from "../../ui/amount";
import { Card } from "../../ui/card";
import { ChipGroup } from "../../ui/chips";
import { NodeLink, WalletLink } from "../../ui/links";
import { RoleBadges, StatusBadge } from "./badges";

type RoleFilter = NodeRole | "all";

const HEAD_CELL = "pb-2 pr-3 font-medium";
const CELL = "py-3 pr-3 align-middle";
const FILTERS = [{ id: "all" as RoleFilter, label: "All" }, ...NODE_ROLES.map((r) => ({ id: r as RoleFilter, label: ROLE_INFO[r].label }))];

/** The live nodes first, then by id. */
export function orderNodes(nodes: readonly ChainNode[]): ChainNode[] {
  return [...nodes].sort((a, b) => Number(isLive(b)) - Number(isLive(a)) || a.id.localeCompare(b.id));
}

export function filterNodes(nodes: readonly ChainNode[], filter: RoleFilter): ChainNode[] {
  return filter === "all" ? [...nodes] : nodes.filter((n) => hasRole(n, filter));
}

function Row({ node }: { node: ChainNode }) {
  return (
    <tr className="border-t border-border/60 first:border-t-0 hover:bg-surface-2">
      <th scope="row" className={cn(CELL, "min-w-0 pl-2 text-left font-normal")}>
        <NodeLink id={node.id} />
      </th>
      <td className={cn(CELL, "hidden md:table-cell")}>
        <WalletLink wallet={{ address: node.operator }} bare />
      </td>
      <td className={CELL}>
        <RoleBadges roles={node.roles} />
      </td>
      <td className={CELL}>
        <StatusBadge status={node.status} />
      </td>
      <td className={cn(CELL, "hidden text-sm sm:table-cell")}>
        <Amount norama={totalBond(node)} bare maxFraction={1} />
      </td>
      <td className={cn(CELL, "hidden font-mono text-sm tabular-nums lg:table-cell")}>{node.asn === 0 ? <span className="text-muted">–</span> : `AS${node.asn}`}</td>
      <td className={cn(CELL, "hidden text-sm lg:table-cell")}>{node.regionHint === "" ? <span className="text-muted">–</span> : node.regionHint}</td>
    </tr>
  );
}

/** Every node, filtered by role. The ASN and region are what the operator declared. */
export function NodeTable({ nodes }: { nodes: readonly ChainNode[] }) {
  const [filter, setFilter] = useState<RoleFilter>("all");
  const rows = orderNodes(filterNodes(nodes, filter));
  return (
    <Card title="Nodes">
      <ChipGroup options={FILTERS} value={filter} onChange={setFilter} label="Filter nodes by role" className="mb-3" />
      {rows.length === 0 ? (
        <p className="py-4 text-center text-sm text-muted">{nodes.length === 0 ? "No node is registered on this chain yet." : "No node runs this role."}</p>
      ) : (
        <table className="w-full border-collapse text-left">
          <caption className="sr-only">Registered nodes: roles, status, ORAMA bonded, and the ASN and region their operators declared</caption>
          <thead>
            <tr className="text-[11px] uppercase tracking-[0.07em] text-muted">
              <th scope="col" className={cn(HEAD_CELL, "pl-2")}>Node</th>
              <th scope="col" className={cn(HEAD_CELL, "hidden md:table-cell")}>Operator</th>
              <th scope="col" className={HEAD_CELL}>Roles</th>
              <th scope="col" className={HEAD_CELL}>Status</th>
              <th scope="col" className={cn(HEAD_CELL, "hidden sm:table-cell")}>Bonded (ORAMA)</th>
              <th scope="col" className={cn(HEAD_CELL, "hidden lg:table-cell")}>ASN</th>
              <th scope="col" className={cn(HEAD_CELL, "hidden lg:table-cell")}>Region</th>
            </tr>
          </thead>
          <tbody>
            {rows.map((node) => (
              <Row key={node.id} node={node} />
            ))}
          </tbody>
        </table>
      )}
    </Card>
  );
}
