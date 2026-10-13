/** How long one read may take before the reader is told the chain could not be reached. */
const REQUEST_TIMEOUT_MS = 15_000;

const HTTP_NOT_FOUND = 404;
const HTTP_UNAVAILABLE = 503;
const HTTP_RATE_LIMITED = 429;
/**
 * What a reader can do about the statuses of a gateway's own trouble. Each stands for several causes the
 * gateway does not tell apart (core/pkg/gateway/handlers/chainread): a 502 is a node that could not be
 * reached or answered wrongly, and also an address or id of the right shape that the indexer refuses; a
 * 503 is an index that is busy, too many queries in flight on the gateway, too many head streams, or a
 * misconfigured proxy (the missing indexer is told apart before this, by its body).
 */
const UPSTREAM_ADVICE: Readonly<Record<number, string>> = {
  502: "could not get a usable answer from its chain node, or refused part of this request (check that the address or id is valid); try again in a minute.",
  503: "is busy or cannot serve this read just now (too many reads at once, or its chain service is not available); try again in a minute.",
  504: "did not get an answer from its chain node in time; try again in a minute.",
};
/** The gateway's words for an index route on a node with no indexer (core/pkg/gateway/handlers/chainread/index.go, msgNoIndexer). */
const NO_INDEXER_MARKER = "runs no chain indexer";
/** A 503 body is read only this far to tell the no-indexer answer from any other. */
const NO_INDEXER_BODY_MAX = 512;

/** The gateway named in a message to the reader: the host of the address, or a stand-in for an address that is not one. */
export function gatewayHost(address: string): string {
  try {
    return new URL(address).host;
  } catch {
    return "the gateway";
  }
}

/**
 * What a reader is told when the browser got no answer from the gateway at `address` (the chain proxy's
 * base, or any URL on that gateway). A script cannot tell why: a gateway that is down, a TLS certificate
 * this browser does not trust (a staging certificate, for one) and a gateway that does not allow this
 * site to read it (CORS) all end in the same failed request, and so does a gateway that predates the one that
 * lets a rate-limited answer through to this site (the browser hides a refusal without CORS headers). The
 * message names the gateway and the usual causes and says how to tell them apart; it never carries what the
 * browser reported.
 */
export function unreachableMessage(address: string, timedOut = false): string {
  const host = gatewayHost(address);
  const what = timedOut
    ? `The gateway at ${host} did not answer within ${REQUEST_TIMEOUT_MS / 1000} seconds.`
    : `This browser could not read the gateway at ${host}.`;
  return (
    `The chain could not be reached. ${what} Likely causes: the gateway is down or cannot be reached from here, its TLS certificate is not one ` +
    `this browser trusts (a staging certificate, for example), it does not allow reads from this site (CORS), or it is limiting how fast ` +
    `this address may read (an older gateway hides that answer from the browser). Open ${statusUrl(address)} in a new tab: a certificate ` +
    `warning there points to the certificate, a normal answer to CORS or the rate limit, no answer at all to the gateway being down.`
  );
}

/** The gateway's status route, on the same scheme and host as `address` (http for a gateway on this machine, https otherwise). */
function statusUrl(address: string): string {
  try {
    return `${new URL(address).origin}${CHAIN_PATH}/status`;
  } catch {
    return "the gateway's /v1/chain/status";
  }
}

/** A read that failed. The message is written for the reader: it never carries what the node said. */
export class ChainReadError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "ChainReadError";
  }
}

/** The thing asked for is not on the chain (HTTP 404). A lookup turns this into null. */
export class ChainNotFound extends ChainReadError {
  constructor() {
    super("That is not on the chain.");
    this.name = "ChainNotFound";
  }
}

/** The gateway answered 503 because the node it reached runs no chain indexer, so none of the index routes (blocks, epochs, supply) can be read from it. */
export class ChainIndexUnavailable extends ChainReadError {
  constructor() {
    super("This node runs no chain indexer, so it cannot serve this page. Another node of the network may; try again later.");
    this.name = "ChainIndexUnavailable";
  }
}

/** The gateway's read-only chain proxy (docs/whitepaper/technical-reference/vol2/42-archive-and-indexer.md, "The gateway's chain proxy"). */
const CHAIN_PATH = "/v1/chain";

