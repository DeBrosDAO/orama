import { Link } from "react-router";
import { useExplorerPaths } from "../../network/active";
import { Card } from "../../ui/card";
import { DataTable } from "../../ui/data-table";
import { LoadMore } from "../../ui/load-more";
import { Query, Skeleton } from "../../ui/query-states";
import type { KeyPaged } from "../../ui/use-key-paged";

/** Contract addresses, one row each, each opening the contract. */
export function ContractAddressList({ title, empty, paged }: { title: string; empty: string; paged: KeyPaged<string> }) {
  const paths = useExplorerPaths();
  return (
    <Card title={title}>
      <Query state={paged.first} onRetry={paged.retry} loading={<Skeleton className="h-32 w-full" />}>
        {() =>
          paged.items.length === 0 ? (
            <p className="py-4 text-center text-sm text-muted">{empty}</p>
          ) : (
            <>
              <DataTable
                caption={title}
                rows={paged.items}
                rowKey={(address) => address}
                minWidth={320}
                columns={[
                  {
                    header: "Contract",
                    cell: (address) => (
                      <Link to={paths.assets.contract(address)} className="font-mono text-xs underline decoration-dotted underline-offset-4 [overflow-wrap:anywhere]">
                        {address}
                      </Link>
                    ),
                  },
                ]}
              />
              <LoadMore paged={paged} what="contracts" />
            </>
          )
        }
      </Query>
    </Card>
  );
}
