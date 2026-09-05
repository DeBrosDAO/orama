import { HttpClient } from "../core/http";
import { Logger } from "../core/logger";
import { AuthError, SDKError } from "../errors";
import { WhoAmI, StorageAdapter, MemoryStorage } from "./types";

/** What to clear, and whether to tell the gateway. */
export interface LogoutOptions {
  /**
   * Keep the API key and make it the active credential again. Use this when
   * the key is an application-level credential and only the signed-in user is
   * leaving. Default: false.
   */
  keepApiKey?: boolean;
  /**
   * Ask the gateway to invalidate the session before clearing locally.
   * Default: true. Set it to false to reset local state only.
   */
  server?: boolean;
}

export class AuthClient {
  private httpClient: HttpClient;
  private storage: StorageAdapter;
  private currentApiKey?: string;
  private currentJwt?: string;
  private readonly log: Logger;

  constructor(config: {
    httpClient: HttpClient;
    storage?: StorageAdapter;
    apiKey?: string;
    jwt?: string;
  }) {
    this.httpClient = config.httpClient;
    this.storage = config.storage ?? new MemoryStorage();
    this.currentApiKey = config.apiKey;
    this.currentJwt = config.jwt;
    this.log = config.httpClient.logger("Auth");

    if (this.currentApiKey) {
      this.httpClient.setApiKey(this.currentApiKey);
    }
    if (this.currentJwt) {
      this.httpClient.setJwt(this.currentJwt);
    }

    // How a key becomes a token. The key is exchanged once, before the first
    // request, and the token is what every request carries — so the key itself
    // never reaches an access log, a proxy trace or a devtools tab.
    this.httpClient.setKeyExchanger(async (apiKey) => this.exchangeKey(apiKey));

    // A 401 now renews the session and replays the request once. `refresh()`
    // has existed since the first release with nothing calling it, so every
    // application built its own refresh-and-retry loop around every call.
    this.httpClient.setTokenRefresher(async () => {
      if (await this.storage.get("refreshToken")) {
        return this.refresh();
      }
      // No user session. If this client was given a key, the way back is to
      // exchange it again — the token it was carrying has expired, and the key
      // has not.
      if (this.currentApiKey) {
        this.httpClient.clearExchangedToken();
        const { token } = await this.exchangeKey(this.currentApiKey);
        return token;
      }
      return null;
    });
  }

  /**
   * Exchange an API key for a short-lived token.
   *
   * The gateway answers with the key's own grants embedded, so the token
   * reaches exactly what the key reached and nothing more. It is a plain
   * request rather than one through the client's own pipeline, because the
   * pipeline is what calls this.
   */
  private async exchangeKey(apiKey: string): Promise<{ token: string; expiresAt: number }> {
    const res = await this.httpClient.request<{
      access_token: string;
      expires_in: number;
    }>("POST", "/v1/auth/token", {
      ownCredential: true,
      headers: { Authorization: `Bearer ${apiKey}` },
    });

    if (!res?.access_token) {
      throw new SDKError("the gateway exchanged the key for no token", 500, "AUTH_EXCHANGE_FAILED");
    }
    return {
      token: res.access_token,
      expiresAt: Date.now() + (res.expires_in ?? 900) * 1000,
    };
  }

  setApiKey(apiKey: string) {
    this.currentApiKey = apiKey;
    // Don't clear JWT - it will be cleared explicitly on logout
    this.httpClient.setApiKey(apiKey);
    this.storage.set("apiKey", apiKey);
  }

  setJwt(jwt: string) {
    this.currentJwt = jwt;
    // Don't clear API key - keep it as fallback for after logout
    this.httpClient.setJwt(jwt);
    this.storage.set("jwt", jwt);
  }

  /**
   * Renew a workload's token with the token it is holding.
   *
   * Only a deployment's own token renews this way. A user session is renewed by
   * its refresh token, which rotates and can be revoked; letting any access
   * token mint its own successor would make a stolen one good for ever.
   */
  async renew(): Promise<{ access_token: string; expires_in: number }> {
    return this.httpClient.request("POST", "/v1/auth/renew", {});
  }

