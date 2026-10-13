import { useSearchParams } from "react-router";
import { useQuery } from "../data/use-query";
import { parseId, proposalTarget } from "../model/lookup";
import { useExplorerPaths } from "../network/active";
import { Card } from "../ui/card";
import { LookupForm } from "../ui/lookup-form";
import { ModuleActivity } from "../ui/module-activity";
import { Page } from "../ui/page";
import { Query, Skeleton } from "../ui/query-states";
import { useDocumentTitle } from "../ui/use-document-title";
import { ProposalList } from "./governance/proposal-list";
import { EnactedCard, ParamsCard } from "./governance/rules-cards";

const PROPOSALS_PER_PAGE = 10;
const FROM_PARAM = "from";

/** `/explorer/governance` */
export function GovernancePage() {
  useDocumentTitle("Governance");
  const paths = useExplorerPaths();
  const [params] = useSearchParams();
  const from = parseId(params.get(FROM_PARAM) ?? "");
  const proposals = useQuery((s) => s.assets.getProposals(from, PROPOSALS_PER_PAGE), [from]);
  const rules = useQuery((s) => s.assets.getGovernance(), []);
  return (
    <Page>
      <div>
        <h1 className="font-display text-3xl font-semibold tracking-tight">Governance</h1>
        <p className="mt-1 max-w-[75ch] text-sm text-muted">
          x/houses decides the few things that can change on this chain. Token holders vote in the token house, node operators in the operator
          house, and a passed proposal waits out a timelock before it runs. The module has no message that halts or pauses the chain, and none that rushes a proposal.
        </p>
      </div>
      <Card title="Open a proposal">
        <LookupForm
          label="Proposal number"
          placeholder="Proposal number"
          resolve={(text) => proposalTarget(text, paths.assets)}
          hint="A proposal is numbered from 1."
        />
      </Card>
      <Query state={proposals.state} onRetry={proposals.refetch} loading={<Skeleton className="h-48 w-full" />}>
        {(page) => <ProposalList page={page} atNewest={from === null} />}
      </Query>
      <Query state={rules.state} onRetry={rules.refetch} loading={<Skeleton className="h-64 w-full" />}>
        {({ params: p, enacted }) => (
          <div className="grid gap-4 lg:grid-cols-2">
            <ParamsCard params={p} />
            <EnactedCard enacted={enacted} />
          </div>
        )}
      </Query>
      <ModuleActivity title="Recent governance activity" modules={["Houses"]} empty="None of the newest transactions touched governance." />
    </Page>
  );
}
