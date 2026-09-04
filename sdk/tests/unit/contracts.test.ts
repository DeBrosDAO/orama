import { existsSync, readFileSync, readdirSync, statSync } from 'node:fs';
import { join, relative, resolve } from 'node:path';
import { describe, expect, it, vi } from 'vitest';
import { createClient } from '../../src/index';

/**
 * The other half of the contract in `contracts/`.
 *
 * Nothing used to check that a body this SDK sends is a body the gateway
 * parses. The unit tests on each side were written against that side alone, and
 * the only thing exercising both was an end-to-end suite that needs a live
 * cluster — so a field renamed on one side and not the other reached
 * production.
 *
 * Each fixture is read twice: a Go test decodes `request` into the handler's
 * own struct with unknown fields rejected, and this test drives the SDK method
 * named in `call` and asserts the body it sends is exactly that JSON. Neither
 * side can move without the other failing.
 */

interface Fixture {
  name: string;
  route: string;
  method: string;
  sdk: string;
  call: { module: string; method: string; args: unknown[] } | null;
  request: unknown;
  response: unknown;
  returns: unknown;
}

const contractsDir = resolve(__dirname, '../../../contracts');

function loadFixtures(): Fixture[] {
  const files: string[] = [];
  const walk = (dir: string) => {
    for (const entry of readdirSync(dir)) {
      const full = join(dir, entry);
      if (statSync(full).isDirectory()) walk(full);
      else if (full.endsWith('.json')) files.push(full);
    }
  };
  walk(contractsDir);

  return files
    .map((file) => ({
      ...(JSON.parse(readFileSync(file, 'utf8')) as Omit<Fixture, 'name'>),
      name: relative(contractsDir, file).replace(/\\/g, '/').replace(/\.json$/, ''),
    }))
    .sort((a, b) => a.name.localeCompare(b.name));
}

/** A client whose transport records the request and answers with the fixture. */
function clientFor(fixture: Fixture) {
  const seen: { url?: string; method?: string; body?: unknown } = {};
  const fetchImpl = vi.fn(async (url: any, init: any) => {
    seen.url = String(url);
    seen.method = init?.method;
    seen.body = init?.body ? JSON.parse(init.body) : undefined;
    return new Response(JSON.stringify(fixture.response), {
      status: 200,
      headers: { 'content-type': 'application/json' },
    });
  });

  const client = createClient({
    baseURL: 'https://gw.example',
    apiKey: 'ak_test:contract',
    maxRetries: 0,
    fetch: fetchImpl as any,
    functionsConfig: { namespace: 'contract' },
  });

  return { client, seen };
}

const fixtures = loadFixtures();

describe('the contract fixtures', () => {
  it('exist', () => {
    expect(existsSync(contractsDir), `${contractsDir} is missing`).toBe(true);
    expect(fixtures.length).toBeGreaterThan(10);
  });

  it.each(fixtures.map((f) => [f.name, f] as const))('%s is complete', (_name, fixture) => {
    expect(fixture.route.startsWith('/v1/') || fixture.route.startsWith('/'), fixture.route).toBe(true);
    expect(['GET', 'POST', 'PUT', 'DELETE']).toContain(fixture.method);
    expect(fixture.sdk, 'every fixture names the SDK method it belongs to').toBeTruthy();
    expect(fixture.request, 'every fixture carries a request body').toBeDefined();
    expect(fixture.response, 'every fixture carries a response body').toBeDefined();
  });
});

const callable = fixtures.filter((f) => f.call !== null);

describe('the SDK sends what the contract says', () => {
  it('has fixtures it can drive', () => {
    expect(callable.length).toBeGreaterThan(10);
  });

  it.each(callable.map((f) => [f.name, f] as const))(
    '%s sends the documented body',
    async (_name, fixture) => {
      const { client, seen } = clientFor(fixture);
      const call = fixture.call!;
      const module = (client as any)[call.module];
      expect(module, `client.${call.module} does not exist`).toBeDefined();
      expect(
        typeof module[call.method],
        `client.${call.module}.${call.method} is not a method`,
      ).toBe('function');

      await module[call.method](...call.args);

      expect(seen.url, `${fixture.sdk} did not call ${fixture.route}`).toBe(
        `https://gw.example${fixture.route}`,
      );
      expect(seen.method).toBe(fixture.method);
      expect(seen.body, `${fixture.sdk} sent a different body than the contract`).toEqual(
        fixture.request,
      );
    },
  );

  it.each(
    callable
      .filter((f) => f.returns !== null && f.returns !== undefined)
      .map((f) => [f.name, f] as const),
  )('%s returns what the contract says', async (_name, fixture) => {
    const { client } = clientFor(fixture);
    const call = fixture.call!;
    const result = await (client as any)[call.module][call.method](...call.args);
    expect(result).toEqual(fixture.returns);
  });
});

/**
 * A fixture that names an SDK method which does not exist would silently stop
 * checking anything, so the module and method are verified separately from the
 * call above.
 */
describe('every fixture names a real method', () => {
  const client = createClient({
    baseURL: 'https://gw.example',
    functionsConfig: { namespace: 'contract' },
  });

  it.each(callable.map((f) => [f.name, f] as const))('%s', (_name, fixture) => {
    const call = fixture.call!;
    const module = (client as any)[call.module];
    expect(module, `no client.${call.module}`).toBeDefined();
    expect(typeof module[call.method]).toBe('function');
  });
});
