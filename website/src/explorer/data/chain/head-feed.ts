import type { Head } from "../../model/types";
import { nextHead } from "../head";
import { ChainReadError, gatewayHost, unreachableMessage } from "./client";
import { iso, rec, str, uint } from "./wire";

/**
 * The chain's head over the gateway's server-sent event stream (/v1/chain/head/stream,
 * core/pkg/gateway/handlers/chainread/head.go). The gateway reads the node once a second for all its
 * readers and tells each open page when the height moves, so a page asks for nothing: it listens.
 *
 * There is no other way to learn the head here. A browser without EventSource, or a stream that
 * cannot be opened, is an error the page shows; it is not met by asking for the status on a timer,
 * which would be the per-reader load the stream exists to remove. EventSource reconnects by itself
 * when the connection drops or the gateway ends a stream (it does, after 100 s). It does not when the
 * gateway refuses the stream (429 or 503: too many open): it gives up and closes, so the feed opens
 * it again itself, further apart each time it is refused.
 */

/** What a feed needs of an EventSource, so a test can stand in for one. */
export interface EventSourceLike {
  addEventListener(type: string, listener: (event: { data?: unknown }) => void): void;
  close(): void;
  onerror: ((event: Event) => unknown) | null;
  /** EventSource.CLOSED (2) once the browser has given up on the connection. */
  readonly readyState: number;
}

export type EventSourceFactory = (url: string) => EventSourceLike;

/** How long the first head may take to arrive before the reader is told the chain could not be reached. */
const FIRST_HEAD_TIMEOUT_MS = 15_000;
/** EventSource.readyState once the browser will not try again. */
const SOURCE_CLOSED = 2;
/** How long to wait before opening a stream the gateway refused, doubling on each refusal up to the ceiling. */
const REOPEN_MIN_MS = 5_000;
const REOPEN_MAX_MS = 60_000;
const MAX_CHAIN_ID = 64;

const CONNECTION_LOST = "The live connection to the chain was lost; it reconnects by itself.";
/** A stream the browser gave up on without a head: the gateway answered an error status, or the browser refused the answer. EventSource says no more than that. */
const refusedMessage = (streamUrl: string) =>
  `The gateway at ${gatewayHost(streamUrl)} refused the live connection: it may have too many open, or it may not allow reads from this site (CORS). Trying again shortly.`;
const NO_EVENT_SOURCE = "This browser cannot follow the chain live.";

/** The browser's EventSource, or null where there is none (a server render, an old browser). */
export function browserEventSource(): EventSourceFactory | null {
  if (typeof EventSource === "undefined") return null;
  return (url) => new EventSource(url);
}

export function readHeadEvent(data: unknown): Head {
  if (typeof data !== "string") throw new ChainReadError("The chain sent a malformed head.");
  let raw: unknown;
  try {
    raw = JSON.parse(data);
  } catch {
    throw new ChainReadError("The chain sent a malformed head.");
  }
  const h = rec(raw, "head");
  return { height: uint(h.height, "block height"), time: iso(h.time, "block time"), chainId: str(h.chain_id, "chain id", MAX_CHAIN_ID) };
}

type Subscriber = { listener: (head: Head) => void; onError: (error: Error) => void; last: Head | null };

export interface HeadFeed {
  /** The current head: the one the stream sends when it opens, or the newest since. Rejects if none arrives. */
  current(): Promise<Head>;
  /**
   * Call `listener` whenever the head advances (or the chain id changes, which is a reset and may be
   * a lower height). It is not called for the head already known. `onError` is called whenever the
   * stream says the chain cannot be read or the connection drops; the subscription goes on.
   */
  subscribe(listener: (head: Head) => void, onError: (error: Error) => void): () => void;
}

