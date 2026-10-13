import { Link } from "react-router";
import type { Proposal } from "../../model/governance";
import type { IdPage } from "../../model/paging";
import { useExplorerPaths } from "../../network/active";
import { Card } from "../../ui/card";
import { DataTable } from "../../ui/data-table";
import { IdPager } from "../../ui/id-pager";
import { WalletLink } from "../../ui/links";
import { RelTime } from "../../ui/rel-time";
import { StatusBadge } from "./status-badge";

/** Proposals, newest first. They are numbered from 1 and never deleted, so the page walks the numbers down from the newest. */
export function ProposalList({ page, atNewest }: { page: IdPage<Proposal>; atNewest: boolean }) {
  const paths = useExplorerPaths();
  return (
    <Card title="Proposals" action={<span className="text-xs text-muted">{page.newest} in all</span>}>
      {page.items.length === 0 ? (
        <p className="py-6 text-center text-sm text-muted">{atNewest ? "No proposal has been submitted on this chain yet." : "There is no proposal at that number."}</p>
      ) : (
        <>
          <DataTable
            caption="Governance proposals, newest first"
            rows={page.items}
            rowKey={(p) => String(p.id)}
            columns={[
              {
                header: "Proposal",
                cell: (p) => (
                  <Link to={paths.assets.proposal(p.id)} className="font-mono font-medium underline decoration-dotted underline-offset-4">
                    #{p.id}
                  </Link>
                ),
              },
              { header: "Kind", cell: (p) => p.content.kind },
              { header: "Status", cell: (p) => <StatusBadge status={p.status} /> },
              { header: "Proposer", cell: (p) => <WalletLink wallet={{ address: p.proposer }} /> },
              { header: "Submitted", cell: (p) => <RelTime iso={p.submittedAt} />, quiet: true },
            ]}
          />
          <IdPager param="from" atNewest={atNewest} next={page.next} />
        </>
      )}
    </Card>
  );
}
