import { RelayCode, RelayError } from "../errors";

export interface ParsedResponse {
  status: number;
  /** Header names lowercased. A repeated header keeps its last value. */
  headers: Record<string, string>;
  body: Uint8Array;
}

type State = "head" | "length" | "chunk-size" | "chunk-data" | "chunk-end" | "trailer" | "close" | "done";

const CRLF = Buffer.from("\r\n");
const HEAD_END = Buffer.from("\r\n\r\n");
/** A status line plus headers longer than this is not a storage node's answer. */
const MAX_HEAD_BYTES = 64 << 10;
const MAX_CHUNK_SIZE_LINE = 1024;
const INITIAL_STORE_BYTES = 4096;

function protocolError(message: string): RelayError {
  return new RelayError(message, 0, RelayCode.ProtocolError);
}

/**
 * Incremental parser for one HTTP/1.1 response read from a stream that the
 * request closed with `Connection: close`. Handles `Content-Length` and
 * chunked transfer coding; interim 1xx responses are skipped.
 *
 * A 200 whose body is delimited only by the connection closing is refused: a
 * storage node always sends the length of an object, and a body that ends when
 * the stream does cannot be told from one that a reset cut short. Other
 * statuses (error pages) may still end with the connection.
 *
 * Input is appended to one buffer that grows geometrically and is consumed from
 * the front, and body bytes are copied out as they arrive, so the work is linear
 * in the response however finely the stream delivers it.
 */
export class Http1ResponseParser {
  private store: Buffer = Buffer.allocUnsafe(INITIAL_STORE_BYTES);
  private start = 0;
  private end = 0;
  /** Where the next search for a head terminator may begin, relative to `start`. */
  private scan = 0;
  private state: State = "head";
  private status = 0;
  private headers: Record<string, string> = {};
  private parts: Buffer[] = [];
  private bodyBytes = 0;
  private remaining = 0;

  constructor(private readonly maxBodyBytes: number) {}

  get done(): boolean {
    return this.state === "done";
  }

  push(chunk: Uint8Array): void {
    if (this.state === "done") return;
    this.append(chunk);
    while (this.step()) {
      /* advance until more input is needed */
    }
  }

  /** The stream ended. Returns the response, or throws if it ended early. */
  finish(): ParsedResponse {
    if (this.state === "close") this.state = "done";
    if (this.state !== "done") {
      throw new RelayError(
        "the connection closed before the response was complete",
        0,
        RelayCode.ConnectFailed
      );
    }
    return {
      status: this.status,
      headers: this.headers,
      body: new Uint8Array(Buffer.concat(this.parts)),
    };
  }

  private get unread(): Buffer {
    return this.store.subarray(this.start, this.end);
  }

  private append(chunk: Uint8Array): void {
    const incoming = chunk.length;
    if (this.end + incoming > this.store.length) {
      const unread = this.end - this.start;
      // Compacting frees only `start` bytes, so it is worth its copy only when that is
      // at least half the buffer; otherwise grow, which keeps the total copying linear.
      if (this.start >= this.store.length / 2 && unread + incoming <= this.store.length) {
        this.store.copy(this.store, 0, this.start, this.end);
      } else {
        const grown = Buffer.allocUnsafe(Math.max(unread + incoming, this.store.length * 2));
        this.store.copy(grown, 0, this.start, this.end);
        this.store = grown;
      }
      this.end = unread;
      this.start = 0;
    }
    this.store.set(chunk, this.end);
    this.end += incoming;
  }

  private consume(n: number): void {
    this.start += n;
    if (this.start === this.end) this.start = this.end = 0;
    this.scan = 0;
  }

  /** Runs one transition; true when it consumed input and may continue. */
  private step(): boolean {
    switch (this.state) {
      case "head":
        return this.readHead();
      case "length":
        return this.readLength();
      case "chunk-size":
        return this.readChunkSize();
      case "chunk-data":
        return this.readChunkData();
      case "chunk-end":
        return this.readChunkEnd();
      case "trailer":
        return this.readTrailer();
      case "close":
        this.take(this.unread.length);
        return false;
      default:
        return false;
    }
  }

