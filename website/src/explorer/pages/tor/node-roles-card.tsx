import type { QueryState } from "../../data/use-query";
import type { ChainNode, NodeSet } from "../../model/nodes";
import { torNodes, unregisteredRelayNodes } from "../../model/tor";
import type { TorRelay } from "../../model/tor";
import { formatInt } from "../../model/units";
import { Card } from "../../ui/card";
import { NodeLink } from "../../ui/links";
import { CutNotice } from "../../ui/not-on-chain";
import { Query, Skeleton } from "../../ui/query-states";
import { StatusBadge } from "../nodes/badges";

function NodeList({ nodes, empty }: { nodes: readonly ChainNode[]; empty: string }) {
  if (nodes.length === 0) return <p className="text-sm text-muted">{empty}</p>;
  return (
    <ul>
      {nodes.map((n) => (
        <li key={n.id} className="flex flex-wrap items-center justify-between gap-x-4 gap-y-1 border-t border-border/60 py-2 first:border-t-0">
          <span className="min-w-0">
            <NodeLink id={n.id} />
            {n.endpoints[0] !== undefined && <span className="ml-2 break-all text-[13px] text-muted">{n.endpoints[0]}</span>}
          </span>
          <StatusBadge status={n.status} />
        </li>
      ))}
    </ul>
  );
}

function Body({ set, relays }: { set: NodeSet; relays: readonly TorRelay[] | null }) {
  const tor = torNodes(set.nodes);
  // A relay list that was cut may hold the relay of a node it seems to lack, so it proves nothing about absence.
  const unregistered = relays === null ? [] : unregisteredRelayNodes(relays, set.nodes);
  return (
    <div className="space-y-4">
      {set.truncated && <CutNotice shown={set.nodes.length} what="nodes" />}
      <div>
        <h3 className="mb-1 text-[11px] font-semibold uppercase tracking-[0.09em] text-muted">
          Directory authorities ({formatInt(tor.authorities.length)})
        </h3>
        <NodeList nodes={tor.authorities} empty="No node holds the directory authority role." />
      </div>
      <div>
        <h3 className="mb-1 text-[11px] font-semibold uppercase tracking-[0.09em] text-muted">
          Nodes that run the relay role ({formatInt(tor.relays.length)}) and the exit role ({formatInt(tor.exits.length)})
        </h3>
        <NodeList nodes={[...new Map([...tor.relays, ...tor.exits].map((n) => [n.id, n])).values()]} empty="No node holds a relay or exit role." />
      </div>
      {unregistered.length > 0 && (
        <p role="note" className="text-[13px] text-muted">
          {unregistered.length} of those node{unregistered.length === 1 ? " has" : "s have"} no relay registered with x/relay, so{" "}
          {unregistered.length === 1 ? "it earns" : "they earn"} no relay reward: {unregistered.map((n) => n.id).join(", ")}.
        </p>
      )}
    </div>
  );
}

/** The nodes that hold a Tor role (directory authority, relay, exit), from x/nodes. */
export function NodeRolesCard({
  nodes,
  relays,
  onRetry,
}: {
  nodes: QueryState<NodeSet>;
  relays: QueryState<{ relays: readonly TorRelay[]; truncated: boolean }>;
  onRetry: () => void;
}) {
  return (
    <Card title="Tor roles on the nodes">
      <Query state={nodes} onRetry={onRetry} loading={<Skeleton className="h-32 w-full" />}>
        {(set) => <Body set={set} relays={relays.status === "ready" && !relays.data.truncated ? relays.data.relays : null} />}
      </Query>
    </Card>
  );
}
