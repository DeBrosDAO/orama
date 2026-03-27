import { useState, useEffect, useCallback, useRef } from "react";
import { Link, useSearchParams } from "react-router";
import * as Dialog from "@radix-ui/react-dialog";
import { createChart, AreaSeries } from "lightweight-charts";
import type { IChartApi, Time } from "lightweight-charts";
import { Page } from "../components/layout/page";
import { DashedPanel } from "../components/ui/dashed-panel";
import { SponsorsShowcase } from "../components/landing/sponsors-showcase";
import { SILVER, SilverButton, SilverMetric } from "../components/ui/silver-theme";
import {
  Wallet,
  Coins,
  Server,
  Code,
  ArrowRight,
  Shield,
  Vote,
  TrendingUp,
  Clock,
  ChevronDown,
  Globe,
  Database,
  HardDrive,
  Layers,
  Cpu,
  Award,
  ExternalLink,
  LogOut,
  Copy,
  Check,
  X,
  BookOpen,
  Users,
  FileText,
  LayoutDashboard,
} from "lucide-react";
import oramaIcon from "../assets/orama-icon.png";
import type {
  Stats,
  MeResponse,
} from "../hooks/useInvestApi";
import {
  fetchStats,
} from "../hooks/useInvestApi";
import { ComingSoonOverlay } from "../components/ui/redacted";


function generateNetworkHistory(): { time: Time; value: number }[] {
  const data: { time: Time; value: number }[] = [];
  const now = new Date();
  const days = 90;

  for (let i = days; i >= 0; i--) {
    const date = new Date(now);
    date.setDate(date.getDate() - i);
    const dateStr = date.toISOString().split("T")[0] as unknown as Time;
    const progress = (days - i) / days;
    const curve = Math.pow(progress, 1.5);
    const noise = 1 + (Math.sin(i * 0.7) * 0.05);
    const value = 50 * curve * noise;
    data.push({ time: dateStr, value: parseFloat(Math.max(value, 1).toFixed(0)) });
  }
  return data;
}

/* ── Tab type ── */
type Tab = "overview" | "bonding" | "license" | "whitelist" | "sponsors";

/* ── Wallet state ── */
interface WalletState {
  address: string;
  chain: "btc";
  connected: boolean;
}

/* ── Helper: truncate address ── */
function truncateAddress(addr: string): string {
  if (addr.length <= 12) return addr;
  return `${addr.slice(0, 6)}...${addr.slice(-4)}`;
}

/* ══════════════════════════════════════════════
   WALLET CONNECTION MODAL
   ══════════════════════════════════════════════ */
