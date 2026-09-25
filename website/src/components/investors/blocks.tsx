import { Check, Minus } from "lucide-react";
import type { Stat } from "../../content/investors/market";
import type { Cell, Grid } from "../../content/investors/competition";
import { SOURCES } from "../../content/investors/sources";
import { cn } from "../../lib/utils";
import { SourceRef } from "./source-ref";

/** Big-number cards, each with its source. */
export function StatGrid({ stats, className }: { stats: readonly Stat[]; className?: string }) {
  return (
    <ul className={cn("grid grid-cols-1 sm:grid-cols-3 gap-px bg-border border border-border", className)}>
      {stats.map((s) => (
        <li key={s.label} className="flex flex-col gap-2 p-6 bg-surface">
          <span className="font-display font-bold text-3xl sm:text-4xl text-fg tabular-nums">
            {s.value}
            <SourceRef ids={[s.source]} />
          </span>
          <span className="text-sm text-accent">{s.label}</span>
          {s.note && <span className="font-mono text-[10px] tracking-wider uppercase text-muted">{s.note}</span>}
        </li>
      ))}
    </ul>
  );
}

function GridCell({ value, ours }: { value: Cell; ours: boolean }) {
  if (value === true) {
    return <Check size={16} role="img" aria-label="Yes" className={cn("mx-auto", ours ? "text-fg" : "text-accent")} />;
  }
  if (value === false) return <Minus size={16} role="img" aria-label="No" className="mx-auto text-muted" />;
  return <span className={cn("text-[11px]", ours ? "text-fg" : "text-muted")}>{value}</span>;
}

/** A feature-by-competitor table; scrolls sideways on narrow screens. */
export function CompetitionGrid({ grid, caption }: { grid: Grid; caption: string }) {
  return (
    <div className="overflow-x-auto border border-dashed border-border" tabIndex={0} role="region" aria-label={caption}>
      <table className="w-full min-w-[640px] text-sm">
        <caption className="sr-only">{caption}</caption>
        <thead>
          <tr className="border-b border-dashed border-border">
            <th scope="col" className="sticky left-0 z-10 bg-surface p-3 text-left font-normal">
              <span className="sr-only">Feature</span>
            </th>
            {grid.columns.map((c, i) => (
              <th
                key={c}
                scope="col"
                className={cn(
                  "p-3 text-center font-display font-semibold",
                  i === grid.ours ? "text-fg bg-fg/[0.04]" : "text-accent",
                )}
              >
                {c}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {grid.rows.map((row) => (
            <tr key={row.label} className="border-b border-dashed border-border last:border-0">
              <th scope="row" className="sticky left-0 z-10 bg-surface p-3 text-left font-normal text-accent">{row.label}</th>
              {row.cells.map((cell, i) => (
                <td key={grid.columns[i]} className={cn("p-3 text-center", i === grid.ours && "bg-fg/[0.04]")}>
                  <GridCell value={cell} ours={i === grid.ours} />
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

/** A labelled list of short cards: moat, risks, revenue lines. */
export function CardList({
  items,
  columns = 3,
}: {
  items: readonly { title: string; line: string; tag?: string }[];
  columns?: 2 | 3;
}) {
  return (
    <ul className={cn("grid grid-cols-1 sm:grid-cols-2 gap-4", columns === 3 && "lg:grid-cols-3")}>
      {items.map((it) => (
        <li key={it.title} className="flex flex-col gap-2 p-5 border border-dashed border-border">
          <h3 className="font-display font-semibold text-fg">{it.title}</h3>
          <p className="text-sm text-muted flex-1">{it.line}</p>
          {it.tag && (
            <span className="font-mono text-[10px] tracking-wider uppercase text-accent pt-2 border-t border-dashed border-border">
              {it.tag}
            </span>
          )}
        </li>
      ))}
    </ul>
  );
}

export function SourcesList() {
  return (
    <ol className="flex flex-col gap-1.5 text-xs text-muted">
      {SOURCES.map((s, i) => (
        <li key={s.id} id={`src-${i + 1}`} className="flex gap-2 scroll-mt-24">
          <span className="font-mono shrink-0 w-7 text-right">[{i + 1}]</span>
          <a href={s.url} target="_blank" rel="noopener noreferrer" className="hover:text-fg transition-colors break-words">
            {s.label}
          </a>
        </li>
      ))}
    </ol>
  );
}
