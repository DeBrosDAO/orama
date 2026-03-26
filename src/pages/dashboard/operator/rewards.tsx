import { PageHeader } from "../components/page-header";
import { MetricGrid } from "../components/metric-grid";
import { DataTable } from "../components/data-table";
import { CliCommandDisplay } from "../components/cli-command";
import { Button } from "../../../components/ui/button";
import { REWARDS_HISTORY } from "../data/mock-data";
import type { Column } from "../components/data-table";

type Reward = (typeof REWARDS_HISTORY)[number];

const columns: Column<Reward>[] = [
  { key: "date", label: "Date", render: (r) => <span className="font-mono text-sm text-fg">{r.date}</span> },
  { key: "orama", label: "$ORAMA", render: (r) => <span className="font-mono text-xs text-muted">{r.orama}</span> },
  { key: "status", label: "Status", align: "right", render: (r) => <span className="font-mono text-xs text-muted">{r.status}</span> },
];

export default function RewardsPage() {
  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title="Rewards"
        actions={<Button size="sm">Claim Pending</Button>}
      />

      <MetricGrid
        metrics={[
          { label: "$ORAMA Earned", value: "***" },
          { label: "Pending", value: "***" },
          { label: "Last Payout", value: "2h ago" },
        ]}
      />

      <DataTable columns={columns} data={REWARDS_HISTORY} keyExtractor={(r) => r.date} />

      <CliCommandDisplay
        commands={[
          { description: "Rewards are distributed on-chain", command: "# Visit: https://dex.orama.network/rewards" },
          { description: "Check reward balance via SDK", command: "const rewards = await client.rewards.getBalance(wallet)" },
        ]}
      />
    </div>
  );
}
