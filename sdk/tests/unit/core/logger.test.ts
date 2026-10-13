import { describe, expect, it } from 'vitest';
import { Logger, type LogSink } from '../../../src/core/logger';

/**
 * The SDK used to write to the importing application's console on every HTTP
 * request, every WebSocket open and close, every pubsub message, and every JWT
 * change — with no way to turn any of it off. Everything now goes through this
 * one gate, and the gate is closed unless the client was created with
 * `debug: true`.
 */

function recordingSink() {
  const lines: Array<[string, unknown[]]> = [];
  const sink: LogSink = {
    log: (...args) => lines.push(['log', args]),
    warn: (...args) => lines.push(['warn', args]),
    error: (...args) => lines.push(['error', args]),
  };
  return { sink, lines };
}

describe('Logger', () => {
  it('prints nothing when it is not enabled', () => {
    const { sink, lines } = recordingSink();
    const log = new Logger(false, 'HttpClient', sink);

    log.log('a message');
    log.warn('a warning');
    log.error('a failure', new Error('boom'));

    expect(lines).toEqual([]);
    expect(log.isEnabled).toBe(false);
  });

  it('prints on the matching sink method when enabled', () => {
    const { sink, lines } = recordingSink();
    const log = new Logger(true, 'HttpClient', sink);

    log.log('one');
    log.warn('two');
    log.error('three');

    expect(lines.map(([level]) => level)).toEqual(['log', 'warn', 'error']);
    expect(log.isEnabled).toBe(true);
  });

  it('prefixes every line with its scope', () => {
    const { sink, lines } = recordingSink();

    new Logger(true, 'WSClient', sink).log('Connected to wss://gw');

    expect(lines[0][1][0]).toBe('[WSClient] Connected to wss://gw');
  });

  it('leaves an unscoped line unprefixed', () => {
    const { sink, lines } = recordingSink();

    new Logger(true, '', sink).log('bare');

    expect(lines[0][1][0]).toBe('bare');
  });

  it('passes extra details through untouched, so objects stay inspectable', () => {
    const { sink, lines } = recordingSink();
    const cause = new Error('connection refused');

    new Logger(true, 'HttpClient', sink).error('GET /v1/x failed:', cause);

    expect(lines[0][1]).toEqual(['[HttpClient] GET /v1/x failed:', cause]);
  });

  it('gives a child the parent enabled state and sink, with its own scope', () => {
    const { sink, lines } = recordingSink();
    const root = new Logger(true, '', sink);

    root.child('Auth').log('restored');
    root.child('CacheClient').warn('slow');

    expect(lines[0][1][0]).toBe('[Auth] restored');
    expect(lines[1][1][0]).toBe('[CacheClient] slow');
  });

  it('gives a disabled parent a silent child', () => {
    const { sink, lines } = recordingSink();

    new Logger(false, '', sink).child('Auth').log('restored');

    expect(lines).toEqual([]);
  });

  it('Logger.disabled() never prints', () => {
    const disabled = Logger.disabled();

    expect(disabled.isEnabled).toBe(false);
    expect(() => {
      disabled.log('x');
      disabled.child('Sub').error('y');
    }).not.toThrow();
  });

  /**
   * Some embedded JavaScript runtimes have no console at all. Every call site
   * used to guard with `typeof console !== "undefined"`; the guard now lives
   * here once.
   */
  it('is inert, not broken, when the runtime has no console', () => {
    const realConsole = globalThis.console;
    let log: Logger;
    try {
      // @ts-expect-error — deliberately modelling a runtime without a console.
      delete globalThis.console;
      log = new Logger(true, 'HttpClient');
    } finally {
      globalThis.console = realConsole;
    }

    expect(log!.isEnabled).toBe(false);
    expect(() => {
      log.log('x');
      log.warn('y');
      log.error('z');
    }).not.toThrow();
  });
});
