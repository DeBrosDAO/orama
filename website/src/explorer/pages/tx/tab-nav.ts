/**
 * Which tab a key moves to in a tab list, or null when the key does nothing.
 * Arrows wrap around; Home and End jump to the ends.
 */
export function nextTabIndex(current: number, count: number, key: string): number | null {
  if (count <= 0 || current < 0 || current >= count) return null;
  switch (key) {
    case "ArrowRight":
      return (current + 1) % count;
    case "ArrowLeft":
      return (current - 1 + count) % count;
    case "Home":
      return 0;
    case "End":
      return count - 1;
    default:
      return null;
  }
}
