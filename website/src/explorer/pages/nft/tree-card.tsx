import { useQuery } from "../../data/use-query";
import type { TreeInfo, TreeSnapshot } from "../../model/nfts";
import { treeCapacity } from "../../model/nfts";
import { groupedInteger } from "../../model/paging";
import { Card } from "../../ui/card";
import { CopyChip } from "../../ui/copy";
import { Facts, Absent } from "../../ui/facts";
import { WalletLink } from "../../ui/links";
import { Query, Skeleton } from "../../ui/query-states";

/** The tree an asset sits in: how big it can grow, how far it has, and the content ids recorded for it. */
export function TreeView({ tree, snapshots }: { tree: TreeInfo; snapshots: readonly TreeSnapshot[] }) {
  return (
    <Facts
      rows={[
        ["Creator", <WalletLink wallet={{ address: tree.creator }} />],
        ["Depth", <span className="font-mono tabular-nums">{tree.depth}</span>],
        ["Capacity", <span className="font-mono tabular-nums">{groupedInteger(treeCapacity(tree.depth).toString())} leaves</span>],
        ["Leaves written", <span className="font-mono tabular-nums">{groupedInteger(tree.nextLeaf)}</span>],
        ["Changes made", <span className="font-mono tabular-nums">{groupedInteger(tree.sequence)}</span>],
        [
          "Snapshots",
          snapshots.length === 0 ? (
            <Absent>None recorded</Absent>
          ) : (
            <ul className="space-y-1">
              {snapshots.map((s) => (
                <li key={s.id} className="flex flex-wrap items-center gap-2">
                  <span className="font-mono text-xs">{s.cid}</span>
                  <CopyChip value={s.cid} label="Copy the snapshot content id" />
                </li>
              ))}
            </ul>
          ),
        ],
      ]}
    />
  );
}

/** Loads a tree and its snapshots. */
export function TreeCard({ treeId }: { treeId: number }) {
  const { state, refetch } = useQuery(async (s) => {
    const [tree, snapshots] = await Promise.all([s.assets.getTree(treeId), s.assets.getTreeSnapshots(treeId)]);
    return { tree, snapshots };
  }, [treeId]);
  return (
    <Card title={`Tree ${treeId}`}>
      <Query state={state} onRetry={refetch} loading={<Skeleton className="h-32 w-full" />}>
        {({ tree, snapshots }) => (tree === null ? <p className="text-sm text-muted">x/cnft has no tree {treeId}.</p> : <TreeView tree={tree} snapshots={snapshots} />)}
      </Query>
    </Card>
  );
}
