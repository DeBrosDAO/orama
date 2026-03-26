import { useState } from "react";
import { PageHeader } from "../components/page-header";
import { MetricGrid } from "../components/metric-grid";
import { DataTable } from "../components/data-table";
import { CliCommandDisplay } from "../components/cli-command";
import { ProvisioningBanner } from "../components/provisioning-banner";
import { Button } from "../../../components/ui/button";
import { DashedPanel } from "../../../components/ui/dashed-panel";
import { CACHE_KEYS } from "../data/mock-data";
import type { Column } from "../components/data-table";

type CacheKey = (typeof CACHE_KEYS)[number];

const columns: Column<CacheKey>[] = [
  { key: "key", label: "Key", render: (k) => <span className="font-mono text-sm text-fg">{k.key}</span> },
  { key: "ttl", label: "TTL", render: (k) => <span className="font-mono text-xs text-muted">{k.ttl}</span> },
  { key: "size", label: "Size", align: "right", render: (k) => <span className="font-mono text-xs text-muted">{k.size}</span> },
];

export default function CachePage() {
  const [showForm, setShowForm] = useState(false);

  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title="Cache (Olric)"
        actions={
          <div className="flex gap-2">
            <Button size="sm" onClick={() => setShowForm(!showForm)}>Set Key</Button>
            <Button variant="ghost" size="sm">Flush All</Button>
          </div>
        }
      />

      <ProvisioningBanner />

      <MetricGrid
        metrics={[
          { label: "Keys", value: String(CACHE_KEYS.length) },
          { label: "Hit Rate", value: "94.2%" },
        ]}
      />

      {showForm && (
        <DashedPanel className="p-6">
          <h3 className="font-mono text-xs text-muted uppercase tracking-wider mb-4">Set Cache Key</h3>
          <div className="flex flex-col gap-3">
            <div className="grid grid-cols-1 sm:grid-cols-3 gap-3">
              <div className="flex flex-col gap-2">
                <label className="font-mono text-xs text-muted uppercase tracking-wider">Key</label>
                <input type="text" placeholder="session:xyz" className="w-full px-3 py-2 bg-bg border border-dashed border-border rounded-sm font-mono text-sm text-fg placeholder:text-muted/50 outline-none focus:border-accent" />
              </div>
              <div className="flex flex-col gap-2">
                <label className="font-mono text-xs text-muted uppercase tracking-wider">Value</label>
                <input type="text" placeholder='{"user_id": 123}' className="w-full px-3 py-2 bg-bg border border-dashed border-border rounded-sm font-mono text-sm text-fg placeholder:text-muted/50 outline-none focus:border-accent" />
              </div>
              <div className="flex flex-col gap-2">
                <label className="font-mono text-xs text-muted uppercase tracking-wider">TTL (seconds)</label>
                <input type="number" placeholder="3600" className="w-full px-3 py-2 bg-bg border border-dashed border-border rounded-sm font-mono text-sm text-fg placeholder:text-muted/50 outline-none focus:border-accent" />
              </div>
            </div>
            <div className="flex gap-2">
              <Button size="sm">Set</Button>
              <Button variant="ghost" size="sm" onClick={() => setShowForm(false)}>Cancel</Button>
            </div>
          </div>
        </DashedPanel>
      )}

      <DataTable columns={columns} data={CACHE_KEYS} keyExtractor={(k) => k.key} />

      <CliCommandDisplay
        commands={[
          { description: "Set a cache key", command: "curl -X PUT https://gateway.orama.network/v1/cache/session:xyz -d '{\"value\":\"...\",\"ttl\":3600}'" },
          { description: "Get a cache key", command: "curl https://gateway.orama.network/v1/cache/session:xyz" },
          { description: "Delete a cache key", command: "curl -X DELETE https://gateway.orama.network/v1/cache/session:xyz" },
          { description: "Flush all keys", command: "curl -X POST https://gateway.orama.network/v1/cache/flush" },
        ]}
      />
    </div>
  );
}
