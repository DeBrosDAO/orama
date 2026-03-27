import { Page } from "../components/layout/page";
import { Section } from "../components/layout/section";
import { SectionHeader } from "../components/ui/section-header";
import { DashedPanel } from "../components/ui/dashed-panel";
import { AnimateIn } from "../components/ui/animate-in";
import { CrosshairDivider } from "../components/ui/crosshair-divider";
import { SILVER, SilverButton, SilverMetric } from "../components/ui/silver-theme";

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

function BtcLogo({ size = 20 }: { size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 32 32" fill="none">
      <circle cx="16" cy="16" r="16" fill="#F7931A" />
      <text x="16" y="21" textAnchor="middle" fontSize="14" fontWeight="bold" fill="#fff" fontFamily="monospace">B</text>
    </svg>
  );
}

export default function Dex() {
  return (
    <Page title="Orama DEX">
      {/* ── Hero ─────────────────────────────────────────────── */}
      <Section padding="default">
        <AnimateIn>
          <SectionHeader
            title="Orama DEX"
            subtitle="Protocol-native order book. One pair: $ORAMA/BTC. No AMM. Pure price discovery."
          />
        </AnimateIn>
      </Section>

      {/* ── Order Book Interface ────────────────────────────── */}
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
                    <span className="flex items-center gap-2 px-3 py-1.5 border border-dashed font-mono text-sm text-zinc-300" style={{ borderColor: SILVER.mid }}>
                      <BtcLogo />
                      BTC
                    </span>
                  </div>
                  <span className="text-xs text-muted font-mono">
                    Bridged BTC on Orama L1
                  </span>
                </div>

                {/* Swap Direction */}
                <div className="flex justify-center">
                  <div
                    className="w-8 h-8 rounded-full border border-dashed flex items-center justify-center text-muted"
                    style={{ borderColor: SILVER.dark }}
                  >
                    <svg width="16" height="16" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
                      <path d="M8 3v10M8 3L5 6M8 3l3 3" />
                    </svg>
                  </div>
                </div>

                {/* You Receive */}
                <div className="flex flex-col gap-3">
                  <span className="text-xs text-muted font-mono tracking-wider uppercase">
                    You Receive
                  </span>
                  <div className="flex items-center gap-3">
                    <span className="flex-1 text-3xl font-mono text-fg min-w-0 truncate">
                      —
                    </span>
                    <span className="flex items-center gap-2 px-3 py-1.5 border border-dashed font-mono text-sm text-zinc-300" style={{ borderColor: SILVER.mid }}>
                      <OramaLogo />
                      $ORAMA
                    </span>
                  </div>
                  <span className="text-xs text-muted font-mono">
                    1 BTC = — ORAMA
                  </span>
                </div>

                {/* Details Row */}
                <div className="border-t border-dashed pt-4 flex items-center justify-between" style={{ borderColor: SILVER.border }}>
                  <span className="text-xs text-muted font-mono">
                    Order Type: Market
                  </span>
                  <span className="text-xs text-muted font-mono">
                    Fee: 1,000 rays
                  </span>
                </div>

                {/* Action Button */}
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
            <SilverMetric label="Order Book Depth" value="—" />
            <SilverMetric label="24h Volume" value="—" />
            <SilverMetric label="$ORAMA Price (BTC)" value="—" />
            <SilverMetric label="Active Pair" value="$ORAMA/BTC" />
          </div>
        </AnimateIn>
      </Section>

      <Section padding="none">
        <CrosshairDivider />
      </Section>

      {/* ── Order Book API ────────────────────────────────────── */}
      <Section>
        <AnimateIn>
          <div className="flex flex-col gap-6">
            <SectionHeader
              title="Order Book Interface"
              subtitle="Native chain primitive — not a smart contract. Callable from WASM contracts and external RPC."
            />
            <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-4">
              {[
                { fn: "place_order(pair, side, amount, price)", desc: "Place a limit order on the book" },
                { fn: "market_order(pair, side, amount)", desc: "Execute at best available price" },
                { fn: "cancel_order(order_id)", desc: "Cancel an open limit order" },
                { fn: "get_orderbook(pair)", desc: "Current bids and asks" },
                { fn: "quote(pair, side, amount)", desc: "Expected fill price and size" },
              ].map((item) => (
                <DashedPanel key={item.fn} withBackground>
                  <div className="flex flex-col gap-2 p-3">
                    <code className="text-xs font-mono text-fg break-all">{item.fn}</code>
                    <span className="text-xs text-muted">{item.desc}</span>
                  </div>
                </DashedPanel>
              ))}
            </div>
            <p className="text-xs text-muted leading-relaxed max-w-xl">
              Any wallet, aggregator, or exchange in the world can integrate by calling these
              functions over Orama's RPC. No permission required. No listing fees. No gatekeepers.
            </p>
          </div>
        </AnimateIn>
      </Section>

      <Section padding="none">
        <CrosshairDivider />
      </Section>

      {/* ── Why Order Book, Not AMM ────────────────────────── */}
      <Section>
        <AnimateIn>
          <div className="flex flex-col gap-6">
            <SectionHeader
              title="Why an Order Book, Not an AMM"
              subtitle="The whitepaper explicitly argues against AMMs for the core trading pair."
            />

            <DashedPanel withCorners withBackground>
              <div className="p-4 overflow-x-auto">
                <table className="w-full text-left">
                  <thead>
                    <tr className="border-b border-dashed border-border">
                      <th className="text-xs font-mono text-muted tracking-wider uppercase py-3 pr-4"></th>
                      <th className="text-xs font-mono text-muted tracking-wider uppercase py-3 pr-4">AMM</th>
                      <th className="text-xs font-mono text-muted tracking-wider uppercase py-3">Order Book</th>
                    </tr>
                  </thead>
                  <tbody>
                    {[
                      { aspect: "Bootstrap", amm: "Needs liquidity providers with both assets", ob: "Just needs sellers and buyers — works from block 1" },
                      { aspect: "Fairness", amm: "LPs earn special yield (privileged class)", ob: "No special roles — everyone is a trader" },
                      { aspect: "Capital Efficiency", amm: "Spread across entire price curve", ob: "Concentrated at actual price levels" },
                      { aspect: "Philosophy", amm: "Complex, opaque", ob: "Simple, transparent, free" },
                    ].map((row) => (
                      <tr key={row.aspect} className="border-b border-border/50">
                        <td className="text-sm text-fg py-3 pr-4 font-semibold">{row.aspect}</td>
                        <td className="text-sm text-muted py-3 pr-4">{row.amm}</td>
                        <td className="text-sm text-fg py-3">{row.ob}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </DashedPanel>
          </div>
        </AnimateIn>
      </Section>

      <Section padding="none">
        <CrosshairDivider />
      </Section>

      {/* ── Bonding Curve as Liquidity Backstop ────────────── */}
      <Section>
        <AnimateIn>
          <div className="flex flex-col gap-6">
            <SectionHeader
              title="Bonding Curve Backstop"
              subtitle="The protocol bonding curve guarantees liquidity even when the order book is thin."
            />

            <div className="grid grid-cols-1 md:grid-cols-2 gap-6">
              <DashedPanel withCorners withBackground>
                <div className="flex flex-col gap-4 p-4">
                  <h3 className="text-xs font-mono text-muted tracking-wider uppercase">How It Works</h3>
                  <p className="text-sm text-muted leading-relaxed">
                    20% of every block reward flows into the curve's sell-side inventory (max 21M $ORAMA).
                    Anyone can buy from the curve by sending BTC. The price follows:
                  </p>
                  <code className="text-sm text-fg font-mono block text-center py-2">
                    Price = 0.0000000006 x sqrt(total_sold)
                  </code>
                  <p className="text-sm text-muted leading-relaxed">
                    BTC paid goes to the protocol reserve, directly backing the BTC bridge.
                    The free market (order book) determines the real price. The curve is a floor.
                  </p>
                </div>
              </DashedPanel>

              <DashedPanel withCorners withBackground>
                <div className="flex flex-col gap-4 p-4">
                  <h3 className="text-xs font-mono text-muted tracking-wider uppercase">Curve Price Schedule</h3>
                  <div className="flex flex-col gap-1">
                    {[
                      { sold: "10,000", price: "0.00000006 BTC", btc: "0.0004 BTC" },
                      { sold: "1,000,000", price: "0.0000006 BTC", btc: "0.4 BTC" },
                      { sold: "5,000,000", price: "0.00000134 BTC", btc: "4.5 BTC" },
                      { sold: "10,000,000", price: "0.0000019 BTC", btc: "12.7 BTC" },
                      { sold: "21,000,000", price: "0.00000275 BTC", btc: "~38.5 BTC" },
                    ].map((r) => (
                      <div key={r.sold} className="grid grid-cols-3 text-xs font-mono py-1.5 border-b border-border/30">
                        <span className="text-muted">{r.sold} sold</span>
                        <span className="text-fg">{r.price}</span>
                        <span className="text-muted text-right">{r.btc} total</span>
                      </div>
                    ))}
                  </div>
                </div>
              </DashedPanel>
            </div>
          </div>
        </AnimateIn>
      </Section>

      <Section padding="none">
        <CrosshairDivider />
      </Section>

      {/* ── Asset Hierarchy ────────────────────────────────── */}
      <Section>
        <AnimateIn>
          <div className="flex flex-col gap-6">
            <SectionHeader
              title="Asset Hierarchy"
              subtitle="BTC at the top. $ORAMA in the middle. Custom tokens at the bottom."
            />

            <DashedPanel withCorners withBackground>
              <div className="flex flex-col items-center gap-6 p-6">
                <div className="flex items-center gap-3 p-4 rounded-sm" style={{ border: `1px dashed ${SILVER.border}`, background: SILVER.bg }}>
                  <BtcLogo size={28} />
                  <div className="flex flex-col">
                    <span className="font-mono text-fg font-bold">BTC</span>
                    <span className="text-xs text-muted">Bridged from Bitcoin mainnet</span>
                  </div>
                </div>

                <div className="flex flex-col items-center gap-1">
                  <div className="w-px h-6 bg-zinc-700" />
                  <span className="text-[10px] font-mono text-muted">Protocol-native order book</span>
                  <div className="w-px h-6 bg-zinc-700" />
                </div>

                <div className="flex items-center gap-3 p-4 rounded-sm" style={{ border: `2px solid ${SILVER.mid}`, background: SILVER.bg }}>
                  <OramaLogo size={28} />
                  <div className="flex flex-col">
                    <span className="font-mono text-fg font-bold">$ORAMA</span>
                    <span className="text-xs text-muted">Gas token, earned through mining</span>
                  </div>
                </div>

                <div className="flex flex-col items-center gap-1">
                  <div className="w-px h-6 bg-zinc-700" />
                  <span className="text-[10px] font-mono text-muted">Permissionless WASM DEX contracts</span>
                  <div className="w-px h-6 bg-zinc-700" />
                </div>

                <div className="flex items-center gap-3 p-4 rounded-sm" style={{ border: `1px dashed ${SILVER.border}`, background: SILVER.bg }}>
                  <div className="w-7 h-7 rounded-full bg-zinc-800 flex items-center justify-center text-xs font-mono text-muted">?</div>
                  <div className="flex flex-col">
                    <span className="font-mono text-fg font-bold">Custom Tokens</span>
                    <span className="text-xs text-muted">Created on Orama via WASM contracts</span>
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

      {/* ── Front-Running Prevention ──────────────────────── */}
      <Section>
        <AnimateIn>
          <div className="flex flex-col gap-6">
            <SectionHeader
              title="Security"
              subtitle="Front-running prevention and price manipulation resistance built into the protocol."
            />

            <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
              <DashedPanel withBackground>
                <div className="flex flex-col gap-3 p-4">
                  <h3 className="font-display font-bold text-fg text-sm">Randomized Order Processing</h3>
                  <p className="text-xs text-muted leading-relaxed">
                    Order book transactions within the same block are processed in randomized order,
                    not by gas price. This eliminates MEV (Miner Extractable Value) — block proposers
                    cannot reorder transactions to front-run traders.
                  </p>
                </div>
              </DashedPanel>
              <DashedPanel withBackground>
                <div className="flex flex-col gap-3 p-4">
                  <h3 className="font-display font-bold text-fg text-sm">Bonding Curve Reference Price</h3>
                  <p className="text-xs text-muted leading-relaxed">
                    The bonding curve provides a mathematical reference price that cannot be manipulated
                    by wash trading on the order book. The curve's price is deterministic based on total
                    tokens sold — pure math, not market games.
                  </p>
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
              <h2 className="font-display font-bold text-2xl sm:text-3xl text-fg">
                Start Trading
              </h2>
              <p className="text-muted max-w-lg leading-relaxed">
                Connect your RootWallet to trade $ORAMA/BTC on the protocol-native order book.
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
