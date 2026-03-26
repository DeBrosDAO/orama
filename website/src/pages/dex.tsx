import { useState, useRef, useEffect } from "react";
import { ArrowUpDown, ExternalLink } from "lucide-react";
import { Page } from "../components/layout/page";
import { Section } from "../components/layout/section";
import { SectionHeader } from "../components/ui/section-header";
import { DashedPanel } from "../components/ui/dashed-panel";
import { AnimateIn } from "../components/ui/animate-in";
import { CrosshairDivider } from "../components/ui/crosshair-divider";
import { SILVER, SilverBadge, SilverButton, SilverMetric } from "../components/ui/silver-theme";
import { Redacted } from "../components/ui/redacted";

/* ── Token logos (inline SVG) ── */
function OramaLogo({ size = 20 }: { size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 32 32" fill="none">
      <circle cx="16" cy="16" r="16" fill="url(#orama-g)" />
      <text x="16" y="21" textAnchor="middle" fontSize="14" fontWeight="bold" fill="#000" fontFamily="monospace">O</text>
      <defs><linearGradient id="orama-g" x1="0" y1="0" x2="32" y2="32"><stop stopColor="#e4e4e7" /><stop offset="1" stopColor="#a1a1aa" /></linearGradient></defs>
    </svg>
  );
}

function EthLogo({ size = 20 }: { size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 32 32" fill="none">
      <circle cx="16" cy="16" r="16" fill="#627EEA" />
      <path d="M16 4v8.87l7.5 3.35L16 4z" fill="#fff" fillOpacity="0.6" />
      <path d="M16 4L8.5 16.22 16 12.87V4z" fill="#fff" />
      <path d="M16 21.97v6.03l7.5-10.4L16 21.97z" fill="#fff" fillOpacity="0.6" />
      <path d="M16 28v-6.03L8.5 17.6 16 28z" fill="#fff" />
      <path d="M16 20.57l7.5-4.35L16 12.87v7.7z" fill="#fff" fillOpacity="0.2" />
      <path d="M8.5 16.22l7.5 4.35v-7.7l-7.5 3.35z" fill="#fff" fillOpacity="0.6" />
    </svg>
  );
}

function SolLogo({ size = 20 }: { size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 32 32" fill="none">
      <circle cx="16" cy="16" r="16" fill="#000" />
      <defs><linearGradient id="sol-g" x1="6" y1="26" x2="26" y2="6"><stop stopColor="#9945FF" /><stop offset="0.5" stopColor="#19FB9B" /><stop offset="1" stopColor="#00D4AA" /></linearGradient></defs>
      <path d="M8 20.5h13.5l2.5-2.5H10.5L8 20.5z" fill="url(#sol-g)" />
      <path d="M8 11.5l2.5 2.5H24l-2.5-2.5H8z" fill="url(#sol-g)" />
      <path d="M8 24l2.5-2.5H24L21.5 24H8z" fill="url(#sol-g)" />
    </svg>
  );
}

const TOKEN_LOGOS: Record<string, React.ReactNode> = {
  ORAMA: <OramaLogo />,
  ETH: <EthLogo />,
  SOL: <SolLogo />,
};

const tokens = [
  { symbol: "ETH", name: "Ethereum" },
  { symbol: "SOL", name: "Solana" },
];

