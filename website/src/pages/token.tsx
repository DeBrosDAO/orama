import { useRef, useEffect, useCallback } from "react";
import { Link } from "react-router";
import {
  Coins,
  Vote,
  CreditCard,
  Server,
  ExternalLink,
  ArrowRight,
  Rocket,
  TrendingUp,
} from "lucide-react";
import { createChart, CandlestickSeries } from "lightweight-charts";
import type { CandlestickData, IChartApi, Time } from "lightweight-charts";
import { Redacted, ComingSoonOverlay } from "../components/ui/redacted";
import { Page } from "../components/layout/page";
import { Section } from "../components/layout/section";
import { SectionHeader } from "../components/ui/section-header";
import { DashedPanel } from "../components/ui/dashed-panel";
import { FeatureCard } from "../components/ui/feature-card";
import { CrosshairDivider } from "../components/ui/crosshair-divider";
import { AnimateIn } from "../components/ui/animate-in";
import { SpecTable } from "../components/ui/spec-table";
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

      /* Floating metallic particles with shimmer */
      for (const p of particles) {
        p.x += p.vx;
        p.y += p.vy;

        if (p.x < 0 || p.x > w) p.vx *= -1;
        if (p.y < 0 || p.y > h) p.vy *= -1;

        const shimmer =
          Math.sin(time * 3 + p.shimmer) * 0.5 + 0.5;
        const alpha = p.opacity * (0.4 + shimmer * 0.6);

        /* Silver-white color with occasional bright flash */
        const bright = shimmer > 0.9 ? 255 : 180 + shimmer * 60;
        ctx.fillStyle = `rgba(${bright}, ${bright}, ${bright + 5}, ${alpha})`;
        ctx.beginPath();
        ctx.arc(p.x, p.y, p.size * (0.8 + shimmer * 0.4), 0, Math.PI * 2);
        ctx.fill();
      }

      /* Subtle connection lines between nearby particles */
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

      /* Radial pulse rings */
      for (let i = 0; i < 3; i++) {
        const radius = ((time * 40 + i * 100) % 300);
        const opacity = Math.max(0, 1 - radius / 300) * 0.05;
        ctx.strokeStyle = `rgba(200, 200, 210, ${opacity})`;
        ctx.lineWidth = 0.5;
        ctx.beginPath();
        ctx.arc(w / 2, h / 2, radius, 0, Math.PI * 2);
        ctx.stroke();
      }

      /* Grid dots */
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
    title: "Staking & Rewards",
    description:
      "Stake $ORAMA to share network revenue. Operators lock tokens while running nodes to earn based on uptime and contributions.",
  },
  {
    icon: <Vote className="w-5 h-5" />,
    title: "Governance",
    description:
      "Vote on network proposals, protocol upgrades, and treasury decisions. Your stake is your voice.",
  },
  {
    icon: <CreditCard className="w-5 h-5" />,
    title: "Payments",
    description:
      "Pay for compute, storage, and bandwidth. Accepted alongside ETH and SOL.",
  },
  {
    icon: <Server className="w-5 h-5" />,
    title: "Node Operations",
    description:
      "Lock $ORAMA while running a node. Higher stake = higher reward multiplier.",
  },
];

const ALLOCATIONS = [
  { label: "Node Rewards", pct: 20, color: SILVER.light },
  { label: "Development", pct: 20, color: SILVER.mid },
  { label: "Community & Ecosystem", pct: 20, color: SILVER.dark },
  { label: "Team & Advisors", pct: 20, color: "#52525b" },
  { label: "Liquidity", pct: 20, color: "#3f3f46" },
];

const VESTING_ROWS = [
  { label: "Node Rewards", value: "***" },
  { label: "Development", value: "***" },
  { label: "Community", value: "***" },
  { label: "Team & Advisors", value: "***" },
  { label: "Liquidity", value: "***" },
];

function buildConicGradient() {
  let cumulative = 0;
  const stops: string[] = [];
  for (const alloc of ALLOCATIONS) {
    stops.push(
      `${alloc.color} ${cumulative}deg ${cumulative + alloc.pct * 3.6}deg`,
    );
    cumulative += alloc.pct * 3.6;
  }
  return `conic-gradient(${stops.join(", ")})`;
}

