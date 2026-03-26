import { useState, useEffect, useCallback, useRef } from "react";
import { Link, useSearchParams } from "react-router";
import * as Dialog from "@radix-ui/react-dialog";
import { createChart, AreaSeries } from "lightweight-charts";
import type { IChartApi, Time } from "lightweight-charts";
import { Page } from "../components/layout/page";
import { DashedPanel } from "../components/ui/dashed-panel";
import { SponsorsShowcase } from "../components/landing/sponsors-showcase";
import { SILVER, SilverBadge, SilverButton, SilverMetric } from "../components/ui/silver-theme";
import {
  Wallet,
  Coins,
  Server,
  Code,
  ArrowRight,
  Shield,
  Vote,
  Lock,
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
import {
  CURRENT_STATS,
  formatBTC,
} from "../data/fundraise";
import { Redacted, ComingSoonOverlay } from "../components/ui/redacted";


function generateFundraiseHistory(): { time: Time; value: number }[] {
  const data: { time: Time; value: number }[] = [];
  const target = CURRENT_STATS.total_raised_btc;
  const now = new Date();
  const days = 90;

  for (let i = days; i >= 0; i--) {
    const date = new Date(now);
    date.setDate(date.getDate() - i);
    const dateStr = date.toISOString().split("T")[0] as unknown as Time;

    // Simulate organic growth curve ending at actual total
    const progress = (days - i) / days;
    const curve = Math.pow(progress, 1.8); // Slow start, accelerating
    const noise = 1 + (Math.sin(i * 0.7) * 0.03); // Subtle variation
    const value = target * curve * noise;

    data.push({ time: dateStr, value: parseFloat(Math.min(value, target).toFixed(2)) });
  }
  return data;
}

/* ── Tab type ── */
type Tab = "overview" | "presale" | "license" | "whitelist" | "sponsors";

/* ── Wallet state ── */
interface WalletState {
  address: string;
  chain: "sol" | "evm";
  connected: boolean;
}

/* ── Helper: truncate address ── */
function truncateAddress(addr: string): string {
  if (addr.length <= 12) return addr;
  return `${addr.slice(0, 6)}...${addr.slice(-4)}`;
}

/* ── Helper: format number ── */

function formatNumber(n: number): string {
  return new Intl.NumberFormat("en-US").format(n);
}

/* ── Helper: time ago ── */

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
              <span style={{ color: "#F7931A" }}>BTC</span> Only
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
            style={{ background: wallet.chain === "sol" ? "#9945FF" : "#627EEA" }}
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
      <SilverMetric label="Total Raised" value={<Redacted />} />
      <SilverMetric
        label="Pre-Sale"
        value={<Redacted />}
      />
      <SilverMetric label="Licenses Sold" value={<Redacted />} />
      <SilverMetric label="Developers" value={<Redacted />} />
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
   FUNDRAISE AREA CHART (lightweight-charts)
   ══════════════════════════════════════════════ */
function FundraiseChart() {
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
    series.setData(generateFundraiseHistory());
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
          Real-time fundraise progress across token pre-sale, node licenses, and developer waitlist.
        </p>
      </div>

      {/* Key Metrics Grid */}
      <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4">
        <DashedPanel withBackground>
          <div className="flex flex-col gap-1 p-2">
            <span className="text-[10px] font-mono text-muted tracking-wider uppercase">Total Raised</span>
            <span className="font-display font-bold text-xl text-fg"><Redacted /> <span style={{ color: "#F7931A" }}>BTC</span></span>
            <Redacted />
            <span className="text-[10px] font-mono text-muted">of <Redacted /> BTC target</span>
          </div>
        </DashedPanel>
        <DashedPanel withBackground>
          <div className="flex flex-col gap-1 p-2">
            <span className="text-[10px] font-mono text-muted tracking-wider uppercase">Tokens Sold</span>
            <span className="font-display font-bold text-xl text-fg"><Redacted /></span>
            <span className="text-[10px] font-mono text-muted">of <Redacted /> total</span>
          </div>
        </DashedPanel>
        <DashedPanel withBackground>
          <div className="flex flex-col gap-1 p-2">
            <span className="text-[10px] font-mono text-muted tracking-wider uppercase">Licenses Sold</span>
            <span className="font-display font-bold text-xl text-fg"><Redacted /></span>
            <span className="text-[10px] font-mono text-muted">of <Redacted /> available</span>
          </div>
        </DashedPanel>
        <DashedPanel withBackground>
          <div className="flex flex-col gap-1 p-2">
            <span className="text-[10px] font-mono text-muted tracking-wider uppercase">Developers</span>
            <span className="font-display font-bold text-xl text-fg"><Redacted /></span>
            <span className="text-[10px] font-mono text-muted">on waitlist</span>
          </div>
        </DashedPanel>
      </div>

      {/* Cumulative Fundraise Chart */}
      <DashedPanel withBackground withCorners>
        <div className="flex flex-col gap-4 p-2">
          <div className="flex items-center justify-between">
            <h3 className="text-xs font-mono text-muted tracking-wider uppercase">Cumulative Fundraise</h3>
            <span className="text-xs font-mono text-fg">
              <Redacted /> <span style={{ color: "#F7931A" }}>BTC</span>
            </span>
          </div>
          <ComingSoonOverlay>
            <FundraiseChart />
          </ComingSoonOverlay>
        </div>
      </DashedPanel>

      {/* Progress Bars */}
      <DashedPanel withBackground withCorners>
        <div className="flex flex-col gap-6 p-2">
          <h3 className="text-xs font-mono text-muted tracking-wider uppercase">Fundraise Progress</h3>
          <ProgressBar
            current={0}
            total={1}
            label="Total Fundraise"
            sublabel="Coming Soon"
          />
          <ProgressBar
            current={0}
            total={1}
            label="Token Pre-Sale"
            sublabel="Coming Soon"
          />
          <ProgressBar
            current={0}
            total={1}
            label="Node Licenses"
            sublabel="Coming Soon"
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
              <span className="text-xs font-mono text-muted tracking-wider uppercase">Token Pre-Sale</span>
            </div>
            <p className="text-muted text-xs leading-relaxed">
              Buy $ORAMA at <Redacted /> <span style={{ color: "#F7931A" }}>BTC</span> per token.
            </p>
            <span className="text-xs font-mono text-fg"><Redacted /> tokens remaining</span>
          </div>
        </DashedPanel>
        <DashedPanel withBackground>
          <div className="flex flex-col gap-3 p-2">
            <div className="flex items-center gap-2">
              <Server className="w-4 h-4 text-muted" />
              <span className="text-xs font-mono text-muted tracking-wider uppercase">Node Licenses</span>
            </div>
            <p className="text-muted text-xs leading-relaxed">
              Operate an Orama node. Earn $ORAMA rewards.
            </p>
            <span className="text-xs font-mono text-fg"><Redacted /> licenses remaining</span>
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
            <span className="text-xs font-mono text-fg"><Redacted /> developers joined</span>
          </div>
        </DashedPanel>
      </div>
    </div>
  );
}

