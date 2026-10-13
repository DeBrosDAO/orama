/** No counter of the chain gets near this; the search stops here rather than run forever on a source that answers yes to everything. */
const MAX_ID = 2 ** 40;

/**
 * The highest id of a record that the chain numbers 1, 2, 3 … and never deletes (a collection, a
 * proposal), found by asking whether ids exist: doubling until one does not, then halving the gap.
 * That is about twice log2 of the count in queries, and none when nothing exists. `exists` must be
 * true for every id up to the newest and false past it.
 */
export async function newestId(exists: (id: number) => Promise<boolean>): Promise<number> {
  if (!(await exists(1))) return 0;
  let known = 1;
  let step = 2;
  while (step <= MAX_ID && (await exists(step))) {
    known = step;
    step *= 2;
  }
  let unknown = Math.min(step, MAX_ID + 1);
  while (unknown - known > 1) {
    const middle = Math.floor((known + unknown) / 2);
    if (await exists(middle)) known = middle;
    else unknown = middle;
  }
  return known;
}

/** The ids of a page of records, newest first: `size` ids ending at `top`, never below 1. */
export function idsDescending(top: number, size: number): number[] {
  const ids: number[] = [];
  for (let id = top; id >= 1 && ids.length < size; id -= 1) ids.push(id);
  return ids;
}
