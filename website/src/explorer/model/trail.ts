import { EXPLORER_BASE, explorerPaths } from "./routes";
import { shortAddress, shortHash } from "./units";

/** Long enough to follow a real investigation, short enough to stay readable. */
export const MAX_TRAIL = 12;

const SHARE_PARAM = "trail";
const SHARE_SEPARATOR = "|";

/**
 * The trail is the ordered list of explorer pages the reader has followed
 * since the home page. Visiting a page already on the trail goes back to it
 * (dropping what came after); visiting a new page appends. Home clears it.
 */
export function nextTrail(trail: readonly string[], path: string): string[] {
  if (path === explorerPaths.home) return [];
  const seen = trail.indexOf(path);
  if (seen >= 0) return trail.slice(0, seen + 1);
  const next = [...trail, path];
  return next.length > MAX_TRAIL ? next.slice(next.length - MAX_TRAIL) : next;
}

/** A short crumb label derived from the path alone. */
export function crumbLabel(path: string): string {
  const [, , kind, id] = path.split("/");
  switch (kind) {
    case "tx":
      return `Tx ${shortHash(id ?? "")}`;
    case "wallet":
      return `Wallet ${shortAddress(id ?? "")}`;
    case "block":
      return `Block ${id ?? ""}`;
    case "validators":
      return "Validators";
    default:
      return "Explorer";
  }
}

const KNOWN_PATH = new RegExp(`^${EXPLORER_BASE}/(validators|(tx|wallet|block)/[A-Za-z0-9]+)$`);

/** True for a path this explorer serves. Used to reject a hostile shared trail. */
export function isTrailPath(path: string): boolean {
  return KNOWN_PATH.test(path);
}

/** A link that reopens the current page with the whole trail attached. */
export function shareUrl(origin: string, current: string, trail: readonly string[]): string {
  if (trail.length === 0) return `${origin}${current}`;
  const q = new URLSearchParams({ [SHARE_PARAM]: trail.join(SHARE_SEPARATOR) });
  return `${origin}${current}?${q.toString()}`;
}

/**
 * Read a shared trail from a query string; anything that is not an explorer
 * path is dropped, and a path repeated later is dropped so each crumb is unique.
 */
export function readSharedTrail(search: string): string[] {
  const raw = new URLSearchParams(search).get(SHARE_PARAM);
  if (!raw) return [];
  return [...new Set(raw.split(SHARE_SEPARATOR).filter(isTrailPath))].slice(0, MAX_TRAIL);
}
