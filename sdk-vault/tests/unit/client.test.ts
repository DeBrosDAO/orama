import { afterEach, describe, expect, it, vi } from 'vitest';
import { AuthClient } from '../../src/auth';
import { VaultClient } from '../../src/client';
import { QuorumError } from '../../src/errors';
import { combine, split } from '../../src/crypto/shamir';
import { adaptiveThreshold, writeQuorum } from '../../src/quorum';
import { GuardianError } from '../../src/transport/guardian';
import type { GuardianClient } from '../../src/transport/guardian';
import type { GuardianEndpoint } from '../../src/transport/types';

/**
 * These exercise the client's own decisions — which guardian gets which share,
 * when an operation has failed, and which shares may be combined — with the
 * guardians stubbed out. Everything below the client is covered by the shamir,
 * transport and auth suites.
 */

const IDENTITY = 'a'.repeat(64);

function endpoints(count: number): GuardianEndpoint[] {
  return Array.from({ length: count }, (_, i) => ({ address: `10.0.0.${i + 1}`, port: 7500 }));
}

interface StoredShare {
  endpoint: string;
  share: Uint8Array;
  version: number;
}

/**
 * A guardian that records what it was told.
 *
 * `authenticateAll` is stubbed to hand back a fresh endpoint object for each
 * guardian rather than the one from the configuration. The client used to find
 * a guardian's share with `guardians.indexOf(endpoint)`, which is an
 * object-identity lookup: a transport that ever returned a copy would send
 * every guardian the wrong share, or none.
 */
function fakeGuardians(
  config: GuardianEndpoint[],
  options: {
    /** Endpoint keys that fail to authenticate. */
    unauthenticated?: string[];
    /** Endpoint keys whose writes are refused. */
    refusing?: string[];
    /** What each endpoint answers a read with. */
    reads?: Record<string, { share: Uint8Array; version: number } | Error>;
    /** What each endpoint answers a list with. */
    listings?: Record<string, Array<{ name: string; version: number; size: number }>>;
  } = {},
) {
  const stored: StoredShare[] = [];
  const deleted: string[] = [];
  const key = (e: GuardianEndpoint) => `${e.address}:${e.port}`;

  vi.spyOn(AuthClient.prototype, 'authenticateAll').mockImplementation(async () =>
    config
      .filter((endpoint) => !options.unauthenticated?.includes(key(endpoint)))
      .map((endpoint) => {
        const id = key(endpoint);
        const client = {
          putSecret: async (name: string, share: Uint8Array, version: number) => {
            if (options.refusing?.includes(id)) {
              throw new GuardianError('AUTH', `${id} refused`);
            }
            stored.push({ endpoint: id, share, version });
            return { status: 'ok', name, version };
          },
          getSecret: async (name: string) => {
            const answer = options.reads?.[id];
            if (!answer) throw new GuardianError('NOT_FOUND', `${id} has no ${name}`);
            if (answer instanceof Error) throw answer;
            return {
              share: answer.share,
              name,
              version: answer.version,
              created_ns: 0,
              updated_ns: 0,
            };
          },
          deleteSecret: async (name: string) => {
            if (options.refusing?.includes(id)) {
              throw new GuardianError('AUTH', `${id} refused`);
            }
            deleted.push(id);
            return { status: 'ok', name };
          },
          listSecrets: async () => ({ secrets: options.listings?.[id] ?? [] }),
        } as unknown as GuardianClient;
        // A copy, not the configured object.
        return { client, endpoint: { address: endpoint.address, port: endpoint.port } };
      }),
  );

  return { stored, deleted };
}

function client(guardians: GuardianEndpoint[]) {
  return new VaultClient({ guardians, identityHex: IDENTITY });
}

function encodedShare(x: number, y: Uint8Array): Uint8Array {
  const bytes = new Uint8Array(1 + y.length);
  bytes[0] = x;
  bytes.set(y, 1);
  return bytes;
}

