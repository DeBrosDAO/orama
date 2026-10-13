/**
 * Chart geometry for amounts that do not fit a double. Values are bigint norama; only the final
 * 0-to-1 position is a number, so a 10^20 total never loses the difference between two points.
 *
 * A series can have gaps: a null is a slot with no figure (an epoch the index could not read). It
 * takes its place on the axis and draws nothing, and it is never read as zero.
 */

const RATIO_SCALE = 100_000n;
const FLAT = 0.5;

/** Values oldest first; null is a gap. */
export type Series = readonly (bigint | null)[];

export interface Extent {
  min: bigint;
  max: bigint;
}

function known(values: Series): bigint[] {
  return values.filter((v): v is bigint => v !== null);
}

/** The range a chart spans, over the values that exist. `fromZero` pins the bottom to 0 (bars); a line fits its own values. */
export function extent(values: Series, fromZero: boolean): Extent {
  const present = known(values);
  if (present.length === 0) return { min: 0n, max: 0n };
  let min = fromZero ? 0n : present[0]!;
  let max = present[0]!;
  for (const v of present) {
    if (v < min) min = v;
    if (v > max) max = v;
  }
  return { min, max };
}

/** Where `value` sits between min (0) and max (1); a range of one value sits in the middle, or on the floor for bars. */
export function position(value: bigint, range: Extent, fromZero: boolean): number {
  const span = range.max - range.min;
  if (span <= 0n) return fromZero ? 0 : FLAT;
  const clamped = value < range.min ? range.min : value > range.max ? range.max : value;
  return Number(((clamped - range.min) * RATIO_SCALE) / span) / Number(RATIO_SCALE);
}

export interface Size {
  width: number;
  height: number;
}

interface Placed {
  index: number;
  value: bigint;
}

/** The stretches of consecutive values that exist, each with its slot number; a gap ends one and starts the next. */
function runs(values: Series): Placed[][] {
  const out: Placed[][] = [];
  let current: Placed[] = [];
  values.forEach((value, index) => {
    if (value === null) {
      if (current.length > 0) out.push(current);
      current = [];
      return;
    }
    current.push({ index, value });
  });
  if (current.length > 0) out.push(current);
  return out;
}

/**
 * The polylines of a line chart, one per stretch between gaps: first slot on the left edge, last on
 * the right, up is larger. A stretch of one value is drawn as a dot (the point twice).
 */
export function linePoints(values: Series, size: Size, range: Extent): string[] {
  const step = values.length > 1 ? size.width / (values.length - 1) : 0;
  return runs(values).map((run) => {
    const points = run.map((p) => `${(p.index * step).toFixed(1)},${(size.height - position(p.value, range, false) * size.height).toFixed(1)}`);
    return (points.length === 1 ? [points[0]!, points[0]!] : points).join(" ");
  });
}

export interface Bar {
  x: number;
  width: number;
  y: number;
  height: number;
}

const BAR_GAP_SHARE = 0.2;

/** One bar per value that exists, left to right, with a gap between them; a gap slot has no bar. A zero value is a bar of no height. */
export function bars(values: Series, size: Size, range: Extent): Bar[] {
  if (values.length === 0) return [];
  const slot = size.width / values.length;
  const width = slot * (1 - BAR_GAP_SHARE);
  return runs(values)
    .flat()
    .map(({ index, value }) => {
      const height = position(value, range, true) * size.height;
      return { x: index * slot + (slot - width) / 2, width, y: size.height - height, height };
    });
}

/** The ceiling drawn across a bar chart: a step per epoch, level over each bar's slot, broken where the epoch has none. */
export function stepPoints(values: Series, size: Size, range: Extent): string[] {
  if (values.length === 0) return [];
  const slot = size.width / values.length;
  return runs(values).map((run) =>
    run
      .flatMap(({ index, value }) => {
        const y = (size.height - position(value, range, true) * size.height).toFixed(1);
        return [`${(index * slot).toFixed(1)},${y}`, `${((index + 1) * slot).toFixed(1)},${y}`];
      })
      .join(" "),
  );
}
