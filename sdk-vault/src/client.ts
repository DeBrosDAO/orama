import { AuthClient } from "./auth";
import { QuorumError } from "./errors";
import { withTimeout, withRetry } from "./transport/fanout";
import type { GuardianClient } from "./transport/guardian";
import type { GuardianEndpoint } from "./transport/types";
import { split, combine } from "./crypto/shamir";
import type { Share } from "./crypto/shamir";
import { adaptiveThreshold, writeQuorum } from "./quorum";
import type {
  VaultConfig,
  StoreResult,
  RetrieveResult,
  ListResult,
  DeleteResult,
  GuardianResult,
  SecretMeta,
} from "./types";

const PULL_TIMEOUT_MS = 10_000;

/** Stable identity of a guardian, independent of object identity. */
function endpointKey(endpoint: GuardianEndpoint): string {
  return `${endpoint.address}:${endpoint.port}`;
}

/** A share on the wire: [x:1 byte][y:rest]. */
function encodeShare(share: Share): Uint8Array {
  const bytes = new Uint8Array(1 + share.y.length);
  bytes[0] = share.x;
  bytes.set(share.y, 1);
  return bytes;
}

function decodeShare(bytes: Uint8Array): Share {
  if (bytes.length < 2) {
    throw new Error(`share too short: ${bytes.length} bytes`);
  }
  return { x: bytes[0]!, y: bytes.slice(1) };
}

