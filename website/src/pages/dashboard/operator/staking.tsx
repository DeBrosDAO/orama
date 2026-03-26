import { PageHeader } from "../components/page-header";
import { MetricGrid } from "../components/metric-grid";
import { CliCommandDisplay } from "../components/cli-command";
import { Button } from "../../../components/ui/button";
import { DashedPanel } from "../../../components/ui/dashed-panel";

const STAKE_HISTORY = [
  { date: "Jan 15", action: "Staked", amount: "***", token: "$ORAMA" },
  { date: "Feb 01", action: "Staked", amount: "***", token: "$ORAMA" },
];

export default function StakingPage() {
  return (
    <div className="flex flex-col gap-6">
      <PageHeader title="Staking" />

      <MetricGrid
        metrics={[
          { label: "Staked", value: "***" },
          { label: "Lock Period", value: "***" },
          { label: "APY", value: "***" },
          { label: "Multiplier", value: "***" },
        ]}
      />

      <DashedPanel withCorners className="p-6">
        <div className="flex flex-col gap-4 text-center">
          <p className="text-sm text-muted">Your stake qualifies for enhanced reward multiplier</p>
          <div className="flex items-center justify-center gap-4">
            <Button size="sm">Stake More</Button>
            <Button variant="ghost" size="sm">Unstake</Button>
          </div>
        </div>
      </DashedPanel>

      {/* Stake/Unstake Form */}
      <DashedPanel className="p-6">
        <h3 className="font-mono text-xs text-muted uppercase tracking-wider mb-4">Stake Tokens</h3>
        <div className="flex flex-col gap-3">
          <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
            <div className="flex flex-col gap-2">
              <label className="font-mono text-xs text-muted uppercase tracking-wider">Amount</label>
              <input type="number" placeholder="10000" className="w-full px-3 py-2 bg-bg border border-dashed border-border rounded-sm font-mono text-sm text-fg placeholder:text-muted/50 outline-none focus:border-accent" />
            </div>
            <div className="flex flex-col gap-2">
              <label className="font-mono text-xs text-muted uppercase tracking-wider">Lock Period</label>
              <select className="w-full px-3 py-2 bg-bg border border-dashed border-border rounded-sm font-mono text-sm text-fg outline-none focus:border-accent">
                <option value="30">*** (***)</option>
                <option value="90">*** (***)</option>
                <option value="180">*** (***)</option>
                <option value="365">*** (***)</option>
              </select>
            </div>
          </div>
          <Button size="sm" className="self-start">Stake</Button>
        </div>
      </DashedPanel>

      {/* History */}
      <DashedPanel className="p-4 sm:p-6">
        <h3 className="font-mono text-xs text-muted uppercase tracking-wider mb-4">Staking History</h3>
        {STAKE_HISTORY.map((s, i) => (
          <div key={s.date} className={`flex items-center justify-between py-3 ${i < STAKE_HISTORY.length - 1 ? "border-b border-dashed border-border" : ""}`}>
            <div className="flex items-center gap-3">
              <span className="font-mono text-sm text-fg">{s.action}</span>
              <span className="font-mono text-xs text-muted">{s.token}</span>
            </div>
            <div className="flex items-center gap-3">
              <span className="font-mono text-sm text-accent">{s.amount}</span>
              <span className="font-mono text-xs text-muted">{s.date}</span>
            </div>
          </div>
        ))}
      </DashedPanel>

      <CliCommandDisplay
        commands={[
          { description: "Staking is managed on-chain via smart contract", command: "# Visit: https://dex.orama.network/staking" },
          { description: "Check staking status via SDK", command: "const stake = await client.staking.getStake(wallet)" },
        ]}
      />
    </div>
  );
}
