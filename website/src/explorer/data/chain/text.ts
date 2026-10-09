/** Control characters and the characters that reorder or hide text: they have no place in a label. */
const UNSAFE = /[\u0000-\u001f\u007f-\u009f؜​-‏‪-‮⁠-⁩﻿]/g;

const ELLIPSIS = "…";

/**
 * Text that anyone could have written to the chain (a moniker, a memo, a failure
 * reason), made safe to show: control and bidirectional-override characters are
 * dropped, and a long text is cut.
 */
export function clean(text: string, max: number): string {
  const safe = text.replace(UNSAFE, "").trim();
  return safe.length > max ? `${safe.slice(0, max - 1)}${ELLIPSIS}` : safe;
}
