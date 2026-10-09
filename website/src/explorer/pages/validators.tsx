import { useQuery } from "../data/use-query";
import { formatCompact } from "../model/units";
import type { ValidatorSet } from "../model/types";
import { Help } from "../ui/help";
import { Page } from "../ui/page";
import { Query, Skeleton } from "../ui/query-states";
import { Stat } from "../ui/stat";
import { useDocumentTitle } from "../ui/use-document-title";
import { HandoverCard } from "./validators/handover-card";
import { summarize } from "./validators/summary";
import { ValidatorTable } from "./validators/validator-table";

const NAKAMOTO_TIP =
  "The smallest number of validators that together could halt or control the network. Higher means more decentralised.";

function Loading() {
  return (
    <div className="space-y-4">
      <Skeleton className="h-40 w-full" />
      <Skeleton className="h-28 w-full" />
      <Skeleton className="h-80 w-full" />
    </div>
  );
}

function ValidatorsBody({ set }: { set: ValidatorSet }) {
  const summary = summarize(set);
  return (
    <>
      <HandoverCard summary={summary} lambda={set.lambda} />
      <div className="grid gap-4 sm:grid-cols-3">
        <Stat
          label="Validators signing"
          value={`${summary.signing} / ${summary.total}`}
          detail={set.jailed > 0 ? `${set.jailed} jailed now` : "None jailed now"}
        />
        <Stat
          label={
            <>
              Nakamoto coefficient
              <Help tip={NAKAMOTO_TIP} />
            </>
          }
          value={set.nakamoto}
          detail={`${set.nakamoto} validator${set.nakamoto === 1 ? "" : "s"} together reach a third of the power`}
        />
        <Stat label="Total staked" value={<>{formatCompact(set.totalStaked)} <span className="text-sm font-normal text-muted">ORAMA</span></>} detail="bonded to validators" />
      </div>
      <ValidatorTable validators={set.validators} />
    </>
  );
}

export function ValidatorsPage() {
  useDocumentTitle("Validators");
  const { state, refetch } = useQuery((s) => s.getValidators(), []);
  return (
    <Page>
      <div>
        <h1 className="font-display text-3xl font-semibold tracking-tight">Who runs the chain</h1>
        <p className="mt-1 text-sm text-muted">
          {state.status === "ready" ? `${state.data.validators.length} validators` : "Validators"} produce blocks. Here is who they are and how power moves.
        </p>
      </div>
      <Query state={state} onRetry={refetch} loading={<Loading />}>
        {(set) => <ValidatorsBody set={set} />}
      </Query>
    </Page>
  );
}
