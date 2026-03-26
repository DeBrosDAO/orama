import { PageHeader } from "../components/page-header";
import { MetricGrid } from "../components/metric-grid";
import { CliCommandDisplay } from "../components/cli-command";
import { ProvisioningBanner } from "../components/provisioning-banner";
import { Button } from "../../../components/ui/button";
import { Badge } from "../../../components/ui/badge";
import { StatusDot } from "../../../components/ui/status-dot";
import { DashedPanel } from "../../../components/ui/dashed-panel";
import { useNamespace } from "../context/namespace-context";

export default function NamespacePage() {
  const { activeNamespace } = useNamespace();

  const statusLabel = activeNamespace?.cluster_status ?? "none";
  const statusVariant = statusLabel === "ready" ? "active" as const : statusLabel === "provisioning" ? "warning" as const : "error" as const;

  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title="Namespace"
        subtitle={activeNamespace?.name}
      />

      <ProvisioningBanner />

      <MetricGrid
        metrics={[
          { label: "Status", value: statusLabel },
          { label: "RQLite Nodes", value: "3" },
          { label: "Olric Nodes", value: "3" },
          { label: "Gateway Nodes", value: "3" },
        ]}
      />

      {/* Namespace Info */}
      <DashedPanel className="p-4 sm:p-6">
        <h3 className="font-mono text-xs text-muted uppercase tracking-wider mb-4">Namespace Details</h3>
        <div className="flex flex-col gap-3">
          {[
            { label: "Name", value: activeNamespace?.name ?? "—" },
            { label: "Cluster Status", value: statusLabel, dot: true },
            { label: "Port Range", value: "10000–10004" },
            { label: "RQLite", value: "3 nodes (1 leader, 2 followers)" },
            { label: "Olric", value: "3 nodes (distributed cache)" },
            { label: "Gateway", value: "3 nodes (load balanced)" },
          ].map((item) => (
            <div key={item.label} className="flex items-center justify-between py-2 border-b border-dashed border-border last:border-0">
              <span className="font-mono text-xs text-muted uppercase tracking-wider">{item.label}</span>
              <div className="flex items-center gap-2">
                {item.dot && <StatusDot status={statusVariant} />}
                <span className="font-mono text-sm text-fg">{item.value}</span>
              </div>
            </div>
          ))}
        </div>
      </DashedPanel>

      {/* WebRTC */}
      <DashedPanel className="p-4 sm:p-6">
        <div className="flex items-center justify-between mb-4">
          <h3 className="font-mono text-xs text-muted uppercase tracking-wider">WebRTC</h3>
          <Badge variant="default">Disabled</Badge>
        </div>
        <p className="text-sm text-muted mb-4">
          Enable WebRTC for real-time communication features. Provisions 3 SFU nodes and 2 TURN relay nodes.
        </p>
        <Button size="sm">Enable WebRTC</Button>
      </DashedPanel>

      {/* Danger Zone */}
      <DashedPanel className="p-4 sm:p-6 border-red-500/20">
        <h3 className="font-mono text-xs text-red-400 uppercase tracking-wider mb-4">Danger Zone</h3>
        <div className="flex items-center justify-between">
          <div>
            <span className="font-mono text-sm text-fg block">Delete Namespace</span>
            <span className="text-xs text-muted">Permanently delete this namespace and all its resources</span>
          </div>
          <Button variant="ghost" size="sm" className="text-red-400 border-red-500/30 hover:bg-red-500/10">Delete</Button>
        </div>
      </DashedPanel>

      <CliCommandDisplay
        commands={[
          { description: "List namespaces", command: "orama namespace list" },
          { description: "Enable WebRTC", command: "orama namespace enable webrtc" },
          { description: "Disable WebRTC", command: "orama namespace disable webrtc" },
          { description: "Check WebRTC status", command: "orama namespace webrtc-status" },
          { description: "Repair namespace cluster", command: "orama namespace repair my-project" },
          { description: "Delete namespace", command: "orama namespace delete --force" },
        ]}
      />
    </div>
  );
}
