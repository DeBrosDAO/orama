import { useState, useEffect, useRef } from "react";
import { Link } from "react-router";
import { Rocket, TrendingUp, Clock, Zap, Crown, Users } from "lucide-react";
import { Page } from "../components/layout/page";
import { Section } from "../components/layout/section";
import { SectionHeader } from "../components/ui/section-header";
import { DashedPanel } from "../components/ui/dashed-panel";
import { AnimateIn } from "../components/ui/animate-in";
import { CrosshairDivider } from "../components/ui/crosshair-divider";
import { SILVER, SilverBadge, SilverButton, SilverMetric } from "../components/ui/silver-theme";
import { Redacted } from "../components/ui/redacted";

/* ── Types ── */
interface LaunchToken {
  id: string;
  name: string;
  ticker: string;
  creator: string;
  holders: number;
  txCount: number;
  createdAt: string;
  graduated: boolean;
  emoji: string;
}

/* ── Fake token data (prices/mcaps/bonding redacted) ── */
const INITIAL_TOKENS: LaunchToken[] = [
  { id: "1", name: "OramaSwap", ticker: "OSWAP", creator: "0x7a3b...f29d", holders: 342, txCount: 1847, createdAt: "2m ago", graduated: false, emoji: "\u{1f504}" },
  { id: "2", name: "DeCloud", ticker: "DCLD", creator: "0x91e2...a84c", holders: 891, txCount: 5230, createdAt: "14m ago", graduated: true, emoji: "\u{2601}\u{fe0f}" },
  { id: "3", name: "NodeFi", ticker: "NOFI", creator: "0x3f8d...c11b", holders: 156, txCount: 892, createdAt: "6m ago", graduated: false, emoji: "\u{1f5a5}\u{fe0f}" },
  { id: "4", name: "VaultDAO", ticker: "VDAO", creator: "0xb47a...ee01", holders: 623, txCount: 3102, createdAt: "28m ago", graduated: false, emoji: "\u{1f510}" },
  { id: "5", name: "MeshNet", ticker: "MESH", creator: "0x12dc...77f3", holders: 89, txCount: 412, createdAt: "1h ago", graduated: false, emoji: "\u{1f310}" },
  { id: "6", name: "ProxyShield", ticker: "PRXY", creator: "0xae5b...33d0", holders: 445, txCount: 2190, createdAt: "42m ago", graduated: false, emoji: "\u{1f6e1}\u{fe0f}" },
  { id: "7", name: "StakeMax", ticker: "SMAX", creator: "0xc9f1...b82e", holders: 1203, txCount: 8901, createdAt: "2h ago", graduated: true, emoji: "\u{1f4c8}" },
  { id: "8", name: "AnonRelay", ticker: "ANON", creator: "0x55a0...d91c", holders: 67, txCount: 234, createdAt: "45s ago", graduated: false, emoji: "\u{1f47b}" },
  { id: "9", name: "DataVault", ticker: "DVLT", creator: "0x882f...1a7e", holders: 972, txCount: 6780, createdAt: "3h ago", graduated: true, emoji: "\u{1f4be}" },
  { id: "10", name: "WarpBridge", ticker: "WARP", creator: "0x1d3c...e540", holders: 298, txCount: 1456, createdAt: "8m ago", graduated: false, emoji: "\u{1f300}" },
  { id: "11", name: "ZeroGas", ticker: "ZGAS", creator: "0xf7e8...0c2a", holders: 34, txCount: 89, createdAt: "12s ago", graduated: false, emoji: "\u{26a1}" },
  { id: "12", name: "OracleX", ticker: "ORCX", creator: "0x64b1...9f3d", holders: 534, txCount: 2870, createdAt: "52m ago", graduated: false, emoji: "\u{1f52e}" },
];

const NEW_TOKEN_NAMES = [
  { name: "FluxDAO", ticker: "FLUX", emoji: "\u{26a1}" },
  { name: "NeonSwap", ticker: "NEON", emoji: "\u{1f49c}" },
  { name: "PixelFi", ticker: "PIXL", emoji: "\u{1f3ae}" },
  { name: "ShadowNet", ticker: "SHDW", emoji: "\u{1f311}" },
  { name: "QuantumBit", ticker: "QBIT", emoji: "\u{1f52c}" },
  { name: "SolarDAO", ticker: "SLRD", emoji: "\u{2600}\u{fe0f}" },
  { name: "ChainLink2", ticker: "CLK2", emoji: "\u{1f517}" },
  { name: "MetaVerse", ticker: "MTVS", emoji: "\u{1f30c}" },
];