/**
 * The gateway of the registry's default network (stagenet), which a source reads
 * when it is given no other base; the explorer's network switcher names the
 * gateway of the network a URL selects (src/explorer/network). The page is served
 * from orama.network, which has no chain behind it, so the browser calls the
 * network's own gateway across origins; that gateway answers the explorer's
 * origin on the chain's read routes (core/pkg/gateway/cors_explorer.go).
 */
export const DEFAULT_GATEWAY_ORIGIN = "https://stagenet.orama.network";

const LOCAL_HOSTS = new Set(["localhost", "127.0.0.1"]);

/**
 * The chain proxy's base URL on a network's gateway. The origin must be exactly
 * an origin (no path, query or trailing slash) and https, or http for a gateway
 * on this machine: a reader's balance and address lookups never travel in clear.
 */
export function chainBase(gatewayOrigin: string): string {
  let url: URL;
  try {
    url = new URL(gatewayOrigin);
  } catch {
    throw new ChainReadError(`"${gatewayOrigin}" is not a gateway address.`);
  }
  const local = url.protocol === "http:" && LOCAL_HOSTS.has(url.hostname);
  if ((url.protocol !== "https:" && !local) || url.origin !== gatewayOrigin) {
    throw new ChainReadError(`"${gatewayOrigin}" is not an https gateway origin such as https://stagenet.orama.network.`);
  }
  return `${url.origin}${CHAIN_PATH}`;
}

/** The chain proxy of the default network. */
export const CHAIN_BASE = chainBase(DEFAULT_GATEWAY_ORIGIN);

export type ChainFetch = (input: string, init?: RequestInit) => Promise<Response>;

export interface ChainClient {
  /** GET one proxy path and return its JSON body. Rejects with ChainNotFound for a 404. */
  get(path: string): Promise<unknown>;
}

/** A path from segments, each percent-encoded, so no value can add a segment or a query. */
export function chainPath(...segments: string[]): string {
  return segments.map(encodeURIComponent).join("/");
}

/** The query string of a path: keys and values are encoded, and an undefined value is left out. */
export function withQuery(path: string, query: Record<string, string | number | undefined>): string {
  const pairs = Object.entries(query)
    .filter((entry): entry is [string, string | number] => entry[1] !== undefined)
    .map(([key, value]) => `${encodeURIComponent(key)}=${encodeURIComponent(String(value))}`);
  return pairs.length === 0 ? path : `${path}?${pairs.join("&")}`;
}

async function saysNoIndexer(response: Response): Promise<boolean> {
  try {
    return (await response.text()).slice(0, NO_INDEXER_BODY_MAX).includes(NO_INDEXER_MARKER);
  } catch {
    return false;
  }
}

export function createClient(fetcher: ChainFetch = (input, init) => fetch(input, init), base: string = CHAIN_BASE): ChainClient {
  return {
    async get(path: string): Promise<unknown> {
      let response: Response;
      try {
        response = await fetcher(`${base}/${path}`, {
          method: "GET",
          headers: { Accept: "application/json" },
          // The browser's own cache, which follows the Cache-Control the gateway answers with: no-store for
          // the live reads, and a few seconds (a block) or ten minutes (a closed epoch) for the index.
          cache: "default",
          credentials: "omit",
          signal: AbortSignal.timeout(REQUEST_TIMEOUT_MS),
        });
      } catch (err) {
        throw new ChainReadError(unreachableMessage(base, err instanceof DOMException && err.name === "TimeoutError"));
      }
      if (response.status === HTTP_NOT_FOUND) throw new ChainNotFound();
      if (response.status === HTTP_UNAVAILABLE && (await saysNoIndexer(response))) throw new ChainIndexUnavailable();
      if (response.status === HTTP_RATE_LIMITED) {
        throw new ChainReadError(
          `The chain could not be read (HTTP 429). ${gatewayHost(base)} is limiting how fast this browser may read; wait a minute and try again.`,
        );
      }
      const advice = UPSTREAM_ADVICE[response.status];
      if (advice !== undefined) throw new ChainReadError(`The chain could not be read (HTTP ${response.status}). ${gatewayHost(base)} ${advice}`);
      if (!response.ok) throw new ChainReadError(`The chain could not be read (HTTP ${response.status}).`);
      try {
        return await response.json();
      } catch {
        throw new ChainReadError("The chain answered something that is not JSON.");
      }
    },
  };
}