describe('VaultClient.store', () => {
  afterEach(() => vi.restoreAllMocks());

  it('refuses to store when no guardians are configured', async () => {
    await expect(client([]).store('k', new Uint8Array([1]), 1)).rejects.toBeInstanceOf(QuorumError);
  });

  it('gives every configured guardian its own distinct share', async () => {
    const config = endpoints(5);
    const { stored } = fakeGuardians(config);

    const result = await client(config).store('api-key', new Uint8Array([7, 7, 7]), 3);

    expect(result.ackCount).toBe(5);
    expect(result.failCount).toBe(0);
    expect(stored).toHaveLength(5);
    expect(stored.map((s) => s.endpoint).sort()).toEqual(config.map((e) => `${e.address}:${e.port}`).sort());
    // Distinct x coordinates: the same share sent twice is not a Shamir split.
    expect(new Set(stored.map((s) => s.share[0])).size).toBe(5);
    expect(new Set(stored.map((s) => s.version))).toEqual(new Set([3]));
  });

  it('binds shares to guardians without relying on object identity', async () => {
    const config = endpoints(3);
    const { stored } = fakeGuardians(config);

    await client(config).store('k', new Uint8Array([9]), 1);

    // Every guardian got exactly one share, so the lookup found all three.
    expect(stored).toHaveLength(3);
    expect(stored.every((s) => s.share.length >= 2)).toBe(true);
  });

  it('reports an unreachable guardian and still succeeds at quorum', async () => {
    const config = endpoints(5);
    const { stored } = fakeGuardians(config, { unauthenticated: ['10.0.0.5:7500'] });

    const result = await client(config).store('k', new Uint8Array([1, 2]), 1);

    // W = writeQuorum(5) = 4, so four acknowledgements are enough.
    expect(writeQuorum(5)).toBe(4);
    expect(result.ackCount).toBe(4);
    expect(result.failCount).toBe(1);
    expect(stored).toHaveLength(4);

    const failed = result.guardianResults.find((r) => !r.success);
    expect(failed?.endpoint).toBe('10.0.0.5:7500');
    expect(failed?.error).toContain('authentication failed');
    // The guardian that could not be reached is still listed, not omitted.
    expect(result.guardianResults).toHaveLength(5);
  });

  it('throws rather than reporting a write that is not durable', async () => {
    const config = endpoints(5);
    fakeGuardians(config, { refusing: ['10.0.0.4:7500', '10.0.0.5:7500'] });

    const error = await client(config)
      .store('k', new Uint8Array([1]), 1)
      .catch((e: unknown) => e);

    expect(error).toBeInstanceOf(QuorumError);
    const quorum = error as QuorumError;
    expect(quorum.ackCount).toBe(3);
    expect(quorum.required).toBe(4);
    expect(quorum.totalContacted).toBe(5);
    expect(quorum.guardianResults.filter((r) => !r.success)).toHaveLength(2);
    expect(quorum.message).toContain('durable');
  });

  it('wipes the shares even when the write fails', async () => {
    const config = endpoints(3);
    const { stored } = fakeGuardians(config, { refusing: config.map((e) => `${e.address}:${e.port}`) });

    await expect(client(config).store('k', new Uint8Array([5]), 1)).rejects.toBeInstanceOf(QuorumError);
    expect(stored).toHaveLength(0);
  });
});

describe('VaultClient.retrieve', () => {
  afterEach(() => vi.restoreAllMocks());

  it('reconstructs the secret from a threshold of shares', async () => {
    const config = endpoints(5);
    const secret = new Uint8Array([1, 2, 3, 4, 5, 6]);
    const shares = split(secret, 5, adaptiveThreshold(5));
    const reads: Record<string, { share: Uint8Array; version: number }> = {};
    config.forEach((endpoint, i) => {
      reads[`${endpoint.address}:${endpoint.port}`] = {
        share: encodedShare(shares[i]!.x, shares[i]!.y),
        version: 4,
      };
    });
    fakeGuardians(config, { reads });

    const result = await client(config).retrieve('api-key');

    expect(Array.from(result.data)).toEqual(Array.from(secret));
    expect(result.version).toBe(4);
    expect(result.sharesCollected).toBe(5);
  });

  /**
   * The reason retrieve groups by version at all. A guardian that missed the
   * last write answers with a share of the previous split. Combining it with
   * newer shares yields neither secret and raises no error, so the caller would
   * be handed convincing rubbish.
   */
  it('never mixes shares from two versions', async () => {
    const config = endpoints(5);
    const current = new Uint8Array([10, 20, 30]);
    const stale = new Uint8Array([99, 98, 97]);
    const currentShares = split(current, 5, adaptiveThreshold(5));
    const staleShares = split(stale, 5, adaptiveThreshold(5));

    const reads: Record<string, { share: Uint8Array; version: number }> = {};
    config.forEach((endpoint, i) => {
      const id = `${endpoint.address}:${endpoint.port}`;
      // The last two guardians never got version 2.
      const useStale = i >= 3;
      const share = useStale ? staleShares[i]! : currentShares[i]!;
      reads[id] = { share: encodedShare(share.x, share.y), version: useStale ? 1 : 2 };
    });
    fakeGuardians(config, { reads });

    const result = await client(config).retrieve('api-key');

    expect(result.version).toBe(2);
    expect(result.sharesCollected).toBe(3);
    expect(Array.from(result.data)).toEqual(Array.from(current));
  });

  it('falls back to the newest version that is actually complete', async () => {
    const config = endpoints(5);
    const older = new Uint8Array([4, 4, 4]);
    const olderShares = split(older, 5, adaptiveThreshold(5));
    const newerShares = split(new Uint8Array([8, 8, 8]), 5, adaptiveThreshold(5));

    const reads: Record<string, { share: Uint8Array; version: number }> = {};
    config.forEach((endpoint, i) => {
      const id = `${endpoint.address}:${endpoint.port}`;
      // Only one guardian took version 3 — that write never reached quorum.
      const share = i === 0 ? newerShares[i]! : olderShares[i]!;
      reads[id] = { share: encodedShare(share.x, share.y), version: i === 0 ? 3 : 2 };
    });
    fakeGuardians(config, { reads });

    const result = await client(config).retrieve('api-key');

    expect(result.version).toBe(2);
    expect(Array.from(result.data)).toEqual(Array.from(older));
  });

  it('reports what it collected when no version can be reconstructed', async () => {
    const config = endpoints(5);
    const shares = split(new Uint8Array([1]), 5, adaptiveThreshold(5));
    fakeGuardians(config, {
      reads: { '10.0.0.1:7500': { share: encodedShare(shares[0]!.x, shares[0]!.y), version: 9 } },
    });

    const error = await client(config)
      .retrieve('api-key')
      .catch((e: unknown) => e);

    expect(error).toBeInstanceOf(QuorumError);
    expect((error as QuorumError).required).toBe(adaptiveThreshold(5));
    expect((error as Error).message).toContain('v9: 1');
  });

  it('agrees with a direct combine of the same shares', async () => {
    const secret = new Uint8Array([42, 43, 44, 45]);
    const shares = split(secret, 5, adaptiveThreshold(5));
    expect(Array.from(combine(shares.slice(0, adaptiveThreshold(5))))).toEqual(Array.from(secret));
  });
});

