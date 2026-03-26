import { PageHeader } from "../components/page-header";
import { MetricGrid } from "../components/metric-grid";
import { DataTable } from "../components/data-table";
import { CliCommandDisplay } from "../components/cli-command";
import { ProvisioningBanner } from "../components/provisioning-banner";
import { Button } from "../../../components/ui/button";
import { DashedPanel } from "../../../components/ui/dashed-panel";
import { STORAGE_FILES } from "../data/mock-data";
import type { Column } from "../components/data-table";

type StorageFile = (typeof STORAGE_FILES)[number];

const columns: Column<StorageFile>[] = [
  { key: "name", label: "Name", render: (f) => <span className="font-mono text-sm text-fg">{f.name}</span> },
  { key: "cid", label: "CID", render: (f) => <span className="font-mono text-xs text-muted">{f.cid}</span> },
  { key: "size", label: "Size", render: (f) => <span className="font-mono text-xs text-muted">{f.size}</span> },
  { key: "pins", label: "Pins", align: "right", render: (f) => <span className="font-mono text-xs text-muted">{f.pins} nodes</span> },
];

export default function StoragePage() {
  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title="Storage (IPFS)"
        actions={<Button size="sm">Upload File</Button>}
      />

      <ProvisioningBanner />

      <MetricGrid
        metrics={[
          { label: "Files", value: String(STORAGE_FILES.length) },
          { label: "Total Size", value: "4.0 MB" },
          { label: "Pin Nodes", value: "3" },
        ]}
      />

      {/* Upload area */}
      <DashedPanel withCorners className="p-8">
        <div className="flex flex-col items-center gap-3 text-center">
          <div className="w-12 h-12 border border-dashed border-border rounded-sm flex items-center justify-center">
            <span className="text-muted text-lg">+</span>
          </div>
          <p className="font-mono text-xs text-muted">
            Drop files here or click to upload
          </p>
          <p className="font-mono text-[10px] text-muted/60">
            Files are pinned to IPFS across all namespace nodes
          </p>
        </div>
      </DashedPanel>

      <DataTable columns={columns} data={STORAGE_FILES} keyExtractor={(f) => f.name} />

      <CliCommandDisplay
        commands={[
          { description: "Upload a file to IPFS", command: "curl -X POST https://gateway.orama.network/v1/ipfs/upload -F file=@photo.jpg" },
          { description: "Pin content by CID", command: "curl -X POST https://gateway.orama.network/v1/ipfs/pin/bafybei...a3f2" },
          { description: "Unpin content", command: "curl -X DELETE https://gateway.orama.network/v1/ipfs/pin/bafybei...a3f2" },
        ]}
      />
    </div>
  );
}
