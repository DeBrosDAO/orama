import type { StateEntry } from "../../model/contracts";
import { Card } from "../../ui/card";
import { DataTable } from "../../ui/data-table";
import { LoadMore } from "../../ui/load-more";
import { Query, Skeleton } from "../../ui/query-states";
import type { KeyPaged } from "../../ui/use-key-paged";

/**
 * The raw key-value state a contract stores, in key order. A key or value that is printable text is
 * shown as text; anything else is shown as hex or by its size. The meaning of an entry is the
 * contract's: the explorer does not run it.
 */
export function StateCard({ paged }: { paged: KeyPaged<StateEntry> }) {
  return (
    <Card title="Stored state">
      <Query state={paged.first} onRetry={paged.retry} loading={<Skeleton className="h-32 w-full" />}>
        {() =>
          paged.items.length === 0 ? (
            <p className="py-3 text-center text-sm text-muted">This contract stores nothing.</p>
          ) : (
            <>
              <DataTable
                caption="Raw state of the contract"
                rows={paged.items}
                rowKey={(e) => e.key}
                minWidth={420}
                columns={[
                  { header: "Key", cell: (e) => <span className="font-mono text-xs [overflow-wrap:anywhere]">{e.key}</span> },
                  { header: "Value", cell: (e) => <span className="font-mono text-xs [overflow-wrap:anywhere]">{e.value}</span> },
                ]}
              />
              <LoadMore paged={paged} what="entries" />
            </>
          )
        }
      </Query>
    </Card>
  );
}
