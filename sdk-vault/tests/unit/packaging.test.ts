import { describe, expect, it } from 'vitest';
import { execFileSync } from 'node:child_process';
import { existsSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';

/**
 * This package is consumed by CLIs and node agents, not only by bundlers, so it
 * must be loadable both ways. The app SDK shipped ESM-only for a long time and
 * `require()` of it failed outright; these lock the same contract down here
 * from the first release rather than after the first bug report.
 *
 * The build tests skip rather than fail when dist is absent, so `pnpm test` on
 * a clean checkout still passes.
 */

const root = resolve(__dirname, '../..');
const pkg = JSON.parse(readFileSync(join(root, 'package.json'), 'utf8'));
const distBuilt = existsSync(join(root, 'dist/index.js'));

describe('package.json', () => {
  it('is the vault package, not the app SDK', () => {
    expect(pkg.name).toBe('@debros/orama-vault');
  });

  it('declares both module formats in exports', () => {
    const entry = pkg.exports['.'];
    expect(entry.import).toBeDefined();
    expect(entry.require).toBeDefined();
  });

  it('gives each format its own type declarations', () => {
    expect(pkg.exports['.'].import.types).toBe('./dist/index.d.ts');
    expect(pkg.exports['.'].require.types).toBe('./dist/index.d.cts');
  });

  it('points main at the CJS build and module at the ESM build', () => {
    expect(pkg.main).toBe('./dist/index.cjs');
    expect(pkg.module).toBe('./dist/index.js');
  });

  it('is marked side-effect free so bundlers can tree-shake it', () => {
    expect(pkg.sideEffects).toBe(false);
  });

  it('runs its tests once rather than watching', () => {
    expect(pkg.scripts.test).toBe('vitest run');
  });

  it('carries the cryptography it needs, which the app SDK no longer does', () => {
    expect(Object.keys(pkg.dependencies)).toEqual(
      expect.arrayContaining(['@noble/ciphers', '@noble/hashes']),
    );
  });
});

describe('the built package', () => {
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

  it.skipIf(!distBuilt)('can be required from CommonJS', () => {
    const dir = mkdtempSync(join(tmpdir(), 'orama-vault-cjs-'));
    try {
      const script = join(dir, 'check.cjs');
      writeFileSync(
        script,
        `const vault = require(${JSON.stringify(join(root, 'dist/index.cjs'))});\n` +
          `if (typeof vault.VaultClient !== 'function') { process.exit(1); }\n` +
          `if (typeof vault.QuorumError !== 'function') { process.exit(2); }\n`,
      );
      execFileSync(process.execPath, [script]);
    } finally {
      rmSync(dir, { recursive: true, force: true });
    }
  });

  it.skipIf(!distBuilt)('can be imported from ESM', () => {
    const dir = mkdtempSync(join(tmpdir(), 'orama-vault-esm-'));
    try {
      const script = join(dir, 'check.mjs');
      writeFileSync(
        script,
        `import { VaultClient, shamirSplit } from ${JSON.stringify(join(root, 'dist/index.js'))};\n` +
          `if (typeof VaultClient !== 'function') { process.exit(1); }\n` +
          `if (typeof shamirSplit !== 'function') { process.exit(2); }\n`,
      );
      execFileSync(process.execPath, [script]);
    } finally {
      rmSync(dir, { recursive: true, force: true });
    }
  });
});
