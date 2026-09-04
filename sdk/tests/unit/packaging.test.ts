import { describe, expect, it } from 'vitest';
import { execFileSync } from 'node:child_process';
import { existsSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';

/**
 * The package was ESM-only while its README promised isomorphic use, so
 * `require('@debros/orama')` failed outright at import: Jest without ESM
 * support, ts-node in CJS mode, and CJS server code in a Next.js app all hit
 * ERR_REQUIRE_ESM. These lock the packaging down.
 *
 * The build tests need `pnpm build` to have run; they skip rather than fail
 * when dist is absent, so `pnpm test` on a clean checkout still passes.
 */

const root = resolve(__dirname, '../..');
const pkg = JSON.parse(readFileSync(join(root, 'package.json'), 'utf8'));
const distBuilt = existsSync(join(root, 'dist/index.js'));

describe('package.json', () => {
  it('declares both module formats in exports', () => {
    const entry = pkg.exports['.'];
    expect(entry.import).toBeDefined();
    expect(entry.require).toBeDefined();
  });

  it('gives each format its own type declarations', () => {
    // A `require` resolving to the ESM .d.ts confuses TypeScript under
    // moduleResolution: node16, which reads the condition it matched.
    expect(pkg.exports['.'].import.types).toBe('./dist/index.d.ts');
    expect(pkg.exports['.'].require.types).toBe('./dist/index.d.cts');
  });

  it('points main at the CJS build and module at the ESM build', () => {
    // Bundlers and tools that predate the exports map read these.
    expect(pkg.main).toBe('./dist/index.cjs');
    expect(pkg.module).toBe('./dist/index.js');
  });

  it('is marked side-effect free so bundlers can tree-shake it', () => {
    expect(pkg.sideEffects).toBe(false);
  });

  it('points at the repository it actually lives in', () => {
    // It said DeBrosOfficial/network, which is not where this is.
    expect(pkg.repository.url).toContain('DeBrosDAO/orama');
    expect(pkg.bugs.url).toContain('DeBrosDAO/orama');
  });

  it('exposes package.json, which tooling reads', () => {
    expect(pkg.exports['./package.json']).toBe('./package.json');
  });

  it('has a runnable example script', () => {
    expect(pkg.scripts.example).toBeDefined();
  });

  it('declares ws as an optional peer', () => {
    // isomorphic-ws is `require("ws")` in Node and the browser WebSocket in a
    // browser, and it declares ws as a peer it does not install. The SDK
    // passed that requirement on to consumers without naming it, so a Node
    // consumer's pubsub failed to resolve ws at import. Optional, because a
    // browser consumer must not be made to install it.
    expect(pkg.peerDependencies.ws).toBeDefined();
    expect(pkg.peerDependenciesMeta.ws.optional).toBe(true);
  });
});

describe('the built package', () => {
  it.skipIf(!distBuilt)('can be required from CommonJS', () => {
    const dir = mkdtempSync(join(tmpdir(), 'orama-cjs-'));
    try {
      const file = join(dir, 'check.cjs');
      writeFileSync(
        file,
        `const sdk = require(${JSON.stringify(join(root, 'dist/index.cjs'))});\n` +
          `if (typeof sdk.createClient !== 'function') { process.exit(1); }\n`,
      );
      execFileSync(process.execPath, [file]);
    } finally {
      rmSync(dir, { recursive: true, force: true });
    }
  });

  it.skipIf(!distBuilt)('can be imported from ESM', () => {
    const dir = mkdtempSync(join(tmpdir(), 'orama-esm-'));
    try {
      const file = join(dir, 'check.mjs');
      writeFileSync(
        file,
        `import { createClient } from ${JSON.stringify(join(root, 'dist/index.js'))};\n` +
          `if (typeof createClient !== 'function') { process.exit(1); }\n`,
      );
      execFileSync(process.execPath, [file]);
    } finally {
      rmSync(dir, { recursive: true, force: true });
    }
  });

  it.skipIf(!distBuilt)('emits every file the exports map names', () => {
    for (const path of [
      pkg.exports['.'].import.default,
      pkg.exports['.'].import.types,
      pkg.exports['.'].require.default,
      pkg.exports['.'].require.types,
    ]) {
      expect(existsSync(join(root, path)), `${path} is named in exports but not built`).toBe(true);
    }
  });
});

describe('examples', () => {
  it('read the gateway from the environment', () => {
    // They hardcoded http://localhost:6001, which is not a port any gateway
    // has listened on since the port block moved to 10100.
    for (const name of ['basic-usage', 'database-crud', 'pubsub-chat']) {
      const source = readFileSync(join(root, `examples/${name}.ts`), 'utf8');
      expect(source, `${name} does not read GATEWAY_BASE_URL`).toContain('GATEWAY_BASE_URL');
      expect(source, `${name} still points at the retired :6001`).not.toContain('6001');
    }
  });
});