/* ── Live feed entry ── */
interface FeedEntry {
  id: string;
  type: "launch" | "buy" | "sell" | "graduated";
  token: string;
  ticker: string;
  amount?: string;
  wallet: string;
  time: string;
}

const INITIAL_FEED: FeedEntry[] = [
  { id: "f1", type: "buy", token: "AnonRelay", ticker: "ANON", amount: "*** ORAMA", wallet: "0x7a3b...f29d", time: "just now" },
  { id: "f2", type: "launch", token: "ZeroGas", ticker: "ZGAS", amount: undefined, wallet: "0xf7e8...0c2a", time: "12s ago" },
  { id: "f3", type: "buy", token: "WarpBridge", ticker: "WARP", amount: "*** ORAMA", wallet: "0x91e2...a84c", time: "25s ago" },
  { id: "f4", type: "graduated", token: "StakeMax", ticker: "SMAX", amount: undefined, wallet: "0xc9f1...b82e", time: "1m ago" },
  { id: "f5", type: "sell", token: "NodeFi", ticker: "NOFI", amount: "*** ORAMA", wallet: "0x3f8d...c11b", time: "1m ago" },
  { id: "f6", type: "buy", token: "VaultDAO", ticker: "VDAO", amount: "*** ORAMA", wallet: "0xb47a...ee01", time: "2m ago" },
  { id: "f7", type: "buy", token: "OramaSwap", ticker: "OSWAP", amount: "*** ORAMA", wallet: "0x12dc...77f3", time: "3m ago" },
  { id: "f8", type: "launch", token: "AnonRelay", ticker: "ANON", amount: undefined, wallet: "0x55a0...d91c", time: "3m ago" },
];

function randomWallet(): string {
  const hex = "0123456789abcdef";
  const a = Array.from({ length: 4 }, () => hex[Math.floor(Math.random() * 16)]).join("");
  const b = Array.from({ length: 4 }, () => hex[Math.floor(Math.random() * 16)]).join("");
  return `0x${a}...${b}`;
}

/* ── Bonding Curve Progress Bar ── */
function BondingBar() {
  return (
    <div className="flex items-center gap-2 w-full">
      <div className="flex-1 h-1.5 rounded-full bg-zinc-800 overflow-hidden">
        <div
          className="h-full rounded-full transition-all duration-500"
          style={{
            width: "0%",
            background: `linear-gradient(90deg, ${SILVER.dark}, ${SILVER.light})`,
          }}
        />
      </div>
      <span className="text-[10px] font-mono text-zinc-500 w-8 text-right">
        <Redacted />
      </span>
    </div>
  );
}

/* ── Token Card (pump.fun style) ── */
function TokenCard({ token }: { token: LaunchToken }) {
  return (
    <Link
      to="/dex"
      className="block p-4 transition-all duration-200 hover:bg-white/[0.02]"
      style={{ border: `1px dashed ${SILVER.border}` }}
    >
      <div className="flex flex-col gap-3">
        {/* Header */}
        <div className="flex items-center justify-between">
          <div className="flex items-center gap-2">
            <span className="text-lg">{token.emoji}</span>
            <div>
              <span className="font-display font-semibold text-fg text-sm">{token.name}</span>
              <span className="text-xs font-mono text-zinc-500 ml-2">${token.ticker}</span>
            </div>
          </div>
          {token.graduated ? (
            <span className="flex items-center gap-1 text-[10px] font-mono text-emerald-400">
              <Crown className="w-3 h-3" />
              GRADUATED
            </span>
          ) : (
            <span className="text-[10px] font-mono text-zinc-500">{token.createdAt}</span>
          )}
        </div>

        {/* Price + Change */}
        <div className="flex items-baseline justify-between">
          <span className="font-mono text-lg font-semibold text-fg">
            <Redacted />
          </span>
          <span className="text-sm font-mono font-semibold text-zinc-500">
            <Redacted />
          </span>
        </div>

        {/* Bonding curve */}
        {!token.graduated && <BondingBar />}

        {/* Footer stats */}
        <div className="flex items-center justify-between text-[10px] font-mono text-zinc-500">
          <span>MCap <Redacted /></span>
          <span className="flex items-center gap-1">
            <Users className="w-2.5 h-2.5" />
            {token.holders}
          </span>
          <span>{token.txCount.toLocaleString()} txns</span>
        </div>
      </div>
    </Link>
  );
}

