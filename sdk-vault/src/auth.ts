import { GuardianClient } from './transport/guardian';
import type { GuardianEndpoint } from './transport/types';

/**
 * Handles challenge-response authentication with guardian nodes.
 * Caches session tokens per guardian endpoint.
 *
 * Auth flow:
 * 1. POST /v2/vault/auth/challenge with identity → get {nonce, created_ns, tag}
 * 2. POST /v2/vault/auth/session with identity + challenge fields → get session token
 * 3. Use session token as X-Session-Token header for V2 requests
 *
 * The session token format is: `<identity_hex>:<expiry_ns>:<tag_hex>`
 */
export class AuthClient {
  private sessions = new Map<string, { token: string; expiryNs: number }>();
  private identityHex: string;
  private timeoutMs: number;

  constructor(identityHex: string, timeoutMs = 10_000) {
    this.identityHex = identityHex;
    this.timeoutMs = timeoutMs;
  }

  /**
   * Authenticate with a guardian and cache the session token.
   * Returns a GuardianClient with the session token set.
   */
  async authenticate(endpoint: GuardianEndpoint): Promise<GuardianClient> {
    const key = `${endpoint.address}:${endpoint.port}`;
    const cached = this.sessions.get(key);

    // Check if we have a valid cached session (with 30s safety margin)
    if (cached) {
      const nowNs = Date.now() * 1_000_000;
      if (cached.expiryNs > nowNs + 30_000_000_000) {
        const client = new GuardianClient(endpoint, this.timeoutMs);
        client.setSessionToken(cached.token);
        return client;
      }
      // Expired, remove
      this.sessions.delete(key);
    }

    const client = new GuardianClient(endpoint, this.timeoutMs);

    // Step 1: Request challenge
    const challenge = await client.requestChallenge(this.identityHex);

    // Step 2: Exchange for session
    const session = await client.createSession(
      this.identityHex,
      challenge.nonce,
      challenge.created_ns,
      challenge.tag,
    );

    // Build token string: identity:expiry_ns:tag
    const token = `${session.identity}:${session.expiry_ns}:${session.tag}`;
    client.setSessionToken(token);

    // Cache
    this.sessions.set(key, { token, expiryNs: session.expiry_ns });

    return client;
  }

  /**
   * Authenticate with multiple guardians in parallel.
   * Returns authenticated GuardianClients for all that succeed.
   */
  async authenticateAll(endpoints: GuardianEndpoint[]): Promise<{ client: GuardianClient; endpoint: GuardianEndpoint }[]> {
    const results = await Promise.allSettled(
      endpoints.map(async (ep) => {
        const client = await this.authenticate(ep);
        return { client, endpoint: ep };
      }),
    );

    const authenticated: { client: GuardianClient; endpoint: GuardianEndpoint }[] = [];
    for (const r of results) {
      if (r.status === 'fulfilled') {
        authenticated.push(r.value);
      }
    }
    return authenticated;
  }

  /** Clear all cached sessions. */
  clearSessions(): void {
    this.sessions.clear();
  }

  /** Get the identity hex string. */
  getIdentityHex(): string {
    return this.identityHex;
  }
}
