import { useQuery } from "../data/use-query";
import type { QueryState } from "../data/use-query";
import type { NodeSet } from "../model/nodes";
import type { TorRelays } from "../model/tor";
import { formatInt } from "../model/units";
import { NotOnChain } from "../ui/not-on-chain";
import { Page } from "../ui/page";
import { Query, Skeleton } from "../ui/query-states";
import { Stat } from "../ui/stat";
import { useDocumentTitle } from "../ui/use-document-title";
import { NodeRolesCard } from "./tor/node-roles-card";
import { RelayTable } from "./tor/relay-table";
import { RewardsCard } from "./tor/rewards-card";

const TOR_NOT_ON_CHAIN = [
  {
    what: "Validators' onion services",
    why: "The onion address of each validator is kept in the network's Tor network file, not on the chain, so this page cannot list it.",
  },
  {
    what: "Whether a relay is in the consensus",
    why: "The relay's own machine works it out from the Tor consensus it holds and reports it to its operator's monitoring. No transaction carries it to the chain.",
  },
  {
    what: "Bandwidth and uptime of a relay",
    why: "Reporters submit them each epoch to settle rewards, and the chain deletes each report once the epoch is settled. It keeps only the epoch's totals, shown above.",
  },
] as const;

function Loading() {
  return (
    <div className="space-y-4">
      <Skeleton className="h-64 w-full" />
      <Skeleton className="h-48 w-full" />
    </div>
  );
}

function RelayStats({ tor }: { tor: TorRelays }) {
  const exits = tor.relays.filter((r) => r.exit).length;
  const jailed = tor.relays.filter((r) => r.jailed).length;
  return (
    <div className="grid gap-4 sm:grid-cols-3">
      <Stat label="Registered relays" value={formatInt(tor.relays.length)} detail={`${formatInt(exits)} declare themselves exits`} />
      <Stat label="Jailed" value={formatInt(jailed)} detail={jailed === 0 ? "no relay is jailed" : "paid nothing until released"} />
      <Stat label="Reporters" value={formatInt(tor.reporters.length)} detail={`${tor.params.reportersQuorum} needed to pay an epoch`} />
    </div>
  );
}

function RelaysBody({ tor, nodes }: { tor: TorRelays; nodes: QueryState<NodeSet> }) {
  return (
    <>
      <RelayStats tor={tor} />
      <RelayTable tor={tor} nodes={nodes.status === "ready" ? nodes.data : null} />
      <RewardsCard tor={tor} />
    </>
  );
}

export function TorPage() {
  useDocumentTitle("Tor");
  const relays = useQuery((s) => s.getTorRelays(), []);
  const nodes = useQuery((s) => s.getNetworkNodes(), []);
  return (
    <Page>
      <div>
        <h1 className="font-display text-3xl font-semibold tracking-tight">Tor and relays</h1>
        <p className="mt-1 max-w-[75ch] text-sm text-muted">
          The network runs its own Tor network. Operators register relays on the chain and are rewarded for the traffic they carry. This page shows what the
          chain records; what it does not record is listed at the bottom.
        </p>
      </div>
      <Query state={relays.state} onRetry={relays.refetch} loading={<Loading />}>
        {(tor) => <RelaysBody tor={tor} nodes={nodes.state} />}
      </Query>
      <NodeRolesCard nodes={nodes.state} relays={relays.state} onRetry={nodes.refetch} />
      <NotOnChain items={TOR_NOT_ON_CHAIN} />
    </Page>
  );
}
