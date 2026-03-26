import { PageHeader } from "../components/page-header";
import { MetricGrid } from "../components/metric-grid";
import { CliCommandDisplay } from "../components/cli-command";
import { DashedPanel } from "../../../components/ui/dashed-panel";
import { StatusDot } from "../../../components/ui/status-dot";
import { Badge } from "../../../components/ui/badge";
import { NODES } from "../data/mock-data";
import { cn } from "../../../lib/utils";

const SERVICES = ["orama-node", "gateway", "rqlite", "olric", "ipfs", "coreDNS", "caddy"];

const SERVICE_MATRIX = NODES.map((node) => ({
  node: node.name,
  services: SERVICES.map((s) => ({ name: s, status: "active" as const })),
}));

const NAMESPACE_USAGE = [
  { name: "my-project", deployments: 3, functions: 2, databases: 3, status: "ready" as const },
  { name: "staging-env", deployments: 1, functions: 0, databases: 1, status: "ready" as const },
];

export default function MonitoringPage() {
  return (
    <div className="flex flex-col gap-6">
      <PageHeader title="Monitoring" />

      <MetricGrid
        metrics={[
          { label: "Total Services", value: String(SERVICES.length * NODES.length) },
          { label: "Healthy", value: String(SERVICES.length * NODES.length) },
          { label: "Warnings", value: "0" },
          { label: "Errors", value: "0" },
        ]}
      />

      {/* Service Matrix */}
      <DashedPanel className="p-4 sm:p-6 overflow-x-auto">
        <h3 className="font-mono text-xs text-muted uppercase tracking-wider mb-4">Service Status Matrix</h3>
        <div className="min-w-[600px]">
          {/* Header */}
          <div className="grid gap-2 pb-3 border-b border-dashed border-border" style={{ gridTemplateColumns: `120px repeat(${SERVICES.length}, 1fr)` }}>
            <span className="font-mono text-[10px] text-muted uppercase tracking-wider">Node</span>
            {SERVICES.map((s) => (
              <span key={s} className="font-mono text-[10px] text-muted uppercase tracking-wider text-center">{s}</span>
            ))}
          </div>
          {/* Rows */}
          {SERVICE_MATRIX.map((row, i) => (
            <div
              key={row.node}
              className={cn(
                "grid gap-2 py-2.5",
                i < SERVICE_MATRIX.length - 1 && "border-b border-dashed border-border",
              )}
              style={{ gridTemplateColumns: `120px repeat(${SERVICES.length}, 1fr)` }}
            >
              <span className="font-mono text-xs text-fg">{row.node}</span>
              {row.services.map((s) => (
                <div key={s.name} className="flex justify-center">
                  <StatusDot status={s.status} />
                </div>
              ))}
            </div>
          ))}
        </div>
      </DashedPanel>

      {/* Namespace Usage */}
      <DashedPanel className="p-4 sm:p-6">
        <h3 className="font-mono text-xs text-muted uppercase tracking-wider mb-4">Namespace Usage</h3>
        {NAMESPACE_USAGE.map((ns, i) => (
          <div key={ns.name} className={`flex items-center justify-between py-3 ${i < NAMESPACE_USAGE.length - 1 ? "border-b border-dashed border-border" : ""}`}>
            <div className="flex items-center gap-3">
              <StatusDot status={ns.status === "ready" ? "active" : "warning"} />
              <span className="font-mono text-sm text-fg">{ns.name}</span>
            </div>
            <div className="flex items-center gap-2">
              <Badge variant="default">{ns.deployments} deploys</Badge>
              <Badge variant="default">{ns.functions} funcs</Badge>
              <Badge variant="default">{ns.databases} dbs</Badge>
            </div>
          </div>
        ))}
      </DashedPanel>

      <CliCommandDisplay
        commands={[
          { description: "Service status across cluster", command: "orama monitor service --env mainnet" },
          { description: "Namespace usage", command: "orama monitor namespaces --env mainnet" },
          { description: "Active alerts", command: "orama monitor alerts --env mainnet" },
          { description: "Per-node health", command: "orama monitor node --env mainnet" },
        ]}
      />
    </div>
  );
}
