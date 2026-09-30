import { formatNorama, parseNorama } from "../../model/units";
import type { BalancePoint, BalanceRange, Norama } from "../../model/types";
import { BALANCE_FRACTION, RANGE_WORDS } from "./constants";

/** Vertical breathing room so the stroke is never clipped at the edges. */
export const CHART_PAD = 4;

export interface ChartPaths {
  /** SVG path data for the line; empty when there are no points. */
  line: string;
  /** The line closed down to the baseline, for the fill. */
  area: string;
  /** The lowest and highest balance drawn, exact. "0" when there are no points. */
  min: Norama;
  max: Norama;
}

const EMPTY: ChartPaths = { line: "", area: "", min: "0", max: "0" };

const fmt = (n: number) => n.toFixed(2);

function xPositions(points: BalancePoint[], width: number): number[] {
  const times = points.map((p) => Date.parse(p.time));
  const first = times[0] as number;
  const span = (times[times.length - 1] as number) - first;
  const last = points.length - 1;
  return times.map((t, i) => (span > 0 ? ((t - first) / span) * width : (i / last) * width));
}

/**
 * Turn a balance history into SVG path strings. Points are placed by time.
 * A flat series is drawn along the middle; a single point is drawn as a flat
 * line across the chart, since one point has no trend to show.
 */
export function buildChartPaths(points: BalancePoint[], width: number, height: number): ChartPaths {
  if (points.length === 0) return EMPTY;
  const values = points.map((p) => parseNorama(p.total));
  const min = values.reduce((a, b) => (b < a ? b : a));
  const max = values.reduce((a, b) => (b > a ? b : a));
  const range = Number(max - min);
  const inner = height - 2 * CHART_PAD;
  const y = (v: bigint) => (range === 0 ? height / 2 : height - CHART_PAD - (Number(v - min) / range) * inner);
  const xs = points.length === 1 ? [0, width] : xPositions(points, width);
  const ys = points.length === 1 ? [y(values[0] as bigint), y(values[0] as bigint)] : values.map(y);
  const line = xs.map((x, i) => `${i === 0 ? "M" : "L"}${fmt(x)},${fmt(ys[i] as number)}`).join(" ");
  const area = `${line} L${fmt(xs[xs.length - 1] as number)},${fmt(height)} L${fmt(xs[0] as number)},${fmt(height)} Z`;
  return { line, area, min: min.toString(), max: max.toString() };
}

/** What a screen reader says for the chart: the first and last balance in ORAMA, never raw norama. */
export function balanceChartLabel(points: BalancePoint[], range: BalanceRange): string {
  const first = points[0];
  const last = points[points.length - 1];
  if (!first || !last) return `No balance history over ${RANGE_WORDS[range]}`;
  const from = formatNorama(first.total, BALANCE_FRACTION);
  const to = formatNorama(last.total, BALANCE_FRACTION);
  return `Balance from ${from} to ${to} ORAMA over ${RANGE_WORDS[range]}`;
}