/* ── Page ── */
export default function Token() {
  return (
    <Page title="$ORAMA Token">
      {/* ── Hero ─────────────────────────────────────────────── */}
      <Section padding="wide">
        <div className="relative min-h-[75vh] flex items-center justify-center overflow-hidden">
          <MetallicCanvas />

          <div className="relative flex flex-col items-center text-center gap-8 z-10">
            {/* Token symbol */}
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
                <Redacted length={5} />
              </span>
              <div className="absolute inset-0 token-symbol-glow" />
            </div>

            <div className="flex flex-col items-center gap-3">
              <h1 className="font-display font-bold text-4xl lg:text-5xl text-fg tracking-tight">
                $ORAMA Token
              </h1>
              <p className="font-mono text-base text-zinc-500 tracking-wider max-w-md">
                The native token of the Orama L1 blockchain. Stake, govern, pay,
                and earn across the decentralized cloud.
              </p>
            </div>

            <div className="flex flex-wrap items-center justify-center gap-2">
              <SilverBadge variant="outline"><Redacted /> TOTAL SUPPLY</SilverBadge>
              <SilverBadge variant="outline">L1 BLOCKCHAIN</SilverBadge>
              <SilverBadge variant="status">LIVE</SilverBadge>
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
                    $ORAMA / USD
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

            {/* Price stats bar */}
            <div
              className="grid grid-cols-2 sm:grid-cols-4 gap-4 p-4 rounded-sm"
              style={{
                border: `1px dashed ${SILVER.border}`,
                background: SILVER.bg,
              }}
            >
              <SilverMetric label="Price" value={<Redacted />} />
              <SilverMetric label="24h Change" value={<Redacted />} />
              <SilverMetric label="Market Cap" value={<Redacted />} />
              <SilverMetric label="24h Volume" value={<Redacted />} />
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
              subtitle="$ORAMA powers every layer of the network — from staking and governance to payments and node operations."
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

      {/* ── Tokenomics — Allocation Chart ────────────────────── */}
      <Section>
        <AnimateIn>
          <div className="flex flex-col gap-8">
            <SectionHeader
              title="Tokenomics"
              subtitle={<>
                <Redacted /> $ORAMA — fixed supply, no inflation.
              </>}
            />

            <ComingSoonOverlay>
              <div className="grid grid-cols-1 md:grid-cols-2 gap-8 items-center">
                {/* Donut chart */}
                <div className="flex justify-center">
                  <div className="relative w-64 h-64 sm:w-72 sm:h-72">
                    <div
                      className="absolute inset-0 rounded-full"
                      style={{ background: buildConicGradient() }}
                    />
                    <div className="absolute inset-[25%] rounded-full bg-bg flex items-center justify-center">
                      <div className="text-center">
                        <span
                          className="font-display text-2xl font-bold block"
                          style={{
                            background: SILVER.gradient,
                            WebkitBackgroundClip: "text",
                            WebkitTextFillColor: "transparent",
                          }}
                        >
                          <Redacted />
                        </span>
                        <span className="font-mono text-xs text-zinc-500">
                          Total Supply
                        </span>
                      </div>
                    </div>
                  </div>
                </div>

                {/* Allocation legend */}
                <div className="flex flex-col gap-0">
                  {ALLOCATIONS.map((alloc) => (
                    <div
                      key={alloc.label}
                      className="p-4"
                      style={{
                        border: `1px dashed ${SILVER.border}`,
                      }}
                    >
                      <div className="flex items-center justify-between">
                        <div className="flex items-center gap-3">
                          <div
                            className="w-3 h-3 rounded-sm shrink-0"
                            style={{ backgroundColor: alloc.color }}
                          />
                          <span className="text-sm text-fg">{alloc.label}</span>
                        </div>
                        <span
                          className="font-mono text-sm font-semibold"
                          style={{ color: SILVER.light }}
                        >
                          <Redacted />
                        </span>
                      </div>
                    </div>
                  ))}
                </div>
              </div>
            </ComingSoonOverlay>
          </div>
        </AnimateIn>
      </Section>

      <Section padding="none">
        <CrosshairDivider />
      </Section>

      {/* ── Vesting Schedule ─────────────────────────────────── */}
      <Section>
        <AnimateIn>
          <div className="flex flex-col gap-8">
            <SectionHeader
              title="Vesting Schedule"
              subtitle="All allocations vest over time to align long-term incentives."
            />
            <SpecTable rows={VESTING_ROWS} />
          </div>
        </AnimateIn>
      </Section>

      <Section padding="none">
        <CrosshairDivider />
      </Section>

      {/* ── Blockchain ───────────────────────────────────────── */}
      <Section>
        <AnimateIn>
          <div className="flex flex-col gap-8">
            <SectionHeader
              title="Orama L1 Blockchain"
              subtitle="Purpose-built for decentralized cloud coordination."
            />

            <div className="grid grid-cols-2 sm:grid-cols-4 gap-4">
              {[
                { label: "Consensus", value: "PoS" },
                { label: "Block Time", value: "~2s" },
                { label: "Finality", value: "Instant" },
                { label: "Validators", value: "100+" },
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
                The Orama L1 is a Proof-of-Stake blockchain optimized for
                infrastructure coordination. It handles staking, governance
                votes, service-level agreements, and payment settlement. Every
                node operator runs a validator, and every transaction is
                finalized in seconds with near-zero fees. The chain is not a
                general-purpose smart contract platform — it is built
                specifically to coordinate decentralized cloud resources.
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
              subtitle="Swap tokens directly on the Orama L1 chain with zero intermediaries and near-zero fees."
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
                    <SilverBadge variant="status">LIVE</SilverBadge>
                  </div>
                  <p className="text-sm text-muted leading-relaxed">
                    The native decentralized exchange on Orama L1. Trade $ORAMA
                    and bridged assets with instant settlement. No
                    order books, no custodians — just on-chain swaps powered by
                    automated market making.
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
              subtitle="Deploy your own token on Orama L1 with built-in liquidity and instant trading."
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
                    <SilverBadge variant="outline">NEW</SilverBadge>
                  </div>
                  <p className="text-sm text-muted leading-relaxed">
                    Launch your own L2 token on Orama with a single transaction.
                    Bonding curve pricing, automatic liquidity bootstrapping,
                    and instant DEX listing — no VCs, no gatekeepers.
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

      {/* ── DEX Links ────────────────────────────────────────── */}
      <Section>
        <AnimateIn>
          <div className="flex flex-col gap-8">
            <SectionHeader
              title="Trade $ORAMA"
              subtitle="Available on decentralized exchanges."
            />

            <div className="grid grid-cols-1 sm:grid-cols-3 gap-4">
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
                      <SilverBadge variant="status">LIVE</SilverBadge>
                    </div>
                    <p className="text-sm text-muted">
                      Swap any token for $ORAMA directly on the Orama L1 chain.
                      Zero intermediaries.
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

              <a
                href="https://aerodrome.finance"
                target="_blank"
                rel="noopener noreferrer"
              >
                <DashedPanel
                  withCorners
                  withBackground
                  className="hover:border-fg/30 transition-colors"
                >
                  <div className="flex flex-col gap-3">
                    <div className="flex items-center justify-between">
                      <span className="font-display font-semibold text-fg text-lg">
                        Aerodrome
                      </span>
                      <SilverBadge variant="status">LIVE</SilverBadge>
                    </div>
                    <p className="text-sm text-muted">
                      Trade $ORAMA on Base via Aerodrome Finance, the leading
                      DEX on the Base network.
                    </p>
                    <span
                      className="inline-flex items-center gap-1.5 text-xs font-mono"
                      style={{ color: SILVER.light }}
                    >
                      Trade on Aerodrome
                      <ExternalLink className="w-3.5 h-3.5" />
                    </span>
                  </div>
                </DashedPanel>
              </a>

              <a
                href="https://app.uniswap.org"
                target="_blank"
                rel="noopener noreferrer"
              >
                <DashedPanel
                  withCorners
                  withBackground
                  className="hover:border-fg/30 transition-colors"
                >
                  <div className="flex flex-col gap-3">
                    <div className="flex items-center justify-between">
                      <span className="font-display font-semibold text-fg text-lg">
                        Uniswap
                      </span>
                      <SilverBadge variant="status">LIVE</SilverBadge>
                    </div>
                    <p className="text-sm text-muted">
                      Swap $ORAMA on Ethereum mainnet via Uniswap, the most
                      widely used decentralized exchange.
                    </p>
                    <span
                      className="inline-flex items-center gap-1.5 text-xs font-mono"
                      style={{ color: SILVER.light }}
                    >
                      Trade on Uniswap
                      <ExternalLink className="w-3.5 h-3.5" />
                    </span>
                  </div>
                </DashedPanel>
              </a>
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
                Run a node, stake tokens, or start building. The network rewards
                everyone who contributes.
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
