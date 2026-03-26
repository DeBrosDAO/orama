import { PageHeader } from "../components/page-header";
import { MetricGrid } from "../components/metric-grid";
import { DataTable } from "../components/data-table";
import { CliCommandDisplay } from "../components/cli-command";
import { ProvisioningBanner } from "../components/provisioning-banner";
import { Button } from "../../../components/ui/button";
import { Badge } from "../../../components/ui/badge";
import { StatusDot } from "../../../components/ui/status-dot";
import { DashedPanel } from "../../../components/ui/dashed-panel";
import { FUNCTIONS } from "../data/mock-data";
import { Tabs, TabsList, TabsTrigger, TabsContent } from "../../../components/ui/tabs";
import type { Column } from "../components/data-table";

type Func = (typeof FUNCTIONS)[number];

const columns: Column<Func>[] = [
  { key: "name", label: "Name", render: (f) => <span className="font-mono text-sm text-fg">{f.name}</span> },
  { key: "runtime", label: "Runtime", render: (f) => <Badge variant="default">{f.runtime}</Badge> },
  { key: "invocations", label: "Invocations", render: (f) => <span className="font-mono text-xs text-muted">{f.invocations}</span> },
  { key: "status", label: "Status", align: "right", render: (f) => <div className="flex items-center gap-2 sm:justify-end"><StatusDot status={f.status} /><span className="text-sm text-muted">active</span></div> },
];

const MOCK_SECRETS = [
  { name: "STRIPE_KEY", created: "2d ago" },
  { name: "DB_PASSWORD", created: "1w ago" },
];

const MOCK_TRIGGERS = [
  { id: "trg-1", topic: "image.uploaded", function: "resize-image", created: "3d ago" },
];

export default function FunctionsPage() {
  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title="Serverless Functions"
        actions={<Button size="sm">Deploy Function</Button>}
      />

      <ProvisioningBanner />

      <MetricGrid
        metrics={[
          { label: "Functions", value: String(FUNCTIONS.length) },
          { label: "Invocations (24h)", value: "3.2K" },
        ]}
      />

      <Tabs defaultValue="functions">
        <TabsList>
          <TabsTrigger value="functions">Functions</TabsTrigger>
          <TabsTrigger value="invoke">Invoke</TabsTrigger>
          <TabsTrigger value="secrets">Secrets</TabsTrigger>
          <TabsTrigger value="triggers">Triggers</TabsTrigger>
        </TabsList>

        <TabsContent value="functions">
          <DataTable columns={columns} data={FUNCTIONS} keyExtractor={(f) => f.name} />
        </TabsContent>

        <TabsContent value="invoke">
          <DashedPanel className="p-6">
            <div className="flex flex-col gap-4">
              <div className="flex flex-col gap-2">
                <label className="font-mono text-xs text-muted uppercase tracking-wider">Function</label>
                <select className="w-full px-3 py-2 bg-bg border border-dashed border-border rounded-sm font-mono text-sm text-fg outline-none focus:border-accent">
                  {FUNCTIONS.map((f) => <option key={f.name} value={f.name}>{f.name}</option>)}
                </select>
              </div>
              <div className="flex flex-col gap-2">
                <label className="font-mono text-xs text-muted uppercase tracking-wider">Input (JSON)</label>
                <textarea
                  placeholder='{"image_url": "https://..."}'
                  rows={4}
                  className="w-full px-3 py-2 bg-bg border border-dashed border-border rounded-sm font-mono text-sm text-fg placeholder:text-muted/50 outline-none focus:border-accent resize-none"
                />
              </div>
              <Button size="sm" className="self-start">Invoke</Button>
            </div>
          </DashedPanel>
        </TabsContent>

        <TabsContent value="secrets">
          <DashedPanel className="p-4 sm:p-6">
            <div className="flex items-center justify-between mb-4">
              <h3 className="font-mono text-xs text-muted uppercase tracking-wider">Function Secrets</h3>
              <Button size="sm">Set Secret</Button>
            </div>
            {MOCK_SECRETS.map((s, i) => (
              <div key={s.name} className={`flex items-center justify-between py-3 ${i < MOCK_SECRETS.length - 1 ? "border-b border-dashed border-border" : ""}`}>
                <span className="font-mono text-sm text-fg">{s.name}</span>
                <span className="font-mono text-xs text-muted">{s.created}</span>
              </div>
            ))}
          </DashedPanel>
        </TabsContent>

        <TabsContent value="triggers">
          <DashedPanel className="p-4 sm:p-6">
            <div className="flex items-center justify-between mb-4">
              <h3 className="font-mono text-xs text-muted uppercase tracking-wider">PubSub Triggers</h3>
              <Button size="sm">Add Trigger</Button>
            </div>
            {MOCK_TRIGGERS.map((t) => (
              <div key={t.id} className="flex items-center gap-3 py-3">
                <Badge variant="outline">{t.topic}</Badge>
                <span className="font-mono text-xs text-muted">→</span>
                <span className="font-mono text-sm text-fg">{t.function}</span>
                <span className="font-mono text-xs text-muted ml-auto">{t.created}</span>
              </div>
            ))}
          </DashedPanel>
        </TabsContent>
      </Tabs>

      <CliCommandDisplay
        commands={[
          { description: "List all functions", command: "orama function list" },
          { description: "Initialize a new function", command: "orama function init my-function" },
          { description: "Build to WASM", command: "orama function build" },
          { description: "Deploy a function", command: "orama function deploy" },
          { description: "Invoke a function", command: 'orama function invoke resize-image --data \'{"url":"..."}\''},
          { description: "View execution logs", command: "orama function logs resize-image" },
          { description: "Manage secrets", command: "orama function secrets set STRIPE_KEY sk_live_..." },
          { description: "Add PubSub trigger", command: "orama function triggers add resize-image" },
        ]}
      />
    </div>
  );
}
