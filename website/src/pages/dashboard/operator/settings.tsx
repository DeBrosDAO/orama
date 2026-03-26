import { PageHeader } from "../components/page-header";
import { CliCommandDisplay } from "../components/cli-command";
import { DashedPanel } from "../../../components/ui/dashed-panel";
import { Badge } from "../../../components/ui/badge";
import { MOCK_ADDRESS } from "../data/mock-data";

export default function OpsSettingsPage() {
  return (
    <div className="flex flex-col gap-6">
      <PageHeader title="Operator Settings" />

      {/* General */}
      <DashedPanel className="p-4 sm:p-6">
        <h3 className="font-mono text-xs text-muted uppercase tracking-wider mb-4">General</h3>
        <div className="flex flex-col gap-3">
          {[
            { label: "Wallet Address", value: MOCK_ADDRESS },
            { label: "Payout Token", value: "$ORAMA" },
            { label: "Notifications", value: "Telegram" },
            { label: "Auto-Upgrade", value: "Enabled" },
          ].map((s) => (
            <div key={s.label} className="flex items-center justify-between py-2 border-b border-dashed border-border last:border-0">
              <span className="font-mono text-xs text-muted uppercase tracking-wider">{s.label}</span>
              <span className="font-mono text-sm text-fg">{s.value}</span>
            </div>
          ))}
        </div>
      </DashedPanel>

      {/* Orama Proxy */}
      <DashedPanel className="p-4 sm:p-6">
        <div className="flex items-center justify-between mb-4">
          <h3 className="font-mono text-xs text-muted uppercase tracking-wider">Orama Proxy Relay</h3>
          <Badge variant="status">Active</Badge>
        </div>
        <div className="flex flex-col gap-3">
          {[
            { label: "Relay Type", value: "Guard + Middle" },
            { label: "Bandwidth Limit", value: "30%" },
            { label: "Monthly Cap", value: "Unlimited" },
            { label: "ORPort", value: "9001" },
            { label: "Nickname", value: "OramaRelay01" },
          ].map((s) => (
            <div key={s.label} className="flex items-center justify-between py-2 border-b border-dashed border-border last:border-0">
              <span className="font-mono text-xs text-muted uppercase tracking-wider">{s.label}</span>
              <span className="font-mono text-sm text-fg">{s.value}</span>
            </div>
          ))}
        </div>
      </DashedPanel>

      {/* Node Family */}
      <DashedPanel className="p-4 sm:p-6">
        <h3 className="font-mono text-xs text-muted uppercase tracking-wider mb-4">Node Family</h3>
        <div className="flex flex-col gap-3">
          <div className="flex items-center justify-between py-2 border-b border-dashed border-border">
            <span className="font-mono text-xs text-muted uppercase tracking-wider">Family Name</span>
            <Badge variant="accent">EU-WEST-CLUSTER</Badge>
          </div>
          <div className="flex items-center justify-between py-2 border-b border-dashed border-border">
            <span className="font-mono text-xs text-muted uppercase tracking-wider">Members</span>
            <span className="font-mono text-sm text-fg">3 nodes</span>
          </div>
          <div className="flex items-center justify-between py-2">
            <span className="font-mono text-xs text-muted uppercase tracking-wider">Health</span>
            <span className="font-mono text-sm text-fg">100%</span>
          </div>
        </div>
      </DashedPanel>

      <CliCommandDisplay
        commands={[
          { description: "Node install with Orama Proxy", command: "sudo orama node install --vps-ip <ip> --proxy-relay --proxy-bandwidth 30" },
          { description: "Upgrade with Orama Proxy", command: "orama node upgrade --restart --proxy-relay --proxy-bandwidth 30" },
          { description: "Set node family during install", command: "sudo orama node install --vps-ip <ip> --proxy-family EU-WEST-CLUSTER" },
        ]}
      />
    </div>
  );
}
