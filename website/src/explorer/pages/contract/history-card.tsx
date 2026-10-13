import { Link } from "react-router";
import type { HistoryEntry } from "../../model/contracts";
import { operationText } from "../../model/contracts";
import { useExplorerPaths } from "../../network/active";
import { Card } from "../../ui/card";
import { BlockLink } from "../../ui/links";
import { LoadMore } from "../../ui/load-more";
import { Query, Skeleton } from "../../ui/query-states";
import type { KeyPaged } from "../../ui/use-key-paged";

function Entry({ entry }: { entry: HistoryEntry }) {
  const paths = useExplorerPaths();
  return (
    <li className="border-t border-border py-3 first:border-t-0 first:pt-0">
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-sm">
        <span className="font-medium">{operationText(entry.operation)}</span>
        <span className="text-muted">
          code{" "}
          <Link to={paths.assets.code(entry.codeId)} className="font-mono underline decoration-dotted underline-offset-4">
            {entry.codeId}
          </Link>
        </span>
        {entry.height > 0 && (
          <span className="text-muted">
            in block <BlockLink height={entry.height} />
          </span>
        )}
      </div>
      {entry.message !== null && (
        <pre tabIndex={0} aria-label="Message of this operation" className="mt-1.5 max-h-40 overflow-auto rounded-lg border border-border bg-bg p-2 font-mono text-xs text-muted">
          {entry.message}
        </pre>
      )}
    </li>
  );
}

/** The code a contract ran: when it was instantiated and each time it was migrated, with the message each carried. */
export function HistoryCard({ paged }: { paged: KeyPaged<HistoryEntry> }) {
  return (
    <Card title="History">
      <Query state={paged.first} onRetry={paged.retry} loading={<Skeleton className="h-24 w-full" />}>
        {() =>
          paged.items.length === 0 ? (
            <p className="py-3 text-center text-sm text-muted">The chain keeps no history for this contract.</p>
          ) : (
            <>
              <ol>
                {paged.items.map((entry, i) => (
                  <Entry key={`${entry.operation}-${entry.codeId}-${entry.height}-${i}`} entry={entry} />
                ))}
              </ol>
              <LoadMore paged={paged} what="history" />
            </>
          )
        }
      </Query>
    </Card>
  );
}
