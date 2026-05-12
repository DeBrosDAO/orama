import { useRef, useEffect, useCallback } from "react";
import { Link } from "react-router";
import {
  Coins,
  Vote,
  Server,
  ExternalLink,
  ArrowRight,
  Rocket,
  TrendingUp,
  Flame,
} from "lucide-react";
import { createChart, CandlestickSeries } from "lightweight-charts";
import type { CandlestickData, IChartApi, Time } from "lightweight-charts";
import { ComingSoonOverlay } from "../components/ui/redacted";
import { Page } from "../components/layout/page";
import { Section } from "../components/layout/section";
import { SectionHeader } from "../components/ui/section-header";
import { DashedPanel } from "../components/ui/dashed-panel";
import { FeatureCard } from "../components/ui/feature-card";
import { CrosshairDivider } from "../components/ui/crosshair-divider";
import { AnimateIn } from "../components/ui/animate-in";
import { SILVER, SilverBadge, SilverButton, SilverMetric } from "../components/ui/silver-theme";

/* ── Animated metallic particle canvas ── */
function MetallicCanvas() {
  const canvasRef = useRef<HTMLCanvasElement>(null);

  useEffect(() => {
    const canvas = canvasRef.current;
    if (!canvas) return;
    const ctx = canvas.getContext("2d");
    if (!ctx) return;

    let animationId: number;
    let time = 0;

    interface Particle {
      x: number;
      y: number;
      vx: number;
      vy: number;
      size: number;
      opacity: number;
      shimmer: number;
    }

    let particles: Particle[] = [];
    let w = 0;
    let h = 0;

    const initParticles = () => {
      w = canvas.offsetWidth;
      h = canvas.offsetHeight;
      const count = Math.floor((w * h) / 6000);
      particles = Array.from({ length: count }, () => ({
        x: Math.random() * w,
        y: Math.random() * h,
        vx: (Math.random() - 0.5) * 0.3,
        vy: (Math.random() - 0.5) * 0.3,
        size: 0.5 + Math.random() * 2,
        opacity: 0.02 + Math.random() * 0.1,
        shimmer: Math.random() * Math.PI * 2,
      }));
    };

    const resize = () => {
      const dpr = window.devicePixelRatio || 1;
      canvas.width = canvas.offsetWidth * dpr;
      canvas.height = canvas.offsetHeight * dpr;
      ctx.scale(dpr, dpr);
      initParticles();
    };
    resize();
    window.addEventListener("resize", resize);

    const draw = () => {
      ctx.clearRect(0, 0, w, h);
      time += 0.004;

      for (const p of particles) {
        p.x += p.vx;
        p.y += p.vy;
        if (p.x < 0 || p.x > w) p.vx *= -1;
        if (p.y < 0 || p.y > h) p.vy *= -1;
        const shimmer = Math.sin(time * 3 + p.shimmer) * 0.5 + 0.5;
        const alpha = p.opacity * (0.4 + shimmer * 0.6);
        const bright = shimmer > 0.9 ? 255 : 180 + shimmer * 60;
        ctx.fillStyle = `rgba(${bright}, ${bright}, ${bright + 5}, ${alpha})`;
        ctx.beginPath();
        ctx.arc(p.x, p.y, p.size * (0.8 + shimmer * 0.4), 0, Math.PI * 2);
        ctx.fill();
      }

      for (let i = 0; i < particles.length; i++) {
        for (let j = i + 1; j < particles.length; j++) {
          const dx = particles[i].x - particles[j].x;
          const dy = particles[i].y - particles[j].y;
          const dist = Math.sqrt(dx * dx + dy * dy);
          if (dist < 80) {
            const alpha = (1 - dist / 80) * 0.04;
            ctx.strokeStyle = `rgba(200, 200, 210, ${alpha})`;
            ctx.lineWidth = 0.5;
            ctx.beginPath();
            ctx.moveTo(particles[i].x, particles[i].y);
            ctx.lineTo(particles[j].x, particles[j].y);
            ctx.stroke();
          }
        }
      }

      for (let i = 0; i < 3; i++) {
        const radius = ((time * 40 + i * 100) % 300);
        const opacity = Math.max(0, 1 - radius / 300) * 0.05;
        ctx.strokeStyle = `rgba(200, 200, 210, ${opacity})`;
        ctx.lineWidth = 0.5;
        ctx.beginPath();
        ctx.arc(w / 2, h / 2, radius, 0, Math.PI * 2);
        ctx.stroke();
      }

      const spacing = 50;
      for (let x = spacing; x < w; x += spacing) {
        for (let y = spacing; y < h; y += spacing) {
          const dist = Math.sqrt((x - w / 2) ** 2 + (y - h / 2) ** 2);
          const wave = Math.sin(dist * 0.008 - time * 2) * 0.5 + 0.5;
          const opacity = wave * 0.08;
          ctx.fillStyle = `rgba(200, 200, 210, ${opacity})`;
          ctx.beginPath();
          ctx.arc(x, y, 0.8, 0, Math.PI * 2);
          ctx.fill();
        }
      }

      animationId = requestAnimationFrame(draw);
    };
    draw();

    return () => {
      window.removeEventListener("resize", resize);
      cancelAnimationFrame(animationId);
    };
  }, []);

  return (
    <canvas
      ref={canvasRef}
      className="absolute inset-0 w-full h-full pointer-events-none"
      aria-hidden="true"
    />
  );
}

