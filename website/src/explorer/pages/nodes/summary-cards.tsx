import { NODE_ROLES, ROLE_INFO, asnShares, isLive, operatorCount, roleCounts, statusCounts, totalBond } from "../../model/nodes";
import type { ChainNode } from "../../model/nodes";
import { formatCompact, formatInt, formatPct } from "../../model/units";
import { Card } from "../../ui/card";
import { Help } from "../../ui/help";
import { Stat } from "../../ui/stat";

const ASN_TIP =
  "The autonomous system number each operator declared for its node. The chain cannot check it, so it shows where operators say their nodes are, not proof of it.";

function describeGone(nodes: readonly ChainNode[]): string {
  const counts = statusCounts(nodes);
  const gone = counts.retired + counts.tombstoned;
  const jailed = counts.jailed > 0 ? `${counts.jailed} jailed` : "none jailed";
  return gone > 0 ? `${jailed}, ${gone} retired or tombstoned` : jailed;
}

export function NodeStats({ nodes }: { nodes: readonly ChainNode[] }) {
  const live = nodes.filter(isLive);
  const declared = asnShares(nodes).filter((s) => s.asn !== 0);
  const bonded = live.reduce((n, node) => n + BigInt(totalBond(node)), 0n).toString();
  return (
    <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
      <Stat label="Nodes" value={formatInt(live.length)} detail={describeGone(nodes)} />
      <Stat label="Operators" value={formatInt(operatorCount(nodes))} detail="distinct wallets running those nodes" />
      <Stat
        label={
          <>
            Declared ASNs
            <Help tip={ASN_TIP} />
          </>
        }
        value={formatInt(declared.length)}
        detail={declared[0] === undefined ? "no node declared one" : `largest holds ${formatPct(declared[0].share)} of nodes`}
      />
      <Stat label="Bonded" value={<>{formatCompact(bonded)} <span className="text-sm font-normal text-muted">ORAMA</span></>} detail="across every role of those nodes" />
    </div>
  );
}

export function RolesCard({ nodes }: { nodes: readonly ChainNode[] }) {
  const counts = roleCounts(nodes);
  return (
    <Card title="What the nodes run">
      <ul>
        {NODE_ROLES.map((role) => (
          <li key={role} className="flex items-baseline justify-between gap-3 border-t border-border/60 py-2 first:border-t-0">
            <span className="min-w-0">
              <span className="text-sm font-medium">{ROLE_INFO[role].label}</span>
              <span className="ml-2 text-[13px] text-muted">{ROLE_INFO[role].blurb}</span>
            </span>
            <span className="font-mono text-sm tabular-nums">{formatInt(counts[role])}</span>
          </li>
        ))}
      </ul>
      <p className="mt-2 text-[13px] text-muted">A node can run several roles, so these add up to more than the number of nodes.</p>
    </Card>
  );
}