  /** Index of the end of a head (or trailer) block in the unread bytes, or -1. */
  private findHeadEnd(what: string): number {
    const unread = this.unread;
    const end = unread.indexOf(HEAD_END, this.scan);
    if (end < 0) {
      if (unread.length > MAX_HEAD_BYTES) throw protocolError(`response ${what} too large`);
      this.scan = Math.max(0, unread.length - (HEAD_END.length - 1));
      return -1;
    }
    if (end > MAX_HEAD_BYTES) throw protocolError(`response ${what} too large`);
    return end;
  }

  private readHead(): boolean {
    const end = this.findHeadEnd("header");
    if (end < 0) return false;
    const lines = this.unread.subarray(0, end).toString("latin1").split("\r\n");
    this.consume(end + HEAD_END.length);

    const match = /^HTTP\/1\.[01] (\d{3})(?: |$)/.exec(lines[0]);
    if (!match) throw protocolError("malformed HTTP status line");
    const status = Number(match[1]);
    if (status >= 100 && status < 200) return true; // interim response; the real one follows

    const headers: Record<string, string> = {};
    for (const line of lines.slice(1)) {
      const colon = line.indexOf(":");
      if (colon <= 0) throw protocolError("malformed HTTP header line");
      headers[line.slice(0, colon).trim().toLowerCase()] = line.slice(colon + 1).trim();
    }
    this.status = status;
    this.headers = headers;
    this.state = this.bodyState(status, headers);
    return true;
  }

  private bodyState(status: number, headers: Record<string, string>): State {
    if (status === 204 || status === 304) return "done";
    if (/\bchunked\b/i.test(headers["transfer-encoding"] ?? "")) return "chunk-size";
    if (headers["content-length"] !== undefined) {
      if (!/^\d+$/.test(headers["content-length"])) throw protocolError("invalid Content-Length");
      this.remaining = Number(headers["content-length"]);
      this.checkCap(this.remaining);
      return this.remaining === 0 ? "done" : "length";
    }
    if (status === 200) {
      throw protocolError(
        "a 200 response with neither Content-Length nor chunked encoding: its end cannot be told from a cut connection"
      );
    }
    return "close";
  }

  private readLength(): boolean {
    if (this.unread.length === 0) return false;
    this.take(Math.min(this.remaining, this.unread.length));
    if (this.remaining === 0) this.state = "done";
    return false;
  }

  private readChunkSize(): boolean {
    const unread = this.unread;
    const end = unread.indexOf(CRLF);
    if (end < 0) {
      if (unread.length > MAX_CHUNK_SIZE_LINE) throw protocolError("chunk size line too long");
      return false;
    }
    const line = unread.subarray(0, end).toString("latin1").split(";")[0].trim();
    if (!/^[0-9a-fA-F]+$/.test(line)) throw protocolError("malformed chunk size");
    const size = parseInt(line, 16);
    this.consume(end + CRLF.length);
    this.checkCap(size);
    this.remaining = size;
    this.state = size === 0 ? "trailer" : "chunk-data";
    return true;
  }

  /** Chunk bytes are taken as they arrive, not held back until the chunk is whole. */
  private readChunkData(): boolean {
    const available = Math.min(this.remaining, this.unread.length);
    if (available === 0) return false;
    this.take(available);
    if (this.remaining === 0) this.state = "chunk-end";
    return true;
  }

  private readChunkEnd(): boolean {
    if (this.unread.length < CRLF.length) return false;
    if (!this.unread.subarray(0, CRLF.length).equals(CRLF)) {
      throw protocolError("chunk not terminated by CRLF");
    }
    this.consume(CRLF.length);
    this.state = "chunk-size";
    return true;
  }

  private readTrailer(): boolean {
    if (this.unread.subarray(0, CRLF.length).equals(CRLF)) {
      this.state = "done";
      return false;
    }
    const end = this.findHeadEnd("trailer");
    if (end < 0) return false;
    this.consume(end + HEAD_END.length);
    this.state = "done";
    return false;
  }

  private take(n: number): void {
    if (n === 0) return;
    this.checkCap(n);
    this.parts.push(Buffer.from(this.unread.subarray(0, n)));
    this.bodyBytes += n;
    this.remaining -= n;
    this.consume(n);
  }

  private checkCap(extra: number): void {
    if (this.bodyBytes + extra > this.maxBodyBytes) {
      throw new RelayError(
        `response body exceeds the ${this.maxBodyBytes} byte limit`,
        0,
        RelayCode.TooLarge
      );
    }
  }
}
