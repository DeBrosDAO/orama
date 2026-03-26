import { useState } from "react";
import { PageHeader } from "../components/page-header";
import { MetricGrid } from "../components/metric-grid";
import { DataTable } from "../components/data-table";
import { CliCommandDisplay } from "../components/cli-command";
import { ProvisioningBanner } from "../components/provisioning-banner";
import { Button } from "../../../components/ui/button";
import { Badge } from "../../../components/ui/badge";
import { StatusDot } from "../../../components/ui/status-dot";
import { DashedPanel } from "../../../components/ui/dashed-panel";
import { DEPLOYMENTS } from "../data/mock-data";
import { Tabs, TabsList, TabsTrigger, TabsContent } from "../../../components/ui/tabs";
import type { Column } from "../components/data-table";

type Deployment = (typeof DEPLOYMENTS)[number];

const columns: Column<Deployment>[] = [
  { key: "name", label: "Name", render: (d) => <span className="font-mono text-sm text-fg">{d.name}</span> },
  { key: "status", label: "Status", render: (d) => <div className="flex items-center gap-2"><StatusDot status={d.status} /><span className="text-sm text-muted">active</span></div> },
  { key: "domain", label: "Domain", render: (d) => <span className="font-mono text-xs text-muted">{d.domain}</span> },
  { key: "requests", label: "Requests", render: (d) => <span className="font-mono text-xs text-muted">{d.requests}</span> },
  { key: "lastDeploy", label: "Last Deploy", align: "right", render: (d) => <span className="font-mono text-xs text-muted">{d.lastDeploy}</span> },
];

const DEPLOY_TYPES = [
  { id: "static", label: "Static Site", description: "React, Vue, Angular, or plain HTML" },
  { id: "nextjs", label: "Next.js", description: "SSR with standalone output" },
  { id: "go", label: "Go Backend", description: "Compiled binary with /health endpoint" },
  { id: "nodejs", label: "Node.js", description: "Express, Fastify, or any Node server" },
];

export default function DeploymentsPage() {
  const [showWizard, setShowWizard] = useState(false);
  const [selectedType, setSelectedType] = useState<string | null>(null);

  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title="Deployments"
        actions={<Button size="sm" onClick={() => setShowWizard(!showWizard)}>New Deployment</Button>}
      />

      <ProvisioningBanner />

      {showWizard && (
        <DashedPanel withCorners className="p-6">
          <h3 className="font-mono text-xs text-muted uppercase tracking-wider mb-4">Choose Deployment Type</h3>
          <div className="grid grid-cols-1 sm:grid-cols-2 gap-3 mb-4">
            {DEPLOY_TYPES.map((t) => (
              <button
                key={t.id}
                type="button"
                onClick={() => setSelectedType(t.id)}
                className={`p-4 border border-dashed rounded-sm text-left transition-colors ${
                  selectedType === t.id ? "border-accent bg-accent/5" : "border-border hover:border-border/80"
                }`}
              >
                <span className="font-mono text-sm text-fg block">{t.label}</span>
                <span className="text-xs text-muted">{t.description}</span>
              </button>
            ))}
          </div>
          {selectedType && (
            <div className="flex flex-col gap-3">
              <div className="flex flex-col gap-2">
                <label className="font-mono text-xs text-muted uppercase tracking-wider">App Name</label>
                <input type="text" placeholder="my-app" className="w-full px-3 py-2 bg-bg border border-dashed border-border rounded-sm font-mono text-sm text-fg placeholder:text-muted/50 outline-none focus:border-accent" />
              </div>
              <div className="flex flex-col gap-2">
                <label className="font-mono text-xs text-muted uppercase tracking-wider">Domain (optional)</label>
                <input type="text" placeholder="myapp.com" className="w-full px-3 py-2 bg-bg border border-dashed border-border rounded-sm font-mono text-sm text-fg placeholder:text-muted/50 outline-none focus:border-accent" />
              </div>
              <div className="flex gap-2">
                <Button size="sm">Deploy</Button>
                <Button variant="ghost" size="sm" onClick={() => { setShowWizard(false); setSelectedType(null); }}>Cancel</Button>
              </div>
            </div>
          )}
        </DashedPanel>
      )}

      <MetricGrid
        metrics={[
          { label: "Total", value: String(DEPLOYMENTS.length) },
          { label: "Active", value: String(DEPLOYMENTS.filter((d) => d.status === "active").length) },
          { label: "Requests (24h)", value: "13.4K" },
        ]}
      />

      <Tabs defaultValue="list">
        <TabsList>
          <TabsTrigger value="list">All Deployments</TabsTrigger>
          <TabsTrigger value="activity">Activity</TabsTrigger>
        </TabsList>
        <TabsContent value="list">
          <DataTable columns={columns} data={DEPLOYMENTS} keyExtractor={(d) => d.name} />
        </TabsContent>
        <TabsContent value="activity">
          <DashedPanel className="p-6">
            <div className="space-y-3">
              {DEPLOYMENTS.map((d) => (
                <div key={d.name} className="flex items-center gap-3">
                  <StatusDot status={d.status} />
                  <span className="font-mono text-sm text-fg">{d.name}</span>
                  <Badge variant="default">{d.type}</Badge>
                  <span className="font-mono text-xs text-muted ml-auto">{d.lastDeploy}</span>
                </div>
              ))}
            </div>
          </DashedPanel>
        </TabsContent>
      </Tabs>

      <CliCommandDisplay
        commands={[
          { description: "List all deployments", command: "orama app list" },
          { description: "Deploy a static site", command: "orama deploy static ./dist --name my-app" },
          { description: "Deploy a Next.js app", command: "orama deploy nextjs . --name my-app --ssr" },
          { description: "Deploy a Go backend", command: "orama deploy go ./bin/server --name api-service" },
          { description: "Deploy a Node.js backend", command: "orama deploy nodejs . --name api-service" },
          { description: "View deployment details", command: "orama app get my-app" },
          { description: "Rollback to previous version", command: "orama app rollback my-app --version 2" },
          { description: "View deployment logs", command: "orama app logs my-app --follow" },
          { description: "Delete a deployment", command: "orama app delete my-app" },
        ]}
      />
    </div>
  );
}
