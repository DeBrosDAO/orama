import { useState } from "react";
import { PageHeader } from "../components/page-header";
import { MetricGrid } from "../components/metric-grid";
import { DataTable } from "../components/data-table";
import { CliCommandDisplay } from "../components/cli-command";
import { ProvisioningBanner } from "../components/provisioning-banner";
import { Button } from "../../../components/ui/button";
import { DashedPanel } from "../../../components/ui/dashed-panel";
import { TABLES } from "../data/mock-data";
import { Tabs, TabsList, TabsTrigger, TabsContent } from "../../../components/ui/tabs";
import type { Column } from "../components/data-table";

type Table = (typeof TABLES)[number];

const columns: Column<Table>[] = [
  { key: "name", label: "Table", render: (t) => <span className="font-mono text-sm text-fg">{t.name}</span> },
  { key: "rows", label: "Rows", render: (t) => <span className="font-mono text-xs text-muted">{t.rows.toLocaleString()}</span> },
  { key: "lastWrite", label: "Last Write", align: "right", render: (t) => <span className="font-mono text-xs text-muted">{t.lastWrite}</span> },
];

const MOCK_BACKUPS = [
  { id: "bk-1", cid: "bafybei...x9f3", size: "2.1 MB", created: "1d ago" },
  { id: "bk-2", cid: "bafybei...k4a7", size: "1.8 MB", created: "3d ago" },
];

export default function DatabasePage() {
  const [query, setQuery] = useState("");
  const [result, setResult] = useState<string | null>(null);

  const handleRunQuery = () => {
    setResult(JSON.stringify([
      { id: 1, email: "alice@example.com", created_at: "2024-01-15" },
      { id: 2, email: "bob@example.com", created_at: "2024-02-20" },
    ], null, 2));
  };

  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title="Database (RQLite)"
        actions={<Button size="sm">Create Database</Button>}
      />

      <ProvisioningBanner />

      <MetricGrid
        metrics={[
          { label: "Tables", value: String(TABLES.length) },
          { label: "Total Rows", value: "46.6K" },
          { label: "Replicas", value: "3" },
        ]}
      />

      <Tabs defaultValue="tables">
        <TabsList>
          <TabsTrigger value="tables">Tables</TabsTrigger>
          <TabsTrigger value="query">Query</TabsTrigger>
          <TabsTrigger value="backups">Backups</TabsTrigger>
        </TabsList>

        <TabsContent value="tables">
          <DataTable columns={columns} data={TABLES} keyExtractor={(t) => t.name} />
        </TabsContent>

        <TabsContent value="query">
          <DashedPanel className="p-6">
            <div className="flex flex-col gap-4">
              <div className="flex flex-col gap-2">
                <label className="font-mono text-xs text-muted uppercase tracking-wider">SQL Query</label>
                <textarea
                  value={query}
                  onChange={(e) => setQuery(e.target.value)}
                  placeholder="SELECT * FROM users LIMIT 10"
                  rows={4}
                  className="w-full px-3 py-2 bg-bg border border-dashed border-border rounded-sm font-mono text-sm text-fg placeholder:text-muted/50 outline-none focus:border-accent resize-none"
                />
              </div>
              <Button size="sm" onClick={handleRunQuery} className="self-start">Run Query</Button>
              {result && (
                <div className="mt-2">
                  <label className="font-mono text-xs text-muted uppercase tracking-wider block mb-2">Result</label>
                  <pre className="p-4 bg-bg border border-dashed border-border rounded-sm font-mono text-xs text-fg overflow-x-auto">
                    {result}
                  </pre>
                </div>
              )}
            </div>
          </DashedPanel>
        </TabsContent>

        <TabsContent value="backups">
          <DashedPanel className="p-4 sm:p-6">
            <div className="flex items-center justify-between mb-4">
              <h3 className="font-mono text-xs text-muted uppercase tracking-wider">IPFS Backups</h3>
              <Button size="sm">Create Backup</Button>
            </div>
            {MOCK_BACKUPS.map((b, i) => (
              <div key={b.id} className={`flex items-center justify-between py-3 ${i < MOCK_BACKUPS.length - 1 ? "border-b border-dashed border-border" : ""}`}>
                <div className="flex flex-col gap-0.5">
                  <span className="font-mono text-sm text-fg">{b.cid}</span>
                  <span className="font-mono text-[10px] text-muted">{b.size}</span>
                </div>
                <div className="flex items-center gap-3">
                  <span className="font-mono text-xs text-muted">{b.created}</span>
                  <Button variant="ghost" size="sm">Restore</Button>
                </div>
              </div>
            ))}
          </DashedPanel>
        </TabsContent>
      </Tabs>

      <CliCommandDisplay
        commands={[
          { description: "Create a database", command: "orama db create mydb" },
          { description: "Run a SQL query", command: 'orama db query mydb "SELECT * FROM users"' },
          { description: "List all databases", command: "orama db list" },
          { description: "Create IPFS backup", command: "orama db backup mydb" },
          { description: "List backups", command: "orama db backups mydb" },
        ]}
      />
    </div>
  );
}