/* ── Generate dummy OHLC data ── */
function generateCandlestickData(): CandlestickData<Time>[] {
  const data: CandlestickData<Time>[] = [];
  let price = 0.42;
  const now = new Date();

  for (let i = 180; i >= 0; i--) {
    const date = new Date(now);
    date.setDate(date.getDate() - i);
    const dateStr = date.toISOString().split("T")[0] as unknown as Time;

    const volatility = 0.03 + Math.random() * 0.05;
    const trend = Math.sin(i * 0.03) * 0.01 + 0.002;
    const change = (Math.random() - 0.48) * volatility + trend;

    const open = price;
    const close = price * (1 + change);
    const high = Math.max(open, close) * (1 + Math.random() * 0.02);
    const low = Math.min(open, close) * (1 - Math.random() * 0.02);

    data.push({
      time: dateStr,
      open: parseFloat(open.toFixed(4)),
      high: parseFloat(high.toFixed(4)),
      low: parseFloat(low.toFixed(4)),
      close: parseFloat(close.toFixed(4)),
    });

    price = close;
  }
  return data;
}

/* ── Candlestick Chart ── */
function OramaChart() {
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
      height: 420,
      layout: {
        background: { color: "transparent" },
        textColor: "#71717a",
        fontFamily: "JetBrains Mono, monospace",
        fontSize: 11,
      },
      grid: {
        vertLines: { color: "rgba(161, 161, 170, 0.06)" },
        horzLines: { color: "rgba(161, 161, 170, 0.06)" },
      },
      crosshair: {
        vertLine: { color: "rgba(161, 161, 170, 0.3)", labelBackgroundColor: "#27272a" },
        horzLine: { color: "rgba(161, 161, 170, 0.3)", labelBackgroundColor: "#27272a" },
      },
      rightPriceScale: {
        borderColor: "rgba(161, 161, 170, 0.1)",
      },
      timeScale: {
        borderColor: "rgba(161, 161, 170, 0.1)",
        timeVisible: false,
      },
    });

    const candlestickSeries = chart.addSeries(CandlestickSeries, {
      upColor: "#c8c8d0",
      downColor: "#3f3f46",
      borderUpColor: "#d4d4d8",
      borderDownColor: "#52525b",
      wickUpColor: "#a1a1aa",
      wickDownColor: "#52525b",
    });

    candlestickSeries.setData(generateCandlestickData());
    chart.timeScale().fitContent();
    chartRef.current = chart;
  }, []);

  useEffect(() => {
    initChart();

    const handleResize = () => {
      if (chartRef.current && containerRef.current) {
        chartRef.current.applyOptions({
          width: containerRef.current.clientWidth,
        });
      }
    };
    window.addEventListener("resize", handleResize);

    return () => {
      window.removeEventListener("resize", handleResize);
      if (chartRef.current) {
        chartRef.current.remove();
        chartRef.current = null;
      }
    };
  }, [initChart]);

  return (
    <div
      ref={containerRef}
      className="w-full rounded-sm"
      style={{
        border: `1px dashed ${SILVER.border}`,
        background: "rgba(10, 10, 10, 0.5)",
      }}
    />
  );
}

