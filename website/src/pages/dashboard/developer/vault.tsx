import { useState } from "react";
import { PageHeader } from "../components/page-header";
import { MetricGrid } from "../components/metric-grid";
import { DataTable } from "../components/data-table";
import { CliCommandDisplay } from "../components/cli-command";
import { ProvisioningBanner } from "../components/provisioning-banner";
import { Button } from "../../../components/ui/button";
import { DashedPanel } from "../../../components/ui/dashed-panel";
import { SECRETS } from "../data/mock-data";
import type { Column } from "../components/data-table";

type Secret = (typeof SECRETS)[number];

const columns: Column<Secret>[] = [
  { key: "name", label: "Name", render: (s) => <span className="font-mono text-sm text-fg">{s.name}</span> },
  { key: "created", label: "Created", render: (s) => <span className="font-mono text-xs text-muted">{s.created}</span> },
  { key: "guardians", label: "Guardians", align: "right", render: (s) => <span className="font-mono text-xs text-muted">{s.guardians}</span> },
];

export default function VaultPage() {
  const [showForm, setShowForm] = useState(false);

  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title="Vault (Secrets)"
        actions={<Button size="sm" onClick={() => setShowForm(!showForm)}>Store Secret</Button>}
      />

      <ProvisioningBanner />

      <MetricGrid
        metrics={[
          { label: "Secrets", value: String(SECRETS.length) },
          { label: "Guardians", value: "5" },
        ]}
      />

      {showForm && (
        <DashedPanel className="p-6">
          <h3 className="font-mono text-xs text-muted uppercase tracking-wider mb-4">Store New Secret</h3>
          <div className="flex flex-col gap-3">
            <div className="flex flex-col gap-2">
              <label className="font-mono text-xs text-muted uppercase tracking-wider">Name</label>
              <input type="text" placeholder="my-secret-key" className="w-full px-3 py-2 bg-bg border border-dashed border-border rounded-sm font-mono text-sm text-fg placeholder:text-muted/50 outline-none focus:border-accent" />
            </div>
            <div className="flex flex-col gap-2">
              <label className="font-mono text-xs text-muted uppercase tracking-wider">Value</label>
              <input type="password" placeholder="secret value" className="w-full px-3 py-2 bg-bg border border-dashed border-border rounded-sm font-mono text-sm text-fg placeholder:text-muted/50 outline-none focus:border-accent" />
            </div>
            <div className="flex gap-2">
              <Button size="sm">Store</Button>
              <Button variant="ghost" size="sm" onClick={() => setShowForm(false)}>Cancel</Button>
            </div>
          </div>
        </DashedPanel>
      )}

      <DataTable columns={columns} data={SECRETS} keyExtractor={(s) => s.name} />

      <CliCommandDisplay
        commands={[
          { description: "List function secrets", command: "orama function secrets list" },
          { description: "Set a secret", command: "orama function secrets set STRIPE_KEY sk_live_..." },
          { description: "Delete a secret", command: "orama function secrets delete STRIPE_KEY" },
        ]}
      />
    </div>
  );
}
