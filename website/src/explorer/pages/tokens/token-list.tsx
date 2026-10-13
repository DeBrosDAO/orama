import { Link } from "react-router";
import type { TokenRow } from "../../model/tokens";
import { groupedInteger } from "../../model/paging";
import { useExplorerPaths } from "../../network/active";
import { Card } from "../../ui/card";
import { DataTable } from "../../ui/data-table";
import { LoadMore } from "../../ui/load-more";
import { Query, Skeleton } from "../../ui/query-states";
import { WalletLink } from "../../ui/links";
import type { KeyPaged } from "../../ui/use-key-paged";

/** The tokens x/token has issued, in denom order, a page at a time. */
export function TokenList({ paged }: { paged: KeyPaged<TokenRow> }) {
  const paths = useExplorerPaths();
  return (
    <Card title="Issued tokens">
      <Query state={paged.first} onRetry={paged.retry} loading={<Skeleton className="h-64 w-full" />}>
        {() =>
          paged.items.length === 0 ? (
            <p className="py-6 text-center text-sm text-muted">No token has been created on this chain yet.</p>
          ) : (
            <>
              <DataTable
                caption="Tokens issued on this chain"
                rows={paged.items}
                rowKey={(t) => t.denom}
                columns={[
                  {
                    header: "Token",
                    cell: (t) => (
                      <Link to={paths.assets.token(t.denom)} className="font-mono font-medium underline decoration-dotted underline-offset-4">
                        {t.subdenom}
                      </Link>
                    ),
                  },
                  { header: "Creator", cell: (t) => <WalletLink wallet={{ address: t.creator }} /> },
                  { header: "Supply", align: "right", cell: (t) => <span className="font-mono tabular-nums">{groupedInteger(t.supply)}</span> },
                ]}
              />
              <LoadMore paged={paged} what="tokens" />
            </>
          )
        }
      </Query>
    </Card>
  );
}
