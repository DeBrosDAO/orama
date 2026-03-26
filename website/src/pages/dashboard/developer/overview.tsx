import { Link } from "react-router";
import { Rocket, Box, Database, HardDrive, Zap, Globe, Shield } from "lucide-react";
import { PageHeader } from "../components/page-header";
import { MetricGrid } from "../components/metric-grid";
import { ProvisioningBanner } from "../components/provisioning-banner";
import { CliCommandDisplay } from "../components/cli-command";
import { DashedPanel } from "../../../components/ui/dashed-panel";
import { StatusDot } from "../../../components/ui/status-dot";
import { useNamespace } from "../context/namespace-context";
import { cn } from "../../../lib/utils";

const QUICK_LINKS = [
  { label: "Deployments", icon: Rocket, path: "/dashboard/dev/deployments", count: "3" },
  { label: "Functions", icon: Box, path: "/dashboard/dev/functions", count: "2" },
  { label: "Database", icon: Database, path: "/dashboard/dev/database", count: "3 tables" },
  { label: "Storage", icon: HardDrive, path: "/dashboard/dev/storage", count: "3 files" },
  { label: "Cache", icon: Zap, path: "/dashboard/dev/cache", count: "94.2% hit" },
  { label: "DNS", icon: Globe, path: "/dashboard/dev/dns", count: "2 domains" },
  { label: "Vault", icon: Shield, path: "/dashboard/dev/vault", count: "3 secrets" },
];

const RECENT_ACTIVITY = [
  { action: "Deployed", target: "my-app v1.4.2", time: "2h ago", status: "active" as const },
  { action: "Function invoked", target: "resize-image", time: "15m ago", status: "active" as const },
  { action: "Database backup", target: "users → IPFS", time: "1d ago", status: "active" as const },
  { action: "Secret updated", target: "jwt-secret", time: "1w ago", status: "active" as const },
];

export default function DevOverview() {
  const { activeNamespace } = useNamespace();

  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title="Overview"
        subtitle={activeNamespace ? `Namespace: ${activeNamespace.name}` : undefined}
      />

      <ProvisioningBanner />

      <MetricGrid
        metrics={[
          { label: "Deployments", value: "3" },
          { label: "Functions", value: "2" },
          { label: "Databases", value: "3" },
          { label: "Requests (24h)", value: "16.6K" },
        ]}
      />

      {/* Quick Links */}
      <div className="grid grid-cols-2 sm:grid-cols-3 lg:grid-cols-4 gap-3">
        {QUICK_LINKS.map((link) => {
          const Icon = link.icon;
          return (
            <Link key={link.label} to={link.path}>
              <DashedPanel className="p-4 hover:border-accent/30 transition-colors">
                <div className="flex items-center gap-3">
                  <Icon size={16} className="text-muted" />
                  <div className="min-w-0">
                    <span className="font-mono text-xs text-fg block">{link.label}</span>
                    <span className="font-mono text-[10px] text-muted">{link.count}</span>
                  </div>
                </div>
              </DashedPanel>
            </Link>
          );
        })}
      </div>

      {/* Recent Activity */}
      <DashedPanel className="p-4 sm:p-6">
        <h3 className="font-mono text-xs text-muted uppercase tracking-wider mb-4">Recent Activity</h3>
        {RECENT_ACTIVITY.map((a, i) => (
          <div
            key={a.target}
            className={cn(
              "flex items-center gap-3 py-2.5",
              i < RECENT_ACTIVITY.length - 1 && "border-b border-dashed border-border",
            )}
          >
            <StatusDot status={a.status} />
            <span className="font-mono text-sm text-fg">{a.action}</span>
            <span className="font-mono text-xs text-muted">{a.target}</span>
            <span className="font-mono text-xs text-muted ml-auto">{a.time}</span>
          </div>
        ))}
      </DashedPanel>

      <CliCommandDisplay
        commands={[
          { description: "List all deployments", command: "orama app list" },
          { description: "List all databases", command: "orama db list" },
          { description: "List all functions", command: "orama function list" },
        ]}
      />
    </div>
  );
}
