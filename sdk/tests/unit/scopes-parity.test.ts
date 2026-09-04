import { existsSync, readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { describe, expect, it } from 'vitest';
import { KEY_PROFILES, PROFILE_SCOPES, SCOPES, isScope, satisfiesScope } from '../../src/scopes';
import type { Scope } from '../../src/scopes';

/**
 * The gateway is the authority on what grants exist. These constants are a copy
 * of that list, so they are checked against the Go source rather than trusted.
 *
 * A client that disagrees with the gateway about grants is worse than one that
 * says nothing: it tells an application to ask for a key that cannot be minted,
 * or hides one that can. That is not hypothetical — `pubsub` was declared in Go
 * as a scope, demanded by every `/v1/pubsub/` route, and left out of the map
 * that validates a mint request, so no key could hold it.
 */

const goSource = resolve(__dirname, '../../../core/pkg/gateway/auth/scopes.go');
const haveGo = existsSync(goSource);
const go = haveGo ? readFileSync(goSource, 'utf8') : '';

/** The value of every `Scope<Name> = "value"` constant. */
function declaredScopes(): string[] {
  return [...go.matchAll(/Scope\w+\s+=\s+"([a-z-]+)"/g)].map((m) => m[1]).sort();
}

/** The keys of the `knownGrants` map, which is what a mint request is validated against. */
function mintableScopes(): string[] {
  const block = go.match(/var knownGrants = map\[string\]struct\{\}\{([\s\S]*?)\n\}/);
  if (!block) return [];
  return [...block[1].matchAll(/Scope(\w+):/g)]
    .map((m) => m[1].toLowerCase())
    .sort();
}

describe('scope constants match the gateway', () => {
  it('finds the gateway source', () => {
    expect(haveGo, `${goSource} is missing — this test cannot check parity`).toBe(true);
  });

  it.skipIf(!haveGo)('lists exactly the grants the gateway declares', () => {
    expect([...SCOPES].sort()).toEqual(declaredScopes());
  });

  it.skipIf(!haveGo)('lists only grants a key can actually be minted with', () => {
    // Compared by the Go identifier's lowercase name, which equals the value
    // for every grant (ScopeWebRTC → "webrtc").
    const mintable = mintableScopes();
    expect(mintable.length).toBeGreaterThan(0);
    for (const scope of SCOPES) {
      expect(mintable, `the gateway cannot mint a key with the "${scope}" grant`).toContain(scope);
    }
  });

  it.skipIf(!haveGo)('agrees with the gateway on which profiles exist', () => {
    const profiles = go.match(/func ProfileGrants[\s\S]*?\n\}/);
    expect(profiles).not.toBeNull();
    for (const profile of KEY_PROFILES) {
      expect(profiles![0], `ProfileGrants has no case for "${profile}"`).toContain(`"${profile}"`);
    }
  });

  it.skipIf(!haveGo)('agrees with the gateway on what app-runtime grants', () => {
    const profiles = go.match(/case "app-runtime", "runtime", "app":\n\s*return \[\]string\{([^}]*)\}/);
    expect(profiles).not.toBeNull();
    const granted = [...profiles![1].matchAll(/Scope(\w+)/g)].map((m) => m[1].toLowerCase()).sort();
    expect([...PROFILE_SCOPES['app-runtime']].sort()).toEqual(granted);
  });
});

describe('scope helpers', () => {
  it('recognises a real grant and rejects anything else', () => {
    expect(isScope('storage')).toBe(true);
    expect(isScope('pubsub')).toBe(true);
    expect(isScope('Storage')).toBe(false);
    expect(isScope('root')).toBe(false);
    expect(isScope(undefined)).toBe(false);
  });

  it('treats admin as a wildcard, the way the gateway does', () => {
    expect(satisfiesScope(['admin'], 'storage')).toBe(true);
    expect(satisfiesScope(['admin'], 'pubsub')).toBe(true);
  });

  it('requires the exact grant otherwise', () => {
    expect(satisfiesScope(['invoke', 'storage'], 'storage')).toBe(true);
    expect(satisfiesScope(['invoke', 'storage'], 'pubsub')).toBe(false);
    expect(satisfiesScope([], 'storage')).toBe(false);
  });

  it('lets any credential through when nothing is required', () => {
    expect(satisfiesScope([], '')).toBe(true);
    expect(satisfiesScope([], undefined)).toBe(true);
  });

  it('keeps admin out of the data-plane set', () => {
    const dataPlane: readonly Scope[] = PROFILE_SCOPES['app-runtime'];
    expect(dataPlane).not.toContain('admin');
  });
});
