import type {
  GuardianEndpoint,
  GuardianErrorCode,
  GuardianErrorBody,
  HealthResponse,
  StatusResponse,
  GuardianInfo,
  PushResponse,
  PullResponse,
  StoreSecretResponse,
  GetSecretResponse,
  DeleteSecretResponse,
  ListSecretsResponse,
  ChallengeResponse,
  SessionResponse,
} from './types';

export class GuardianError extends Error {
  constructor(public readonly code: GuardianErrorCode, message: string) {
    super(message);
    this.name = 'GuardianError';
  }
}

const DEFAULT_TIMEOUT_MS = 10_000;

/**
 * HTTP client for a single orama-vault guardian node.
 * Supports V1 (push/pull) and V2 (CRUD secrets) endpoints.
 */
export class GuardianClient {
  private baseUrl: string;
  private timeoutMs: number;
  private sessionToken: string | null = null;

  constructor(endpoint: GuardianEndpoint, timeoutMs = DEFAULT_TIMEOUT_MS) {
    this.baseUrl = `http://${endpoint.address}:${endpoint.port}`;
    this.timeoutMs = timeoutMs;
  }

  /** Set a session token for authenticated V2 requests. */
  setSessionToken(token: string): void {
    this.sessionToken = token;
  }

  /** Get the current session token. */
  getSessionToken(): string | null {
    return this.sessionToken;
  }

  /** Clear the session token. */
  clearSessionToken(): void {
    this.sessionToken = null;
  }

  // ── V1 endpoints ────────────────────────────────────────────────────

  /** GET /v1/vault/health */
  async health(): Promise<HealthResponse> {
    return this.get<HealthResponse>('/v1/vault/health');
  }

  /** GET /v1/vault/status */
  async status(): Promise<StatusResponse> {
    return this.get<StatusResponse>('/v1/vault/status');
  }

  /** GET /v1/vault/guardians */
  async guardians(): Promise<GuardianInfo> {
    return this.get<GuardianInfo>('/v1/vault/guardians');
  }

  /** POST /v1/vault/push — store a share (V1). */
  async push(identity: string, share: Uint8Array): Promise<PushResponse> {
    return this.post<PushResponse>('/v1/vault/push', {
      identity,
      share: uint8ToBase64(share),
    });
  }

  /** POST /v1/vault/pull — retrieve a share (V1). */
  async pull(identity: string): Promise<Uint8Array> {
    const resp = await this.post<PullResponse>('/v1/vault/pull', { identity });
    return base64ToUint8(resp.share);
  }

  /** Check if this guardian is reachable. */
  async isReachable(): Promise<boolean> {
    try {
      await this.health();
      return true;
    } catch {
      return false;
    }
  }

  // ── V2 auth endpoints ───────────────────────────────────────────────

  /** POST /v2/vault/auth/challenge — request an auth challenge. */
  async requestChallenge(identity: string): Promise<ChallengeResponse> {
    return this.post<ChallengeResponse>('/v2/vault/auth/challenge', { identity });
  }

  /** POST /v2/vault/auth/session — exchange challenge for session token. */
  async createSession(identity: string, nonce: string, created_ns: number, tag: string): Promise<SessionResponse> {
    return this.post<SessionResponse>('/v2/vault/auth/session', {
      identity,
      nonce,
      created_ns,
      tag,
    });
  }

  // ── V2 secrets CRUD ─────────────────────────────────────────────────

  /** PUT /v2/vault/secrets/{name} — store a secret. Requires session token. */
  async putSecret(name: string, share: Uint8Array, version: number): Promise<StoreSecretResponse> {
    return this.authedRequest<StoreSecretResponse>('PUT', `/v2/vault/secrets/${encodeURIComponent(name)}`, {
      share: uint8ToBase64(share),
      version,
    });
  }

  /** GET /v2/vault/secrets/{name} — retrieve a secret. Requires session token. */
  async getSecret(name: string): Promise<{ share: Uint8Array; name: string; version: number; created_ns: number; updated_ns: number }> {
    const resp = await this.authedRequest<GetSecretResponse>('GET', `/v2/vault/secrets/${encodeURIComponent(name)}`);
    return {
      share: base64ToUint8(resp.share),
      name: resp.name,
      version: resp.version,
      created_ns: resp.created_ns,
      updated_ns: resp.updated_ns,
    };
  }

  /** DELETE /v2/vault/secrets/{name} — delete a secret. Requires session token. */
  async deleteSecret(name: string): Promise<DeleteSecretResponse> {
    return this.authedRequest<DeleteSecretResponse>('DELETE', `/v2/vault/secrets/${encodeURIComponent(name)}`);
  }

