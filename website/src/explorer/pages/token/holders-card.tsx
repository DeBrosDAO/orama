import type { TokenHolder } from "../../model/tokens";
import { groupedInteger } from "../../model/paging";
import { Card } from "../../ui/card";
import { DataTable } from "../../ui/data-table";
import { LoadMore } from "../../ui/load-more";
import { WalletLink } from "../../ui/links";
import { Query, Skeleton } from "../../ui/query-states";
import type { KeyPaged } from "../../ui/use-key-paged";

/**
 * The accounts that hold the token, in address order (the bank keeps balances by address, so the
 * chain cannot list the largest first). Tokens held inside the shielded pool belong to no address
 * and are not here.
 */
export function HoldersCard({ paged }: { paged: KeyPaged<TokenHolder> }) {
  return (
    <Card title="Holders">
      <Query state={paged.first} onRetry={paged.retry} loading={<Skeleton className="h-40 w-full" />}>
        {() =>
          paged.items.length === 0 ? (
            <p className="py-4 text-center text-sm text-muted">No account holds this token.</p>
          ) : (
            <>
              <DataTable
                caption="Accounts holding the token"
                rows={paged.items}
                rowKey={(h) => h.address}
                minWidth={360}
                columns={[
                  { header: "Account", cell: (h) => <WalletLink wallet={{ address: h.address }} /> },
                  { header: "Balance", align: "right", cell: (h) => <span className="font-mono tabular-nums">{groupedInteger(h.balance)}</span> },
                ]}
              />
              <LoadMore paged={paged} what="holders" />
            </>
          )
        }
      </Query>
      <p className="mt-3 text-xs text-muted">Holders are listed by address, not by size. A balance inside the shielded pool belongs to no address and is not counted here.</p>
    </Card>
  );
}
