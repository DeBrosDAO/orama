import { RelayCode, RelayError, SDKError } from "../errors";
import type { FetchCapability, FetchOptions, FetchTransport } from "./fetch-transport";
import { aborted, exchange } from "./relay-exchange";
import { storageRefusal } from "./relay-refusals";

/**
 * Node-only. Published as `@debros/orama/relay` so the core entry never
 * references `node:tls` or `node:stream` and stays bundleable for React Native
 * and browsers. Those apps need a native helper that terminates TLS end to end
 * on the device and speaks the relay's WebSocket framing.
 */

const RELAY_PATH = "/v1/proxy/relay";
const RELAYED_FETCH_PATH = "/v1/storage/relayed/";
const NAMESPACE_PORT = 443;
const CID_PATTERN = /^[A-Za-z0-9]+$/;
const TOKEN_PATTERN = /^[\x21-\x7e]+$/;
/** A hostname in letters, digits and hyphens (RFC 952/1123): what the relay accepts as a destination. */
const LDH_HOST_PATTERN = /^(?=.{1,253}$)[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)*$/i;

const DEFAULT_MAX_BODY_BYTES = 64 << 20; // the relay's per-stream cap
const DEFAULT_TIMEOUT_MS = 5 * 60_000; // the relay's per-stream lifetime

export interface RelayedFetchOptions {
  /** Base URLs of relay gateways (`https://node-1.example.com`). One is picked at random per fetch. */
  relays: string[];
  /** The namespace gateway host that holds the content (`ns-foo.example.com`). Never used as a relay. */
  namespaceHost: string;
  /** Wait a random 0..jitterMs after the tunnel is up and before the request is sent. Default 0. */
  jitterMs?: number;
  /**
   * `"fresh"` (default): the relay builds a new Tor circuit for every fetch.
   * `"session"`: reuse one circuit across a batch; cheaper, but the storage
   * node's exit can then link the fetches of that batch.
   */
  circuit?: "fresh" | "session";
  /** Trusted CA certificates for the namespace host, PEM (Node `tls` `ca` option). Default: system roots. */
  ca?: string | Buffer | Array<string | Buffer>;
  /** Largest response body accepted. Default 64 MiB. */
  maxBodyBytes?: number;
  /** Whole-fetch deadline. Default 5 minutes. */
  timeoutMs?: number;
  /** Source of randomness in [0,1), for relay choice and jitter. Default `Math.random`. */
  random?: () => number;
}

