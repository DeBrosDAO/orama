const SECOND = 1000;
const MINUTE = 60 * SECOND;
const HOUR = 60 * MINUTE;
const DAY = 24 * HOUR;

export const TIME = { SECOND, MINUTE, HOUR, DAY } as const;

/** "14 s ago", "3 min ago", "2 h ago", "5 d ago", then the date. */
export function formatRelative(iso: string, nowMs: number): string {
  const diff = nowMs - Date.parse(iso);
  if (Number.isNaN(diff)) return "";
  if (diff < 0) return "just now";
  if (diff < 5 * SECOND) return "just now";
  if (diff < MINUTE) return `${Math.floor(diff / SECOND)} s ago`;
  if (diff < HOUR) return `${Math.floor(diff / MINUTE)} min ago`;
  if (diff < DAY) return `${Math.floor(diff / HOUR)} h ago`;
  if (diff < 30 * DAY) return `${Math.floor(diff / DAY)} d ago`;
  return formatDate(iso);
}

const MONTHS = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];

/** "12 Aug 2026". */
export function formatDate(iso: string): string {
  const d = new Date(iso);
  return `${d.getUTCDate()} ${MONTHS[d.getUTCMonth()]} ${d.getUTCFullYear()}`;
}

/** "2026-09-29 14:02:11 UTC": the exact time, shown on hover. */
export function formatUtc(iso: string): string {
  return `${iso.slice(0, 10)} ${iso.slice(11, 19)} UTC`;
}

/** Day heading for grouped lists: Today, Yesterday, or "12 Aug". */
export function formatDayHeading(iso: string, nowMs: number): string {
  const startOfDay = (ms: number) => Math.floor(ms / DAY) * DAY;
  const days = (startOfDay(nowMs) - startOfDay(Date.parse(iso))) / DAY;
  if (days <= 0) return "Today";
  if (days === 1) return "Yesterday";
  const d = new Date(iso);
  return `${d.getUTCDate()} ${MONTHS[d.getUTCMonth()]}`;
}

/** "9 h 22 m", for a countdown to a future instant. */
export function formatCountdown(iso: string, nowMs: number): string {
  const ms = Math.max(0, Date.parse(iso) - nowMs);
  const h = Math.floor(ms / HOUR);
  const m = Math.floor((ms % HOUR) / MINUTE);
  return h > 0 ? `${h} h ${m} m` : `${m} m`;
}
