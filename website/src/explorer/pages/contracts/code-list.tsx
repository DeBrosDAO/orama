import { Link } from "react-router";
import type { CodeInfo } from "../../model/contracts";
import { permissionText } from "../../model/contracts";
import { useExplorerPaths } from "../../network/active";
import { Card } from "../../ui/card";
import { DataTable } from "../../ui/data-table";
import { LoadMore } from "../../ui/load-more";
import { WalletLink } from "../../ui/links";
import { Query, Skeleton } from "../../ui/query-states";
import type { KeyPaged } from "../../ui/use-key-paged";

/** The wasm codes stored on the chain, in code id order. */
export function CodeList({ paged }: { paged: KeyPaged<CodeInfo> }) {
  const paths = useExplorerPaths();
  return (
    <Card title="Stored codes">
      <Query state={paged.first} onRetry={paged.retry} loading={<Skeleton className="h-48 w-full" />}>
        {() =>
          paged.items.length === 0 ? (
            <p className="py-6 text-center text-sm text-muted">No contract code has been stored on this chain yet.</p>
          ) : (
            <>
              <DataTable
                caption="Wasm codes stored on this chain"
                rows={paged.items}
                rowKey={(c) => String(c.codeId)}
                columns={[
                  {
                    header: "Code",
                    cell: (c) => (
                      <Link to={paths.assets.code(c.codeId)} className="font-mono font-medium underline decoration-dotted underline-offset-4">
                        {c.codeId}
                      </Link>
                    ),
                  },
                  { header: "Uploaded by", cell: (c) => <WalletLink wallet={{ address: c.creator }} /> },
                  { header: "Checksum", cell: (c) => <span className="font-mono text-xs" title={c.checksum}>{c.checksum.slice(0, 12)}…</span>, quiet: true },
                  { header: "Who may instantiate", cell: (c) => permissionText(c.permission), quiet: true },
                ]}
              />
              <LoadMore paged={paged} what="codes" />
            </>
          )
        }
      </Query>
    </Card>
  );
}
