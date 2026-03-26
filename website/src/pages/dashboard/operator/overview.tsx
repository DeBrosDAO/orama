import { PageHeader } from "../components/page-header";
import { MetricGrid } from "../components/metric-grid";
import { CliCommandDisplay } from "../components/cli-command";
import { DashedPanel } from "../../../components/ui/dashed-panel";
import { StatusDot } from "../../../components/ui/status-dot";
import { Badge } from "../../../components/ui/badge";
import { cn } from "../../../lib/utils";

const CLUSTER_INFO = [
  { label: "Leader", value: "node-eu-1 (10.0.0.1)" },
  { label: "Raft State", value: "Healthy" },
  { label: "Quorum", value: "3/3 voters" },
  { label: "WireGuard Mesh", value: "3/3 connected" },
];

const ALERTS = [
  { level: "active" as const, message: "All systems operational", time: "now" },
];

export default function OpsOverview() {
  return (
    <div className="flex flex-col gap-6">
      <PageHeader title="Cluster Overview" />

      <MetricGrid
        metrics={[
          { label: "Nodes", value: "3" },
          { label: "Uptime", value: "99.97%" },
          { label: "Namespaces", value: "2" },
          { label: "Active Alerts", value: "0" },
        ]}
      />

      {/* Cluster Status */}
      <DashedPanel className="p-4 sm:p-6">
        <h3 className="font-mono text-xs text-muted uppercase tracking-wider mb-4">Cluster Status</h3>
        <div className="flex flex-col gap-3">
          {CLUSTER_INFO.map((item) => (
            <div key={item.label} className="flex items-center justify-between py-2 border-b border-dashed border-border last:border-0">
              <span className="font-mono text-xs text-muted uppercase tracking-wider">{item.label}</span>
              <span className="font-mono text-sm text-fg">{item.value}</span>
            </div>
          ))}
        </div>
      </DashedPanel>

      {/* Alerts */}
      <DashedPanel className="p-4 sm:p-6">
        <div className="flex items-center justify-between mb-4">
          <h3 className="font-mono text-xs text-muted uppercase tracking-wider">Alerts</h3>
          <Badge variant="status">All Clear</Badge>
        </div>
        {ALERTS.map((a, i) => (
          <div
            key={a.message}
            className={cn(
              "flex items-center gap-3 py-2.5",
              i < ALERTS.length - 1 && "border-b border-dashed border-border",
            )}
          >
            <StatusDot status={a.level} />
            <span className="font-mono text-sm text-fg">{a.message}</span>
            <span className="font-mono text-xs text-muted ml-auto">{a.time}</span>
          </div>
        ))}
      </DashedPanel>

      <CliCommandDisplay
        commands={[
          { description: "Cluster overview", command: "orama monitor cluster --env mainnet" },
          { description: "Active alerts", command: "orama monitor alerts --env mainnet" },
          { description: "Full cluster report", command: "orama monitor report --env mainnet" },
          { description: "Interactive TUI", command: "orama monitor live --env mainnet" },
        ]}
      />
    </div>
  );
}
