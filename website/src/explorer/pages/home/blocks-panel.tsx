import { Link } from "react-router";
import { useLiveQuery } from "../../data/use-query";
import { explorerPaths } from "../../model/routes";
import type { BlockSummary } from "../../model/types";
import { Card } from "../../ui/card";
import { Help } from "../../ui/help";
import { Query, Skeleton } from "../../ui/query-states";
import { cn } from "../../../lib/utils";
import { averageTxs, blockLabel, blockShade, formatSeconds } from "./logic";
import type { BlockShade } from "./logic";

const BLOCK_COUNT = 24;
const TIP_BLOCK = "A block is a batch of transactions. Darker squares carry more transactions. The outlined one just arrived.";

const SHADE_CLASS: Record<BlockShade, string> = {
  0: "bg-surface-2 text-muted",
  1: "bg-border text-muted",
  2: "bg-muted/60 text-fg",
  3: "bg-muted text-bg",
};

const SQUARE = "grid h-[38px] w-[38px] place-items-center rounded-lg border border-border font-mono text-[10.5px] transition-colors hover:border-fg/60";
const NEWEST = "outline outline-1 outline-gain";

function BlockSquares({ blocks }: { blocks: BlockSummary[] }) {
  return (
    <ul className="flex flex-wrap gap-[5px]">
      {blocks.map((b, i) => (
        <li key={b.height}>
          <Link
            to={explorerPaths.block(b.height)}
            aria-label={blockLabel(b)}
            title={blockLabel(b)}
            className={cn(SQUARE, SHADE_CLASS[blockShade(b.txCount)], i === 0 ? NEWEST : "")}
          >
            {b.txCount}
          </Link>
        </li>
      ))}
    </ul>
  );
}

function BlocksSkeleton() {
  return (
    <div className="flex flex-wrap gap-[5px]">
      {Array.from({ length: BLOCK_COUNT }, (_, i) => (
        <Skeleton key={i} className="h-[38px] w-[38px] rounded-lg" />
      ))}
    </div>
  );
}

function footnote(blocks: BlockSummary[], blockTimeSeconds: number | null): string {
  const parts: string[] = [];
  if (blockTimeSeconds !== null) parts.push(`New block every ~${formatSeconds(blockTimeSeconds)} s`);
  const avg = averageTxs(blocks);
  if (avg !== null) parts.push(`avg ${avg} txs`);
  return parts.join(" · ");
}

export function BlocksPanel({ blockTimeSeconds }: { blockTimeSeconds: number | null }) {
  const { state, refetch } = useLiveQuery((s) => s.getRecentBlocks(BLOCK_COUNT), []);
  return (
    <Card title={<>Latest blocks<Help tip={TIP_BLOCK} /></>}>
      <Query state={state} loading={<BlocksSkeleton />} onRetry={refetch}>
        {(blocks) => (
          <>
            <BlockSquares blocks={blocks} />
            <p className="mt-2.5 text-[12.5px] text-muted">{footnote(blocks, blockTimeSeconds)}</p>
          </>
        )}
      </Query>
    </Card>
  );
}
