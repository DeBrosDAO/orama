/** The gateway's read-only chain proxy (docs/whitepaper/technical-reference/vol2/42-archive-and-indexer.md, "The gateway's chain proxy"). */
export const CHAIN_BASE = "/v1/chain";

/** How long one read may take before the reader is told the chain could not be reached. */
const REQUEST_TIMEOUT_MS = 15_000;

const HTTP_NOT_FOUND = 404;

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

export function createClient(fetcher: ChainFetch = (input, init) => fetch(input, init), base: string = CHAIN_BASE): ChainClient {
  return {
    async get(path: string): Promise<unknown> {
      let response: Response;
      try {
        response = await fetcher(`${base}/${path}`, {
          method: "GET",
          headers: { Accept: "application/json" },
          cache: "no-store",
          credentials: "omit",
          signal: AbortSignal.timeout(REQUEST_TIMEOUT_MS),
        });
      } catch {
        throw new ChainReadError("The chain could not be reached.");
      }
      if (response.status === HTTP_NOT_FOUND) throw new ChainNotFound();
      if (!response.ok) throw new ChainReadError(`The chain could not be read (HTTP ${response.status}).`);
      try {
        return await response.json();
      } catch {
        throw new ChainReadError("The chain answered something that is not JSON.");
      }
    },
  };
}
