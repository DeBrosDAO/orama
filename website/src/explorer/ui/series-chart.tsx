import { bars, extent, linePoints, stepPoints } from "../model/chart";
import type { Series } from "../model/chart";
import { cn } from "../../lib/utils";

const WIDTH = 640;
const HEIGHT = 160;
const STROKE_WIDTH = 2;
const REFERENCE_DASH = "5 4";

export type ChartTone = "gain" | "signal" | "loss" | "muted";

const TONES: Record<ChartTone, string> = {
  gain: "text-gain",
  signal: "text-signal",
  loss: "text-loss",
  muted: "text-muted",
};

export interface ChartTable {
  head: string[];
  rows: string[][];
}

export interface SeriesChartProps {
  /** What the chart shows, in a sentence: it is the chart's accessible name. */
  label: string;
  kind: "line" | "bars";
  /** Oldest first. Norama, so large totals keep every digit. A null is a gap: a slot with no figure, drawn as nothing and never as zero. */
  values: Series;
  /** Drawn dashed over the bars, one per value: the schedule's ceiling. A null breaks the line. */
  reference?: Series;
  /** The top and bottom axis values. */
  format: (value: bigint) => string;
  /** What the left and right ends of the chart are, e.g. "epoch 3" and "epoch 42". */
  from: string;
  to: string;
  tone?: ChartTone;
  /** The same numbers as text, for readers who cannot see the chart. */
  table: ChartTable;
}

function DataTable({ label, table }: { label: string; table: ChartTable }) {
  return (
    <details className="mt-2 text-[13px]">
      <summary className="cursor-pointer text-muted hover:text-fg">View as a table</summary>
      <div className="mt-2 max-h-72 overflow-auto rounded-md border border-border">
        <table className="w-full text-left text-[12px] tabular-nums">
          <caption className="sr-only">{label}</caption>
          <thead className="sticky top-0 bg-surface-2 text-muted">
            <tr>
              {table.head.map((h) => (
                <th key={h} scope="col" className="px-3 py-1.5 font-medium">
                  {h}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {table.rows.map((row, i) => (
              <tr key={i} className="border-t border-border">
                {row.map((cell, j) => (
                  <td key={j} className="px-3 py-1.5 font-mono">
                    {cell}
                  </td>
                ))}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </details>
  );
}

/** A small dependency-free chart: a polyline or bars, two axis labels, and the data as a table under it. */
export function SeriesChart({ label, kind, values, reference, format, from, to, tone = "signal", table }: SeriesChartProps) {
  if (values.length === 0) return null;
  const present = values.filter((v): v is bigint => v !== null);
  if (present.length === 0 || (kind === "line" && present.length < 2)) {
    return (
      <div>
        <p className="rounded-md bg-surface-2/40 px-3 py-6 text-center text-sm text-muted">
          {present.length === 0
            ? "The index has no figure for any of these epochs. They are listed in the table."
            : "Only one epoch has a figure so far, and a trend needs two. It is in the table."}
        </p>
        <DataTable label={label} table={table} />
      </div>
    );
  }
  const size = { width: WIDTH, height: HEIGHT };
  const fromZero = kind === "bars";
  const range = extent(reference ? [...values, ...reference] : values, fromZero);
  const last = present[present.length - 1]!;
  const gaps = values.length - present.length;
  const gapNote = gaps === 0 ? "" : ` ${gaps} slot${gaps === 1 ? " has" : "s have"} no figure and ${gaps === 1 ? "is" : "are"} left blank.`;
  const summary = `${label}. ${present.length} point${present.length === 1 ? "" : "s"}, from ${from} to ${to}; last ${format(last)}, lowest ${format(range.min)}, highest ${format(range.max)}.${gapNote}`;
  return (
    <figure className="m-0">
      <div className={cn("grid grid-cols-[auto_1fr] gap-x-2", TONES[tone])}>
        <div className="flex flex-col justify-between py-0.5 text-right font-mono text-[11px] tabular-nums text-muted" aria-hidden="true">
          <span>{format(range.max)}</span>
          <span>{format(range.min)}</span>
        </div>
        <svg
          viewBox={`0 0 ${WIDTH} ${HEIGHT}`}
          preserveAspectRatio="none"
          role="img"
          aria-label={summary}
          className="h-40 w-full overflow-visible rounded-md bg-surface-2/40"
        >
          <line x1="0" y1="0" x2={WIDTH} y2="0" className="stroke-border" vectorEffect="non-scaling-stroke" />
          <line x1="0" y1={HEIGHT} x2={WIDTH} y2={HEIGHT} className="stroke-border" vectorEffect="non-scaling-stroke" />
          {kind === "line" ? (
            linePoints(values, size, range).map((points, i) => (
              <polyline
                key={i}
                points={points}
                fill="none"
                stroke="currentColor"
                strokeWidth={STROKE_WIDTH}
                strokeLinejoin="round"
                strokeLinecap="round"
                vectorEffect="non-scaling-stroke"
              />
            ))
          ) : (
            bars(values, size, range).map((b, i) => (
              <rect key={i} x={b.x} y={b.y} width={b.width} height={b.height} fill="currentColor" opacity="0.85" />
            ))
          )}
          {reference &&
            stepPoints(reference, size, range).map((points, i) => (
              <polyline
                key={i}
                points={points}
                fill="none"
                stroke="currentColor"
                strokeWidth="1.5"
                strokeDasharray={REFERENCE_DASH}
                vectorEffect="non-scaling-stroke"
                className="text-fg"
              />
            ))}
        </svg>
        <span aria-hidden="true" />
        <div className="mt-1 flex justify-between text-[11px] text-muted" aria-hidden="true">
          <span>{from}</span>
          <span>{to}</span>
        </div>
      </div>
      <DataTable label={label} table={table} />
    </figure>
  );
}