  getToken(): string | undefined {
    return this.httpClient.getToken();
  }

  /**
   * Ask the gateway who this client is authenticated as.
   *
   * A rejected credential is an answer: `{ authenticated: false }`. Anything
   * else — an unreachable gateway, a 500, a timeout — is a failure and is
   * thrown. This used to catch everything and report "not authenticated", so an
   * application could not tell a bad API key from a gateway that was down, and
   * a connectivity problem looked like a login problem.
   */
  async whoami(): Promise<WhoAmI> {
    try {
      return await this.httpClient.get<WhoAmI>("/v1/auth/whoami");
    } catch (error) {
      if (error instanceof AuthError || (error instanceof SDKError && error.httpStatus === 401)) {
        return { authenticated: false };
      }
      throw error;
    }
  }

  /**
   * Exchange a stored refresh token for a fresh access token.
   *
   * Pulls the refresh token (and the namespace it was issued for) out of
   * storage — both are persisted by `verify()` after a successful wallet
   * sign-in. The gateway returns a new access token and may rotate the
   * refresh token; we persist the rotated one if present.
   *
   * Bug #239: previously this method (a) sent no body and (b) read the
   * wrong response field, so the call always 400-ed AND silently wrote
   * `undefined` as the in-memory JWT. Both issues fixed.
   */
  async refresh(): Promise<string> {
    const refreshToken = await this.storage.get("refreshToken");
    if (!refreshToken) {
      throw new Error(
        "refresh failed: no refresh token in storage — call verify() first"
      );
    }
    const namespace = (await this.storage.get("namespace")) ?? "default";

    const response = await this.httpClient.post<{
      access_token: string;
      refresh_token?: string;
      expires_in?: number;
      subject?: string;
      namespace?: string;
      token_type?: string;
    }>("/v1/auth/refresh", { refresh_token: refreshToken, namespace });

    if (!response?.access_token) {
      throw new Error("refresh failed: server returned no access_token");
    }

    this.setJwt(response.access_token);

    // Rotate the stored refresh token if the server returned a new one
    // (rqlite-side gateway currently echoes the same token; future versions
    // may rotate, so handle both shapes).
    if (response.refresh_token && response.refresh_token !== refreshToken) {
      await this.storage.set("refreshToken", response.refresh_token);
    }

    return response.access_token;
  }

  /**
   * End the session.
   *
   * There used to be three methods for this — `logout`, `logoutUser` and
   * `clear` — differing in two independent ways that their names did not
   * convey: whether the gateway is told, and whether the API key survives.
   * Both are options now.
   *
   * @param options.keepApiKey Keep the API key and make it the active
   * credential again. For a signed-in user leaving an application whose key is
   * application-level.
   * @param options.server Ask the gateway to invalidate the session first.
   */
  async logout(options: LogoutOptions = {}): Promise<void> {
    const { keepApiKey = false, server = true } = options;

    // Only a JWT has a server-side session; an API key has nothing to end.
    if (server && this.currentJwt) {
      try {
        await this.httpClient.post("/v1/auth/logout", { all: true });
      } catch (error) {
        // Local cleanup matters more than the gateway's acknowledgement, so
        // this is reported and not raised. Tracked separately: an application
        // cannot currently tell that the server was not told.
        this.log.warn(
          "Server-side logout failed, continuing with local cleanup:",
          error
        );
      }
    }

    this.currentJwt = undefined;
    this.httpClient.setJwt(undefined);

    if (!keepApiKey) {
      this.currentApiKey = undefined;
      this.httpClient.setApiKey(undefined);
      await this.storage.clear();
      return;
    }

    await this.storage.set("jwt", "");

    // The key may only exist in storage, if this client was built from a
    // stored session rather than from a config.
    if (!this.currentApiKey) {
      const stored = await this.storage.get("apiKey");
      if (stored) {
        this.currentApiKey = stored;
      }
    }

    if (this.currentApiKey) {
      this.httpClient.setApiKey(this.currentApiKey);
      this.log.log("API key restored after user logout");
    } else {
      this.log.warn("No API key available after logout");
    }
  }