/** Lowercased host of a relay base URL; a bare `host[:port]` is read as https. */
function parseRelay(relay: string): URL {
  const url = new URL(/^[a-z][a-z0-9+.-]*:\/\//i.test(relay) ? relay : `https://${relay}`);
  if (url.protocol === "https:") url.protocol = "wss:";
  else if (url.protocol === "http:") url.protocol = "ws:";
  if (url.protocol !== "wss:" && url.protocol !== "ws:") {
    throw new RelayError(`relay ${relay} must be an http(s) or ws(s) URL`, 0, RelayCode.NoRelay);
  }
  return url;
}

/**
 * Pick a relay uniformly at random from `relays`, never one whose host is the
 * namespace host: a relay that is the storage node would see the client's
 * address and the CID together.
 */
export function pickRelay(relays: string[], namespaceHost: string, random: () => number = Math.random): string {
  const host = namespaceHost.toLowerCase();
  const usable = relays.filter((r) => parseRelay(r).hostname.toLowerCase() !== host);
  if (usable.length === 0) {
    throw new RelayError(
      `no relay is usable for ${namespaceHost}: the relay list is empty or only names the namespace host`,
      0,
      RelayCode.NoRelay
    );
  }
  return usable[Math.min(usable.length - 1, Math.floor(random() * usable.length))];
}

function isNode(): boolean {
  const proc = (globalThis as { process?: { versions?: { node?: string } } }).process;
  return typeof proc?.versions?.node === "string";
}

/**
 * Downloads with a fetch capability through a relay, so the storage node sees
 * a Tor exit instead of the caller's address.
 *
 * Per fetch: pick a relay at random, open its WebSocket with no credential,
 * run TLS to the namespace host inside the WebSocket's byte stream (the relay
 * carries ciphertext), and send the capability-authenticated request. The
 * relay sees the caller's address and the namespace host; it never sees the
 * CID, the capability or the content. The storage node sees the CID and the
 * capability, but not the caller's address.
 *
 * Any failure of the relay or the tunnel is thrown as a `RelayError`. There
 * is no fallback to a direct request: that would send the caller's address to
 * the node this class exists to hide it from.
 */
export class RelayedFetch implements FetchTransport {
  private readonly relays: string[];
  private readonly namespaceHost: string;
  private readonly jitterMs: number;
  private readonly circuit: "fresh" | "session";
  private readonly ca?: RelayedFetchOptions["ca"];
  private readonly maxBodyBytes: number;
  private readonly timeoutMs: number;
  private readonly random: () => number;

  constructor(options: RelayedFetchOptions) {
    if (!LDH_HOST_PATTERN.test(options.namespaceHost)) {
      throw new SDKError(
        `namespaceHost must be a hostname of letters, digits and hyphens, got ${JSON.stringify(options.namespaceHost)}`,
        400,
        "VALIDATION_FAILED"
      );
    }
    this.relays = [...options.relays];
    this.namespaceHost = options.namespaceHost;
    this.jitterMs = options.jitterMs ?? 0;
    this.circuit = options.circuit ?? "fresh";
    this.ca = options.ca;
    this.maxBodyBytes = options.maxBodyBytes ?? DEFAULT_MAX_BODY_BYTES;
    this.timeoutMs = options.timeoutMs ?? DEFAULT_TIMEOUT_MS;
    this.random = options.random ?? Math.random;
    // Fail at construction, not on the first fetch, when no relay can be used.
    pickRelay(this.relays, this.namespaceHost, () => 0);
  }

  async fetch(cid: string, cap: FetchCapability, opts?: FetchOptions): Promise<Uint8Array> {
    if (!CID_PATTERN.test(cid)) {
      throw new SDKError(`invalid CID: ${JSON.stringify(cid)}`, 400, "VALIDATION_FAILED");
    }
    if (!TOKEN_PATTERN.test(cap.token)) {
      throw new SDKError("fetch capability token is malformed", 400, "VALIDATION_FAILED");
    }
    if (opts?.signal?.aborted) throw aborted();
    if (!isNode()) {
      throw new RelayError(
        "RelayedFetch needs Node.js (node:tls). React Native and browser apps need a native helper that runs TLS to the namespace host over the relay stream.",
        0,
        RelayCode.UnsupportedRuntime
      );
    }

    const relay = parseRelay(pickRelay(this.relays, this.namespaceHost, this.random));
    relay.pathname = RELAY_PATH;
    relay.search = new URLSearchParams({
      host: this.namespaceHost,
      port: String(NAMESPACE_PORT),
      ...(this.circuit === "session" ? { circuit: "session" } : {}),
    }).toString();

    const response = await exchange({
      relay,
      namespaceHost: this.namespaceHost,
      request: this.request(cid, cap.token),
      ca: this.ca,
      maxBodyBytes: this.maxBodyBytes,
      timeoutMs: this.timeoutMs,
      jitterMs: this.jitterMs,
      random: this.random,
      signal: opts?.signal,
    });
    if (response.status !== 200) throw storageRefusal(response);
    return response.body;
  }

  /** The exact bytes sent through the tunnel. */
  private request(cid: string, token: string): string {
    return (
      `GET ${RELAYED_FETCH_PATH}${cid} HTTP/1.1\r\n` +
      `Host: ${this.namespaceHost}\r\n` +
      `X-Orama-Fetch-Cap: ${token}\r\n` +
      `Connection: close\r\n\r\n`
    );
  }
}
