import { existsSync, readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { describe, expect, it } from 'vitest';
import * as sdk from '../../src/index';

/**
 * The documentation is checked against the code, because this SDK's docs had
 * drifted into describing an API that was not there: automatic reconnection
 * that did not exist, binary payloads that could not be read back, a default
 * timeout of 30s when the code said 60s, `hmacKey` "for guardian
 * authentication" that nothing read, and a `functionsConfig.gatewayURL` that
 * produced a URL the runtime could not parse.
 *
 * A doc that says a method exists when it does not sends a developer looking
 * for their own mistake, so the method names are the part worth asserting.
 */

const repoRoot = resolve(__dirname, '../../..');
const docs = [
  resolve(repoRoot, 'docs/TS_SDK.md'),
  resolve(repoRoot, 'sdk/README.md'),
  resolve(repoRoot, 'sdk/QUICKSTART.md'),
];

/** The modules `createClient` returns, and the methods each one has. */
function clientSurface(): Record<string, Set<string>> {
  const client = sdk.createClient({ baseURL: 'https://gw.example' });
  const surface: Record<string, Set<string>> = {};

  for (const [name, module] of Object.entries(client)) {
    if (!module || typeof module !== 'object') continue;
    const methods = new Set<string>();
    let proto = Object.getPrototypeOf(module);
    while (proto && proto !== Object.prototype) {
      for (const key of Object.getOwnPropertyNames(proto)) {
        if (key !== 'constructor') methods.add(key);
      }
      proto = Object.getPrototypeOf(proto);
    }
    surface[name] = methods;
  }
  return surface;
}

/** Every `client.<module>.<method>(` a document claims exists. */
function documentedCalls(source: string): Array<{ module: string; method: string }> {
  return [...source.matchAll(/\bclient\.([a-z]+)\.([a-zA-Z]+)\s*\(/g)].map((m) => ({
    module: m[1],
    method: m[2],
  }));
}

describe('the documentation matches the client', () => {
  const surface = clientSurface();

  it('builds a client with every module documented', () => {
    expect(Object.keys(surface).sort()).toEqual([
      'auth',
      'cache',
      'db',
      'functions',
      'network',
      'pubsub',
      'storage',
    ]);
  });

  it.each(docs)('%s names only methods that exist', (path) => {
    expect(existsSync(path), `${path} is missing`).toBe(true);
    const calls = documentedCalls(readFileSync(path, 'utf8'));
    expect(calls.length, `${path} shows no client calls at all`).toBeGreaterThan(3);

    const missing = calls.filter(({ module, method }) => {
      const methods = surface[module];
      return !methods || !methods.has(method);
    });

    expect(
      missing.map(({ module, method }) => `client.${module}.${method}()`),
      'documented but not on the client',
    ).toEqual([]);
  });

  it('would notice a method that does not exist', () => {
    // Proves the check above is not vacuous.
    const calls = documentedCalls('await client.db.definitelyNotAMethod({});');
    expect(calls).toEqual([{ module: 'db', method: 'definitelyNotAMethod' }]);
    expect(surface.db.has('definitelyNotAMethod')).toBe(false);
  });
});

describe('the documented defaults match the code', () => {
  const tsSdk = readFileSync(resolve(repoRoot, 'docs/TS_SDK.md'), 'utf8');
  const http = readFileSync(resolve(repoRoot, 'sdk/src/core/http.ts'), 'utf8');

  /** The `??` default for a named HttpClient config field. */
  function defaultFor(field: string): string | undefined {
    const match = http.match(new RegExp(`this\\.${field} = config\\.${field} \\?\\? ([^;]+);`));
    return match?.[1].trim();
  }

  it('documents the real request timeout', () => {
    expect(defaultFor('timeout')).toBe('60000');
    expect(tsSdk).toContain('| `timeout` | `number` | `60000` |');
  });

  it('documents the real retry count', () => {
    expect(defaultFor('maxRetries')).toBe('3');
    expect(tsSdk).toContain('| `maxRetries` | `number` | `3` |');
  });

  it('documents the real retry delay', () => {
    expect(defaultFor('retryDelayMs')).toBe('1000');
    expect(tsSdk).toContain('| `retryDelayMs` | `number` | `1000` |');
  });
});

describe('the documented grants match the code', () => {
  const tsSdk = readFileSync(resolve(repoRoot, 'docs/TS_SDK.md'), 'utf8');

  it('lists every grant the SDK knows', () => {
    for (const scope of sdk.SCOPES) {
      expect(tsSdk, `TS_SDK.md does not mention the "${scope}" grant`).toContain(`\`${scope}\``);
    }
  });
});