/** One stream shared by every reader, opened when the first needs it and closed when the last is done. */
export function createHeadFeed(streamUrl: string, open: EventSourceFactory | null): HeadFeed {
  const unreachable = () => new ChainReadError(unreachableMessage(streamUrl));
  const subscribers = new Set<Subscriber>();
  const waiting = new Set<{ resolve: (h: Head) => void; reject: (e: Error) => void }>();
  let source: EventSourceLike | null = null;
  let last: Head | null = null;
  let refusals = 0;
  let reopen: ReturnType<typeof setTimeout> | null = null;

  const fail = (error: Error) => {
    for (const s of subscribers) s.onError(error);
  };
  const failWaiting = (error: Error) => {
    for (const w of waiting) w.reject(error);
    waiting.clear();
    release();
  };

  const onHead = (event: { data?: unknown }) => {
    let head: Head;
    try {
      head = readHeadEvent(event.data);
    } catch (err) {
      fail(err instanceof Error ? err : new ChainReadError("The chain sent a malformed head."));
      return;
    }
    refusals = 0;
    last = nextHead(last, head);
    for (const w of waiting) w.resolve(last);
    waiting.clear();
    release();
    for (const s of subscribers) {
      if (s.last !== null && (s.last.chainId !== head.chainId || head.height > s.last.height)) s.listener(head);
      s.last = nextHead(s.last, head);
    }
  };

  const connect = () => {
    // While a refused stream waits to be opened again, a new reader waits with it: opening at once would skip the wait.
    if (source !== null || reopen !== null) return;
    if (open === null) throw new ChainReadError(NO_EVENT_SOURCE);
    const opened = open(streamUrl);
    opened.addEventListener("head", onHead);
    opened.addEventListener("unavailable", () => {
      const error = unreachable();
      failWaiting(error);
      fail(error);
    });
    opened.onerror = () => {
      if (source !== opened) return;
      if (opened.readyState !== SOURCE_CLOSED) {
        // Nothing has ever been received: there was no connection to lose, the gateway cannot be reached.
        fail(last === null ? unreachable() : new ChainReadError(CONNECTION_LOST));
        return;
      }
      // The browser does not retry a stream the gateway refused. Let go of it, say so, and open it again later.
      opened.close();
      source = null;
      const error = new ChainReadError(refusedMessage(streamUrl));
      failWaiting(error);
      fail(error);
      scheduleReopen();
    };
    source = opened;
  };

  const scheduleReopen = () => {
    if (reopen !== null || (subscribers.size === 0 && waiting.size === 0)) return;
    const delay = Math.min(REOPEN_MAX_MS, REOPEN_MIN_MS * 2 ** refusals);
    refusals += 1;
    reopen = setTimeout(() => {
      reopen = null;
      if (subscribers.size > 0) connect();
    }, delay);
  };

  function release() {
    if (subscribers.size > 0 || waiting.size > 0) return;
    if (reopen !== null) {
      clearTimeout(reopen);
      reopen = null;
    }
    refusals = 0;
    if (source === null) return;
    source.close();
    source = null;
    last = null;
  }

  return {
    current(): Promise<Head> {
      if (last !== null) return Promise.resolve(last);
      return new Promise<Head>((resolve, reject) => {
        try {
          connect();
        } catch (err) {
          reject(err);
          return;
        }
        const timer = setTimeout(() => {
          waiting.delete(entry);
          entry.reject(unreachable());
          release();
        }, FIRST_HEAD_TIMEOUT_MS);
        const entry = {
          resolve: (h: Head) => {
            clearTimeout(timer);
            resolve(h);
          },
          reject: (e: Error) => {
            clearTimeout(timer);
            reject(e);
          },
        };
        waiting.add(entry);
      });
    },

    subscribe(listener, onError): () => void {
      const subscriber: Subscriber = { listener, onError, last };
      try {
        connect();
      } catch (err) {
        onError(err instanceof Error ? err : new ChainReadError(NO_EVENT_SOURCE));
        return () => undefined;
      }
      subscribers.add(subscriber);
      return () => {
        subscribers.delete(subscriber);
        release();
      };
    },
  };
}
