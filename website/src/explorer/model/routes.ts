import { ASSET_PAGE_KINDS, assetPathsFor } from "./assets-routes";
import type { AssetPaths } from "./assets-routes";

/**
 * Every explorer URL is built here, so a route change is one edit.
 *
 * A URL is /explorer[/<network>]/<page>. The registry's default network has no
 * segment (/explorer/tx/ABC, the form every link had before the explorer read
 * more than one network); any other network is named first
 * (/explorer/testnet/tx/ABC), and a gateway the reader typed is /explorer/custom
 * with the gateway in the `gateway` query. A page's URL also carries `chain`, the
 * chain id the page was made on, so a link kept across a reset says so.
 */
export const EXPLORER_BASE = "/explorer";

/** The network segment of a gateway the reader named; no registry network may use it. */
export const CUSTOM_NETWORK = "custom";
export const GATEWAY_PARAM = "gateway";
export const CHAIN_PARAM = "chain";

/** The first segment of every page, which a network segment can therefore never be. */
export const PAGE_KINDS = ["validators", "validator", "tx", "wallet", "block", "epochs", "epoch", "emission", "economics", "nodes", "node", "storage", "tor", "rewards"] as const;
export type PageKind = (typeof PAGE_KINDS)[number] | (typeof ASSET_PAGE_KINDS)[number];

/** True for the first segment of a page of the explorer, which no network may be named. */
export const isPageKind = (segment: string): segment is PageKind =>
  (PAGE_KINDS as readonly string[]).includes(segment) || (ASSET_PAGE_KINDS as readonly string[]).includes(segment);

export interface ExplorerPaths {
  home: string;
  validators: string;
  /** `address` is the validator's operator wallet (orama1…). */
  validator: (address: string) => string;
  tx: (hash: string) => string;
  wallet: (address: string) => string;
  block: (height: number) => string;
  epochs: string;
  epoch: (epoch: number) => string;
  emission: string;
  economics: string;
  rewards: string;
  nodes: string;
  /** `id` is the node id the chain knows the node by. */
  node: (id: string) => string;
  storage: string;
  tor: string;
  /** A pathname of this explorer (one the trail kept) with what this network needs to open again. */
  keep: (pathname: string) => string;
  /** The pages for tokens, NFTs, contracts, the market, governance and the shielded pool. */
  assets: AssetPaths;
}

/**
 * The URL builders of one network. `segment` is its first path segment, or
 * null for the default network; `query` is appended to every URL (a custom
 * network's `?gateway=…`).
 */
export function explorerPathsFor(segment: string | null, query = ""): ExplorerPaths {
  const base = segment === null ? EXPLORER_BASE : `${EXPLORER_BASE}/${segment}`;
  return {
    home: `${base}${query}`,
    validators: `${base}/validators${query}`,
    validator: (address) => `${base}/validator/${encodeURIComponent(address)}${query}`,
    tx: (hash) => `${base}/tx/${encodeURIComponent(hash)}${query}`,
    wallet: (address) => `${base}/wallet/${encodeURIComponent(address)}${query}`,
    block: (height) => `${base}/block/${height}${query}`,
    epochs: `${base}/epochs${query}`,
    epoch: (epoch) => `${base}/epoch/${epoch}${query}`,
    emission: `${base}/emission${query}`,
    economics: `${base}/economics${query}`,
    rewards: `${base}/rewards${query}`,
    nodes: `${base}/nodes${query}`,
    node: (id) => `${base}/node/${encodeURIComponent(id)}${query}`,
    storage: `${base}/storage${query}`,
    tor: `${base}/tor${query}`,
    keep: (pathname) => `${pathname}${query}`,
    assets: assetPathsFor(base, query),
  };
}

/** The default network's builders: the URLs that existed before the network switcher. */
export const explorerPaths = explorerPathsFor(null);

const NETWORK_SEGMENT = /^[a-z][a-z0-9-]{0,31}$/;

export interface ParsedPath {
  /** The network segment, or null for the default network. */
  network: string | null;
  kind: PageKind | "home" | "other";
  /** The page's identifier segment (hash, address, height, valoper), raw. */
  id: string;
}

/** Splits an explorer pathname into its network segment and page. Knows no registry: any segment that is not a page name and looks like a network name is read as one. */
export function parseExplorerPath(pathname: string): ParsedPath {
  const segs = pathname.split("/").filter((s) => s !== "").slice(1);
  let network: string | null = null;
  const first = segs[0];
  if (first !== undefined && !isPageKind(first) && NETWORK_SEGMENT.test(first)) {
    network = first;
    segs.shift();
  }
  const kind = segs[0];
  if (kind === undefined) return { network, kind: "home", id: "" };
  if (isPageKind(kind)) return { network, kind, id: segs[1] ?? "" };
  return { network, kind: "other", id: "" };
}
