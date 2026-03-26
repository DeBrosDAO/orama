import { PageHeader } from "../components/page-header";
import { CliCommandDisplay } from "../components/cli-command";
import { DashedPanel } from "../../../components/ui/dashed-panel";
import { Badge } from "../../../components/ui/badge";
import { Button } from "../../../components/ui/button";
import { MOCK_ADDRESS } from "../data/mock-data";

const MOCK_API_KEYS = [
  { key: "ak_5lw7...Mz:my-project", created: "2d ago", lastUsed: "1h ago" },
  { key: "ak_Rj9p...Kx:my-project", created: "1w ago", lastUsed: "3d ago" },
];

export default function DevSettingsPage() {
  return (
    <div className="flex flex-col gap-6">
      <PageHeader title="Settings" />

      {/* Account */}
      <DashedPanel className="p-4 sm:p-6">
        <h3 className="font-mono text-xs text-muted uppercase tracking-wider mb-4">Account</h3>
        <div className="flex flex-col gap-3">
          {[
            { label: "Wallet Address", value: MOCK_ADDRESS },
            { label: "Status", value: "Connected" },
          ].map((s) => (
            <div key={s.label} className="flex items-center justify-between py-2 border-b border-dashed border-border last:border-0">
              <span className="font-mono text-xs text-muted uppercase tracking-wider">{s.label}</span>
              <span className="font-mono text-sm text-fg">{s.value}</span>
            </div>
          ))}
        </div>
      </DashedPanel>

      {/* Environment */}
      <DashedPanel className="p-4 sm:p-6">
        <h3 className="font-mono text-xs text-muted uppercase tracking-wider mb-4">Environment</h3>
        <div className="flex gap-2">
          {["devnet", "testnet", "mainnet"].map((env) => (
            <button
              key={env}
              type="button"
              className={`px-3 py-1.5 font-mono text-xs uppercase tracking-wider rounded-sm border border-dashed transition-colors ${
                env === "mainnet"
                  ? "border-accent bg-accent/10 text-accent"
                  : "border-border text-muted hover:text-fg"
              }`}
            >
              {env}
            </button>
          ))}
        </div>
      </DashedPanel>

      {/* API Keys */}
      <DashedPanel className="p-4 sm:p-6">
        <div className="flex items-center justify-between mb-4">
          <h3 className="font-mono text-xs text-muted uppercase tracking-wider">API Keys</h3>
          <Button size="sm">Create Key</Button>
        </div>
        {MOCK_API_KEYS.map((k, i) => (
          <div key={k.key} className={`flex items-center justify-between py-3 ${i < MOCK_API_KEYS.length - 1 ? "border-b border-dashed border-border" : ""}`}>
            <div className="flex flex-col gap-0.5">
              <span className="font-mono text-sm text-fg">{k.key}</span>
              <span className="font-mono text-[10px] text-muted">Created {k.created} · Last used {k.lastUsed}</span>
            </div>
            <div className="flex items-center gap-2">
              <Badge variant="default">Active</Badge>
              <Button variant="ghost" size="sm">Revoke</Button>
            </div>
          </div>
        ))}
      </DashedPanel>

      <CliCommandDisplay
        commands={[
          { description: "Show current auth status", command: "orama auth whoami" },
          { description: "Show detailed auth info", command: "orama auth status" },
          { description: "List stored credentials", command: "orama auth list" },
          { description: "Switch between accounts", command: "orama auth switch" },
          { description: "Show current environment", command: "orama env current" },
          { description: "Switch environment", command: "orama env use mainnet" },
          { description: "List environments", command: "orama env list" },
        ]}
      />
    </div>
  );
}
