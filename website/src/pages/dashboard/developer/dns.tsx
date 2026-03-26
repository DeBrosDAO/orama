import { PageHeader } from "../components/page-header";
import { DataTable } from "../components/data-table";
import { CliCommandDisplay } from "../components/cli-command";
import { ProvisioningBanner } from "../components/provisioning-banner";
import { Button } from "../../../components/ui/button";
import { Badge } from "../../../components/ui/badge";
import { StatusDot } from "../../../components/ui/status-dot";
import { DashedPanel } from "../../../components/ui/dashed-panel";
import { DOMAINS } from "../data/mock-data";
import type { Column } from "../components/data-table";

type Domain = (typeof DOMAINS)[number];

const columns: Column<Domain>[] = [
  { key: "domain", label: "Domain", render: (d) => <span className="font-mono text-sm text-fg">{d.domain}</span> },
  { key: "target", label: "Target", render: (d) => <span className="font-mono text-xs text-muted">{d.target}</span> },
  { key: "tls", label: "TLS", render: (d) => <Badge variant="default">{d.tls ? "Active" : "Pending"}</Badge> },
  { key: "status", label: "Status", align: "right", render: (d) => <div className="flex items-center gap-2 sm:justify-end"><StatusDot status={d.status} /><span className="text-sm text-muted">active</span></div> },
];

export default function DnsPage() {
  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title="DNS (Domains)"
        actions={<Button size="sm">Add Domain</Button>}
      />

      <ProvisioningBanner />

      {/* Verification steps */}
      <DashedPanel className="p-4 sm:p-6">
        <h3 className="font-mono text-xs text-muted uppercase tracking-wider mb-3">How to add a custom domain</h3>
        <div className="flex flex-col gap-3">
          {[
            { step: "1", text: "Add your domain using the button above" },
            { step: "2", text: "Add a CNAME record pointing to your-app.orama.network" },
            { step: "3", text: "Wait for DNS propagation (up to 48h)" },
            { step: "4", text: "TLS certificate is automatically provisioned via Caddy" },
          ].map((s) => (
            <div key={s.step} className="flex items-start gap-3">
              <span className="font-mono text-xs text-accent font-bold shrink-0 w-5 h-5 flex items-center justify-center border border-dashed border-accent/30 rounded-sm">
                {s.step}
              </span>
              <span className="text-sm text-muted">{s.text}</span>
            </div>
          ))}
        </div>
      </DashedPanel>

      <DataTable columns={columns} data={DOMAINS} keyExtractor={(d) => d.domain} />

      <CliCommandDisplay
        commands={[
          { description: "Deploy with custom domain", command: "orama deploy static ./dist --name my-app --domain myapp.com" },
          { description: "Update domain for existing deployment", command: "orama deploy static ./dist --name my-app --domain api.myapp.com --update" },
        ]}
      />
    </div>
  );
}
