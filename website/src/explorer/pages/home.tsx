import { Link } from "react-router";
import { useLiveQuery } from "../data/use-query";
import { useExplorerPaths } from "../network/active";
import { Card } from "../ui/card";
import { Page } from "../ui/page";
import { ActivityFeed } from "./home/activity-feed";
import { BlocksPanel } from "./home/blocks-panel";
import { Hero } from "./home/hero";
import { HealthPill } from "./home/health-pill";
import { IndexLagLine } from "../ui/index-lag";
import { StatTilesSection } from "./home/stats";
import { cn } from "../../lib/utils";

const VALIDATORS_TEXT =
  "Validators sign every block. See who they are, how much voting power each holds, and how that power shifts to the community over time.";

function ValidatorsCard() {
  const paths = useExplorerPaths();
  return (
    <Card title="Who runs the chain">
      <p className="text-sm">{VALIDATORS_TEXT}</p>
      <Link
        to={paths.validators}
        className={cn("mt-3 inline-block rounded-lg border border-border bg-surface-2 px-3 py-1.5 text-sm hover:border-fg/30")}
      >
        See the validators →
      </Link>
    </Card>
  );
}

export function HomePage() {
  const { state, refetch } = useLiveQuery((s) => s.getNetwork(), []);
  const blockTimeSeconds = state.status === "ready" ? state.data.blockTimeSeconds : null;
  return (
    <Page className="space-y-6">
      <section>
        <Hero />
        <HealthPill state={state} />
        {state.status === "ready" && <IndexLagLine index={state.data.index} className="mt-2 text-center" />}
      </section>
      <StatTilesSection state={state} onRetry={refetch} />
      <div className="grid gap-3.5 lg:grid-cols-[1.55fr_1fr]">
        <ActivityFeed />
        <div className="grid content-start gap-3.5">
          <BlocksPanel blockTimeSeconds={blockTimeSeconds} />
          <ValidatorsCard />
        </div>
      </div>
    </Page>
  );
}
