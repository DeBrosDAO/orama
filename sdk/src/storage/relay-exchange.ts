import WebSocket from "isomorphic-ws";
import type { Duplex } from "node:stream";
import type { TLSSocket } from "node:tls";
import { NetworkError, RelayCode, RelayError } from "../errors";
import { Http1ResponseParser, type ParsedResponse } from "./http-response";
import { relayRefusal } from "./relay-refusals";

/** Node-only, like `relay-transport.ts`: it opens `node:tls` over the relay's WebSocket. */

const HANDSHAKE_TIMEOUT_MS = 30_000;
const REFUSAL_BODY_LIMIT = 4096;

export interface ExchangeOptions {
  /** `ws(s)://` URL of the relay with the destination in its query. */
  relay: URL;
  /** The namespace host: the TLS server name. */
  namespaceHost: string;
  /** The exact request bytes, written once TLS is up. */
  request: string;
  ca?: string | Buffer | Array<string | Buffer>;
  maxBodyBytes: number;
  timeoutMs: number;
  jitterMs: number;
  random: () => number;
  signal?: AbortSignal;
}

export function aborted(): NetworkError {
  return new NetworkError("request aborted by caller", "ABORTED", { cause: "caller-abort" });
}

function sleep(ms: number, signal?: AbortSignal): Promise<void> {
  return new Promise((resolve, reject) => {
    if (signal?.aborted) return reject(aborted());
    const onAbort = () => {
      clearTimeout(timer);
      reject(aborted());
    };
    const timer = setTimeout(() => {
      signal?.removeEventListener("abort", onAbort);
      resolve();
    }, ms);
    signal?.addEventListener("abort", onAbort, { once: true });
  });
}

function connectFailed(what: string, err: unknown): RelayError {
  return new RelayError(
    `${what}: ${err instanceof Error ? err.message : String(err)}`,
    0,
    RelayCode.ConnectFailed
  );
}

/**
 * Opens the relay's WebSocket with no credential, runs TLS to the namespace
 * host inside its byte stream (the relay carries ciphertext), writes the
 * request and returns the parsed response. One call is one tunnel.
 */
export async function exchange(options: ExchangeOptions): Promise<ParsedResponse> {
  const [{ connect }, { Duplex: DuplexClass }] = await Promise.all([
    import("node:tls"),
    import("node:stream"),
  ]);
  return new Promise<ParsedResponse>((resolve, reject) => {
    new Exchange(options, connect, DuplexClass, resolve, reject).start();
  });
}

type Connect = typeof import("node:tls").connect;
type DuplexConstructor = typeof import("node:stream").Duplex;

class Exchange {
  private settled = false;
  private tls: TLSSocket | undefined;
  private ws: any;
  private deadline: ReturnType<typeof setTimeout> | undefined;
  private readonly parser: Http1ResponseParser;

  constructor(
    private readonly o: ExchangeOptions,
    private readonly connect: Connect,
    private readonly DuplexClass: DuplexConstructor,
    private readonly resolve: (r: ParsedResponse) => void,
    private readonly reject: (e: Error) => void
  ) {
    this.parser = new Http1ResponseParser(o.maxBodyBytes);
  }

  start(): void {
    this.deadline = setTimeout(
      () => this.fail(new NetworkError("relayed fetch timed out", "TIMEOUT")),
      this.o.timeoutMs
    );
    this.o.signal?.addEventListener("abort", this.onAbort, { once: true });
    // `ws` options the isomorphic type does not carry.
    this.ws = new (WebSocket as any)(this.o.relay.toString(), { handshakeTimeout: HANDSHAKE_TIMEOUT_MS });
    // Pre-upgrade refusals arrive as an ordinary HTTP response.
    this.ws.on("unexpected-response", (req: any, res: any) => this.onRefusal(req, res));
    this.ws.on("error", (err: Error) => this.fail(connectFailed("relay connection failed", err)));
    this.ws.on("open", () => this.onOpen());
  }

  private readonly onAbort = () => this.fail(aborted());

  private settle(fn: () => void): void {
    if (this.settled) return;
    this.settled = true;
    clearTimeout(this.deadline);
    this.o.signal?.removeEventListener("abort", this.onAbort);
    this.tls?.destroy();
    try {
      this.ws.terminate();
    } catch {
      // Already closed.
    }
    fn();
  }

  private fail(err: Error): void {
    this.settle(() => this.reject(err));
  }

  private succeed(response: ParsedResponse): void {
    this.settle(() => this.resolve(response));
  }

  private onRefusal(req: any, res: any): void {
    const chunks: Buffer[] = [];
    let size = 0;
    res.on("data", (c: Buffer) => {
      if (size < REFUSAL_BODY_LIMIT) chunks.push(c);
      size += c.length;
    });
    const done = () => {
      req.destroy();
      const retryAfter = res.headers["retry-after"];
      this.fail(
        relayRefusal(
          res.statusCode,
          Array.isArray(retryAfter) ? retryAfter[0] : retryAfter,
          Buffer.concat(chunks).toString("utf8")
        )
      );
    };
    res.on("end", done);
    res.on("error", done);
  }

  private onOpen(): void {
    const stream = this.byteStream();
    this.ws.on("message", (data: Buffer, isBinary: boolean) => {
      if (!isBinary) {
        stream.destroy(new RelayError("relay sent a text frame on a byte stream", 0, RelayCode.ProtocolError));
      } else if (!stream.push(data)) {
        this.ws.pause();
      }
    });
    this.ws.on("close", () => stream.push(null));
    this.attachTls(stream);
  }

  /** The WebSocket as a Duplex, for `tls.connect` to run over. */
  private byteStream(): Duplex {
    return new this.DuplexClass({
      read: () => this.ws.resume(),
      write: (chunk: Buffer, _enc: string, cb: (err?: Error | null) => void) =>
        this.ws.send(chunk, { binary: true }, cb),
      final: (cb: () => void) => cb(),
      destroy: (err: Error | null, cb: (err?: Error | null) => void) => {
        try {
          this.ws.terminate();
        } catch {
          // Already closed.
        }
        cb(err);
      },
    });
  }

  private attachTls(stream: Duplex): void {
    const socket = this.connect({
      socket: stream,
      servername: this.o.namespaceHost,
      ca: this.o.ca,
      ALPNProtocols: ["http/1.1"],
    });
    this.tls = socket;
    socket.on("secureConnect", () => this.sendRequest(socket).catch((e) => this.fail(e)));
    socket.on("data", (chunk: Buffer) => this.onData(chunk));
    socket.on("end", () => this.onEnd());
    socket.on("close", () => this.onEnd());
    socket.on("error", (err: Error) =>
      this.fail(err instanceof RelayError ? err : connectFailed("tunnel to the namespace host failed", err))
    );
  }

  private async sendRequest(socket: TLSSocket): Promise<void> {
    if (this.o.jitterMs > 0) {
      await sleep(Math.floor(this.o.random() * (this.o.jitterMs + 1)), this.o.signal);
    }
    if (!this.settled) socket.write(this.o.request);
  }

  private onData(chunk: Buffer): void {
    try {
      this.parser.push(chunk);
      if (this.parser.done) this.succeed(this.parser.finish());
    } catch (err) {
      this.fail(err as Error);
    }
  }

  private onEnd(): void {
    try {
      this.succeed(this.parser.finish());
    } catch (err) {
      this.fail(err as Error);
    }
  }
}