function WalletConnectModal({
  open,
  onOpenChange,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  return (
    <Dialog.Root open={open} onOpenChange={onOpenChange}>
      <Dialog.Portal>
        <Dialog.Overlay className="fixed inset-0 z-50 bg-black/60 backdrop-blur-sm data-[state=open]:animate-in data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=open]:fade-in-0" />
        <Dialog.Content className="fixed left-1/2 top-1/2 z-50 -translate-x-1/2 -translate-y-1/2 w-full max-w-sm border border-dashed border-border bg-bg p-6 shadow-2xl data-[state=open]:animate-in data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=open]:fade-in-0 data-[state=closed]:zoom-out-95 data-[state=open]:zoom-in-95">
          <div className="flex items-center justify-between mb-6">
            <Dialog.Title className="font-display font-bold text-fg text-sm tracking-wider uppercase">
              Connect Wallet
            </Dialog.Title>
            <Dialog.Close className="text-muted hover:text-fg transition-colors cursor-pointer">
              <X className="w-4 h-4" />
            </Dialog.Close>
          </div>

          <div className="flex flex-col items-center gap-4 py-4">
            <Wallet className="w-8 h-8 text-muted" />
            <p className="text-muted text-xs font-mono text-center">
              Wallet login via RootWallet is coming soon.
            </p>
            <span className="text-[10px] font-mono text-zinc-600 tracking-wider uppercase">
              RootWallet + Orama L1
            </span>
          </div>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}

/* ══════════════════════════════════════════════
   WALLET BUTTON (header bar)
   ══════════════════════════════════════════════ */
function WalletButton({
  wallet,
  onOpenModal,
  onDisconnect,
}: {
  wallet: WalletState | null;
  onOpenModal: () => void;
  onDisconnect: () => void;
}) {
  const [copied, setCopied] = useState(false);

  const handleCopy = useCallback(() => {
    if (!wallet) return;
    navigator.clipboard.writeText(wallet.address);
    setCopied(true);
    setTimeout(() => setCopied(false), 2000);
  }, [wallet]);

  if (wallet?.connected) {
    return (
      <div className="flex items-center gap-3">
        <div
          className="flex items-center gap-2 px-3 py-1.5 text-xs font-mono"
          style={{ border: `1px dashed ${SILVER.border}`, background: SILVER.bg }}
        >
          <span
            className="w-2 h-2 rounded-full"
            style={{ background: "#F7931A" }}
          />
          <span className="text-fg">{truncateAddress(wallet.address)}</span>
          <button
            type="button"
            onClick={handleCopy}
            className="text-muted hover:text-fg transition-colors cursor-pointer"
            title="Copy address"
          >
            {copied ? <Check className="w-3 h-3" /> : <Copy className="w-3 h-3" />}
          </button>
        </div>
        <button
          type="button"
          onClick={onDisconnect}
          className="flex items-center gap-1.5 px-3 py-1.5 text-xs font-mono text-muted hover:text-fg transition-colors cursor-pointer"
          style={{ border: `1px dashed ${SILVER.border}` }}
        >
          <LogOut className="w-3 h-3" />
        </button>
      </div>
    );
  }

  return (
    <button
      type="button"
      onClick={onOpenModal}
      className="flex items-center gap-2 px-4 py-2 text-xs font-mono tracking-wider uppercase text-muted hover:text-fg border border-dashed border-border hover:border-fg/30 transition-all cursor-pointer"
    >
      <Wallet className="w-3.5 h-3.5" />
      Connect Wallet
    </button>
  );
}

/* ══════════════════════════════════════════════
   STATS BAR (always visible)
   ══════════════════════════════════════════════ */
function StatsBar({ stats }: { stats: Stats | null }) {
  if (!stats) return null;

  return (
    <div
      className="grid grid-cols-2 md:grid-cols-4 gap-3 sm:gap-4 p-3 sm:p-4 rounded-sm"
      style={{ border: `1px dashed ${SILVER.border}`, background: SILVER.bg }}
    >
      <SilverMetric label="Nodes Online" value="—" />
      <SilverMetric label="Blocks Produced" value="—" />
      <SilverMetric label="$ORAMA Mined" value="—" />
      <SilverMetric label="Developers" value="—" />
    </div>
  );
}

/* ══════════════════════════════════════════════
   PROGRESS BAR
   ══════════════════════════════════════════════ */
function ProgressBar({
  current,
  total,
  label,
  sublabel,
}: {
  current: number;
  total: number;
  label: string;
  sublabel?: string;
}) {
  const pct = Math.min((current / total) * 100, 100);

  return (
    <div className="flex flex-col gap-2">
      <div className="flex items-center justify-between text-xs font-mono text-muted">
        <span>{label}</span>
        <span>{sublabel || `${pct.toFixed(1)}%`}</span>
      </div>
      <div className="h-3 bg-surface-2 rounded-full overflow-hidden border border-border">
        <div
          className="h-full rounded-full transition-all duration-1000"
          style={{ width: `${Math.max(pct, 1)}%`, background: SILVER.gradient }}
        />
      </div>
    </div>
  );
}

/* ══════════════════════════════════════════════
   FAQ ACCORDION
   ══════════════════════════════════════════════ */
function FAQ({ items }: { items: { q: string; a: string }[] }) {
  const [openIndex, setOpenIndex] = useState<number | null>(null);

  return (
    <div className="flex flex-col gap-2">
      <h3 className="text-xs font-mono text-muted tracking-wider uppercase mb-2">
        Frequently Asked Questions
      </h3>
      {items.map((item, i) => (
        <button
          key={i}
          type="button"
          onClick={() => setOpenIndex(openIndex === i ? null : i)}
          className="w-full text-left"
        >
          <DashedPanel withBackground>
            <div className="flex flex-col gap-0 p-2">
              <div className="flex items-center justify-between gap-4">
                <h4 className="font-display font-bold text-fg text-sm">{item.q}</h4>
                <ChevronDown
                  className={`w-4 h-4 shrink-0 text-muted transition-transform duration-200 ${
                    openIndex === i ? "rotate-180" : ""
                  }`}
                />
              </div>
              <div
                className={`overflow-hidden transition-all duration-300 ${
                  openIndex === i ? "max-h-60 mt-3 opacity-100" : "max-h-0 opacity-0"
                }`}
              >
                <p className="text-muted text-sm leading-relaxed">{item.a}</p>
              </div>
            </div>
          </DashedPanel>
        </button>
      ))}
    </div>
  );
}

/* ══════════════════════════════════════════════
   NETWORK ACTIVITY CHART (lightweight-charts)
   ══════════════════════════════════════════════ */
function NetworkChart() {
  const containerRef = useRef<HTMLDivElement>(null);
  const chartRef = useRef<IChartApi | null>(null);

  const initChart = useCallback(() => {
    if (!containerRef.current) return;
    if (chartRef.current) {
      chartRef.current.remove();
      chartRef.current = null;
    }
    const chart = createChart(containerRef.current, {
      width: containerRef.current.clientWidth,
      height: window.innerWidth < 640 ? 200 : 300,
      layout: { background: { color: "transparent" }, textColor: "#71717a", fontFamily: "JetBrains Mono, monospace", fontSize: 10 },
      grid: { vertLines: { color: "rgba(161,161,170,0.06)" }, horzLines: { color: "rgba(161,161,170,0.06)" } },
      crosshair: { vertLine: { color: "rgba(161,161,170,0.3)", labelBackgroundColor: "#27272a" }, horzLine: { color: "rgba(161,161,170,0.3)", labelBackgroundColor: "#27272a" } },
      rightPriceScale: { borderColor: "rgba(161,161,170,0.1)" },
      timeScale: { borderColor: "rgba(161,161,170,0.1)", timeVisible: false },
    });
    const series = chart.addSeries(AreaSeries, {
      lineColor: "#d4d4d8",
      topColor: "rgba(161,161,170,0.3)",
      bottomColor: "rgba(161,161,170,0.02)",
      lineWidth: 2,
    });
    series.setData(generateNetworkHistory());
    chart.timeScale().fitContent();
    chartRef.current = chart;
  }, []);

  useEffect(() => {
    initChart();
    const handleResize = () => {
      if (chartRef.current && containerRef.current)
        chartRef.current.applyOptions({ width: containerRef.current.clientWidth });
    };
    window.addEventListener("resize", handleResize);
    return () => {
      window.removeEventListener("resize", handleResize);
      if (chartRef.current) { chartRef.current.remove(); chartRef.current = null; }
    };
  }, [initChart]);

  return <div ref={containerRef} className="w-full rounded-sm" style={{ border: `1px dashed ${SILVER.border}`, background: "rgba(10,10,10,0.5)" }} />;
}

/* ══════════════════════════════════════════════
   DONATE SECTION (reusable)
   ══════════════════════════════════════════════ */
const DONATE_WALLETS = [
  { chain: "BTC", address: "bc1qzpkjguxh4pl9pdhj76zeztur42prhfed2hd22z", label: "Bitcoin" },
];

function DonateSection({ compact }: { compact?: boolean }) {
  const [copiedIdx, setCopiedIdx] = useState<number | null>(null);

  const handleCopy = (address: string, idx: number) => {
    navigator.clipboard.writeText(address);
    setCopiedIdx(idx);
    setTimeout(() => setCopiedIdx(null), 2000);
  };

  return (
    <div className={compact ? "flex flex-col gap-3" : "flex flex-col gap-4"}>
      {!compact && (
        <div className="flex flex-col gap-1">
          <h3 className="text-xs font-mono text-muted tracking-wider uppercase">Support the Cause</h3>
          <p className="text-xs text-muted leading-relaxed">
            Donate directly to support Orama Network development. Every contribution helps build decentralized infrastructure.
          </p>
        </div>
      )}
      {DONATE_WALLETS.map((w, i) => (
        <div
          key={w.chain}
          className="flex items-center justify-between gap-3 px-3 py-2 border border-dashed border-border hover:border-fg/20 transition-colors"
        >
          <div className="flex items-center gap-2 min-w-0">
            <span className="text-[10px] font-mono font-bold tracking-wider text-muted w-8 shrink-0">{w.chain}</span>
            <span className="text-xs font-mono text-fg truncate">{w.address}</span>
          </div>
          <button
            type="button"
            onClick={() => handleCopy(w.address, i)}
            className="text-muted hover:text-fg transition-colors cursor-pointer shrink-0"
            title={`Copy ${w.label} address`}
          >
            {copiedIdx === i ? <Check className="w-3.5 h-3.5" /> : <Copy className="w-3.5 h-3.5" />}
          </button>
        </div>
      ))}
    </div>
  );
}

/* ══════════════════════════════════════════════
   OVERVIEW TAB
   ══════════════════════════════════════════════ */
function OverviewTab({
  stats: _stats,
}: {
  stats: Stats | null;
}) {
  return (
    <div className="flex flex-col gap-8">
      {/* Header */}
      <div className="flex flex-col gap-2">
        <h2 className="font-display font-bold text-2xl text-fg">Overview</h2>
        <p className="text-muted text-sm leading-relaxed max-w-xl">
          Real-time network stats. 210M $ORAMA hard cap, 100% earned through mining.
        </p>
      </div>

      {/* Key Metrics Grid */}
      <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4">
        <DashedPanel withBackground>
          <div className="flex flex-col gap-1 p-2">
            <span className="text-[10px] font-mono text-muted tracking-wider uppercase">Nodes Online</span>
            <span className="font-display font-bold text-xl text-fg">—</span>
            <span className="text-[10px] font-mono text-muted">300 genesis target</span>
          </div>
        </DashedPanel>
        <DashedPanel withBackground>
          <div className="flex flex-col gap-1 p-2">
            <span className="text-[10px] font-mono text-muted tracking-wider uppercase">Blocks Produced</span>
            <span className="font-display font-bold text-xl text-fg">—</span>
            <span className="text-[10px] font-mono text-muted">6-second block time</span>
          </div>
        </DashedPanel>
        <DashedPanel withBackground>
          <div className="flex flex-col gap-1 p-2">
            <span className="text-[10px] font-mono text-muted tracking-wider uppercase">$ORAMA Mined</span>
            <span className="font-display font-bold text-xl text-fg">—</span>
            <span className="text-[10px] font-mono text-muted">of 210,000,000 total</span>
          </div>
        </DashedPanel>
        <DashedPanel withBackground>
          <div className="flex flex-col gap-1 p-2">
            <span className="text-[10px] font-mono text-muted tracking-wider uppercase">Developers</span>
            <span className="font-display font-bold text-xl text-fg">—</span>
            <span className="text-[10px] font-mono text-muted">on waitlist</span>
          </div>
        </DashedPanel>
      </div>

      {/* Network Activity Chart */}
      <DashedPanel withBackground withCorners>
        <div className="flex flex-col gap-4 p-2">
          <div className="flex items-center justify-between">
            <h3 className="text-xs font-mono text-muted tracking-wider uppercase">Network Activity</h3>
            <span className="text-xs font-mono text-fg">
              — nodes
            </span>
          </div>
          <ComingSoonOverlay>
            <NetworkChart />
          </ComingSoonOverlay>
        </div>
      </DashedPanel>

      {/* Emission Progress */}
      <DashedPanel withBackground withCorners>
        <div className="flex flex-col gap-6 p-2">
          <h3 className="text-xs font-mono text-muted tracking-wider uppercase">Emission Progress</h3>
          <ProgressBar
            current={0}
            total={210000000}
            label="$ORAMA Mined"
            sublabel="Era 1 — 100 $ORAMA/block"
          />
          <ProgressBar
            current={0}
            total={300}
            label="Genesis Nodes"
            sublabel="Target: 300"
          />
        </div>
      </DashedPanel>

      {/* Sponsors */}
      <SponsorsShowcase />

      {/* Donate */}
      <DashedPanel withBackground withCorners>
        <div className="p-2">
          <DonateSection />
        </div>
      </DashedPanel>

      {/* Quick Links */}
      <div className="grid grid-cols-1 sm:grid-cols-3 gap-4">
        <DashedPanel withBackground>
          <div className="flex flex-col gap-3 p-2">
            <div className="flex items-center gap-2">
              <Coins className="w-4 h-4 text-muted" />
              <span className="text-xs font-mono text-muted tracking-wider uppercase">Bonding Curve</span>
            </div>
            <p className="text-muted text-xs leading-relaxed">
              Buy $ORAMA from the protocol. Price = k x sqrt(n). BTC flows to protocol reserve.
            </p>
            <span className="text-xs font-mono text-fg">Coming Soon</span>
          </div>
        </DashedPanel>
        <DashedPanel withBackground>
          <div className="flex flex-col gap-3 p-2">
            <div className="flex items-center gap-2">
              <Server className="w-4 h-4 text-muted" />
              <span className="text-xs font-mono text-muted tracking-wider uppercase">Node License</span>
            </div>
            <p className="text-muted text-xs leading-relaxed">
              Operate an Orama node. Details TBA.
            </p>
            <span className="text-xs font-mono text-fg">Coming Soon</span>
          </div>
        </DashedPanel>
        <DashedPanel withBackground>
          <div className="flex flex-col gap-3 p-2">
            <div className="flex items-center gap-2">
              <Code className="w-4 h-4 text-muted" />
              <span className="text-xs font-mono text-muted tracking-wider uppercase">Dev Waitlist</span>
            </div>
            <p className="text-muted text-xs leading-relaxed">
              Join the waitlist for early access to deploy on the network.
            </p>
            <span className="text-xs font-mono text-fg">— developers joined</span>
          </div>
        </DashedPanel>
      </div>
    </div>
  );
}

/* ══════════════════════════════════════════════
   BONDING CURVE TAB (replaces Token Pre-Sale)
   ══════════════════════════════════════════════ */
function BondingCurveTab({
  wallet,
}: {
  wallet: WalletState | null;
  stats: Stats | null;
  me: MeResponse | null;
}) {
  const isConnected = wallet?.connected;

  return (
    <div className="flex flex-col gap-8">
      {/* Headline */}
      <div className="flex flex-col gap-2">
        <h2 className="font-display font-bold text-2xl text-fg">Bonding Curve</h2>
        <p className="text-muted text-sm leading-relaxed max-w-xl">
          The protocol itself is the first market maker. 20% of every block reward flows into the curve's
          inventory (max 21M tokens). Buy $ORAMA by sending BTC. Price follows a square root function —
          cheap early, expensive later. All BTC goes to the protocol reserve backing the bridge.
        </p>
      </div>

      {/* Price Formula */}
      <DashedPanel withCorners withBackground>
        <div className="flex flex-col gap-4 p-4">
          <code className="text-lg text-fg font-mono block text-center py-2">
            Price = 0.0000000006 x sqrt(total_sold)
          </code>
          <p className="text-xs text-muted text-center">
            k = 0.0000000006 BTC. Max 21,000,000 tokens. Total BTC to fill: ~38.5 BTC.
          </p>
        </div>
      </DashedPanel>

      {/* Price Table */}
      <DashedPanel withCorners withBackground>
        <div className="flex flex-col gap-4 p-4">
          <h3 className="text-xs font-mono text-muted tracking-wider uppercase">Price Schedule</h3>
          <div className="overflow-x-auto">
            <table className="w-full text-left">
              <thead>
                <tr className="border-b border-dashed border-border">
                  <th className="text-xs font-mono text-muted tracking-wider uppercase py-3 pr-4">Tokens Sold</th>
                  <th className="text-xs font-mono text-muted tracking-wider uppercase py-3 pr-4">Price per $ORAMA</th>
                  <th className="text-xs font-mono text-muted tracking-wider uppercase py-3 pr-4">USD (at $100K BTC)</th>
                  <th className="text-xs font-mono text-muted tracking-wider uppercase py-3">Cumulative BTC</th>
                </tr>
              </thead>
              <tbody>
                {[
                  { sold: "10,000", price: "0.00000006 BTC", usd: "$0.006", btc: "0.0004 BTC" },
                  { sold: "100,000", price: "0.00000019 BTC", usd: "$0.019", btc: "0.013 BTC" },
                  { sold: "1,000,000", price: "0.0000006 BTC", usd: "$0.06", btc: "0.4 BTC" },
                  { sold: "5,000,000", price: "0.00000134 BTC", usd: "$0.134", btc: "4.47 BTC" },
                  { sold: "10,000,000", price: "0.0000019 BTC", usd: "$0.19", btc: "12.65 BTC" },
                  { sold: "21,000,000", price: "0.00000275 BTC", usd: "$0.275", btc: "~38.5 BTC" },
                ].map((row) => (
                  <tr key={row.sold} className="border-b border-border/50">
                    <td className="text-sm text-fg py-3 pr-4 font-mono">{row.sold}</td>
                    <td className="text-sm text-fg py-3 pr-4 font-mono">{row.price}</td>
                    <td className="text-sm text-muted py-3 pr-4 font-mono">{row.usd}</td>
                    <td className="text-sm text-muted py-3 font-mono">{row.btc}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      </DashedPanel>

      {/* Buy Panel */}
      <DashedPanel withCorners withBackground>
        <div className="flex flex-col gap-5 p-4">
          <span className="text-xs font-mono text-muted tracking-wider uppercase">
            Buy $ORAMA from the Curve
          </span>

          <div className="flex flex-col items-center justify-center py-8 gap-3">
            <Coins className="w-8 h-8 text-zinc-700" />
            <p className="text-sm text-zinc-600 font-mono">
              {isConnected ? "Bonding curve not yet live" : "Connect RootWallet to purchase"}
            </p>
          </div>

          <SilverButton size="lg" className="w-full" disabled>
            Coming Soon — Buy with RootWallet
          </SilverButton>
          <p className="text-[10px] font-mono text-zinc-600 text-center">
            BTC payments via RootWallet on Orama L1.
          </p>
        </div>
      </DashedPanel>

      {/* How It Works */}
      <DashedPanel withCorners withBackground>
        <div className="flex flex-col gap-4 p-4">
          <h3 className="text-xs font-mono text-muted tracking-wider uppercase">How It Works</h3>
          {[
            { step: "1", title: "Bridge BTC onto Orama", desc: "Deposit BTC via the trust-minimized bridge. Receive native BTC on Orama L1 (1:1)." },
            { step: "2", title: "Buy from the curve", desc: "Send BTC to the bonding curve. Receive $ORAMA at the current curve price. The more tokens sold, the higher the price." },
            { step: "3", title: "BTC goes to protocol reserve", desc: "All BTC paid to the curve accumulates in the protocol reserve, directly backing the BTC bridge." },
            { step: "4", title: "Use your $ORAMA", desc: "Stake for consensus, vote on governance, pay for compute, or trade on the native order book." },
          ].map((item) => (
            <div key={item.step} className="flex items-start gap-4">
              <span
                className="font-mono text-xs shrink-0 w-6 h-6 flex items-center justify-center border border-dashed rounded-sm"
                style={{ borderColor: SILVER.dark, color: SILVER.light }}
              >
                {item.step}
              </span>
              <div>
                <h4 className="font-display font-bold text-sm text-fg">{item.title}</h4>
                <p className="text-xs text-muted leading-relaxed mt-1">{item.desc}</p>
              </div>
            </div>
          ))}
        </div>
      </DashedPanel>

      {/* FAQ */}
      <FAQ
        items={[
          {
            q: "Is this a pre-sale?",
            a: "No. The bonding curve is a protocol-level market maker, not a traditional pre-sale. There are no allocations, no vesting, and no special deals. Anyone can buy at any time at the mathematically determined price. Early buyers get a lower price because the curve starts cheap.",
          },
          {
            q: "What happens to the BTC I pay?",
            a: "It goes to the protocol reserve — not a team wallet. This reserve directly backs the BTC bridge, providing additional collateral beyond 1:1 deposits.",
          },
          {
            q: "Can the price go down?",
            a: "The curve price only goes up as more tokens are sold. However, the order book price can be lower than the curve price if miners are selling below the curve. The free market determines the real price.",
          },
          {
            q: "When does the curve stop?",
            a: "The curve is capped at 21M tokens total (10% of supply). When the order book achieves sufficient organic liquidity (defined by governance), the curve stops receiving new inventory. It remains available with whatever inventory it has.",
          },
        ]}
      />
    </div>
  );
}

/* ══════════════════════════════════════════════
   NODE LICENSE TAB
   ══════════════════════════════════════════════ */
function NodeLicenseTab() {
  return (
    <div className="flex flex-col gap-8">
      {/* Headline */}
      <div className="flex flex-col gap-2">
        <h2 className="font-display font-bold text-2xl text-fg">Node License</h2>
        <p className="text-muted text-sm leading-relaxed max-w-xl">
          Node licenses will provide the right to operate an Orama Network node with priority access
          and additional benefits. Details are being finalized.
        </p>
      </div>

      {/* Coming Soon */}
      <DashedPanel withCorners withBackground>
        <div className="flex flex-col items-center justify-center py-12 gap-4">
          <Server className="w-10 h-10 text-zinc-700" />
          <h3 className="font-display font-bold text-lg text-fg">Coming Soon</h3>
          <p className="text-sm text-muted max-w-md text-center">
            Node license details, pricing, and mechanics will be announced soon.
            Join the Telegram community to be notified.
          </p>
          <a href="https://t.me/debrosportal" target="_blank" rel="noopener noreferrer">
            <SilverButton size="lg">
              Join Telegram
              <ArrowRight className="w-3.5 h-3.5 ml-2" />
            </SilverButton>
          </a>
        </div>
      </DashedPanel>

      {/* What We Know */}
      <div className="flex flex-col gap-4">
        <h3 className="text-xs font-mono text-muted tracking-wider uppercase">
          What We Know So Far
        </h3>
        <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
          {[
            {
              icon: Server,
              title: "Run a Node",
              desc: "License holders will operate Orama Network nodes, earning $ORAMA through block rewards.",
            },
            {
              icon: Shield,
              title: "OramaOS Support",
              desc: "Nodes running OramaOS receive a 1.5x Infrastructure Multiplier for block rewards.",
            },
            {
              icon: TrendingUp,
              title: "Testnet Is Free",
              desc: "During testnet, anyone can run a node with no license and no staking required. Tokens earned carry over to mainnet.",
            },
            {
              icon: Vote,
              title: "Details TBA",
              desc: "License pricing, supply, and specific mechanics have not yet been announced.",
            },
          ].map((card) => (
            <DashedPanel key={card.title} withBackground>
              <div className="flex flex-col gap-2 p-3">
                <card.icon className="w-4 h-4" style={{ color: SILVER.light }} />
                <h4 className="font-display font-bold text-sm text-fg">{card.title}</h4>
                <p className="text-xs text-muted leading-relaxed">{card.desc}</p>
              </div>
            </DashedPanel>
          ))}
        </div>
      </div>
    </div>
  );
}

/* ══════════════════════════════════════════════
   DEVELOPER WAITLIST TAB
   ══════════════════════════════════════════════ */
function DevWhitelistTab({
  wallet,
  stats,
  me,
}: {
  wallet: WalletState | null;
  stats: Stats | null;
  me: MeResponse | null;
}) {
  const isConnected = wallet?.connected;
  const isWhitelisted = me?.on_whitelist || false;

  return (
    <div className="flex flex-col gap-8">
      <div className="flex flex-col gap-2">
        <h2 className="font-display font-bold text-2xl text-fg">Developer Waitlist</h2>
        <p className="text-muted text-sm leading-relaxed max-w-xl">
          Get early access to the decentralized cloud. Free — no purchase required. Join with your
          wallet to reserve your spot.
        </p>
      </div>

      <DashedPanel withCorners withBackground>
        <div className="flex flex-col items-center text-center gap-6 py-8 px-4">
          {!isConnected ? (
            <>
              <Code className="w-10 h-10 text-zinc-700" />
              <p className="text-sm text-zinc-600 font-mono">Connect your wallet to join</p>
            </>
          ) : isWhitelisted ? (
            <>
              <div className="flex items-center gap-2">
                <span
                  className="w-3 h-3 rounded-full animate-pulse"
                  style={{ background: SILVER.light }}
                />
                <span
                  className="font-mono text-sm font-semibold tracking-wider uppercase"
                  style={{ color: SILVER.light }}
                >
                  You&apos;re on the waitlist
                </span>
              </div>
              <p className="text-muted text-sm max-w-md">
                We&apos;ll notify you when the platform opens for deployment. Early waitlist
                members get free compute credits and priority support.
              </p>
            </>
          ) : (
            <>
              <Code className="w-10 h-10" style={{ color: SILVER.light }} />
              <div className="flex flex-col gap-2">
                <h3 className="font-display font-bold text-lg text-fg">
                  Join as {truncateAddress(wallet.address)}
                </h3>
                <p className="text-sm text-muted">
                  One click — your wallet is your identity. No forms, no email required.
                </p>
              </div>
              <a href="https://t.me/debrosportal" target="_blank" rel="noopener noreferrer">
                <SilverButton size="lg">
                  Join the Waitlist
                  <ArrowRight className="w-3.5 h-3.5 ml-2" />
                </SilverButton>
              </a>
            </>
          )}
        </div>
      </DashedPanel>

      {stats && (
        <div
          className="flex items-center justify-center gap-2 p-4 rounded-sm"
          style={{ border: `1px dashed ${SILVER.border}`, background: SILVER.bg }}
        >
          <span className="font-mono text-2xl font-bold" style={{ color: SILVER.light }}>
            —
          </span>
          <span className="text-sm text-muted">developers have joined</span>
          <span className="text-xs font-mono text-zinc-600 ml-2">· no limit</span>
        </div>
      )}

      <div className="flex flex-col gap-4">
        <h3 className="text-xs font-mono text-muted tracking-wider uppercase">What You Get</h3>
        <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
          {[
            { icon: Coins, title: "Free Compute Credits", desc: "Waitlist members receive compute credits to deploy apps at no cost when the platform opens." },
            { icon: Clock, title: "Priority Access", desc: "You'll be among the first developers to deploy on the decentralized cloud." },
            { icon: Shield, title: "Direct Support", desc: "Direct line to the core engineering team for onboarding, debugging, and feedback." },
            { icon: Vote, title: "Shape the Product", desc: "Your feedback directly influences what we build next." },
          ].map((card) => (
            <DashedPanel key={card.title} withBackground>
              <div className="flex flex-col gap-2 p-3">
                <card.icon className="w-4 h-4" style={{ color: SILVER.light }} />
                <h4 className="font-display font-bold text-sm text-fg">{card.title}</h4>
                <p className="text-xs text-muted leading-relaxed">{card.desc}</p>
              </div>
            </DashedPanel>
          ))}
        </div>
      </div>

      <div className="flex flex-col gap-4">
        <h3 className="text-xs font-mono text-muted tracking-wider uppercase">What You Can Deploy</h3>
        <div className="grid grid-cols-2 sm:grid-cols-3 gap-3">
          {[
            { icon: Globe, label: "React / Static Sites" },
            { icon: Database, label: "SQL Databases" },
            { icon: Cpu, label: "Go / Node.js APIs" },
            { icon: HardDrive, label: "File Storage (IPFS)" },
            { icon: Layers, label: "In-Memory Cache" },
            { icon: Code, label: "WASM Functions" },
          ].map((s) => (
            <DashedPanel key={s.label} withBackground>
              <div className="flex items-center gap-3 p-3">
                <s.icon className="w-4 h-4 shrink-0" style={{ color: SILVER.light }} />
                <span className="text-sm text-fg font-mono">{s.label}</span>
              </div>
            </DashedPanel>
          ))}
        </div>
      </div>

      <FAQ
        items={[
          { q: "When will I get access?", a: "Waitlist members will be onboarded in batches as we open the platform. Early signups get priority. Join our Telegram (t.me/debrosportal) for updates." },
          { q: "Is it really free?", a: "Yes. The waitlist costs nothing. When the platform opens, there will be a free tier with generous compute limits. Waitlist members get bonus credits on top." },
          { q: "Can I also run a node?", a: "Absolutely. During testnet, anyone can run a node with no staking required and earn $ORAMA block rewards." },
        ]}
      />
    </div>
  );
}

/* ══════════════════════════════════════════════
   SPONSORS TAB
   ══════════════════════════════════════════════ */
const SPONSOR_TIERS = [
  { tier: "Platinum", minInvestment: 25_000, color: "#5CE0D8", benefits: ["Featured on website & whitepaper", "Direct line to core team", "Priority validator set"] },
  { tier: "Gold", minInvestment: 10_000, color: "#FFD700", benefits: ["Listed on sponsors page", "Governance voting power", "Early feature access"] },
  { tier: "Silver", minInvestment: 0, color: "#C0C0C0", benefits: ["Listed on sponsors page", "Community recognition", "Sponsor badge on profile"] },
];

function SponsorsTab({ me: _me }: { me: MeResponse | null }) {
  return (
    <div className="flex flex-col gap-8">
      <div className="flex flex-col gap-2">
        <h2 className="font-display font-bold text-2xl text-fg">Sponsors</h2>
        <p className="text-muted text-sm leading-relaxed max-w-xl">
          Back the Orama Network and earn sponsor recognition. Support development of
          the decentralized cloud.
        </p>
      </div>

      {/* Tier breakdown */}
      <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
        {SPONSOR_TIERS.map((tier) => (
          <DashedPanel key={tier.tier} withBackground className="h-full">
            <div className="flex flex-col gap-4 p-4" style={{ borderLeft: `2px solid ${tier.color}` }}>
              <div className="flex items-center justify-between">
                <span
                  className="px-2.5 py-0.5 text-[10px] font-mono font-bold tracking-widest uppercase rounded-full"
                  style={{ background: `${tier.color}20`, color: tier.color, border: `1px solid ${tier.color}50` }}
                >
                  {tier.tier}
                </span>
                <span className="text-xs font-mono text-muted">
                  {tier.minInvestment > 0 ? "—" : "Any contribution"}
                </span>
              </div>
              <div className="flex flex-col gap-2">
                {tier.benefits.map((b) => (
                  <div key={b} className="flex items-start gap-2">
                    <span className="w-1 h-1 rounded-full bg-accent/50 mt-2 shrink-0" />
                    <span className="text-xs text-muted">{b}</span>
                  </div>
                ))}
              </div>
            </div>
          </DashedPanel>
        ))}
      </div>

      {/* Current sponsors */}
      <div>
        <h3 className="text-xs font-mono text-muted tracking-wider uppercase mb-4">Current Sponsors</h3>
        <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
          <DashedPanel withBackground className="border-[#5CE0D8]/30">
            <div className="flex items-center justify-between p-3">
              <div className="flex items-center gap-3">
                <img src="/images/debrosnet.png" alt="DeBros" className="w-8 h-8 object-contain" />
                <span className="font-display font-bold text-sm text-fg">DeBros</span>
              </div>
              <span className="px-2 py-0.5 text-[10px] font-mono font-bold tracking-widest uppercase rounded-full" style={{ background: "rgba(92,224,216,0.15)", color: "#5CE0D8", border: "1px solid rgba(92,224,216,0.3)" }}>
                Platinum
              </span>
            </div>
          </DashedPanel>
          <DashedPanel withBackground className="border-[#FFD700]/30">
            <div className="flex items-center justify-between p-3">
              <div className="flex items-center gap-3">
                <img src="/images/icxcnika.webp" alt="ICXCNIKA" className="w-8 h-8 object-contain" />
                <span className="font-display font-bold text-sm text-fg">ICXCNIKA</span>
              </div>
              <span className="px-2 py-0.5 text-[10px] font-mono font-bold tracking-widest uppercase rounded-full" style={{ background: "rgba(255,215,0,0.12)", color: "#FFD700", border: "1px solid rgba(255,215,0,0.3)" }}>
                Gold
              </span>
            </div>
          </DashedPanel>
        </div>
      </div>

      {/* Donate */}
      <DashedPanel withBackground withCorners>
        <div className="p-4">
          <DonateSection />
        </div>
      </DashedPanel>
    </div>
  );
}

/* ══════════════════════════════════════════════
   MAIN PAGE
   ══════════════════════════════════════════════ */
export default function Invest() {
  const wallet: WalletState | null = null; // Wallet login coming soon via RootWallet
  const me: MeResponse | null = null;
  const [walletModalOpen, setWalletModalOpen] = useState(false);
  const [searchParams] = useSearchParams();
  const initialTab = (searchParams.get("tab") as Tab) || "overview";
  const [activeTab, setActiveTab] = useState<Tab>(initialTab);
  const [stats, setStats] = useState<Stats | null>(null);

  // Fetch public stats on mount
  useEffect(() => {
    fetchStats().then(setStats).catch(() => {});
    const interval = setInterval(() => {
      fetchStats().then(setStats).catch(() => {});
    }, 30000);
    return () => clearInterval(interval);
  }, []);

  const sidebarItems: { key: Tab; label: string; icon: React.ReactNode }[] = [
    { key: "overview", label: "Overview", icon: <LayoutDashboard className="w-4 h-4" /> },
    { key: "bonding", label: "Bonding Curve", icon: <Coins className="w-4 h-4" /> },
    { key: "license", label: "Node License", icon: <Server className="w-4 h-4" /> },
    { key: "whitelist", label: "Dev Waitlist", icon: <Code className="w-4 h-4" /> },
    { key: "sponsors", label: "Sponsors", icon: <Award className="w-4 h-4" /> },
  ];

  return (
    <Page title="Invest — Orama Network">
      {/* Wallet Connect Modal */}
      <WalletConnectModal
        open={walletModalOpen}
        onOpenChange={setWalletModalOpen}
      />

      <div className="flex min-h-screen">
        {/* ── Desktop Sidebar ── */}
        <aside className="hidden md:flex w-60 shrink-0 border-r border-dashed border-border bg-surface/50 flex-col h-screen sticky top-0 overflow-y-auto">
          <div className="p-4 border-b border-dashed border-border flex flex-col gap-3">
            <Link to="/" className="flex items-center gap-2 group">
              <img src={oramaIcon} alt="Orama" className="h-6 w-6 shrink-0" />
              <span className="font-display text-sm font-bold tracking-widest text-fg">ORAMA</span>
            </Link>
            <WalletButton
              wallet={wallet}
              onOpenModal={() => setWalletModalOpen(true)}
              onDisconnect={() => {}}
            />
          </div>

          <nav className="flex flex-col py-2">
            <span className="px-4 py-2 text-[10px] font-mono text-muted tracking-wider uppercase">
              Invest
            </span>
            {sidebarItems.map((item) => (
              <button
                key={item.key}
                type="button"
                onClick={() => setActiveTab(item.key)}
                className={`flex items-center gap-2.5 px-4 py-2.5 text-xs font-mono tracking-wider uppercase transition-colors cursor-pointer ${
                  activeTab === item.key
                    ? "text-fg bg-white/[0.06] border-r-2"
                    : "text-muted hover:text-fg hover:bg-white/[0.03]"
                }`}
                style={activeTab === item.key ? { borderColor: SILVER.light } : undefined}
              >
                {item.icon}
                {item.label}
              </button>
            ))}

            <div className="border-t border-dashed border-border my-3" />

            <span className="px-4 py-2 text-[10px] font-mono text-muted tracking-wider uppercase">
              Resources
            </span>
            <Link
              to="/whitepaper"
              className="flex items-center gap-2.5 px-4 py-2.5 text-xs font-mono tracking-wider uppercase text-muted hover:text-fg transition-colors"
            >
              <FileText className="w-4 h-4" />
              Whitepaper
            </Link>
            <Link
              to="/docs"
              className="flex items-center gap-2.5 px-4 py-2.5 text-xs font-mono tracking-wider uppercase text-muted hover:text-fg transition-colors"
            >
              <BookOpen className="w-4 h-4" />
              Documentation
            </Link>
            <Link
              to="/blockchain"
              className="flex items-center gap-2.5 px-4 py-2.5 text-xs font-mono tracking-wider uppercase text-muted hover:text-fg transition-colors"
            >
              <Globe className="w-4 h-4" />
              Blockchain
            </Link>
            <Link
              to="/contributors"
              className="flex items-center gap-2.5 px-4 py-2.5 text-xs font-mono tracking-wider uppercase text-muted hover:text-fg transition-colors"
            >
              <Users className="w-4 h-4" />
              Contributors
            </Link>
            <a
              href="https://t.me/debrosportal"
              target="_blank"
              rel="noopener noreferrer"
              className="flex items-center gap-2.5 px-4 py-2.5 text-xs font-mono tracking-wider uppercase text-muted hover:text-fg transition-colors"
            >
              <ExternalLink className="w-4 h-4" />
              Telegram
            </a>
          </nav>

          <div className="mt-auto p-4 border-t border-dashed border-border">
            <Link
              to="/"
              className="flex items-center gap-2 text-xs font-mono text-muted hover:text-fg transition-colors tracking-wider uppercase"
            >
              <ArrowRight className="w-3.5 h-3.5 rotate-180" />
              Back to Main Site
            </Link>
          </div>
        </aside>

        {/* ── Mobile Top Bar ── */}
        <div className="md:hidden fixed top-0 left-0 right-0 z-30 flex flex-col border-b border-dashed border-border bg-bg/95 backdrop-blur-sm">
          <div className="flex items-center justify-between px-4 py-3 border-b border-dashed border-border">
            <Link to="/" className="flex items-center gap-2">
              <img src={oramaIcon} alt="Orama" className="h-5 w-5" />
              <span className="font-display text-xs font-bold tracking-widest text-fg">INVEST</span>
            </Link>
            <WalletButton
              wallet={wallet}
              onOpenModal={() => setWalletModalOpen(true)}
              onDisconnect={() => {}}
            />
          </div>
          <div className="flex overflow-x-auto px-4 py-2 gap-1">
            {sidebarItems.map((item) => (
              <button
                key={item.key}
                type="button"
                onClick={() => setActiveTab(item.key)}
                className={`flex items-center gap-1.5 px-3 py-1.5 text-[10px] font-mono tracking-wider uppercase whitespace-nowrap rounded-sm transition-colors shrink-0 cursor-pointer ${
                  activeTab === item.key ? "text-fg bg-white/[0.06]" : "text-muted"
                }`}
              >
                {item.icon}
                {item.label}
              </button>
            ))}
          </div>
        </div>

        {/* ── Main Content ── */}
        <main className="flex-1 overflow-y-auto pt-24 md:pt-0">
          <div className="max-w-5xl mx-auto px-4 sm:px-6 py-8">
            {/* Stats Bar */}
            <div className="mb-8">
              <StatsBar stats={stats} />
            </div>

            {/* Tab Content */}
            {activeTab === "overview" && (
              <OverviewTab stats={stats} />
            )}
            {activeTab === "bonding" && (
              <BondingCurveTab
                wallet={wallet}
                stats={stats}
                me={me}
              />
            )}
            {activeTab === "license" && (
              <NodeLicenseTab />
            )}
            {activeTab === "whitelist" && (
              <DevWhitelistTab
                wallet={wallet}
                stats={stats}
                me={me}
              />
            )}
            {activeTab === "sponsors" && (
              <SponsorsTab me={me} />
            )}

          </div>
        </main>
      </div>
    </Page>
  );
}