/* ── Data ── */
const UTILITY_CARDS = [
  {
    icon: <Coins className="w-5 h-5" />,
    title: "Staking (Consensus)",
    description:
      "Stake $ORAMA to validate blocks. Effective Power = Stake x (1 + Contribution Score) x Infrastructure Multiplier. Minimum 1,000 $ORAMA at mainnet.",
  },
  {
    icon: <Vote className="w-5 h-5" />,
    title: "Governance (25% cap)",
    description:
      "Token holders get 25% of governance power with quadratic voting (sqrt of tokens held). NFT holders control the other 75%. No whale capture.",
  },
  {
    icon: <Flame className="w-5 h-5" />,
    title: "Gas (Base Burned)",
    description:
      "All fees paid in $ORAMA. Base fee is burned permanently (deflationary). Priority fee goes to block proposer. EIP-1559 style congestion pricing.",
  },
  {
    icon: <Server className="w-5 h-5" />,
    title: "Node Operations",
    description:
      "Lock $ORAMA while running a node. OramaOS = 1.5x multiplier. Contribution score rewards real work: uptime (40%), bandwidth (30%), compute (20%), reliability (10%).",
  },
];

/* ── Page ── */
export default function Token() {
  return (
    <Page title="$ORAMA Token">
      {/* ── Hero ─────────────────────────────────────────────── */}
      <Section padding="wide">
        <div className="relative min-h-[75vh] flex items-center justify-center overflow-hidden">
          <MetallicCanvas />

          <div className="relative flex flex-col items-center text-center gap-8 z-10">
            <div className="relative">
              <span
                className="token-symbol text-[8rem] lg:text-[12rem] font-mono font-bold leading-none select-none"
                style={{
                  background:
                    "linear-gradient(180deg, #e4e4e7 0%, #a1a1aa 40%, #71717a 80%, #52525b 100%)",
                  WebkitBackgroundClip: "text",
                  WebkitTextFillColor: "transparent",
                  filter: "drop-shadow(0 0 60px rgba(200, 200, 210, 0.15))",
                }}
              >
                $ORAMA
              </span>
              <div className="absolute inset-0 token-symbol-glow" />
            </div>

            <div className="flex flex-col items-center gap-3">
              <h1 className="font-display font-bold text-4xl lg:text-5xl text-fg tracking-tight">
                $ORAMA Token
              </h1>
              <p className="font-mono text-base text-zinc-500 tracking-wider max-w-md">
                210,000,000 hard cap. Zero pre-mine. 100% earned through mining.
                The native token of the Orama L1 blockchain.
              </p>
            </div>

            <div className="flex flex-wrap items-center justify-center gap-2">
              <SilverBadge variant="outline">210,000,000 TOTAL SUPPLY</SilverBadge>
              <SilverBadge variant="outline">100% MINED</SilverBadge>
              <SilverBadge variant="outline">BTC-ONLY ECONOMY</SilverBadge>
            </div>

            <div className="flex flex-wrap items-center justify-center gap-3 pt-2">
              <Link to="/dex" className="opacity-50 pointer-events-none">
                <SilverButton size="lg">Coming Soon</SilverButton>
              </Link>
              <Link to="/launchpad">
                <SilverButton variant="ghost" size="lg">
                  Launch a Coin
                </SilverButton>
              </Link>
            </div>
          </div>
        </div>
      </Section>

      <Section padding="none">
        <CrosshairDivider />
      </Section>

      {/* ── Live Price Chart ────────────────────────────────── */}
      <Section>
        <AnimateIn>
          <div className="flex flex-col gap-6">
            <div className="flex items-center justify-between">
              <div className="flex flex-col gap-2">
                <div className="flex items-center gap-4">
                  <h2 className="font-display text-xl font-bold text-fg whitespace-nowrap tracking-tight">
                    $ORAMA / BTC
                  </h2>
                  <div className="flex-1 border-t border-dashed" style={{ borderColor: SILVER.border }} />
                </div>
                <p className="text-sm text-zinc-500">
                  Live price chart — candlestick view
                </p>
              </div>
              <div className="flex items-center gap-3">
                <SilverBadge variant="status">
                  <TrendingUp className="w-3 h-3 mr-1.5" />
                  LIVE
                </SilverBadge>
              </div>
            </div>

            <div
              className="grid grid-cols-2 sm:grid-cols-4 gap-4 p-4 rounded-sm"
              style={{
                border: `1px dashed ${SILVER.border}`,
                background: SILVER.bg,
              }}
            >
              <SilverMetric label="Price (BTC)" value="—" />
              <SilverMetric label="24h Change" value="—" />
              <SilverMetric label="Circulating Supply" value="—" />
              <SilverMetric label="24h Volume" value="—" />
            </div>

            <ComingSoonOverlay>
              <OramaChart />
            </ComingSoonOverlay>
          </div>
        </AnimateIn>
      </Section>

      <Section padding="none">
        <CrosshairDivider />
      </Section>

      {/* ── Token Utility ────────────────────────────────────── */}
      <Section>
        <AnimateIn>
          <div className="flex flex-col gap-8">
            <SectionHeader
              title="Token Utility"
              subtitle="$ORAMA powers every layer of the network — staking, governance, gas, and node operations."
            />
            <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-0">
              {UTILITY_CARDS.map((card) => (
                <FeatureCard
                  key={card.title}
                  icon={card.icon}
                  title={card.title}
                  description={card.description}
                />
              ))}
            </div>
          </div>
        </AnimateIn>
      </Section>

      <Section padding="none">
        <CrosshairDivider />
      </Section>

      {/* ── Emission Schedule (replaces allocation pie chart + vesting) ── */}
      <Section>
        <AnimateIn>
          <div className="flex flex-col gap-8">
            <SectionHeader
              title="Emission Schedule"
              subtitle="210,000,000 $ORAMA — 100% mined. Bitcoin-style halving every 2 years."
            />

            <DashedPanel withCorners withBackground>
              <div className="p-4">
                <div className="overflow-x-auto">
                  <table className="w-full text-left">
                    <thead>
                      <tr className="border-b border-dashed border-border">
                        <th className="text-xs font-mono text-muted tracking-wider uppercase py-3 pr-4">Era</th>
                        <th className="text-xs font-mono text-muted tracking-wider uppercase py-3 pr-4">Years</th>
                        <th className="text-xs font-mono text-muted tracking-wider uppercase py-3 pr-4">Block Reward</th>
                        <th className="text-xs font-mono text-muted tracking-wider uppercase py-3 pr-4">Annual Emission</th>
                        <th className="text-xs font-mono text-muted tracking-wider uppercase py-3">Cumulative</th>
                      </tr>
                    </thead>
                    <tbody>
                      {[
                        { era: "1", years: "1-2", reward: "100 $ORAMA", annual: "~52.5M", cumulative: "~105M (50%)" },
                        { era: "2", years: "3-4", reward: "50 $ORAMA", annual: "~26.25M", cumulative: "~157.5M (75%)" },
                        { era: "3", years: "5-6", reward: "25 $ORAMA", annual: "~13.1M", cumulative: "~183.7M (87.5%)" },
                        { era: "4", years: "7-8", reward: "12.5 $ORAMA", annual: "~6.6M", cumulative: "~196.9M (93.8%)" },
                        { era: "5", years: "9-10", reward: "6.25 $ORAMA", annual: "~3.3M", cumulative: "~203.5M (96.9%)" },
                        { era: "6+", years: "11+", reward: "Halving continues", annual: "Decreasing", cumulative: "Approaches 210M" },
                      ].map((row) => (
                        <tr key={row.era} className="border-b border-border/50">
                          <td className="text-sm text-fg py-3 pr-4 font-mono font-bold">{row.era}</td>
                          <td className="text-sm text-muted py-3 pr-4">{row.years}</td>
                          <td className="text-sm text-fg py-3 pr-4 font-mono">{row.reward}</td>
                          <td className="text-sm text-muted py-3 pr-4 font-mono">{row.annual}</td>
                          <td className="text-sm text-fg py-3 font-mono">{row.cumulative}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
                <p className="text-xs text-muted mt-4 leading-relaxed">
                  Block reward split: 80% to block proposer, 20% to protocol bonding curve (capped at 21M tokens).
                  Once the curve has 21M tokens, miners receive 100%. 1 $ORAMA = 1,000,000 rays.
                </p>
              </div>
            </DashedPanel>
          </div>
        </AnimateIn>
      </Section>

      <Section padding="none">
        <CrosshairDivider />
      </Section>

      {/* ── Fee Schedule ── */}
      <Section>
        <AnimateIn>
          <div className="flex flex-col gap-8">
            <SectionHeader
              title="Fee Schedule"
              subtitle="All fees in $ORAMA (rays). Base fee burned. Priority fee to block proposer."
            />

            <DashedPanel withCorners withBackground>
              <div className="p-4">
                <div className="overflow-x-auto">
                  <table className="w-full text-left">
                    <thead>
                      <tr className="border-b border-dashed border-border">
                        <th className="text-xs font-mono text-muted tracking-wider uppercase py-3 pr-4">Operation</th>
                        <th className="text-xs font-mono text-muted tracking-wider uppercase py-3">Cost</th>
                      </tr>
                    </thead>
                    <tbody>
                      {[
                        { op: "$ORAMA / BTC transfer", cost: "1,000 rays (0.001 $ORAMA)" },
                        { op: "WASM contract execution", cost: "1,000 rays per 1M instructions" },
                        { op: "SQL query", cost: "500 rays" },
                        { op: "IPFS storage", cost: "10,000 rays per MB" },
                        { op: "KV store read/write", cost: "200 rays" },
                        { op: "Private transaction (zk-SNARK)", cost: "4x the public equivalent" },
                        { op: "DEX order book trade", cost: "1,000 rays" },
                      ].map((row) => (
                        <tr key={row.op} className="border-b border-border/50">
                          <td className="text-sm text-fg py-3 pr-4">{row.op}</td>
                          <td className="text-sm text-muted py-3 font-mono">{row.cost}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
                <p className="text-xs text-muted mt-4 leading-relaxed">
                  Congestion multiplier: EIP-1559 style. 1x at 50% block capacity (~500 tx), up to 10x at full capacity (1,000 tx).
                  Fee schedule is governance-adjustable to keep the network affordable as $ORAMA appreciates.
                </p>
              </div>
            </DashedPanel>
          </div>
        </AnimateIn>
      </Section>

      <Section padding="none">
        <CrosshairDivider />
      </Section>

      {/* ── Effective Power Formula ── */}
      <Section>
        <AnimateIn>
          <div className="flex flex-col gap-8">
            <SectionHeader
              title="Effective Power"
              subtitle="A node's power is more than just stake. Real work matters."
            />

            <DashedPanel withCorners withBackground>
              <div className="p-4 flex flex-col gap-6">
                <code className="text-lg text-fg font-mono block text-center py-4">
                  Effective Power = Staked $ORAMA x (1 + Contribution Score) x Infrastructure Multiplier
                </code>

                <div className="grid grid-cols-1 sm:grid-cols-3 gap-4">
                  <div className="flex flex-col gap-2 p-3" style={{ border: `1px dashed ${SILVER.border}` }}>
                    <span className="text-xs font-mono text-muted tracking-wider uppercase">Proof of Stake</span>
                    <p className="text-sm text-muted">Classic staking for economic security. Min 1,000 $ORAMA at mainnet.</p>
                  </div>
                  <div className="flex flex-col gap-2 p-3" style={{ border: `1px dashed ${SILVER.border}` }}>
                    <span className="text-xs font-mono text-muted tracking-wider uppercase">Proof of Contribution</span>
                    <p className="text-sm text-muted">Real work measured on-chain every epoch. Uptime 40%, bandwidth 30%, compute 20%, reliability 10%.</p>
                  </div>
                  <div className="flex flex-col gap-2 p-3" style={{ border: `1px dashed ${SILVER.border}` }}>
                    <span className="text-xs font-mono text-muted tracking-wider uppercase">Proof of Infrastructure</span>
                    <p className="text-sm text-muted">OramaOS = 1.5x multiplier (TPM-verified). Without OramaOS = 1.0x. No faking allowed.</p>
                  </div>
                </div>
              </div>
            </DashedPanel>
          </div>
        </AnimateIn>
      </Section>

      <Section padding="none">
        <CrosshairDivider />
      </Section>

      {/* ── Blockchain Specs ───────────────────────────────────── */}
      <Section>
        <AnimateIn>
          <div className="flex flex-col gap-8">
            <SectionHeader
              title="Orama L1 Blockchain"
              subtitle="Hybrid PoS + PoC + PoI consensus. Purpose-built for decentralized cloud."
            />

            <div className="grid grid-cols-2 sm:grid-cols-4 gap-4">
              {[
                { label: "Consensus", value: "Hybrid PoS+PoC+PoI" },
                { label: "Block Time", value: "6 seconds" },
                { label: "Finality", value: "1-hour BFT" },
                { label: "Genesis Validators", value: "300+" },
              ].map((m) => (
                <div
                  key={m.label}
                  className="p-4"
                  style={{
                    border: `1px dashed ${SILVER.border}`,
                    background: SILVER.bg,
                  }}
                >
                  <SilverMetric label={m.label} value={m.value} />
                </div>
              ))}
            </div>

            <DashedPanel withCorners withBackground>
              <p className="text-muted leading-relaxed text-sm">
                The Orama L1 is a standalone blockchain combining the security and scarcity
                of Bitcoin with a full decentralized cloud. It handles staking, BTC bridging,
                native order book trading, WASM smart contracts, distributed SQL, KV store,
                IPFS, serverless compute, and per-transaction privacy via PLONK zk-SNARKs.
                Every node operator runs a validator. BFT checkpoints finalize each epoch
                with two-thirds of Effective Power.
              </p>
            </DashedPanel>
          </div>
        </AnimateIn>
      </Section>

      <Section padding="none">
        <CrosshairDivider />
      </Section>

      {/* ── Trade on Orama DEX ─────────────────────────────── */}
      <Section>
        <AnimateIn>
          <div className="flex flex-col gap-8">
            <SectionHeader
              title="Trade on Orama DEX"
              subtitle="Protocol-native order book. One pair: $ORAMA/BTC. No AMM — pure price discovery."
            />

            <DashedPanel withCorners withBackground>
              <div className="flex flex-col sm:flex-row items-start sm:items-center justify-between gap-6">
                <div className="flex flex-col gap-3 max-w-lg">
                  <div className="flex items-center gap-3">
                    <div
                      className="w-10 h-10 border border-dashed flex items-center justify-center"
                      style={{ borderColor: SILVER.mid }}
                    >
                      <ArrowRight className="w-5 h-5" style={{ color: SILVER.light }} />
                    </div>
                    <span className="font-display font-semibold text-fg text-lg">
                      Orama DEX
                    </span>
                    <SilverBadge variant="outline">ORDER BOOK</SilverBadge>
                  </div>
                  <p className="text-sm text-muted leading-relaxed">
                    The native order book on Orama L1. Place limit orders, market orders,
                    or cancel anytime. The bonding curve provides a guaranteed liquidity backstop.
                    Custom tokens trade against $ORAMA via permissionless WASM DEX contracts.
                  </p>
                </div>
                <Link to="/dex">
                  <SilverButton size="lg">Go to DEX</SilverButton>
                </Link>
              </div>
            </DashedPanel>
          </div>
        </AnimateIn>
      </Section>

      <Section padding="none">
        <CrosshairDivider />
      </Section>

      {/* ── Launch on Orama ────────────────────────────────── */}
      <Section>
        <AnimateIn>
          <div className="flex flex-col gap-8">
            <SectionHeader
              title="Launch on Orama"
              subtitle="Deploy your own token as a WASM smart contract. Trades against $ORAMA."
            />

            <DashedPanel withCorners withBackground>
              <div className="flex flex-col sm:flex-row items-start sm:items-center justify-between gap-6">
                <div className="flex flex-col gap-3 max-w-lg">
                  <div className="flex items-center gap-3">
                    <div
                      className="w-10 h-10 border border-dashed flex items-center justify-center"
                      style={{ borderColor: SILVER.mid }}
                    >
                      <Rocket className="w-5 h-5" style={{ color: SILVER.light }} />
                    </div>
                    <span className="font-display font-semibold text-fg text-lg">
                      Orama Launchpad
                    </span>
                    <SilverBadge variant="outline">WASM</SilverBadge>
                  </div>
                  <p className="text-sm text-muted leading-relaxed">
                    Launch your own token on Orama as a WASM smart contract. Bonding curve pricing,
                    automatic liquidity bootstrapping, and trading against $ORAMA.
                    No VCs, no gatekeepers. Cost in $ORAMA (rays).
                  </p>
                </div>
                <Link to="/launchpad">
                  <SilverButton size="lg">Go to Launchpad</SilverButton>
                </Link>
              </div>
            </DashedPanel>
          </div>
        </AnimateIn>
      </Section>

      <Section padding="none">
        <CrosshairDivider />
      </Section>

      {/* ── Trade $ORAMA (single link — native DEX only) ──── */}
      <Section>
        <AnimateIn>
          <div className="flex flex-col gap-8">
            <SectionHeader
              title="Trade $ORAMA"
              subtitle="Available on the protocol-native order book."
            />

            <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
              <Link to="/dex">
                <DashedPanel
                  withCorners
                  withBackground
                  className="hover:border-fg/30 transition-colors"
                >
                  <div className="flex flex-col gap-3">
                    <div className="flex items-center justify-between">
                      <span className="font-display font-semibold text-fg text-lg">
                        Orama DEX
                      </span>
                      <SilverBadge variant="outline">NATIVE</SilverBadge>
                    </div>
                    <p className="text-sm text-muted">
                      Trade $ORAMA/BTC on the protocol-native order book.
                      No intermediaries, no AMM. Pure price discovery.
                    </p>
                    <span
                      className="inline-flex items-center gap-1.5 text-xs font-mono"
                      style={{ color: SILVER.light }}
                    >
                      Trade on Orama DEX
                      <ArrowRight className="w-3.5 h-3.5" />
                    </span>
                  </div>
                </DashedPanel>
              </Link>

              <DashedPanel
                withCorners
                withBackground
              >
                <div className="flex flex-col gap-3">
                  <div className="flex items-center justify-between">
                    <span className="font-display font-semibold text-fg text-lg">
                      Bonding Curve
                    </span>
                    <SilverBadge variant="outline">PROTOCOL</SilverBadge>
                  </div>
                  <p className="text-sm text-muted">
                    The protocol bonding curve is a guaranteed liquidity backstop.
                    Price = k x sqrt(tokens_sold). Max 21M tokens. BTC flows to protocol reserve.
                  </p>
                  <span
                    className="inline-flex items-center gap-1.5 text-xs font-mono"
                    style={{ color: SILVER.light }}
                  >
                    Coming Soon
                    <ExternalLink className="w-3.5 h-3.5" />
                  </span>
                </div>
              </DashedPanel>
            </div>
          </div>
        </AnimateIn>
      </Section>

      <Section padding="none">
        <CrosshairDivider />
      </Section>

      {/* ── CTA ──────────────────────────────────────────────── */}
      <Section>
        <AnimateIn>
          <DashedPanel withCorners withBackground>
            <div className="flex flex-col items-center text-center gap-6 py-4">
              <SilverBadge variant="outline">EARN $ORAMA</SilverBadge>
              <h2 className="font-display font-bold text-2xl sm:text-3xl text-fg">
                Start Earning $ORAMA
              </h2>
              <p className="text-muted max-w-lg leading-relaxed">
                Run a node, mine tokens, or start building. 100% of $ORAMA is earned
                through mining — the only fair way.
              </p>
              <div className="flex flex-wrap justify-center gap-3">
                <Link to="/operators">
                  <SilverButton size="lg">Become an Operator</SilverButton>
                </Link>
                <Link to="/dashboard">
                  <SilverButton variant="ghost" size="lg">
                    Start Building
                  </SilverButton>
                </Link>
              </div>
            </div>
          </DashedPanel>
        </AnimateIn>
      </Section>
    </Page>
  );
}
