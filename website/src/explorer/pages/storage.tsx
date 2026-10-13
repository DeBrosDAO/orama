import { useQuery } from "../data/use-query";
import { Page } from "../ui/page";
import { useDocumentTitle } from "../ui/use-document-title";
import { ArchiveCard } from "./storage/archive-card";
import { DealsCard } from "./storage/deal-table";
import { ProvidersCard } from "./storage/providers-card";
import { RepairCard } from "./storage/repair-card";

/** Storage deals, the providers that hold them, and the archive of old blocks. */
export function StoragePage() {
  useDocumentTitle("Storage");
  const deals = useQuery((s) => s.getDeals(), []);
  const nodes = useQuery((s) => s.getNetworkNodes(), []);
  const archive = useQuery((s) => s.getArchiveHealth(), []);
  const queue = useQuery((s) => s.getSettlementQueue(), []);
  return (
    <Page>
      <div>
        <h1 className="font-display text-3xl font-semibold tracking-tight">Storage and archive</h1>
        <p className="mt-1 max-w-[75ch] text-sm text-muted">
          Clients pay providers to keep copies of their data, and providers prove every epoch that they still hold them. The protocol also pays providers to
          keep the chain&apos;s own old blocks, so a node can delete them.
        </p>
      </div>
      <DealsCard state={deals.state} onRetry={deals.refetch} />
      <ProvidersCard state={nodes.state} onRetry={nodes.refetch} />
      <ArchiveCard state={archive.state} onRetry={archive.refetch} />
      <RepairCard queue={queue.state} deals={deals.state} onRetry={queue.refetch} />
    </Page>
  );
}
