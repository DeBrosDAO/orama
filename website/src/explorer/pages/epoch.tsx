import { Link, useParams } from "react-router";
import { useQuery } from "../data/use-query";
import type { EpochRow } from "../model/epochs";
import { useExplorerPaths } from "../network/active";
import { formatInt } from "../model/units";
import { Badge } from "../ui/badge";
import { IndexQuery } from "../ui/index-query";
import { Page } from "../ui/page";
import { NotFoundBox, Skeleton } from "../ui/query-states";
import { useDocumentTitle } from "../ui/use-document-title";
import { EpochFacts, FeesCard, MintedCard, PaidCard, PaidSplitCard } from "./epochs/epoch-summary";
import { UnavailableBadge, UnavailableCard } from "./epochs/unavailable";
import { EpochRewardsCard } from "./rewards/epoch-rewards";
import { EpochValidators } from "./epochs/epoch-validators";
import { parseEpochParam } from "./epochs/logic";
import { MissingEpochBox } from "./epochs/missing-epoch";

const NAV_LINK = "rounded-lg border border-border bg-surface-2 px-3 py-1.5 text-sm hover:border-fg/30";

function Loading() {
  return (
    <div className="space-y-4">
      <Skeleton className="h-24 w-full" />
      <Skeleton className="h-56 w-full" />
      <Skeleton className="h-40 w-full" />
    </div>
  );
}

function EpochNav({ epoch }: { epoch: number }) {
  const explorerPaths = useExplorerPaths();
  return (
    <nav aria-label="Neighbouring epochs" className="flex items-center gap-2">
      {epoch > 1 && (
        <Link to={explorerPaths.epoch(epoch - 1)} className={NAV_LINK} rel="prev">
          Epoch {formatInt(epoch - 1)}
        </Link>
      )}
      <Link to={explorerPaths.epoch(epoch + 1)} className={NAV_LINK} rel="next">
        Epoch {formatInt(epoch + 1)}
      </Link>
    </nav>
  );
}

export function EpochBody({ row }: { row: EpochRow }) {
  if (row.unavailable) {
    return (
      <>
        <EpochFacts row={row} />
        <UnavailableCard row={row} />
        <PaidSplitCard row={row} />
        <EpochRewardsCard epoch={row.epoch} />
        <EpochValidators epoch={row.epoch} />
      </>
    );
  }
  return (
    <>
      <EpochFacts row={row} />
      <MintedCard row={row} />
      <PaidCard row={row} />
      <EpochRewardsCard epoch={row.epoch} />
      <FeesCard row={row} />
      <EpochValidators epoch={row.epoch} />
    </>
  );
}

function EpochDetail({ epoch }: { epoch: number }) {
  const explorerPaths = useExplorerPaths();
  useDocumentTitle(`Epoch ${epoch}`);
  const { state, refetch } = useQuery((s) => s.getEpoch(epoch), [epoch]);
  return (
    <Page>
      <div className="flex flex-wrap items-end justify-between gap-3">
        <div>
          <p className="text-sm text-muted">
            <Link to={explorerPaths.epochs} className="hover:text-fg">
              Epochs
            </Link>
          </p>
          <h1 className="font-display text-3xl font-semibold tracking-tight">
            Epoch {formatInt(epoch)}
            {state.status === "ready" && state.data?.partial && (
              <Badge tone="signal" className="ml-3 align-middle">
                partial
              </Badge>
            )}
          </h1>
          {state.status === "ready" && state.data?.unavailable && <UnavailableBadge row={state.data} className="ml-3 align-middle" />}
          {state.status === "ready" && state.data?.partial && !state.data.unavailable && (
            <p className="mt-1 max-w-prose text-sm text-muted">
              The index started inside this epoch, so its minted, burned and fee figures cover only the blocks the index saw. Its start is the chain's.
            </p>
          )}
        </div>
        <EpochNav epoch={epoch} />
      </div>
      <IndexQuery state={state} onRetry={refetch} loading={<Loading />}>
        {(row) => (row === null ? <MissingEpochBox epoch={epoch} /> : <EpochBody row={row} />)}
      </IndexQuery>
    </Page>
  );
}

export function EpochPage() {
  const { epoch: raw } = useParams();
  const epoch = parseEpochParam(raw);
  if (epoch === null) {
    return (
      <Page>
        <NotFoundBox title="That is not an epoch number" hint="An epoch is a whole number from 1. Open the list of epochs to pick one." />
      </Page>
    );
  }
  return <EpochDetail epoch={epoch} />;
}
