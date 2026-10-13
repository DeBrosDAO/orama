import type { CSSProperties } from "react";
import { Link } from "react-router";
import { ArrowLeftRight, ArrowRight, ArrowUpRight } from "lucide-react";
import { FLYWHEEL, PRODUCTS, SEE_FOR_YOURSELF } from "../../content/investors/products";
import type { ProductLink } from "../../content/investors/products";
import { MARKET_LAYERS, OUTAGES } from "../../content/investors/market";
import { PROOF_POINTS, TIMELINE } from "../../content/funding";
import { cn } from "../../lib/utils";
import oramaIcon from "../../assets/orama-icon.png";
import { SourceRef } from "./source-ref";

const MARKS: Record<string, string> = {
  orama: oramaIcon,
  rootwallet: "/images/apps/rootwallet-mark.png",
};

const pillClass =
  "inline-flex items-center gap-1.5 px-3 py-1.5 border border-border/70 rounded-full font-mono text-[11px] uppercase tracking-wider text-accent hover:text-fg hover:border-fg/30 transition-colors";

/** Links as pills: pages of this site in place, other sites in a new tab. */
export function LinkPills({ links }: { links: readonly ProductLink[] }) {
  return (
    <div className="flex flex-wrap gap-2">
      {links.map((l) =>
        l.external ? (
          <a key={l.href} href={l.href} target="_blank" rel="noopener noreferrer" className={pillClass}>
            {l.label}
            <ArrowUpRight size={12} aria-hidden="true" />
          </a>
        ) : (
          <Link key={l.href} to={l.href} className={pillClass}>
            {l.label}
            <ArrowRight size={12} aria-hidden="true" />
          </Link>
        ),
      )}
    </div>
  );
}

/** Every product, with every place an investor can go to check it. */
export function SeeForYourself() {
  return (
    <ul className="grid grid-cols-1 md:grid-cols-3 gap-4">
      {SEE_FOR_YOURSELF.map((p) => (
        <li key={p.name} className="flex flex-col gap-4 p-6 border border-dashed border-border">
          <h3 className="font-display font-semibold text-lg text-fg">{p.name}</h3>
          <p className="text-sm text-muted flex-1">{p.line}</p>
          <LinkPills links={p.links} />
        </li>
      ))}
    </ul>
  );
}

/** The two products side by side, each with its own verified traction. */
export function ProductPair() {
  return (
    <div className="grid grid-cols-1 lg:grid-cols-2 gap-4">
      {PRODUCTS.map((p) => (
        <article key={p.id} className="flex flex-col gap-5 p-6 sm:p-8 border border-dashed border-border bg-surface/60">
          <div className="flex items-center gap-4">
            <span className="flex items-center justify-center w-12 h-12 rounded-xl border border-border bg-surface-2 overflow-hidden">
              <img src={MARKS[p.id]} alt="" className="w-7 h-7 object-contain" loading="lazy" />
            </span>
            <div>
              <h3 className="font-display font-bold text-xl text-fg">{p.name}</h3>
              <span className="font-mono text-[10px] tracking-[0.2em] uppercase text-muted">{p.role}</span>
            </div>
          </div>
          <p className="text-accent">{p.line}</p>
          <dl className="grid grid-cols-2 gap-px bg-border border border-border">
            {p.traction.map((t) => (
              <div key={t.label} className="flex flex-col-reverse justify-end gap-1 p-4 bg-surface">
                <dt className="text-xs text-muted">{t.label}</dt>
                <dd className="font-display font-bold text-2xl text-fg tabular-nums">{t.value}</dd>
              </div>
            ))}
          </dl>
          <span className="font-mono text-[10px] tracking-wider uppercase text-muted">{p.asOf}</span>
          <LinkPills links={p.links} />
        </article>
      ))}
    </div>
  );
}

