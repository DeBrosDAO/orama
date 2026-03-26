import { useState } from "react";
import { PageHeader } from "../components/page-header";
import { MetricGrid } from "../components/metric-grid";
import { DataTable } from "../components/data-table";
import { CliCommandDisplay } from "../components/cli-command";
import { Button } from "../../../components/ui/button";
import { StatusDot } from "../../../components/ui/status-dot";
import { DashedPanel } from "../../../components/ui/dashed-panel";
import { Badge } from "../../../components/ui/badge";
import { NODES } from "../data/mock-data";
import { Tabs, TabsList, TabsTrigger, TabsContent } from "../../../components/ui/tabs";
import type { Column } from "../components/data-table";

type Node = (typeof NODES)[number];

const columns: Column<Node>[] = [
  { key: "name", label: "Name", render: (n) => <span className="font-mono text-sm text-fg">{n.name}</span> },
  { key: "ip", label: "IP", render: (n) => <span className="font-mono text-xs text-muted">{n.ip}</span> },
  { key: "status", label: "Status", render: (n) => <div className="flex items-center gap-2"><StatusDot status={n.status} /><span className="text-sm text-muted">active</span></div> },
  { key: "uptime", label: "Uptime", render: (n) => <span className="font-mono text-xs text-muted">{n.uptime}</span> },
  { key: "region", label: "Region", align: "right", render: (n) => <span className="font-mono text-xs text-muted">{n.region}</span> },
];

const MOCK_SERVICES = [
  { name: "orama-node", status: "active" as const },
  { name: "orama-gateway", status: "active" as const },
  { name: "rqlite", status: "active" as const },
  { name: "olric", status: "active" as const },
  { name: "ipfs", status: "active" as const },
  { name: "coreDNS", status: "active" as const },
  { name: "caddy", status: "active" as const },
];

export default function NodesPage() {
  const [showInvite, setShowInvite] = useState(false);

  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title="Nodes"
        actions={
          <div className="flex gap-2">
            <Button size="sm" onClick={() => setShowInvite(!showInvite)}>Generate Invite</Button>
            <Button variant="ghost" size="sm">Add Node</Button>
          </div>
        }
      />

      <MetricGrid
        metrics={[
          { label: "Active", value: String(NODES.length) },
          { label: "Avg Uptime", value: "99.97%" },
          { label: "Regions", value: "3" },
        ]}
      />

      {showInvite && (
        <DashedPanel withCorners className="p-6">
          <h3 className="font-mono text-xs text-muted uppercase tracking-wider mb-3">Invite Token</h3>
          <p className="text-sm text-muted mb-4">
            Single-use token for a new node to join the cluster. Expires in 1 hour.
          </p>
          <div className="p-3 bg-bg border border-dashed border-border rounded-sm font-mono text-xs text-fg break-all mb-3">
            sudo orama node install --vps-ip &lt;NEW_IP&gt; --join https://gateway.orama.network --token eyJhbGciOi...mock_token
          </div>
          <div className="flex items-center gap-3">
            <Button size="sm">Copy Command</Button>
            <span className="font-mono text-[10px] text-muted">Expires: 1h from now</span>
          </div>
        </DashedPanel>
      )}

      <Tabs defaultValue="nodes">
        <TabsList>
          <TabsTrigger value="nodes">All Nodes</TabsTrigger>
          <TabsTrigger value="services">Services</TabsTrigger>
        </TabsList>

        <TabsContent value="nodes">
          <DataTable columns={columns} data={NODES} keyExtractor={(n) => n.name} />
        </TabsContent>

        <TabsContent value="services">
          <DashedPanel className="p-4 sm:p-6">
            <h3 className="font-mono text-xs text-muted uppercase tracking-wider mb-4">Service Status (node-eu-1)</h3>
            {MOCK_SERVICES.map((s, i) => (
              <div key={s.name} className={`flex items-center justify-between py-2.5 ${i < MOCK_SERVICES.length - 1 ? "border-b border-dashed border-border" : ""}`}>
                <span className="font-mono text-sm text-fg">{s.name}</span>
                <div className="flex items-center gap-2">
                  <StatusDot status={s.status} />
                  <Badge variant="status">active</Badge>
                </div>
              </div>
            ))}
          </DashedPanel>
        </TabsContent>
      </Tabs>

      <CliCommandDisplay
        commands={[
          { description: "Show node service status", command: "orama node status" },
          { description: "Run diagnostics", command: "sudo orama node doctor" },
          { description: "Generate invite token", command: "orama node invite --expiry 24h" },
          { description: "View node logs", command: "sudo orama node logs gateway" },
          { description: "Node health report (JSON)", command: "sudo orama node report --json" },
        ]}
      />
    </div>
  );
}
