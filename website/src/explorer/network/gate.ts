/**
 * A custom gateway is a server the registry does not vouch for, and a link can
 * name one. Opening such a link must not make the browser call that server
 * before the reader agrees, so the page asks first. Typing the gateway into the
 * switcher, or pressing the button the page shows, is the agreement. It lasts
 * for the tab (sessionStorage): following links inside the explorer and
 * reloading do not ask again, a link opened in a new tab does.
 */
const STORAGE_KEY = "orama-explorer-confirmed-gateways";
const MAX_REMEMBERED = 16;

/** Agreements made on this page load. sessionStorage can be missing or refused (private windows, blocked site data); then an agreement lasts until the page is closed. */
const remembered = new Set<string>();

function stored(): string[] {
  try {
    const parsed: unknown = JSON.parse(globalThis.sessionStorage?.getItem(STORAGE_KEY) ?? "[]");
    return Array.isArray(parsed) ? parsed.filter((g): g is string => typeof g === "string") : [];
  } catch {
    return [];
  }
}

/** True when the reader has agreed, in this tab, to read from `gateway`. */
export function isConfirmed(gateway: string): boolean {
  return remembered.has(gateway) || stored().includes(gateway);
}

export function confirmGateway(gateway: string): void {
  remembered.add(gateway);
  try {
    const next = [...new Set([...stored(), gateway])].slice(-MAX_REMEMBERED);
    globalThis.sessionStorage?.setItem(STORAGE_KEY, JSON.stringify(next));
  } catch {
    // The agreement is kept in memory above; only its survival of a reload is lost.
  }
}

/** Forgets every agreement, so the next link to a custom gateway asks again. */
export function forgetConfirmedGateways(): void {
  remembered.clear();
  try {
    globalThis.sessionStorage?.removeItem(STORAGE_KEY);
  } catch {
    // Nothing was stored to forget.
  }
}
