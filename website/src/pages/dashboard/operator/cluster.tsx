import { PageHeader } from "../components/page-header";
import { MetricGrid } from "../components/metric-grid";
import { CliCommandDisplay } from "../components/cli-command";
import { DashedPanel } from "../../../components/ui/dashed-panel";
import { StatusDot } from "../../../components/ui/status-dot";
import { Badge } from "../../../components/ui/badge";
import { Button } from "../../../components/ui/button";
import { Tabs, TabsList, TabsTrigger, TabsContent } from "../../../components/ui/tabs";

const RAFT_NODES = [
  { name: "node-eu-1", ip: "10.0.0.1", role: "Leader", commitIndex: 15847 },
  { name: "node-us-1", ip: "10.0.0.2", role: "Follower", commitIndex: 15847 },
  { name: "node-ap-1", ip: "10.0.0.3", role: "Follower", commitIndex: 15846 },
];

const WG_MESH = [
  { from: "node-eu-1", to: "node-us-1", status: "active" as const, latency: "45ms" },
  { from: "node-eu-1", to: "node-ap-1", status: "active" as const, latency: "120ms" },
  { from: "node-us-1", to: "node-ap-1", status: "active" as const, latency: "95ms" },
];

const DNS_RECORDS = [
  { zone: "orama.network", type: "A", count: 12 },
  { zone: "orama.network", type: "CNAME", count: 8 },
  { zone: "orama.network", type: "TXT", count: 3 },
];

export default function ClusterPage() {
  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title="Cluster Diagnostics"
        actions={<Button size="sm">Trigger Backup</Button>}
      />

      <MetricGrid
        metrics={[
          { label: "Raft State", value: "Healthy" },
          { label: "Leader", value: "node-eu-1" },
          { label: "Voters", value: "3/3" },
          { label: "Commit Index", value: "15,847" },
        ]}
      />

      <Tabs defaultValue="raft">
        <TabsList>
          <TabsTrigger value="raft">Raft</TabsTrigger>
          <TabsTrigger value="wireguard">WireGuard</TabsTrigger>
          <TabsTrigger value="dns">DNS</TabsTrigger>
        </TabsList>

        <TabsContent value="raft">
          <DashedPanel className="p-4 sm:p-6">
            <h3 className="font-mono text-xs text-muted uppercase tracking-wider mb-4">RQLite Raft Cluster</h3>
            {RAFT_NODES.map((n, i) => (
              <div key={n.name} className={`flex items-center justify-between py-3 ${i < RAFT_NODES.length - 1 ? "border-b border-dashed border-border" : ""}`}>
                <div className="flex items-center gap-3">
                  <StatusDot status="active" />
                  <span className="font-mono text-sm text-fg">{n.name}</span>
                  <span className="font-mono text-xs text-muted">{n.ip}</span>
                </div>
                <div className="flex items-center gap-3">
                  <Badge variant={n.role === "Leader" ? "accent" : "default"}>{n.role}</Badge>
                  <span className="font-mono text-xs text-muted">idx: {n.commitIndex}</span>
                </div>
              </div>
            ))}
          </DashedPanel>
        </TabsContent>

        <TabsContent value="wireguard">
          <DashedPanel className="p-4 sm:p-6">
            <h3 className="font-mono text-xs text-muted uppercase tracking-wider mb-4">WireGuard Mesh</h3>
            {WG_MESH.map((link, i) => (
              <div key={`${link.from}-${link.to}`} className={`flex items-center justify-between py-3 ${i < WG_MESH.length - 1 ? "border-b border-dashed border-border" : ""}`}>
                <div className="flex items-center gap-2">
                  <StatusDot status={link.status} />
                  <span className="font-mono text-sm text-fg">{link.from}</span>
                  <span className="font-mono text-xs text-muted">↔</span>
                  <span className="font-mono text-sm text-fg">{link.to}</span>
                </div>
                <span className="font-mono text-xs text-muted">{link.latency}</span>
              </div>
            ))}
          </DashedPanel>
        </TabsContent>

        <TabsContent value="dns">
          <DashedPanel className="p-4 sm:p-6">
            <h3 className="font-mono text-xs text-muted uppercase tracking-wider mb-4">CoreDNS Records</h3>
            {DNS_RECORDS.map((r, i) => (
              <div key={r.type} className={`flex items-center justify-between py-3 ${i < DNS_RECORDS.length - 1 ? "border-b border-dashed border-border" : ""}`}>
                <div className="flex items-center gap-3">
                  <span className="font-mono text-sm text-fg">{r.zone}</span>
                  <Badge variant="default">{r.type}</Badge>
                </div>
                <span className="font-mono text-xs text-muted">{r.count} records</span>
              </div>
            ))}
          </DashedPanel>
        </TabsContent>
      </Tabs>

      <CliCommandDisplay
        commands={[
          { description: "Cluster status", command: "orama cluster status" },
          { description: "Raft state", command: "orama cluster raft-status" },
          { description: "List voters", command: "orama cluster voters" },
          { description: "WireGuard mesh", command: "orama monitor mesh --env mainnet" },
          { description: "DNS health", command: "orama monitor dns --env mainnet" },
          { description: "Trigger RQLite backup", command: "orama cluster backup" },
        ]}
      />
    </div>
  );
}