export default function Dex() {
  const [selectedToken, setSelectedToken] = useState("ETH");
  const [tokenSelectorOpen, setTokenSelectorOpen] = useState(false);
  const selectorRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    function handleClickOutside(e: MouseEvent) {
      if (selectorRef.current && !selectorRef.current.contains(e.target as Node)) {
        setTokenSelectorOpen(false);
      }
    }
    document.addEventListener("mousedown", handleClickOutside);
    return () => document.removeEventListener("mousedown", handleClickOutside);
  }, []);

  return (
    <Page title="Orama DEX">
      {/* ── Hero ─────────────────────────────────────────────── */}
      <Section padding="default">
        <AnimateIn>
          <SectionHeader
            title="Orama DEX"
            subtitle="Swap any token for $ORAMA on the Orama L1 chain."
          />
        </AnimateIn>
      </Section>

      {/* ── Swap Card ────────────────────────────────────────── */}
      <Section padding="narrow">
        <AnimateIn>
          <div className="max-w-md mx-auto">
            <DashedPanel withCorners withBackground>
              <div className="flex flex-col gap-6">
                {/* You Pay */}
                <div className="flex flex-col gap-3">
                  <span className="text-xs text-muted font-mono tracking-wider uppercase">
                    You Pay
                  </span>
                  <div className="flex items-center gap-3">
                    <input
                      type="text"
                      inputMode="decimal"
                      className="flex-1 bg-transparent text-3xl font-mono text-fg border-none focus:outline-none min-w-0"
                      placeholder="0.0"
                      disabled
                    />
                    <div className="relative" ref={selectorRef}>
                      <button
                        type="button"
                        onClick={() => setTokenSelectorOpen(!tokenSelectorOpen)}
                        className="flex items-center gap-2 px-3 py-1.5 border border-dashed text-fg font-mono text-sm tracking-wider hover:border-fg/30 transition-colors cursor-pointer"
                        style={{ borderColor: SILVER.dark }}
                      >
                        <span className="shrink-0">{TOKEN_LOGOS[selectedToken]}</span>
                        {selectedToken}
                        <svg
                          width="10"
                          height="6"
                          viewBox="0 0 10 6"
                          fill="none"
                          className={`transition-transform ${tokenSelectorOpen ? "rotate-180" : ""}`}
                        >
                          <path
                            d="M1 1L5 5L9 1"
                            stroke="currentColor"
                            strokeWidth="1.5"
                            strokeLinecap="round"
                            strokeLinejoin="round"
                          />
                        </svg>
                      </button>

                      {tokenSelectorOpen && (
                        <div className="absolute right-0 top-full mt-1 z-50 border border-dashed bg-[#0a0a0a] min-w-[200px]" style={{ borderColor: SILVER.dark }}>
                          {tokens.map((token) => (
                            <button
                              key={token.symbol}
                              type="button"
                              onClick={() => {
                                setSelectedToken(token.symbol);
                                setTokenSelectorOpen(false);
                              }}
                              className={`w-full flex items-center gap-3 px-3 py-2.5 text-left hover:bg-white/[0.04] transition-colors cursor-pointer ${
                                selectedToken === token.symbol
                                  ? "text-zinc-200"
                                  : "text-fg"
                              }`}
                            >
                              <span className="shrink-0">{TOKEN_LOGOS[token.symbol]}</span>
                              <span className="font-mono text-sm">{token.symbol}</span>
                              <span className="text-xs text-muted ml-auto">{token.name}</span>
                            </button>
                          ))}
                        </div>
                      )}
                    </div>
                  </div>
                  <span className="text-xs text-muted font-mono">
                    &asymp; <Redacted />
                  </span>
                </div>

                {/* Swap Direction Button */}
                <div className="flex justify-center">
                  <div
                    className="w-8 h-8 rounded-full border border-dashed flex items-center justify-center text-muted hover:text-fg transition-colors"
                    style={{ borderColor: SILVER.dark }}
                  >
                    <ArrowUpDown className="w-4 h-4" />
                  </div>
                </div>

                {/* You Receive */}
                <div className="flex flex-col gap-3">
                  <span className="text-xs text-muted font-mono tracking-wider uppercase">
                    You Receive
                  </span>
                  <div className="flex items-center gap-3">
                    <span className="flex-1 text-3xl font-mono text-fg min-w-0 truncate">
                      <Redacted />
                    </span>
                    <span className="flex items-center gap-2 px-3 py-1.5 border border-dashed font-mono text-sm text-zinc-300" style={{ borderColor: SILVER.mid }}>
                      <OramaLogo />
                      $ORAMA
                    </span>
                  </div>
                  <span className="text-xs text-muted font-mono">
                    1 {selectedToken} &asymp; <Redacted /> ORAMA
                  </span>
                </div>

                {/* Details Row */}
                <div className="border-t border-dashed pt-4 flex items-center justify-between" style={{ borderColor: SILVER.border }}>
                  <span className="text-xs text-muted font-mono">
                    Rate: 1 {selectedToken} = <Redacted /> ORAMA
                  </span>
                  <span className="text-xs text-muted font-mono">
                    Slippage: <Redacted />
                  </span>
                </div>

                {/* Swap Button — Coming Soon */}
                <SilverButton size="lg" className="w-full opacity-50 cursor-not-allowed" disabled>
                  Coming Soon
                </SilverButton>
              </div>
            </DashedPanel>
          </div>
        </AnimateIn>
      </Section>

      <Section padding="none">
        <CrosshairDivider />
      </Section>

      {/* ── Stats Bar ────────────────────────────────────────── */}
      <Section>
        <AnimateIn>
          <div
            className="grid grid-cols-2 sm:grid-cols-4 gap-4 p-4 rounded-sm"
            style={{
              border: `1px dashed ${SILVER.border}`,
              background: SILVER.bg,
            }}
          >
            <SilverMetric label="Total Liquidity" value={<Redacted />} />
            <SilverMetric label="24h Volume" value={<Redacted />} />
            <SilverMetric label="$ORAMA Price" value={<Redacted />} />
            <SilverMetric label="Active Pairs" value="3" />
          </div>
        </AnimateIn>
      </Section>

      <Section padding="none">
        <CrosshairDivider />
      </Section>

      {/* ── Supported Tokens ─────────────────────────────────── */}
      <Section>
        <AnimateIn>
          <div className="flex flex-col gap-6">
            <SectionHeader title="Supported Tokens" />
            <div className="grid grid-cols-2 sm:grid-cols-4 gap-4">
              {[
                { symbol: "ORAMA", name: "$ORAMA" },
                { symbol: "ETH", name: "Ethereum" },
                { symbol: "SOL", name: "Solana" },
              ].map((t) => (
                <div
                  key={t.symbol}
                  className="flex items-center gap-3 p-4 rounded-sm"
                  style={{
                    border: `1px dashed ${SILVER.border}`,
                    background: SILVER.bg,
                  }}
                >
                  <span className="shrink-0">{TOKEN_LOGOS[t.symbol]}</span>
                  <div className="flex flex-col min-w-0">
                    <span className="font-mono text-sm text-fg font-semibold truncate">{t.symbol}</span>
                    <span className="text-xs text-muted truncate">{t.name}</span>
                  </div>
                  <span className="font-mono text-xs text-zinc-400 ml-auto shrink-0"><Redacted /></span>
                </div>
              ))}
            </div>
          </div>
        </AnimateIn>
      </Section>

      <Section padding="none">
        <CrosshairDivider />
      </Section>

      {/* ── Also Available On ────────────────────────────────── */}
      <Section>
        <AnimateIn>
          <div className="flex flex-col gap-8">
            <SectionHeader title="Also Available On" />

            <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
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
                        Aerodrome Finance
                      </span>
                      <SilverBadge variant="status">LIVE</SilverBadge>
                    </div>
                    <p className="text-sm text-muted">
                      Trade $ORAMA on Base via Aerodrome Finance, the leading DEX
                      on the Base network.
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
                      Swap $ORAMA on Ethereum mainnet via Uniswap, the most widely
                      used decentralized exchange.
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
              <h2 className="font-display font-bold text-2xl sm:text-3xl text-fg">
                Start Trading
              </h2>
              <p className="text-muted max-w-lg leading-relaxed">
                Connect your wallet to swap tokens on the Orama L1 chain.
              </p>
              <SilverButton size="lg" disabled className="opacity-50 cursor-not-allowed">
                Coming Soon
              </SilverButton>
            </div>
          </DashedPanel>
        </AnimateIn>
      </Section>
    </Page>
  );
}