  /** GET /v2/vault/secrets — list all secrets. Requires session token. */
  async listSecrets(): Promise<ListSecretsResponse> {
    return this.authedRequest<ListSecretsResponse>('GET', '/v2/vault/secrets');
  }

  // ── Internal HTTP methods ───────────────────────────────────────────

  private async authedRequest<T>(method: string, path: string, body?: unknown): Promise<T> {
    if (!this.sessionToken) {
      throw new GuardianError('AUTH', 'No session token set. Call authenticate() first.');
    }

    const controller = new AbortController();
    const timeout = setTimeout(() => controller.abort(), this.timeoutMs);

    try {
      const headers: Record<string, string> = {
        'X-Session-Token': this.sessionToken,
      };
      const init: RequestInit = {
        method,
        headers,
        signal: controller.signal,
      };

      if (body !== undefined) {
        headers['Content-Type'] = 'application/json';
        init.body = JSON.stringify(body);
      }

      const resp = await fetch(`${this.baseUrl}${path}`, init);

      if (!resp.ok) {
        const errBody = (await resp.json().catch(() => ({}))) as GuardianErrorBody;
        const msg = errBody.error || `HTTP ${resp.status}`;
        throw new GuardianError(classifyHttpStatus(resp.status), msg);
      }

      return (await resp.json()) as T;
    } catch (err) {
      throw classifyError(err);
    } finally {
      clearTimeout(timeout);
    }
  }

  private async get<T>(path: string): Promise<T> {
    const controller = new AbortController();
    const timeout = setTimeout(() => controller.abort(), this.timeoutMs);

    try {
      const resp = await fetch(`${this.baseUrl}${path}`, {
        method: 'GET',
        signal: controller.signal,
      });

      if (!resp.ok) {
        const body = (await resp.json().catch(() => ({}))) as GuardianErrorBody;
        const msg = body.error || `HTTP ${resp.status}`;
        throw new GuardianError(classifyHttpStatus(resp.status), msg);
      }

      return (await resp.json()) as T;
    } catch (err) {
      throw classifyError(err);
    } finally {
      clearTimeout(timeout);
    }
  }

  private async post<T>(path: string, body: unknown): Promise<T> {
    const controller = new AbortController();
    const timeout = setTimeout(() => controller.abort(), this.timeoutMs);

    try {
      const resp = await fetch(`${this.baseUrl}${path}`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
        signal: controller.signal,
      });

      if (!resp.ok) {
        const errBody = (await resp.json().catch(() => ({}))) as GuardianErrorBody;
        const msg = errBody.error || `HTTP ${resp.status}`;
        throw new GuardianError(classifyHttpStatus(resp.status), msg);
      }

      return (await resp.json()) as T;
    } catch (err) {
      throw classifyError(err);
    } finally {
      clearTimeout(timeout);
    }
  }
}

// ── Error classification ──────────────────────────────────────────────────

function classifyHttpStatus(status: number): GuardianErrorCode {
  if (status === 404) return 'NOT_FOUND';
  if (status === 401 || status === 403) return 'AUTH';
  if (status === 409) return 'CONFLICT';
  if (status >= 500) return 'SERVER_ERROR';
  return 'NETWORK';
}

function classifyError(err: unknown): GuardianError {
  if (err instanceof GuardianError) return err;
  if (err instanceof Error) {
    if (err.name === 'AbortError') {
      return new GuardianError('TIMEOUT', `Request timed out: ${err.message}`);
    }
    if (err.name === 'TypeError' || err.message.includes('fetch')) {
      return new GuardianError('NETWORK', `Network error: ${err.message}`);
    }
    return new GuardianError('NETWORK', err.message);
  }
  return new GuardianError('NETWORK', String(err));
}

// ── Base64 helpers ────────────────────────────────────────────────────────

function uint8ToBase64(bytes: Uint8Array): string {
  if (typeof Buffer !== 'undefined') {
    return Buffer.from(bytes).toString('base64');
  }
  let binary = '';
  for (let i = 0; i < bytes.length; i++) {
    binary += String.fromCharCode(bytes[i]!);
  }
  return btoa(binary);
}

function base64ToUint8(b64: string): Uint8Array {
  if (typeof Buffer !== 'undefined') {
    return new Uint8Array(Buffer.from(b64, 'base64'));
  }
  const binary = atob(b64);
  const bytes = new Uint8Array(binary.length);
  for (let i = 0; i < binary.length; i++) {
    bytes[i] = binary.charCodeAt(i);
  }
  return bytes;
}