/**
 * High-level client for the orama-vault distributed secrets store.
 *
 * Handles authentication with guardian nodes, Shamir split and combine, and
 * quorum-checked writes and reads over the V2 CRUD endpoints.
 *
 * The guardian set in the configuration is the unit of redundancy: a secret is
 * always split into one share per configured guardian, whether or not every
 * guardian is reachable at the time of the write. Splitting over only the
 * reachable ones would quietly reduce the cluster's redundancy to whatever
 * happened to be up during a write, and would leave the guardians that were
 * down holding shares of an older split.
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
   * @throws QuorumError when fewer than the write quorum acknowledged. The
   * secret is then not durable, and the error carries what each guardian did.
   */
  async store(name: string, data: Uint8Array, version: number): Promise<StoreResult> {
    const guardians = this.config.guardians;
    const n = guardians.length;
    if (n === 0) {
      throw new QuorumError("no guardians are configured", {
        ackCount: 0,
        required: 0,
        totalContacted: 0,
      });
    }

    const k = adaptiveThreshold(n);
    const w = writeQuorum(n);

    const authed = await this.auth.authenticateAll(guardians);
    const clients = new Map<string, GuardianClient>(
      authed.map(({ client, endpoint }) => [endpointKey(endpoint), client]),
    );

    // One share per configured guardian, in configuration order. The share is
    // addressed to the guardian at the same position, so a guardian that is
    // unreachable costs exactly its own share and nobody else's.
    const shares = split(data, n, k);

    let results: PromiseSettledResult<unknown>[];
    try {
      results = await Promise.allSettled(
        guardians.map(async (endpoint, index) => {
          const client = clients.get(endpointKey(endpoint));
          if (!client) {
            throw new Error("authentication failed");
          }
          return withRetry(() =>
            client.putSecret(name, encodeShare(shares[index]!), version),
          );
        }),
      );
    } finally {
      for (const share of shares) {
        share.y.fill(0);
      }
    }

    const guardianResults: GuardianResult[] = guardians.map((endpoint, index) => {
      const result = results[index]!;
      if (result.status === "fulfilled") {
        return { endpoint: endpointKey(endpoint), success: true };
      }
      return {
        endpoint: endpointKey(endpoint),
        success: false,
        error: (result.reason as Error).message,
      };
    });

    const ackCount = guardianResults.filter((r) => r.success).length;
    if (ackCount < w) {
      throw new QuorumError(
        `stored ${ackCount} of the ${w} shares needed to make "${name}" durable across ${n} guardians`,
        { ackCount, required: w, totalContacted: n, guardianResults },
      );
    }

    return {
      ackCount,
      totalContacted: n,
      failCount: n - ackCount,
      guardianResults,
    };
  }

  /**
   * Retrieve and reconstruct a secret from guardian nodes.
   *
   * Shares are grouped by the version they belong to, and only shares of a
   * single version are ever combined. A guardian that missed the last write
   * still answers, with a share of an older split; combining it with newer
   * shares reconstructs neither version and reports no error, so the caller
   * would receive plausible-looking rubbish.
   *
   * @param name - Secret name
   * @throws QuorumError when no single version has the threshold of shares.
   */
  async retrieve(name: string): Promise<RetrieveResult> {
    const guardians = this.config.guardians;
    const n = guardians.length;
    const k = adaptiveThreshold(n);

    const authed = await this.auth.authenticateAll(guardians);

    const pulls = await Promise.allSettled(
      authed.map(({ client }) => withTimeout(client.getSecret(name), PULL_TIMEOUT_MS)),
    );

    const byVersion = new Map<number, Share[]>();
    for (const pull of pulls) {
      if (pull.status !== "fulfilled") continue;
      const share = decodeShare(pull.value.share);
      const forVersion = byVersion.get(pull.value.version);
      if (forVersion) {
        forVersion.push(share);
      } else {
        byVersion.set(pull.value.version, [share]);
      }
    }

    // The newest version that can actually be reconstructed. A newer version
    // with too few shares means a write did not reach quorum; the previous
    // complete version is still the secret's last durable value.
    let chosenVersion: number | undefined;
    for (const [version, shares] of byVersion) {
      if (shares.length >= k && (chosenVersion === undefined || version > chosenVersion)) {
        chosenVersion = version;
      }
    }

    if (chosenVersion === undefined) {
      const collected = [...byVersion.entries()]
        .map(([version, shares]) => `v${version}: ${shares.length}`)
        .join(", ");
      throw new QuorumError(
        `no version of "${name}" has the ${k} shares needed to reconstruct it ` +
          `(collected ${collected || "nothing"} from ${authed.length} of ${n} guardians)`,
        { ackCount: 0, required: k, totalContacted: n },
      );
    }

    const shares = byVersion.get(chosenVersion)!;
    try {
      return {
        data: combine(shares),
        sharesCollected: shares.length,
        version: chosenVersion,
      };
    } finally {
      for (const collected of byVersion.values()) {
        for (const share of collected) {
          share.y.fill(0);
        }
      }
    }
  }

  /**
   * List the secrets stored for this identity.
   *
   * A secret is listed when at least the read threshold of guardians report
   * holding a share of it, because that is exactly the condition under which it
   * can be reconstructed. Asking a single guardian — which is what this used to
   * do — reports whatever that one node happens to know, including secrets it
   * alone still has after a delete and secrets it alone missed.
   */
  async list(): Promise<ListResult> {
    const guardians = this.config.guardians;
    const n = guardians.length;
    const k = adaptiveThreshold(n);

    const authed = await this.auth.authenticateAll(guardians);
    if (authed.length === 0) {
      throw new QuorumError("no guardians are reachable", {
        ackCount: 0,
        required: k,
        totalContacted: n,
      });
    }

    const listings = await Promise.allSettled(
      authed.map(({ client }) => client.listSecrets()),
    );

    // Per name: how many guardians hold it, and the newest entry any of them
    // reported. Versions are monotonic and a stored write reached more than k
    // guardians, so the newest version seen is the current one.
    const seen = new Map<string, { holders: number; newest: SecretMeta }>();
    for (const listing of listings) {
      if (listing.status !== "fulfilled") continue;
      for (const entry of listing.value.secrets) {
        const known = seen.get(entry.name);
        if (!known) {
          seen.set(entry.name, { holders: 1, newest: { ...entry } });
          continue;
        }
        known.holders += 1;
        if (entry.version > known.newest.version) {
          known.newest = { ...entry };
        }
      }
    }

    const secrets = [...seen.values()]
      .filter(({ holders }) => holders >= k)
      .map(({ newest }) => newest)
      .sort((a, b) => a.name.localeCompare(b.name));

    return { secrets };
  }

  /**
   * Delete a secret from all guardian nodes.
   *
   * @param name - Secret name to delete
   * @throws QuorumError when fewer than the write quorum acknowledged. Enough
   * guardians still hold a share to reconstruct the secret, so it is not gone.
   */
  async delete(name: string): Promise<DeleteResult> {
    const guardians = this.config.guardians;
    const n = guardians.length;
    if (n === 0) {
      throw new QuorumError("no guardians are configured", {
        ackCount: 0,
        required: 0,
        totalContacted: 0,
      });
    }

    const w = writeQuorum(n);
    const authed = await this.auth.authenticateAll(guardians);
    const clients = new Map<string, GuardianClient>(
      authed.map(({ client, endpoint }) => [endpointKey(endpoint), client]),
    );

    const results = await Promise.allSettled(
      guardians.map(async (endpoint) => {
        const client = clients.get(endpointKey(endpoint));
        if (!client) {
          throw new Error("authentication failed");
        }
        return withRetry(() => client.deleteSecret(name));
      }),
    );

    const guardianResults: GuardianResult[] = guardians.map((endpoint, index) => {
      const result = results[index]!;
      if (result.status === "fulfilled") {
        return { endpoint: endpointKey(endpoint), success: true };
      }
      return {
        endpoint: endpointKey(endpoint),
        success: false,
        error: (result.reason as Error).message,
      };
    });

    const ackCount = guardianResults.filter((r) => r.success).length;
    if (ackCount < w) {
      throw new QuorumError(
        `deleted "${name}" from ${ackCount} of the ${w} guardians needed; ` +
          `enough shares remain to reconstruct it`,
        { ackCount, required: w, totalContacted: n, guardianResults },
      );
    }

    return { ackCount, totalContacted: n, guardianResults };
  }

  /** Clear all cached auth sessions. */
  clearSessions(): void {
    this.auth.clearSessions();
  }
}