  /**
   * @deprecated Use `logout({ keepApiKey: true })`.
   */
  async logoutUser(): Promise<void> {
    return this.logout({ keepApiKey: true });
  }

  /**
   * Reset local authentication state without telling the gateway.
   *
   * @deprecated Use `logout({ server: false })`.
   */
  async clear(): Promise<void> {
    return this.logout({ server: false });
  }

  /**
   * Ask the gateway for the message this wallet should sign.
   *
   * `message` is a Sign-In with Ethereum (EIP-4361) message, or its Solana
   * counterpart for `chain_type: "SOL"`. Sign it **verbatim** — it is what the
   * wallet shows the user, and the gateway verifies the signature against the
   * exact text it issued, so re-rendering the same fields produces a signature
   * over different bytes and a failed login.
   *
   * The domain, namespace, nonce and expiry are all inside it. That is the
   * point: a signature over a bare nonce says only that someone holding the key
   * signed some bytes, which makes it valid for any site that ever sees it.
   */
  async challenge(params: {
    wallet: string;
    purpose?: string;
    namespace?: string;
    chain_type?: "ETH" | "SOL";
  }): Promise<{
    message: string;
    nonce: string;
    wallet: string;
    namespace: string;
    chain_type: string;
    issued_at: string;
    expires_at: string;
  }> {
    const response = await this.httpClient.post("/v1/auth/challenge", {
      wallet: params.wallet,
      purpose: params.purpose || "authentication",
      namespace: params.namespace || "default",
      chain_type: params.chain_type || "ETH",
    });
    return response;
  }

  /**
   * Verify a signed sign-in message and get a JWT.
   *
   * Send back the `message` from {@link challenge} exactly as received, with
   * the signature over it. The wallet, the nonce and the namespace are read out
   * of the message rather than passed beside it — those are the fields the user
   * approved, and the ones the signature covers.
   */
  async verify(params: {
    message: string;
    signature: string;
  }): Promise<{
    access_token: string;
    refresh_token?: string;
    subject: string;
    namespace: string;
    api_key?: string;
    expires_in?: number;
    token_type?: string;
  }> {
    const response = await this.httpClient.post("/v1/auth/verify", {
      message: params.message,
      signature: params.signature,
    });

    // Persist JWT
    this.setJwt(response.access_token);

    // Persist API key if server provided it (created in verifyHandler)
    if ((response as any).api_key) {
      this.setApiKey((response as any).api_key);
    }

    // Persist refresh token if present (optional, for silent renewal)
    if ((response as any).refresh_token) {
      await this.storage.set("refreshToken", (response as any).refresh_token);
    }

    // Persist the namespace this JWT was issued for so refresh() can
    // include it in the refresh request body (the gateway scopes refresh
    // tokens to the issuing namespace). Bug #239 — without this, refresh
    // would default to "default" and fail for namespace-scoped sessions.
    const issuedNamespace = (response as any).namespace || "default";
    await this.storage.set("namespace", issuedNamespace);

    return response as any;
  }

  /**
   * Get an API key for the wallet that signed the message (creates namespace
   * ownership). Takes the same signed message as {@link verify}.
   */
  async getApiKey(params: {
    message: string;
    signature: string;
    plan?: string;
  }): Promise<{
    api_key: string;
    namespace: string;
    wallet: string;
  }> {
    const response = await this.httpClient.post("/v1/auth/api-key", {
      message: params.message,
      signature: params.signature,
      ...(params.plan ? { plan: params.plan } : {}),
    });

    // Automatically set the API key
    this.setApiKey(response.api_key);

    return response;
  }
}
