/**
 * The SDK's only console output.
 *
 * Every client used to write to the host application's console directly and
 * unconditionally: a line per HTTP request, a line per WebSocket open and
 * close, a line per pubsub message received, and `[HttpClient] JWT set: true`
 * on every token change. An application importing the SDK had no way to turn
 * any of it off, and a chatty topic could produce a console line per message
 * for the lifetime of the process.
 *
 * Nothing here prints unless the client was created with `debug: true`. That is
 * the whole point: the SDK reports failure by throwing a typed `SDKError` and
 * by calling the handlers the application registered. The console is for
 * someone who has deliberately asked to watch.
 */

/** The subset of `console` the SDK uses. */
export interface LogSink {
  log(...args: unknown[]): void;
  warn(...args: unknown[]): void;
  error(...args: unknown[]): void;
}

/**
 * A debug-gated, scoped logger.
 *
 * Create the root from the client's `debug` setting and take a `child` per
 * component, so lines stay prefixed the way they always were
 * (`[HttpClient] …`, `[WSClient] …`).
 */
export class Logger {
  private readonly enabled: boolean;
  private readonly scope: string;
  private readonly sink?: LogSink;

  /**
   * @param enabled  Whether to print at all. This is the client's `debug` flag.
   * @param scope    Component name, printed as `[scope]`. Empty for the root.
   * @param sink     Where to print. Defaults to the platform console, which is
   *                 absent in some embedded runtimes — hence the optional type
   *                 rather than a guard at each call site.
   */
  constructor(enabled: boolean, scope: string = "", sink?: LogSink) {
    this.enabled = enabled;
    this.scope = scope;
    this.sink = sink ?? (typeof console !== "undefined" ? console : undefined);
  }

  /** A logger that never prints. For components given no logger of their own. */
  static disabled(): Logger {
    return new Logger(false);
  }

  /** A logger for one component, sharing this one's enabled state and sink. */
  child(scope: string): Logger {
    return new Logger(this.enabled, scope, this.sink);
  }

  /**
   * Whether anything would be printed.
   *
   * Use it to skip building a message that is expensive to assemble — parsing
   * a request body to describe it, for instance.
   */
  get isEnabled(): boolean {
    return this.enabled && this.sink !== undefined;
  }

  log(message: string, ...details: unknown[]): void {
    if (!this.isEnabled) return;
    this.sink!.log(this.prefix(message), ...details);
  }

  warn(message: string, ...details: unknown[]): void {
    if (!this.isEnabled) return;
    this.sink!.warn(this.prefix(message), ...details);
  }

  error(message: string, ...details: unknown[]): void {
    if (!this.isEnabled) return;
    this.sink!.error(this.prefix(message), ...details);
  }

  private prefix(message: string): string {
    return this.scope ? `[${this.scope}] ${message}` : message;
  }
}
