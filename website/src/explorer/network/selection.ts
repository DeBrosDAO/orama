import { ChainReadError, chainBase } from "../data/chain/client";
import { CHAIN_PARAM, CUSTOM_NETWORK, EXPLORER_BASE, GATEWAY_PARAM, explorerPathsFor } from "../model/routes";
import type { ExplorerPaths } from "../model/routes";
import { findNetwork } from "./registry";
import type { Registry } from "./registry";

const CHAIN_ID = /^[a-z0-9]([a-z0-9-]{0,46}[a-z0-9])?$/;

/** The network a URL names, and everything the app derives from it. */
export interface Selection {
  /** The segment the network is chosen by: its registry name, or "custom". */
  key: string;
  /** What the reader sees it called. */
  label: string;
  custom: boolean;
  /** The gateway's https origin, or null for a custom network whose address is missing or refused. */
  gateway: string | null;
  /** Why a custom gateway cannot be read, written for the reader; null otherwise. */
  problem: string | null;
  /** The chain id the registry lists; null for a custom gateway, which no registry vouches for. */
  expectedChainId: string | null;
  /** The URL builders of this network. */
  paths: ExplorerPaths;
  /** The pathname the pages are routed by: /explorer plus the page, without the network segment. */
  routePath: string;
}

function customGateway(search: string): { gateway: string | null; problem: string | null } {
  const raw = new URLSearchParams(search).get(GATEWAY_PARAM);
  if (raw === null || raw === "") return { gateway: null, problem: null };
  try {
    chainBase(raw);
    return { gateway: raw, problem: null };
  } catch (err) {
    return { gateway: null, problem: err instanceof ChainReadError ? err.message : "That is not a gateway address." };
  }
}

/** The query a custom network's URLs carry: its gateway. */
export function customQuery(gateway: string): string {
  return `?${GATEWAY_PARAM}=${encodeURIComponent(gateway)}`;
}

/**
 * Which network a URL is for. The first segment after /explorer names a
 * registry network or "custom"; anything else is a page of the default
 * network, so every URL that existed before the switcher still opens.
 */
export function selectNetwork(pathname: string, search: string, registry: Registry): Selection {
  const rest = pathname.startsWith(`${EXPLORER_BASE}/`) ? pathname.slice(EXPLORER_BASE.length) : "";
  const [, first = "", ...more] = rest.split("/");
  const tail = more.length > 0 ? `/${more.join("/")}` : "";

  if (first === CUSTOM_NETWORK) {
    const { gateway, problem } = customGateway(search);
    return {
      key: CUSTOM_NETWORK,
      label: "Custom gateway",
      custom: true,
      gateway,
      problem,
      expectedChainId: null,
      paths: explorerPathsFor(CUSTOM_NETWORK, gateway === null ? "" : customQuery(gateway)),
      routePath: `${EXPLORER_BASE}${tail}`,
    };
  }

  const named = findNetwork(registry, first);
  const network = named ?? findNetwork(registry, registry.defaultName);
  if (network === undefined) throw new Error(`the registry has no default network "${registry.defaultName}"`);
  const isDefault = network.manifest.name === registry.defaultName;
  return {
    key: network.manifest.name,
    label: network.manifest.name,
    custom: false,
    gateway: network.gateway,
    problem: null,
    expectedChainId: network.manifest.chainId,
    paths: explorerPathsFor(isDefault ? null : network.manifest.name),
    routePath: named === undefined ? pathname : `${EXPLORER_BASE}${tail}`,
  };
}

/** The chain id a URL was made for (`?chain=`), or null when it has none or it is not a chain id. */
export function pinnedChain(search: string): string | null {
  const raw = new URLSearchParams(search).get(CHAIN_PARAM);
  return raw !== null && CHAIN_ID.test(raw) ? raw : null;
}

/** What a link to this page must carry besides its trail: the custom gateway and the chain id, when the URL has them. */
export function networkQuery(search: string): URLSearchParams {
  const from = new URLSearchParams(search);
  const out = new URLSearchParams();
  for (const key of [GATEWAY_PARAM, CHAIN_PARAM]) {
    const value = from.get(key);
    if (value !== null) out.set(key, value);
  }
  return out;
}

/** The query string with `chain` set to `chainId`, every other parameter kept. */
export function withPinnedChain(search: string, chainId: string): string {
  const q = new URLSearchParams(search);
  q.set(CHAIN_PARAM, chainId);
  return `?${q.toString()}`;
}

/** The network switcher's choices: what URL each leads to. A reset chain's old pages do not exist, so a switch opens the network's front page. */
export function switchTargets(registry: Registry): { key: string; label: string; to: string }[] {
  return registry.networks.map((n) => ({
    key: n.manifest.name,
    label: n.manifest.name,
    to: explorerPathsFor(n.manifest.name === registry.defaultName ? null : n.manifest.name).home,
  }));
}

/** The URL of a custom gateway's front page; throws ChainReadError for a gateway chainBase refuses. */
export function customHome(gateway: string): string {
  chainBase(gateway);
  return explorerPathsFor(CUSTOM_NETWORK, customQuery(gateway)).home;
}