/* ══════════════════════════════════════════════
   TOKEN PRE-SALE TAB
   ══════════════════════════════════════════════ */
function TokenPreSaleTab({
  wallet,
  stats,
  me,
}: {
  wallet: WalletState | null;
  stats: Stats | null;
  me: MeResponse | null;
}) {
  const [amount, setAmount] = useState("");
  const _parsedTokens = parseFloat(amount) || 0;
  void _parsedTokens;
  const isConnected = wallet?.connected;

  return (
    <div className="flex flex-col gap-8">
      {/* Headline */}
      <div className="flex flex-col gap-2">
        <h2 className="font-display font-bold text-2xl text-fg">$ORAMA Token Pre-Sale</h2>
        <p className="text-muted text-sm leading-relaxed max-w-xl">
          Buy $ORAMA at the pre-sale price of <Redacted /> BTC per token.
          Tokens will be minted to your wallet when the Orama L1 mainnet launches.
        </p>
      </div>


      {/* Buy + Holdings */}
      <div className="grid grid-cols-1 lg:grid-cols-2 gap-6">
        {/* Buy Panel */}
        <DashedPanel withCorners withBackground>
          <div className="flex flex-col gap-5 p-2">
            <span className="text-xs font-mono text-muted tracking-wider uppercase">
              Buy $ORAMA
            </span>

            <div className="flex flex-col gap-2">
              <label className="text-xs font-mono text-muted">Amount ($ORAMA)</label>
              <input
                type="text"
                inputMode="decimal"
                value={amount}
                onChange={(e) => setAmount(e.target.value)}
                placeholder="e.g. 10000"
                className="w-full px-4 py-3 bg-surface-2 border border-border text-fg text-lg font-mono placeholder:text-muted/40 focus:outline-none focus:border-accent/50 transition-colors rounded-sm"
                disabled={!isConnected}
              />
            </div>

            {_parsedTokens > 0 && (
              <div className="flex flex-col gap-2">
                <div
                  className="flex items-center justify-between p-3 rounded-sm"
                  style={{ border: `1px dashed ${SILVER.border}`, background: SILVER.bg }}
                >
                  <span className="text-xs font-mono text-muted">Cost</span>
                  <span className="font-mono text-lg font-bold text-fg">
                    <Redacted /> <span style={{ color: "#F7931A" }}>BTC</span>
                  </span>
                </div>
                <Redacted />
              </div>
            )}

            <div className="flex items-center gap-2 text-xs font-mono text-muted">
              <span><Redacted /> <span style={{ color: "#F7931A" }}>BTC</span> / token</span>
              <span className="text-zinc-600">·</span>
              <span>Min: <Redacted /> $ORAMA</span>
              <span className="text-zinc-600">·</span>
              <span>Pay with <span style={{ color: "#F7931A" }}>BTC</span></span>
            </div>

            <div className="flex flex-col gap-2">
              <SilverButton
                size="lg"
                className="w-full"
                disabled
              >
                Coming Soon — Pay with RootWallet
              </SilverButton>
              <p className="text-[10px] font-mono text-zinc-600 text-center">
                BTC payments via RootWallet are under development.
              </p>
            </div>
          </div>
        </DashedPanel>

        {/* Holdings Panel */}
        <DashedPanel withCorners withBackground>
          <div className="flex flex-col gap-5 p-2">
            <span className="text-xs font-mono text-muted tracking-wider uppercase">
              Your Holdings
            </span>

            {!isConnected ? (
              <div className="flex flex-col items-center justify-center py-8 gap-3">
                <Wallet className="w-8 h-8 text-zinc-700" />
                <p className="text-sm text-zinc-600 font-mono">Connect wallet to view holdings</p>
              </div>
            ) : (
              <div className="flex flex-col gap-4">
                <div className="grid grid-cols-2 gap-4">
                  <div className="flex flex-col gap-1">
                    <span className="text-xs font-mono text-muted">$ORAMA Purchased</span>
                    <span
                      className="text-2xl font-bold font-mono"
                      style={{
                        background: SILVER.gradient,
                        WebkitBackgroundClip: "text",
                        WebkitTextFillColor: "transparent",
                      }}
                    >
                      {formatNumber(me?.tokens_purchased || 0)}
                    </span>
                  </div>
                  <div className="flex flex-col gap-1">
                    <span className="text-xs font-mono text-muted">Total Invested</span>
                    <span
                      className="text-2xl font-bold font-mono"
                      style={{
                        background: SILVER.gradient,
                        WebkitBackgroundClip: "text",
                        WebkitTextFillColor: "transparent",
                      }}
                    >
                      {formatBTC(me?.tokens_spent || 0)}
                    </span>
                  </div>
                </div>

                {/* Purchase history */}
                {me && me.purchase_history.length > 0 && (
                  <div className="flex flex-col gap-2">
                    <span className="text-xs font-mono text-muted">Purchase History</span>
                    {me.purchase_history
                      .filter((p) => p.type === "token")
                      .map((p, i) => (
                        <div
                          key={i}
                          className="flex items-center justify-between text-xs font-mono p-2 rounded-sm"
                          style={{ border: `1px dashed ${SILVER.border}` }}
                        >
                          <span className="text-fg">{formatBTC(p.amount)}</span>
                          <span className="text-muted">{p.currency}</span>
                          <span className="text-zinc-600">{truncateAddress(p.tx_hash)}</span>
                        </div>
                      ))}
                  </div>
                )}

                {me && me.purchase_history.filter((p) => p.type === "token").length === 0 && (
                  <p className="text-sm text-zinc-600 font-mono py-4 text-center">
                    No purchases yet
                  </p>
                )}
              </div>
            )}
          </div>
        </DashedPanel>
      </div>

      {/* Progress */}
      {stats && (
        <ProgressBar
          current={0}
          total={1}
          label="Tokens sold"
          sublabel="Coming Soon"
        />
      )}

      {/* What You Get */}
      <div className="flex flex-col gap-4">
        <h3 className="text-xs font-mono text-muted tracking-wider uppercase">
          What Exactly You Get
        </h3>
        <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-4">
          {[
            {
              icon: TrendingUp,
              title: "Upside at Launch",
              desc: "You buy at a discounted pre-sale price per token.",
            },
            {
              icon: Vote,
              title: "Governance Rights",
              desc: "1 token = 1 vote. You get to vote on network proposals, protocol upgrades, and treasury decisions from day one.",
            },
            {
              icon: Coins,
              title: "Staking Rewards",
              desc: "Once mainnet launches, stake your $ORAMA to earn a share of network revenue. Higher stake = higher multiplier.",
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

      {/* How It Works */}
      <DashedPanel withCorners withBackground>
        <div className="flex flex-col gap-4 p-2">
          <h3 className="text-xs font-mono text-muted tracking-wider uppercase">
            How It Works — Step by Step
          </h3>
          <div className="flex flex-col gap-4">
            {[
              {
                step: "1",
                title: "You pay BTC",
                desc: "Your payment goes directly to our treasury wallet. The transaction is recorded on-chain (Solana or Ethereum).",
              },
              {
                step: "2",
                title: "We record your allocation",
                desc: "There is no $ORAMA token on-chain yet. We record how many tokens you purchased in our database, linked to your wallet address.",
              },
              {
                step: "3",
                title: "Vesting begins at mainnet launch",
                desc: "When the Orama L1 blockchain launches, your tokens are minted. Vesting terms are coming soon.",
              },
              {
                step: "4",
                title: "Use your tokens",
                desc: "Once vested, trade on Orama DEX, stake for rewards, vote on governance proposals, or pay for compute services.",
              },
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

          {/* Vesting timeline visual */}
          <div
            className="flex flex-col gap-2 p-3 rounded-sm mt-2"
            style={{ border: `1px dashed ${SILVER.border}`, background: SILVER.bg }}
          >
            <span className="text-xs font-mono text-muted">Vesting Timeline</span>
            <div className="flex items-center gap-1 h-6">
              <div className="flex-1 h-full rounded-sm bg-zinc-800 flex items-center justify-center">
                <span className="text-[9px] font-mono text-zinc-500">Coming Soon</span>
              </div>
            </div>
            <div className="flex items-center justify-between text-[9px] font-mono text-zinc-600">
              <span>Vesting details TBA</span>
            </div>
          </div>
        </div>
      </DashedPanel>

      {/* FAQ */}
      <FAQ
        items={[
          {
            q: "Is there a token on-chain right now?",
            a: "No. $ORAMA does not exist on any blockchain yet. When you buy in the pre-sale, we record your allocation in our database linked to your wallet address. Tokens will be minted on the Orama L1 blockchain when it launches.",
          },
          {
            q: "When does mainnet launch?",
            a: "Target is 2028. The network is currently live on devnet with 50+ nodes across devnet and testnet. Progress is transparent — follow our changelog and GitHub.",
          },
          {
            q: "What if mainnet is delayed?",
            a: "Your allocation is recorded permanently. If mainnet is delayed, your tokens vest later but your allocation is guaranteed. Total supply details coming soon.",
          },
          {
            q: "Can I buy more later?",
            a: "Yes, as long as tokens remain in the pre-sale allocation. The price stays fixed until the allocation is sold out.",
          },
          {
            q: "Is there a maximum I can buy?",
            a: "No maximum per wallet. Minimum purchase details coming soon.",
          },
          {
            q: "Where can I verify my allocation?",
            a: "Connect the same wallet you used to purchase. Your holdings and transaction history are shown on this page. All payment transactions are verifiable on-chain (Solana or Ethereum).",
          },
        ]}
      />

    </div>
  );
}

/* ══════════════════════════════════════════════
   NODE LICENSE TAB
   ══════════════════════════════════════════════ */
function NodeLicenseTab({
  wallet,
  stats,
  me,
}: {
  wallet: WalletState | null;
  stats: Stats | null;
  me: MeResponse | null;
}) {
  const isConnected = wallet?.connected;
  const licenses = me?.licenses || [];

  return (
    <div className="flex flex-col gap-8">
      {/* Headline */}
      <div className="flex flex-col gap-2">
        <h2 className="font-display font-bold text-2xl text-fg">Node License</h2>
        <p className="text-muted text-sm leading-relaxed max-w-xl">
          Purchase the right to operate an Orama Network node. Earn $ORAMA rewards
          for every request your node serves.
        </p>
      </div>


      {/* Buy + Holdings */}
      <div className="grid grid-cols-1 lg:grid-cols-2 gap-6">
        {/* Buy Panel */}
        <DashedPanel withCorners withBackground>
          <div className="flex flex-col gap-5 p-2">
            <span className="text-xs font-mono text-muted tracking-wider uppercase">
              Purchase License
            </span>

            {/* License card visual */}
            <div
              className="relative overflow-hidden rounded-sm p-6 flex flex-col items-center gap-4"
              style={{
                background: `linear-gradient(135deg, rgba(228,228,231,0.08) 0%, rgba(161,161,170,0.04) 100%)`,
                border: `1px solid ${SILVER.border}`,
              }}
            >
              <div className="absolute top-3 right-3">
                <SilverBadge variant="outline">NODE LICENSE</SilverBadge>
              </div>
              <div
                className="w-16 h-16 rounded-lg flex items-center justify-center"
                style={{ border: `2px dashed ${SILVER.mid}` }}
              >
                <Server className="w-8 h-8" style={{ color: SILVER.light }} />
              </div>
              <div className="text-center">
                <span
                  className="font-display font-bold text-3xl"
                  style={{
                    background: SILVER.gradient,
                    WebkitBackgroundClip: "text",
                    WebkitTextFillColor: "transparent",
                  }}
                >
                  <Redacted /> <span style={{ color: "#F7931A" }}>BTC</span>
                </span>
                <Redacted />
                <p className="text-[10px] font-mono text-zinc-500 mt-0.5">One-time purchase</p>
              </div>
            </div>

            <div className="flex items-center gap-2 text-xs font-mono text-muted">
              <span>Pay with <span style={{ color: "#F7931A" }}>BTC</span></span>
              <span className="text-zinc-600">·</span>
              <span><Redacted /> <span style={{ color: "#F7931A" }}>BTC</span> per license</span>
            </div>


            <SilverButton
              size="lg"
              className="w-full"
              disabled
            >
              Coming Soon — Pay with RootWallet
            </SilverButton>
          </div>
        </DashedPanel>

        {/* Your Licenses */}
        <DashedPanel withCorners withBackground>
          <div className="flex flex-col gap-5 p-2">
            <span className="text-xs font-mono text-muted tracking-wider uppercase">
              Your Licenses
            </span>

            {!isConnected ? (
              <div className="flex flex-col items-center justify-center py-8 gap-3">
                <Server className="w-8 h-8 text-zinc-700" />
                <p className="text-sm text-zinc-600 font-mono">Connect wallet to view licenses</p>
              </div>
            ) : licenses.length === 0 ? (
              <div className="flex flex-col items-center justify-center py-8 gap-3">
                <Server className="w-8 h-8 text-zinc-700" />
                <p className="text-sm text-zinc-600 font-mono">No licenses yet</p>
              </div>
            ) : (
              <div className="flex flex-col gap-3">
                {licenses.map((l) => (
                  <div
                    key={l.license_number}
                    className="flex items-center justify-between p-4 rounded-sm"
                    style={{
                      border: `1px solid ${SILVER.border}`,
                      background: "rgba(228,228,231,0.04)",
                    }}
                  >
                    <div className="flex items-center gap-3">
                      <Server className="w-5 h-5" style={{ color: SILVER.light }} />
                      <div>
                        <span className="font-mono text-sm font-bold text-fg">
                          License #{l.license_number}
                        </span>
                        {l.claimed_via_nft && (
                          <SilverBadge variant="outline" className="ml-2 text-[9px]">
                            NFT CLAIM
                          </SilverBadge>
                        )}
                      </div>
                    </div>
                    <span className="text-xs font-mono text-muted">{l.purchased_at}</span>
                  </div>
                ))}
              </div>
            )}
          </div>
        </DashedPanel>
      </div>

      {/* Progress */}
      {stats && (
        <ProgressBar
          current={0}
          total={1}
          label="Licenses sold"
          sublabel="Coming Soon"
        />
      )}

      {/* What You Get */}
      <DashedPanel withCorners withBackground>
        <div className="flex flex-col gap-5 p-2">
          <h3 className="text-xs font-mono text-muted tracking-wider uppercase">
            What Exactly You Get
          </h3>

          <div className="flex flex-col gap-4">
            {[
              {
                icon: Server,
                title: "Right to Operate a Node",
                desc: "You are purchasing the right to run one Orama Network node. This is the license — not the hardware. Think of it like a taxi medallion: you need it to operate.",
              },
              {
                icon: Coins,
                title: "Earn $ORAMA",
                desc: "Your node earns $ORAMA tokens for every compute request, database query, and file served. The built-in Orama Proxy provides onion-routed privacy for all traffic.",
              },
              {
                icon: TrendingUp,
                title: "Staking Multipliers",
                desc: "Rewards scale with how much $ORAMA you stake. Higher tiers unlock higher multipliers and governance power.",
              },
              {
                icon: Cpu,
                title: "Hardware Options",
                desc: "Option A: Orama One (Q2 2026) — a pre-built plug-and-play hardware node. Compact, silent, always-on. No configuration needed. Option B: Self-hosted VPS — run on your own server (Ubuntu, 2 CPU, 4GB RAM minimum).",
              },
              {
                icon: Lock,
                title: "Transferable & Resellable",
                desc: "Your license is yours. You can resell it on secondary markets at any time. It's tied to your wallet, transferable via a simple on-chain transaction once mainnet launches.",
              },
              {
                icon: Shield,
                title: "Priority Mainnet Access",
                desc: "License holders are first in line when mainnet launches (target 2028). You'll be earning while others are still waiting for access.",
              },
            ].map((item) => (
              <div key={item.title} className="flex gap-3">
                <item.icon className="w-4 h-4 mt-0.5 shrink-0" style={{ color: SILVER.light }} />
                <div>
                  <h4 className="font-display font-bold text-sm text-fg">{item.title}</h4>
                  <p className="text-xs text-muted leading-relaxed mt-1">{item.desc}</p>
                </div>
              </div>
            ))}
          </div>

          {/* Staking tiers table */}
          <div
            className="mt-2 rounded-sm overflow-hidden"
            style={{ border: `1px dashed ${SILVER.border}` }}
          >
            <div className="grid grid-cols-3 text-xs font-mono text-muted uppercase tracking-wider p-3 border-b border-dashed border-border">
              <span>Tier</span>
              <span>Stake Required</span>
              <span>Multiplier</span>
            </div>
            {[
              { tier: "Base", stake: "***", mult: "***" },
              { tier: "Enhanced", stake: "***", mult: "***" },
              { tier: "Governor", stake: "***", mult: "***" },
            ].map((row) => (
              <div
                key={row.tier}
                className="grid grid-cols-3 text-sm p-3 border-b border-border/50 last:border-b-0"
              >
                <span className="text-fg font-semibold">{row.tier}</span>
                <span className="text-muted font-mono text-xs">{row.stake}</span>
                <span
                  className="font-mono font-bold"
                  style={{ color: SILVER.light }}
                >
                  {row.mult}
                </span>
              </div>
            ))}
          </div>
        </div>
      </DashedPanel>

      {/* How It Works */}
      <DashedPanel withCorners withBackground>
        <div className="flex flex-col gap-4 p-2">
          <h3 className="text-xs font-mono text-muted tracking-wider uppercase">
            What Happens After You Purchase
          </h3>
          {[
            { step: "1", title: "Pay with BTC", desc: "Transaction recorded on-chain. Your license is registered immediately." },
            { step: "2", title: "Choose your hardware", desc: "Wait for Orama One to be announced to you, or set up a VPS yourself using our setup guide." },
            { step: "3", title: "Your node joins the network", desc: "Once connected, your node starts serving compute, storage, and bandwidth to developers using the network." },
            { step: "4", title: "Earn daily", desc: "Rewards are calculated daily based on uptime, bandwidth contributed, and compute served. Paid in $ORAMA tokens." },
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
            q: "Do I need technical skills to run a node?",
            a: "No — if you wait for Orama One, it's literally plug-in and go. If you want to self-host on a VPS, basic Linux terminal knowledge is helpful but our setup guide walks you through everything.",
          },
          {
            q: "What are the hardware requirements for self-hosting?",
            a: "Minimum: Ubuntu 22.04+, 2 CPU cores, 4GB RAM, 50GB SSD, static IP. Recommended: 4 CPU, 8GB RAM, 100GB SSD.",
          },
          {
            q: "When does my node start earning?",
            a: "As soon as it's online and serving traffic. On devnet, nodes are earning reputation and uptime scores now. Monetary rewards begin at mainnet launch.",
          },
          {
            q: "Can I run multiple nodes with one license?",
            a: "No. One license = one node. If you want to run multiple nodes, purchase multiple licenses.",
          },
          {
            q: "What if my node goes offline?",
            a: "You stop earning during downtime. Extended downtime reduces your reputation score, which affects future reward allocation. There is no slashing or penalty — you just miss rewards.",
          },
        ]}
      />
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
      {/* Headline */}
      <div className="flex flex-col gap-2">
        <h2 className="font-display font-bold text-2xl text-fg">Developer Waitlist</h2>
        <p className="text-muted text-sm leading-relaxed max-w-xl">
          Get early access to the decentralized cloud. Free — no purchase required. Join with your
          wallet to reserve your spot.
        </p>
      </div>

      {/* Join Panel */}
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

      {/* Stats */}
      {stats && (
        <div
          className="flex items-center justify-center gap-2 p-4 rounded-sm"
          style={{ border: `1px dashed ${SILVER.border}`, background: SILVER.bg }}
        >
          <span className="font-mono text-2xl font-bold" style={{ color: SILVER.light }}>
            <Redacted />
          </span>
          <span className="text-sm text-muted">developers have joined</span>
          <span className="text-xs font-mono text-zinc-600 ml-2">· no limit</span>
        </div>
      )}

      {/* What You Get */}
      <div className="flex flex-col gap-4">
        <h3 className="text-xs font-mono text-muted tracking-wider uppercase">
          What You Get
        </h3>
        <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
          {[
            {
              icon: Coins,
              title: "Free Compute Credits",
              desc: "Waitlist members receive compute credits to deploy apps at no cost when the platform opens.",
            },
            {
              icon: Clock,
              title: "Priority Access",
              desc: "You'll be among the first developers to deploy on the decentralized cloud — before public launch.",
            },
            {
              icon: Shield,
              title: "Direct Support",
              desc: "Direct line to the core engineering team for onboarding, debugging, and feedback.",
            },
            {
              icon: Vote,
              title: "Shape the Product",
              desc: "Your feedback directly influences what we build next. Early members have outsized impact.",
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

      {/* What You Can Deploy */}
      <div className="flex flex-col gap-4">
        <h3 className="text-xs font-mono text-muted tracking-wider uppercase">
          What You Can Deploy
        </h3>
        <div className="grid grid-cols-2 sm:grid-cols-3 gap-3">
          {[
            { icon: Globe, label: "React / Static Sites" },
            { icon: Database, label: "SQL Databases" },
            { icon: Cpu, label: "Go / Node.js APIs" },
            { icon: HardDrive, label: "File Storage (IPFS)" },
            { icon: Layers, label: "In-Memory Cache" },
            { icon: Code, label: "Serverless Functions" },
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

      {/* FAQ */}
      <FAQ
        items={[
          {
            q: "When will I get access?",
            a: "Waitlist members will be onboarded in batches as we open the platform. Early signups get priority. Join our Telegram (t.me/debrosportal) for updates.",
          },
          {
            q: "Is it really free?",
            a: "Yes. The waitlist costs nothing. When the platform opens, there will be a free tier with generous compute limits. Waitlist members get bonus credits on top.",
          },
          {
            q: "Can I also buy tokens or a license?",
            a: "Absolutely. Switch to the Token Pre-Sale or Node License tab above. Many people join the waitlist AND invest.",
          },
        ]}
      />
    </div>
  );
}

const SPONSOR_TIERS = [
  { tier: "Platinum", minInvestment: 25_000, color: "#5CE0D8", benefits: ["Featured on website & whitepaper", "Direct line to core team", "Priority validator set"] },
  { tier: "Gold", minInvestment: 10_000, color: "#FFD700", benefits: ["Listed on sponsors page", "Governance voting power", "Early feature access"] },
  { tier: "Silver", minInvestment: 0, color: "#C0C0C0", benefits: ["Listed on sponsors page", "Community recognition", "Sponsor badge on profile"] },
];

function getUserTier(me: MeResponse | null): string {
  if (!me) return "none";
  const totalInvested = me.tokens_spent + me.licenses.length * 3000;
  if (totalInvested >= 25_000) return "Platinum";
  if (totalInvested >= 10_000) return "Gold";
  if (totalInvested > 0) return "Silver";
  return "none";
}

function SponsorsTab({ me }: { me: MeResponse | null }) {
  const userTier = getUserTier(me);

  return (
    <div className="flex flex-col gap-8">
      <div className="flex flex-col gap-2">
        <h2 className="font-display font-bold text-2xl text-fg">Sponsors</h2>
        <p className="text-muted text-sm leading-relaxed max-w-xl">
          Back the Orama Network and earn sponsor recognition. Your tier is determined by
          your total investment across token pre-sale and node licenses.
        </p>
      </div>

      {/* User's current tier */}
      {me && (
        <DashedPanel withCorners withBackground>
          <div className="flex items-center justify-between p-4">
            <div>
              <span className="text-xs font-mono text-muted tracking-wider uppercase">Your Sponsor Tier</span>
              <p className="font-display font-bold text-xl text-fg mt-1">
                {userTier === "none" ? "Not yet a sponsor" : userTier}
              </p>
              {userTier === "none" && (
                <p className="text-xs text-muted mt-1">Make your first investment to become a Silver sponsor.</p>
              )}
            </div>
            {userTier !== "none" && (
              <span
                className="px-3 py-1 text-xs font-mono font-bold tracking-widest uppercase rounded-full"
                style={{
                  background: `${SPONSOR_TIERS.find((t) => t.tier === userTier)?.color}20`,
                  color: SPONSOR_TIERS.find((t) => t.tier === userTier)?.color,
                  border: `1px solid ${SPONSOR_TIERS.find((t) => t.tier === userTier)?.color}50`,
                }}
              >
                {userTier}
              </span>
            )}
          </div>
        </DashedPanel>
      )}

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
                  {tier.minInvestment > 0 ? <Redacted /> : "Any investment"}
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

      {/* How to qualify table */}
      <DashedPanel withBackground>
        <div className="p-4">
          <h3 className="text-xs font-mono text-muted tracking-wider uppercase mb-3">How to Qualify</h3>
          <div className="overflow-x-auto">
            <table className="w-full text-left">
              <thead>
                <tr className="border-b border-dashed border-border">
                  <th className="text-xs font-mono text-muted tracking-wider uppercase py-3 pr-4">Tier</th>
                  <th className="text-xs font-mono text-muted tracking-wider uppercase py-3 pr-4">Min. Investment</th>
                  <th className="text-xs font-mono text-muted tracking-wider uppercase py-3">Equivalent</th>
                </tr>
              </thead>
              <tbody>
                <tr className="border-b border-border/50">
                  <td className="text-sm py-3 pr-4" style={{ color: "#5CE0D8" }}>Platinum</td>
                  <td className="text-sm text-fg py-3 pr-4 font-mono"><Redacted /></td>
                  <td className="text-sm text-muted py-3"><Redacted /></td>
                </tr>
                <tr className="border-b border-border/50">
                  <td className="text-sm py-3 pr-4" style={{ color: "#FFD700" }}>Gold</td>
                  <td className="text-sm text-fg py-3 pr-4 font-mono"><Redacted /></td>
                  <td className="text-sm text-muted py-3"><Redacted /></td>
                </tr>
                <tr className="border-b border-border/50">
                  <td className="text-sm py-3 pr-4" style={{ color: "#C0C0C0" }}>Silver</td>
                  <td className="text-sm text-fg py-3 pr-4 font-mono">Any amount</td>
                  <td className="text-sm text-muted py-3">Any token or license purchase</td>
                </tr>
              </tbody>
            </table>
          </div>
        </div>
      </DashedPanel>

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
    { key: "presale", label: "Token Pre-Sale", icon: <Coins className="w-4 h-4" /> },
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
          {/* Header: Logo + Wallet */}
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

          {/* Navigation */}
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

          {/* Back to site */}
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
            {activeTab === "presale" && (
              <TokenPreSaleTab
                wallet={wallet}
                stats={stats}
                me={me}
              />
            )}
            {activeTab === "license" && (
              <NodeLicenseTab
                wallet={wallet}
                stats={stats}
                me={me}
              />
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
