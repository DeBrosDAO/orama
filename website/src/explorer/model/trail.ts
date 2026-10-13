import { parseExplorerPath } from "./routes";
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
  if (parseExplorerPath(path).kind === "home") return [];
  const seen = trail.indexOf(path);
  if (seen >= 0) return trail.slice(0, seen + 1);
  const next = [...trail, path];
  return next.length > MAX_TRAIL ? next.slice(next.length - MAX_TRAIL) : next;
}

/** A path segment as the reader would type it; the segment as it is when it is not valid percent-encoding. */
function readableSegment(segment: string): string {
  try {
    return decodeURIComponent(segment);
  } catch {
    return segment;
  }
}

/** A short crumb label derived from the path alone. */
export function crumbLabel(path: string): string {
  const { kind, id } = parseExplorerPath(path);
  switch (kind) {
    case "tx":
      return `Tx ${shortHash(id)}`;
    case "wallet":
      return `Wallet ${shortAddress(id)}`;
    case "validator":
      return `Validator ${shortAddress(id)}`;
    case "block":
      return `Block ${id}`;
    case "validators":
      return "Validators";
    case "nodes":
      return "Nodes";
    case "node":
      return `Node ${readableSegment(id)}`;
    case "epochs":
      return "Epochs";
    case "epoch":
      return `Epoch ${readableSegment(id)}`;
    case "emission":
      return "Emission";
    case "economics":
      return "Supply";
    case "rewards":
      return "Rewards";
    case "storage":
      return "Storage";
    case "tor":
      return "Tor";
    default:
      return assetCrumbLabel(kind, id) ?? "Explorer";
  }
}

const ASSET_LISTS: Readonly<Record<string, string>> = {
  tokens: "Tokens",
  nfts: "NFTs",
  contracts: "Contracts",
  market: "Market",
  governance: "Governance",
  shielded: "Shielded pool",
};

/** The crumb of a token, NFT, contract, market, governance or shielded page; null for any other kind. */
function assetCrumbLabel(kind: string, id: string): string | null {
  const list = ASSET_LISTS[kind];
  if (list !== undefined) return list;
  switch (kind) {
    case "token":
      return "Token";
    case "nft":
      return `NFT ${shortHash(id)}`;
    case "collection":
      return `Collection ${id}`;
    case "code":
      return `Code ${id}`;
    case "contract":
      return `Contract ${shortAddress(id)}`;
    case "listing":
      return `Listing ${id}`;
    case "proposal":
      return `Proposal ${id}`;
    default:
      return null;
  }
}

const KNOWN_PATH =
  /^\/explorer(\/[a-z][a-z0-9-]{0,31})?\/(validators|nodes|storage|tor|tokens|nfts|contracts|market|governance|shielded|(tx|wallet|block|validator|nfts|nft|collection|code|contract|listing|proposal)\/[A-Za-z0-9]+|node\/[A-Za-z0-9%][A-Za-z0-9._:%-]{0,191}|token\/[A-Za-z0-9]+\/[a-z0-9]+)$/;

/** True for a path this explorer serves. Used to reject a hostile shared trail. */
export function isTrailPath(path: string): boolean {
  return KNOWN_PATH.test(path);
}

/**
 * A link that reopens the current page with the whole trail attached. `query`
 * is what the page needs besides the trail to open again: a custom network's
 * gateway, and the chain id the trail was followed on.
 */
export function shareUrl(origin: string, current: string, trail: readonly string[], query: URLSearchParams = new URLSearchParams()): string {
  const q = new URLSearchParams(query);
  if (trail.length > 0) q.set(SHARE_PARAM, trail.join(SHARE_SEPARATOR));
  const text = q.toString();
  return text === "" ? `${origin}${current}` : `${origin}${current}?${text}`;
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