export function Flywheel() {
  return (
    <ul className="grid grid-cols-1 md:grid-cols-3 gap-4">
      {FLYWHEEL.map((f) => (
        <li key={f.line} className="flex flex-col gap-3 p-5 border border-dashed border-border">
          <span className="flex items-center gap-2 font-mono text-[11px] tracking-wider uppercase text-fg">
            {f.from} <ArrowLeftRight size={12} className="text-muted" /> {f.to}
          </span>
          <p className="text-sm text-muted">{f.line}</p>
        </li>
      ))}
    </ul>
  );
}

export function OutageList() {
  return (
    <ol className="flex flex-col divide-y divide-dashed divide-border border-y border-dashed border-border">
      {OUTAGES.map((o) => (
        <li key={o.what + o.when} className="grid grid-cols-[5.5rem_1fr] sm:grid-cols-[7rem_12rem_1fr] gap-x-4 gap-y-1 py-3 items-baseline">
          <span className="font-mono text-xs text-muted">{o.when}</span>
          <span className="font-display font-semibold text-fg">{o.what}</span>
          <span className="col-start-2 sm:col-start-auto text-sm text-accent">
            {o.impact}
            <SourceRef ids={[o.source]} />
          </span>
        </li>
      ))}
    </ol>
  );
}

/** Market layers as nested bars, widest first. */
export function MarketLayers() {
  const widths = ["100%", "78%", "62%", "46%"];
  return (
    <ol className="flex flex-col gap-3">
      {MARKET_LAYERS.map((m, i) => (
        <li key={m.label}>
          <div
            className={cn(
              "w-full sm:w-[var(--w)] flex flex-col sm:flex-row sm:items-center gap-1 sm:gap-6 p-4 border",
              i === 0 ? "border-fg/25 bg-fg/[0.03]" : "border-dashed border-border",
            )}
            style={{ "--w": widths[i] } as CSSProperties}
          >
            <span className="font-display font-bold text-2xl text-fg tabular-nums whitespace-nowrap shrink-0 sm:w-32">
              {m.value}
            </span>
            <span className="flex flex-col">
              <span className="font-display font-semibold text-fg">{m.label}</span>
              <span className="text-xs text-muted">
                {m.line}
                <SourceRef ids={m.sources} />
              </span>
            </span>
          </div>
        </li>
      ))}
    </ol>
  );
}

export function RoundTimeline() {
  return (
    <div className="relative">
      <div
        aria-hidden="true"
        className="absolute left-[7px] top-2 bottom-2 border-l md:left-0 md:right-0 md:top-[7px] md:bottom-auto md:border-l-0 md:border-t border-dashed border-border"
      />
      <ol className="relative grid grid-cols-1 md:grid-cols-5 gap-8 md:gap-4">
        {TIMELINE.map((stop, i) => (
          <li key={stop.month} className="relative flex md:flex-col gap-4">
            <span
              className={cn(
                "relative z-10 mt-0.5 w-3.5 h-3.5 shrink-0 rounded-full border",
                i === TIMELINE.length - 1 ? "bg-fg border-fg" : "bg-bg border-fg/50",
              )}
            />
            <div className="flex flex-col gap-1">
              <span className="font-mono text-xs tracking-widest uppercase text-muted">Month {stop.month}</span>
              <span className="font-display font-semibold text-fg">{stop.title}</span>
              <span className="text-sm text-muted">{stop.line}</span>
            </div>
          </li>
        ))}
      </ol>
    </div>
  );
}

export function ProofPoints() {
  return (
    <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
      {PROOF_POINTS.map((p) => (
        <div key={p.product} className="p-6 border border-dashed border-border">
          <h4 className="font-display font-semibold text-fg mb-4">{p.product}</h4>
          <ul className="flex flex-col gap-2">
            {p.items.map((it) => (
              <li key={it} className="flex items-start gap-3 text-sm text-accent">
                <span className="mt-2 w-1.5 h-1.5 rounded-full bg-signal shrink-0" />
                {it}
              </li>
            ))}
          </ul>
        </div>
      ))}
    </div>
  );
}
