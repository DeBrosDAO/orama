import { AuthClient } from './auth';
import { withTimeout, withRetry } from './transport/fanout';
import { split, combine } from './crypto/shamir';
import type { Share } from './crypto/shamir';
import { adaptiveThreshold, writeQuorum } from './quorum';
import type {
  VaultConfig,
  StoreResult,
  RetrieveResult,
  ListResult,
  DeleteResult,
  GuardianResult,
} from './types';

const PULL_TIMEOUT_MS = 10_000;

/**
 * High-level client for the orama-vault distributed secrets store.
 *
 * Handles:
 * - Authentication with guardian nodes
 * - Shamir split/combine for data distribution
 * - Quorum-based writes and reads
 * - V2 CRUD operations (store, retrieve, list, delete)
 */
export class VaultClient {
  private config: VaultConfig;
  private auth: AuthClient;

  constructor(config: VaultConfig) {
    this.config = config;
    this.auth = new AuthClient(config.identityHex, config.timeoutMs);
  }

  /**
   * Store a secret across guardian nodes using Shamir splitting.
   *
   * @param name - Secret name (alphanumeric, _, -, max 128 chars)
   * @param data - Secret data to store
   * @param version - Monotonic version number (must be > previous)
   */
  async store(name: string, data: Uint8Array, version: number): Promise<StoreResult> {
    const guardians = this.config.guardians;
    const n = guardians.length;
    const k = adaptiveThreshold(n);

    // Shamir split the data
    const shares = split(data, n, k);

    // Authenticate and push to all guardians
    const authed = await this.auth.authenticateAll(guardians);

    const results = await Promise.allSettled(
      authed.map(async ({ client, endpoint }, _i) => {
        // Find the share for this guardian's index
        const guardianIdx = guardians.indexOf(endpoint);
        const share = shares[guardianIdx];
        if (!share) throw new Error('share index out of bounds');

        // Encode share as [x:1byte][y:rest]
        const shareBytes = new Uint8Array(1 + share.y.length);
        shareBytes[0] = share.x;
        shareBytes.set(share.y, 1);

        return withRetry(() => client.putSecret(name, shareBytes, version));
      }),
    );

    // Wipe shares
    for (const share of shares) {
      share.y.fill(0);
    }

    const guardianResults: GuardianResult[] = authed.map(({ endpoint }, i) => {
      const ep = `${endpoint.address}:${endpoint.port}`;
      const r = results[i]!;
      if (r.status === 'fulfilled') {
        return { endpoint: ep, success: true };
      }
      return { endpoint: ep, success: false, error: (r.reason as Error).message };
    });

    const ackCount = results.filter((r) => r.status === 'fulfilled').length;
    const failCount = results.filter((r) => r.status === 'rejected').length;
    const w = writeQuorum(n);

    return {
      ackCount,
      totalContacted: authed.length,
      failCount,
      quorumMet: ackCount >= w,
      guardianResults,
    };
  }

  /**
   * Retrieve and reconstruct a secret from guardian nodes.
   *
   * @param name - Secret name
   */
  async retrieve(name: string): Promise<RetrieveResult> {
    const guardians = this.config.guardians;
    const n = guardians.length;
    const k = adaptiveThreshold(n);

    // Authenticate and pull from all guardians
    const authed = await this.auth.authenticateAll(guardians);

    const pullResults = await Promise.allSettled(
      authed.map(async ({ client }) => {
        const resp = await withTimeout(client.getSecret(name), PULL_TIMEOUT_MS);
        const shareBytes = resp.share;
        if (shareBytes.length < 2) throw new Error('Share too short');
        return {
          x: shareBytes[0]!,
          y: shareBytes.slice(1),
        } as Share;
      }),
    );

    const shares: Share[] = [];
    for (const r of pullResults) {
      if (r.status === 'fulfilled') {
        shares.push(r.value);
      }
    }

    if (shares.length < k) {
      throw new Error(
        `Not enough shares: collected ${shares.length} of ${k} required (contacted ${authed.length} guardians)`,
      );
    }

    // Reconstruct
    const data = combine(shares);

    // Wipe collected shares
    for (const share of shares) {
      share.y.fill(0);
    }

    return {
      data,
      sharesCollected: shares.length,
    };
  }

  /**
   * List all secrets for this identity.
   * Queries the first reachable guardian (metadata is replicated).
   */
  async list(): Promise<ListResult> {
    const guardians = this.config.guardians;
    const authed = await this.auth.authenticateAll(guardians);

    if (authed.length === 0) {
      throw new Error('No guardians reachable');
    }

    // Query first authenticated guardian
    const resp = await authed[0]!.client.listSecrets();
    return { secrets: resp.secrets };
  }

  /**
   * Delete a secret from all guardian nodes.
   *
   * @param name - Secret name to delete
   */
  async delete(name: string): Promise<DeleteResult> {
    const guardians = this.config.guardians;
    const n = guardians.length;

    const authed = await this.auth.authenticateAll(guardians);

    const results = await Promise.allSettled(
      authed.map(async ({ client }) => {
        return withRetry(() => client.deleteSecret(name));
      }),
    );

    const ackCount = results.filter((r) => r.status === 'fulfilled').length;
    const w = writeQuorum(n);

    return {
      ackCount,
      totalContacted: authed.length,
      quorumMet: ackCount >= w,
    };
  }

  /** Clear all cached auth sessions. */
  clearSessions(): void {
    this.auth.clearSessions();
  }
}
