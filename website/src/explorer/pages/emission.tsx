import { Link } from "react-router";
import { useQuery } from "../data/use-query";
import { useExplorerPaths } from "../network/active";
import { Page } from "../ui/page";
import { Skeleton } from "../ui/query-states";
import { useDocumentTitle } from "../ui/use-document-title";
import { ClockPanel } from "./epochs/clock-card";
import { HalvingCard } from "./emission/halving-card";
import { ScheduleCard } from "./emission/schedule-card";
import { MintedAgainstCeilingCard, SplitCard } from "./emission/split-card";

export function EmissionPage() {
  const explorerPaths = useExplorerPaths();
  useDocumentTitle("Emission");
  const { state, refetch } = useQuery((s) => s.getEpochClock(), []);
  const epoch = state.status === "ready" ? state.data.epoch : null;
  return (
    <Page>
      <div>
        <h1 className="font-display text-3xl font-semibold tracking-tight">Emission</h1>
        <p className="mt-1 max-w-prose text-sm text-muted">
          New ORAMA is created only by the chain, once per epoch, up to a ceiling fixed in its code. See every closed epoch under{" "}
          <Link to={explorerPaths.epochs} className="underline decoration-dotted underline-offset-4 hover:text-fg">
            Epochs
          </Link>
          , and the resulting supply under{" "}
          <Link to={explorerPaths.economics} className="underline decoration-dotted underline-offset-4 hover:text-fg">
            Supply
          </Link>
          .
        </p>
      </div>
      <div className="grid gap-4 lg:grid-cols-2">
        <ClockPanel state={state} refetch={refetch} showLink={false} />
        {state.status === "ready" && <HalvingCard clock={state.data} />}
        {state.status === "loading" && <Skeleton className="h-48 w-full" />}
      </div>
      {epoch !== null && <SplitCard epoch={epoch} />}
      <MintedAgainstCeilingCard />
      <ScheduleCard currentEpoch={epoch} />
    </Page>
  );
}
