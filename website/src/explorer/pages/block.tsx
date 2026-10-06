import { useParams } from "react-router";
import { useHead } from "../data/provider";
import { useQuery } from "../data/use-query";
import type { Block } from "../model/types";
import { formatInt } from "../model/units";
import { NotFoundBox, Query } from "../ui/query-states";
import { Page } from "../ui/page";
import { useDocumentTitle } from "../ui/use-document-title";
import { BlockHeader } from "./block/block-header";
import { FIRST_BLOCK, blockNeighbours, missingBlockHint, parseBlockHeight } from "./block/block-nav";
import { BlockSkeleton } from "./block/block-skeleton";
import { BlockStats } from "./block/block-stats";
import { BlockTxs } from "./block/block-txs";
import { useBlockKeys } from "./block/use-block-keys";

const NOT_FOUND_TITLE = "Block not found";

function BlockBody({ block, head }: { block: Block; head: number | null }) {
  const nav = blockNeighbours(block.height, head);
  useBlockKeys(nav);
  return (
    <div className="space-y-4">
      <BlockHeader height={block.height} hash={block.hash} nav={nav} />
      <BlockStats block={block} />
      <BlockTxs txs={block.txs} />
    </div>
  );
}

function BlockView({ height }: { height: number }) {
  const head = useHead()?.height ?? null;
  const { state, refetch } = useQuery((s) => s.getBlock(height), [height]);
  useDocumentTitle(`Block ${formatInt(height)}`);
  return (
    <Query state={state} onRetry={refetch} loading={<BlockSkeleton />}>
      {(block) =>
        block ? (
          <BlockBody block={block} head={head} />
        ) : (
          <NotFoundBox title={NOT_FOUND_TITLE} hint={missingBlockHint(height, head)} />
        )
      }
    </Query>
  );
}

/** `/explorer/block/:height` */
export function BlockPage() {
  const { height: raw } = useParams();
  const height = parseBlockHeight(raw);
  return (
    <Page>
      {height !== null && height >= FIRST_BLOCK ? (
        <BlockView key={height} height={height} />
      ) : (
        <NotFoundBox title={NOT_FOUND_TITLE} hint={missingBlockHint(height, null)} />
      )}
    </Page>
  );
}