/* ── Live Feed Item ── */
function FeedItem({ entry }: { entry: FeedEntry }) {
  const colors = {
    launch: "text-yellow-400",
    buy: "text-emerald-400",
    sell: "text-red-400",
    graduated: "text-purple-400",
  };
  const labels = {
    launch: "LAUNCHED",
    buy: "BUY",
    sell: "SELL",
    graduated: "GRADUATED",
  };
  const icons = {
    launch: <Rocket className="w-3 h-3" />,
    buy: <TrendingUp className="w-3 h-3" />,
    sell: <TrendingUp className="w-3 h-3 rotate-180" />,
    graduated: <Crown className="w-3 h-3" />,
  };

  return (
    <div className="flex items-center gap-3 px-3 py-2 text-xs font-mono border-b border-zinc-900 last:border-b-0">
      <span className={`flex items-center gap-1 shrink-0 ${colors[entry.type]}`}>
        {icons[entry.type]}
        <span className="w-[72px]">{labels[entry.type]}</span>
      </span>
      <span className="text-fg truncate">{entry.token}</span>
      <span className="text-zinc-600">${entry.ticker}</span>
      {entry.amount && <span className="text-zinc-500 ml-auto shrink-0">{entry.amount}</span>}
      {!entry.amount && <span className="ml-auto" />}
      <span className="text-zinc-600 shrink-0">{entry.time}</span>
    </div>
  );
}

