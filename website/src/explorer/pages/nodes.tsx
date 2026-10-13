import { useQuery } from "../data/use-query";
import type { NodeSet } from "../model/nodes";
import { Page } from "../ui/page";
import { CutNotice, NotOnChain } from "../ui/not-on-chain";
import { Query, Skeleton } from "../ui/query-states";
import { StatusLink } from "../ui/status-link";
import { useDocumentTitle } from "../ui/use-document-title";
import { AsnCard } from "./nodes/asn-card";
import { NodeTable } from "./nodes/node-table";
import { NodeStats, RolesCard } from "./nodes/summary-cards";

const NODES_NOT_ON_CHAIN = [
  { what: "Software version", why: "A node does not register the version it runs, so no page of the chain can list it." },
  {
    what: "Whether a node is up",
    why: "The chain records what a node registered and bonded, not whether it answers now. The network's public status page shows the health of its services.",
  },
] as const;

function Loading() {
  return (
    <div className="space-y-4">
      <Skeleton className="h-24 w-full" />
      <Skeleton className="h-56 w-full" />
      <Skeleton className="h-80 w-full" />
    </div>
  );
}

export function NodesBody({ set }: { set: NodeSet }) {
  return (
    <>
      {set.truncated && <CutNotice shown={set.nodes.length} what="nodes" />}
      <NodeStats nodes={set.nodes} />
      <div className="grid gap-4 lg:grid-cols-2">
        <RolesCard nodes={set.nodes} />
        <AsnCard nodes={set.nodes} />
      </div>
      <NodeTable nodes={set.nodes} />
      <NotOnChain items={NODES_NOT_ON_CHAIN} />
    </>
  );
}

export function NodesPage() {
  useDocumentTitle("Nodes");
  const { state, refetch } = useQuery((s) => s.getNetworkNodes(), []);
  return (
    <Page>
      <div className="flex flex-wrap items-end justify-between gap-3">
        <div>
          <h1 className="font-display text-3xl font-semibold tracking-tight">Nodes</h1>
          <p className="mt-1 max-w-[75ch] text-sm text-muted">
            The machines that make up the network&apos;s global layer: who registered them, what they run and what they put at stake. The chain keeps this
            list.
          </p>
        </div>
        <StatusLink />
      </div>
      <Query state={state} onRetry={refetch} loading={<Loading />}>
        {(set) => <NodesBody set={set} />}
      </Query>
    </Page>
  );
}