describe('VaultClient.list', () => {
  afterEach(() => vi.restoreAllMocks());

  it('refuses to answer when no guardian is reachable', async () => {
    const config = endpoints(3);
    fakeGuardians(config, { unauthenticated: config.map((e) => `${e.address}:${e.port}`) });

    await expect(client(config).list()).rejects.toBeInstanceOf(QuorumError);
  });

  /**
   * Asking one guardian reports whatever that node happens to know. A secret
   * only one guardian still holds cannot be reconstructed, so listing it invites
   * a read that must fail.
   */
  it('lists only secrets a threshold of guardians hold', async () => {
    const config = endpoints(5);
    const shared = { name: 'shared', version: 2, size: 16 };
    const orphan = { name: 'orphan', version: 1, size: 8 };
    fakeGuardians(config, {
      listings: {
        '10.0.0.1:7500': [shared, orphan],
        '10.0.0.2:7500': [shared],
        '10.0.0.3:7500': [shared],
        '10.0.0.4:7500': [],
        '10.0.0.5:7500': [],
      },
    });

    const result = await client(config).list();

    expect(result.secrets.map((s) => s.name)).toEqual(['shared']);
  });

  it('reports the newest version any guardian holds', async () => {
    const config = endpoints(3);
    fakeGuardians(config, {
      listings: {
        '10.0.0.1:7500': [{ name: 'k', version: 7, size: 32 }],
        '10.0.0.2:7500': [{ name: 'k', version: 5, size: 24 }],
        '10.0.0.3:7500': [{ name: 'k', version: 5, size: 24 }],
      },
    });

    const result = await client(config).list();

    expect(result.secrets).toEqual([{ name: 'k', version: 7, size: 32 }]);
  });

  it('returns names in a stable order', async () => {
    const config = endpoints(3);
    const all = [
      { name: 'zeta', version: 1, size: 1 },
      { name: 'alpha', version: 1, size: 1 },
      { name: 'mid', version: 1, size: 1 },
    ];
    fakeGuardians(config, {
      listings: {
        '10.0.0.1:7500': all,
        '10.0.0.2:7500': all,
        '10.0.0.3:7500': all,
      },
    });

    const result = await client(config).list();

    expect(result.secrets.map((s) => s.name)).toEqual(['alpha', 'mid', 'zeta']);
  });
});

describe('VaultClient.delete', () => {
  afterEach(() => vi.restoreAllMocks());

  it('removes the share from every reachable guardian', async () => {
    const config = endpoints(3);
    const { deleted } = fakeGuardians(config);

    const result = await client(config).delete('k');

    expect(result.ackCount).toBe(3);
    expect(deleted).toHaveLength(3);
    expect(result.guardianResults).toHaveLength(3);
  });

  it('throws when enough guardians still hold a share to reconstruct', async () => {
    const config = endpoints(5);
    fakeGuardians(config, { refusing: ['10.0.0.3:7500', '10.0.0.4:7500', '10.0.0.5:7500'] });

    const error = await client(config)
      .delete('k')
      .catch((e: unknown) => e);

    expect(error).toBeInstanceOf(QuorumError);
    expect((error as QuorumError).ackCount).toBe(2);
    expect((error as Error).message).toContain('reconstruct');
  });

  it('refuses to delete when no guardians are configured', async () => {
    await expect(client([]).delete('k')).rejects.toBeInstanceOf(QuorumError);
  });
});

describe('VaultClient sessions', () => {
  it('clearSessions does not throw', () => {
    expect(() => client(endpoints(1)).clearSessions()).not.toThrow();
  });
});