/* ── Page ── */
export default function Launchpad() {
  const [tab, setTab] = useState<"all" | "new" | "graduating" | "graduated">("all");
  const [feed, setFeed] = useState(INITIAL_FEED);
  const [tokens] = useState(INITIAL_TOKENS);
  const feedIdRef = useRef(100);

  // Live feed simulation
  useEffect(() => {
    const interval = setInterval(() => {
      const types: FeedEntry["type"][] = ["buy", "buy", "buy", "sell", "launch"];
      const type = types[Math.floor(Math.random() * types.length)];
      const token = INITIAL_TOKENS[Math.floor(Math.random() * INITIAL_TOKENS.length)];
      const newName = NEW_TOKEN_NAMES[Math.floor(Math.random() * NEW_TOKEN_NAMES.length)];

      const entry: FeedEntry = type === "launch"
        ? {
            id: `f${++feedIdRef.current}`,
            type: "launch",
            token: newName.name,
            ticker: newName.ticker,
            wallet: randomWallet(),
            time: "just now",
          }
        : {
            id: `f${++feedIdRef.current}`,
            type,
            token: token.name,
            ticker: token.ticker,
            amount: "*** ORAMA",
            wallet: randomWallet(),
            time: "just now",
          };

      setFeed((prev) => [entry, ...prev.slice(0, 19)]);
    }, 2500 + Math.random() * 2000);

    return () => clearInterval(interval);
  }, []);

  const filteredTokens = tokens.filter((t) => {
    if (tab === "new") return !t.graduated;
    if (tab === "graduating") return !t.graduated;
    if (tab === "graduated") return t.graduated;
    return true;
  });

  return (
    <Page title="Launchpad">
      {/* ── Hero ─────────────────────────────────────────────── */}
      <Section padding="wide">
        <AnimateIn>
          <div className="flex flex-col items-center text-center gap-6">
            <SilverBadge variant="default" className="w-fit">
              <Zap className="w-3 h-3 mr-1 inline" />
              ORAMA L1 LAUNCHPAD
            </SilverBadge>
            <h1 className="font-display font-bold text-4xl lg:text-5xl leading-tight text-fg">
              Launch. Trade.{" "}
              <span
                style={{
                  background: SILVER.gradient,
                  WebkitBackgroundClip: "text",
                  WebkitTextFillColor: "transparent",
                }}
              >
                Instantly.
              </span>
            </h1>
            <p className="text-muted text-lg leading-relaxed max-w-xl">
              Create a token on Orama L1 in seconds. Bonding curve pricing,
              automatic liquidity, instant DEX listing. No VCs, no gatekeepers.
            </p>

            {/* Live hero stats */}
            <div
              className="flex flex-wrap items-center justify-center gap-6 p-4 rounded-sm"
              style={{
                border: `1px dashed ${SILVER.border}`,
                background: SILVER.bg,
              }}
            >
              <SilverMetric label="Tokens Launched" value={<Redacted />} />
              <div className="w-px h-10 bg-zinc-800 hidden sm:block" />
              <SilverMetric label="Total Volume" value={<Redacted />} />
              <div className="w-px h-10 bg-zinc-800 hidden sm:block" />
              <SilverMetric label="Graduated" value={<Redacted />} />
              <div className="w-px h-10 bg-zinc-800 hidden sm:block" />
              <SilverMetric label="Active Now" value={<Redacted />} />
            </div>

            <div className="flex flex-wrap gap-3 pt-2">
              <a href="#launch">
                <SilverButton size="lg" className="opacity-50 cursor-not-allowed" disabled>
                  <Rocket className="w-4 h-4 mr-2 inline" />
                  Coming Soon
                </SilverButton>
              </a>
              <a href="#tokens">
                <SilverButton variant="ghost" size="lg">
                  Browse Tokens
                </SilverButton>
              </a>
            </div>
          </div>
        </AnimateIn>
      </Section>

      <Section padding="none">
        <CrosshairDivider />
      </Section>

      {/* ── Live Feed + Token Grid ─────────────────────────── */}
      <Section id="tokens">
        <AnimateIn>
          <div className="grid grid-cols-1 lg:grid-cols-4 gap-6">
            {/* Left: Live Activity Feed */}
            <div className="lg:col-span-1">
              <div
                className="rounded-sm overflow-hidden sticky top-24"
                style={{ border: `1px dashed ${SILVER.border}` }}
              >
                <div className="px-3 py-2.5 flex items-center gap-2" style={{ background: SILVER.bg }}>
                  <span className="w-1.5 h-1.5 rounded-full bg-emerald-400 animate-pulse" />
                  <span className="text-xs font-mono text-zinc-400 uppercase tracking-wider">Live Activity</span>
                </div>
                <div className="max-h-[600px] overflow-y-auto">
                  {feed.map((entry) => (
                    <FeedItem key={entry.id} entry={entry} />
                  ))}
                </div>
              </div>
            </div>

            {/* Right: Token Grid */}
            <div className="lg:col-span-3 flex flex-col gap-4">
              {/* Tabs */}
              <div className="flex items-center gap-1" style={{ borderBottom: `1px dashed ${SILVER.border}` }}>
                {(["all", "new", "graduating", "graduated"] as const).map((t) => (
                  <button
                    key={t}
                    type="button"
                    onClick={() => setTab(t)}
                    className={`px-4 py-2.5 text-xs font-mono uppercase tracking-wider transition-colors cursor-pointer ${
                      tab === t
                        ? "text-fg border-b-2"
                        : "text-zinc-500 hover:text-zinc-300"
                    }`}
                    style={tab === t ? { borderColor: SILVER.light } : undefined}
                  >
                    {t === "all" && "All Tokens"}
                    {t === "new" && (
                      <>
                        <Clock className="w-3 h-3 mr-1 inline" />
                        New
                      </>
                    )}
                    {t === "graduating" && (
                      <>
                        <TrendingUp className="w-3 h-3 mr-1 inline" />
                        Graduating
                      </>
                    )}
                    {t === "graduated" && (
                      <>
                        <Crown className="w-3 h-3 mr-1 inline" />
                        Graduated
                      </>
                    )}
                  </button>
                ))}
              </div>

              {/* Grid */}
              <div className="grid grid-cols-1 sm:grid-cols-2 xl:grid-cols-3 gap-0">
                {filteredTokens.map((token) => (
                  <TokenCard key={token.id} token={token} />
                ))}
              </div>

              {filteredTokens.length === 0 && (
                <div className="py-12 text-center text-zinc-500 font-mono text-sm">
                  No tokens in this category yet
                </div>
              )}
            </div>
          </div>
        </AnimateIn>
      </Section>

      <Section padding="none">
        <CrosshairDivider />
      </Section>

      {/* ── Launch Form ────────────────────────────────────── */}
      <Section id="launch">
        <AnimateIn>
          <div className="flex flex-col gap-8">
            <SectionHeader
              title="Launch Your Token"
              subtitle={<>Deploy in seconds. Bonding curve pricing starts at <Redacted />. Graduates to DEX at <Redacted /> market cap.</>}
            />

            <div className="grid grid-cols-1 md:grid-cols-5 gap-8">
              <div className="md:col-span-3">
                <DashedPanel withCorners withBackground>
                  <div className="flex flex-col gap-5">
                    <div>
                      <label className="text-xs font-mono text-muted uppercase tracking-wider mb-2 block">
                        Token Name
                      </label>
                      <input
                        type="text"
                        placeholder="e.g. MyToken"
                        className="bg-transparent border border-dashed p-3 rounded-sm text-fg font-mono text-sm w-full placeholder:text-muted/40 focus:outline-none"
                        style={{ borderColor: SILVER.border }}
                        readOnly
                      />
                    </div>

                    <div className="grid grid-cols-2 gap-4">
                      <div>
                        <label className="text-xs font-mono text-muted uppercase tracking-wider mb-2 block">
                          Ticker
                        </label>
                        <input
                          type="text"
                          placeholder="e.g. MTK"
                          maxLength={5}
                          className="bg-transparent border border-dashed p-3 rounded-sm text-fg font-mono text-sm w-full placeholder:text-muted/40 focus:outline-none"
                          style={{ borderColor: SILVER.border }}
                          readOnly
                        />
                      </div>
                      <div>
                        <label className="text-xs font-mono text-muted uppercase tracking-wider mb-2 block">
                          Initial Buy
                        </label>
                        <input
                          type="text"
                          placeholder="0.5 ORAMA"
                          className="bg-transparent border border-dashed p-3 rounded-sm text-fg font-mono text-sm w-full placeholder:text-muted/40 focus:outline-none"
                          style={{ borderColor: SILVER.border }}
                          readOnly
                        />
                      </div>
                    </div>

                    <div>
                      <label className="text-xs font-mono text-muted uppercase tracking-wider mb-2 block">
                        Description
                      </label>
                      <textarea
                        placeholder="Describe your token..."
                        rows={3}
                        className="bg-transparent border border-dashed p-3 rounded-sm text-fg font-mono text-sm w-full placeholder:text-muted/40 focus:outline-none resize-none"
                        style={{ borderColor: SILVER.border }}
                        readOnly
                      />
                    </div>

                    <SilverButton size="lg" className="w-full opacity-50 cursor-not-allowed" disabled>
                      <Rocket className="w-4 h-4 mr-2 inline" />
                      Coming Soon
                    </SilverButton>

                    <p className="text-xs text-muted text-center">
                      Cost: <Redacted /> $ORAMA &middot; Requires wallet connection
                    </p>
                  </div>
                </DashedPanel>
              </div>

              <div className="md:col-span-2">
                <DashedPanel withCorners withBackground className="h-full">
                  <div className="flex flex-col gap-5">
                    <h3 className="font-display font-semibold text-fg">
                      How It Works
                    </h3>

                    <div className="flex flex-col gap-4">
                      <div className="flex items-start gap-3">
                        <span className="font-mono text-xs text-zinc-500 shrink-0 w-5 h-5 flex items-center justify-center border border-dashed" style={{ borderColor: SILVER.dark }}>1</span>
                        <div>
                          <span className="text-sm text-fg font-semibold block">Create</span>
                          <span className="text-xs text-muted">Pick a name, ticker, and make an initial buy</span>
                        </div>
                      </div>
                      <div className="flex items-start gap-3">
                        <span className="font-mono text-xs text-zinc-500 shrink-0 w-5 h-5 flex items-center justify-center border border-dashed" style={{ borderColor: SILVER.dark }}>2</span>
                        <div>
                          <span className="text-sm text-fg font-semibold block">Trade</span>
                          <span className="text-xs text-muted">Anyone can buy/sell on the bonding curve immediately</span>
                        </div>
                      </div>
                      <div className="flex items-start gap-3">
                        <span className="font-mono text-xs text-zinc-500 shrink-0 w-5 h-5 flex items-center justify-center border border-dashed" style={{ borderColor: SILVER.dark }}>3</span>
                        <div>
                          <span className="text-sm text-fg font-semibold block">Graduate</span>
                          <span className="text-xs text-muted">At <Redacted /> market cap, liquidity auto-deposits to Orama DEX</span>
                        </div>
                      </div>
                    </div>

                    <div className="border-t border-dashed pt-4 flex flex-col gap-2" style={{ borderColor: SILVER.border }}>
                      <span className="text-xs font-mono text-zinc-500">BONDING CURVE</span>
                      <p className="text-xs text-muted leading-relaxed">
                        Every token starts with a bonding curve. Price increases as
                        more people buy. When market cap reaches <Redacted />, all liquidity
                        is deposited into the Orama DEX and the token is freely tradable.
                      </p>
                    </div>
                  </div>
                </DashedPanel>
              </div>
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
              <h2 className="font-display font-bold text-2xl sm:text-3xl text-fg">
                Ready to launch?
              </h2>
              <p className="text-muted max-w-lg leading-relaxed">
                The next 100x token is one click away.
              </p>
              <Link to="/dashboard">
                <SilverButton size="lg">Get Started</SilverButton>
              </Link>
            </div>
          </DashedPanel>
        </AnimateIn>
      </Section>
    </Page>
  );
}
