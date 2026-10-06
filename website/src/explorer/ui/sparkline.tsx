const PAD = 2;
const STROKE_WIDTH = 1.5;

export interface SparklineProps {
  values: readonly number[];
  width?: number;
  height?: number;
  className?: string;
}

/**
 * The polyline `points` for a trend line. No data or a single value is a flat
 * line across the whole width, so it never draws nothing.
 */
export function sparklinePoints(values: readonly number[], width: number, height: number): string {
  if (values.length < 2) {
    const y = height / 2;
    return `0,${y} ${width},${y}`;
  }
  const max = Math.max(1, ...values);
  const min = Math.min(...values, max);
  const span = max - min || 1;
  const step = width / (values.length - 1);
  return values
    .map((v, i) => `${(i * step).toFixed(1)},${(height - PAD - ((v - min) / span) * (height - 2 * PAD)).toFixed(1)}`)
    .join(" ");
}

/** A tiny trend line with no axes. */
export function Sparkline({ values, width = 100, height = 26, className }: SparklineProps) {
  return (
    <svg width={width} height={height} viewBox={`0 0 ${width} ${height}`} className={className} aria-hidden="true">
      <polyline
        points={sparklinePoints(values, width, height)}
        fill="none"
        stroke="currentColor"
        strokeWidth={STROKE_WIDTH}
        strokeLinejoin="round"
        strokeLinecap="round"
      />
    </svg>
  );
}
