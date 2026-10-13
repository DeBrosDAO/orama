import type { ReactNode } from "react";
import { ChainIndexUnavailable } from "../data/chain/client";
import type { QueryState } from "../data/use-query";
import { Card } from "./card";
import { Query } from "./query-states";
import type { QueryProps } from "./query-states";

/** What a page that reads the chain index shows when the node it reached runs none. */
export function NoIndexerBox({ onRetry, asPageTitle = false }: { onRetry?: () => void; asPageTitle?: boolean }) {
  const Title = asPageTitle ? "h1" : "p";
  return (
    <Card>
      <div role="status" className="space-y-3">
        <Title className="font-medium">This node runs no chain indexer.</Title>
        <p className="text-sm text-muted">
          Epochs, supply and the other history on this page are kept by the chain indexer, and the node that answered does not run one. It is not an
          outage of the chain: blocks and the current epoch still load. Another node of the network may answer next time.
        </p>
        {onRetry && (
          <button type="button" onClick={onRetry} className="rounded-lg border border-border bg-surface-2 px-3 py-1.5 text-sm hover:border-fg/30 cursor-pointer">
            Try again
          </button>
        )}
      </div>
    </Card>
  );
}

export function isNoIndexer<T>(state: QueryState<T>): boolean {
  return state.status === "error" && state.error instanceof ChainIndexUnavailable;
}

/** Query for a read of the chain index: the same loading, error and ready states, and a plain notice when the node runs no indexer. */
export function IndexQuery<T>({ state, subject, loading, onRetry, children }: QueryProps<T>): ReactNode {
  if (isNoIndexer(state)) return <NoIndexerBox onRetry={onRetry} asPageTitle={subject !== undefined} />;
  return (
    <Query state={state} subject={subject} loading={loading} onRetry={onRetry}>
      {children}
    </Query>
  );
}
