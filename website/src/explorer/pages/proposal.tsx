import { useParams } from "react-router";
import { useQuery } from "../data/use-query";
import type { Proposal } from "../model/governance";
import { parseId } from "../model/lookup";
import { Card } from "../ui/card";
import { Facts } from "../ui/facts";
import { WalletLink } from "../ui/links";
import { Page } from "../ui/page";
import { NotFoundBox, Query, Skeleton } from "../ui/query-states";
import { useDocumentTitle } from "../ui/use-document-title";
import { StatusBadge } from "./governance/status-badge";
import { TallyCard } from "./proposal/tally-card";
import { When } from "./proposal/when";
import { VoteLookup } from "./proposal/vote-lookup";
import { FieldList } from "./tx/message-fields";

const NOT_FOUND_TITLE = "Proposal not found";

function ProposalView({ proposal }: { proposal: Proposal }) {
  return (
    <>
      <header>
        <div className="flex flex-wrap items-center gap-2 text-[13px] text-muted">
          <span>Proposal #{proposal.id}</span>
          <StatusBadge status={proposal.status} />
        </div>
        <h1 className="mt-1.5 font-display text-3xl font-semibold tracking-tight">{proposal.content.kind}</h1>
      </header>
      {proposal.failReason !== "" && (
        <Card>
          <p role="status" className="text-sm text-loss">Failed: {proposal.failReason}</p>
        </Card>
      )}
      <div className="grid gap-4 lg:grid-cols-2">
        <Card title="What it would do">
          <FieldList fields={proposal.content.facts} className="text-[13px]" />
        </Card>
        <Card title="Timeline">
          <Facts
            rows={[
              ["Proposer", <WalletLink wallet={{ address: proposal.proposer }} />],
              ["Submitted", <When iso={proposal.submittedAt} />],
              ["Voting ends", <When iso={proposal.votingEndsAt} />],
              ["Veto window ends", <When iso={proposal.vetoEndsAt} />],
              ["Timelock ends", <When iso={proposal.timelockEndsAt} />],
            ]}
          />
        </Card>
      </div>
      <TallyCard proposal={proposal} />
      <VoteLookup proposalId={proposal.id} />
    </>
  );
}

function ProposalLoader({ id }: { id: number }) {
  const { state, refetch } = useQuery((s) => s.assets.getProposal(id), [id]);
  useDocumentTitle(`Proposal ${id}`);
  return (
    <Query state={state} subject={`proposal ${id}`} onRetry={refetch} loading={<Skeleton className="h-72 w-full" />}>
      {(proposal) =>
        proposal === null ? (
          <NotFoundBox title={NOT_FOUND_TITLE} hint={`x/houses has no proposal ${id}. Proposals are numbered from 1 in the order they were submitted.`} />
        ) : (
          <ProposalView proposal={proposal} />
        )
      }
    </Query>
  );
}

/** `/explorer/proposal/:id` */
export function ProposalPage() {
  const id = parseId(useParams().id ?? "");
  useDocumentTitle(id === null ? NOT_FOUND_TITLE : null);
  return (
    <Page>
      {id === null ? <NotFoundBox title={NOT_FOUND_TITLE} hint="A proposal is numbered from 1: /proposal/1." /> : <ProposalLoader key={id} id={id} />}
    </Page>
  );
}
