import { HttpClient } from "../core/http";
import { AuthConfig, WhoAmI, StorageAdapter, MemoryStorage } from "./types";

export class AuthClient {
  private httpClient: HttpClient;
  private storage: StorageAdapter;
  private currentApiKey?: string;
  private currentJwt?: string;

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

    if (this.currentApiKey) {
      this.httpClient.setApiKey(this.currentApiKey);
    }
    if (this.currentJwt) {
      this.httpClient.setJwt(this.currentJwt);
    }
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

  getToken(): string | undefined {
    return this.httpClient.getToken();
  }

  async whoami(): Promise<WhoAmI> {
    try {
      const response = await this.httpClient.get<WhoAmI>("/v1/auth/whoami");
      return response;
    } catch {
      return { authenticated: false };
    }
  }

  async refresh(): Promise<string> {
    const response = await this.httpClient.post<{ token: string }>(
      "/v1/auth/refresh"
    );
    const token = response.token;
    this.setJwt(token);
    return token;
  }

  /**
   * Logout user and clear JWT, but preserve API key
   * Use this for user logout in apps where API key is app-level credential
   */
  async logoutUser(): Promise<void> {
    // Attempt server-side logout if using JWT
    if (this.currentJwt) {
      try {
        await this.httpClient.post("/v1/auth/logout", { all: true });
      } catch (error) {
        // Log warning but don't fail - local cleanup is more important
        console.warn(
          "Server-side logout failed, continuing with local cleanup:",
          error
        );
      }
    }

    // Clear JWT only, preserve API key
    this.currentJwt = undefined;
    this.httpClient.setJwt(undefined);
    await this.storage.set("jwt", ""); // Clear JWT from storage

    // Ensure API key is loaded and set as active auth method
    if (!this.currentApiKey) {
      // Try to load from storage
      const storedApiKey = await this.storage.get("apiKey");
      if (storedApiKey) {
        this.currentApiKey = storedApiKey;
      }
    }

    // Restore API key as the active auth method
    if (this.currentApiKey) {
      this.httpClient.setApiKey(this.currentApiKey);
      console.log("[Auth] API key restored after user logout");
    } else {
      console.warn("[Auth] No API key available after logout");
    }
  }

  /**
   * Full logout - clears both JWT and API key
   * Use this to completely reset authentication state
   */
  async logout(): Promise<void> {
    // Only attempt server-side logout if using JWT
    // API keys don't support server-side logout with all=true
    if (this.currentJwt) {
      try {
        await this.httpClient.post("/v1/auth/logout", { all: true });
      } catch (error) {
        // Log warning but don't fail - local cleanup is more important
        console.warn(
          "Server-side logout failed, continuing with local cleanup:",
          error
        );
      }
    }

    // Always clear local state
    this.currentApiKey = undefined;
    this.currentJwt = undefined;
    this.httpClient.setApiKey(undefined);
    this.httpClient.setJwt(undefined);
    await this.storage.clear();
  }

  async clear(): Promise<void> {
    this.currentApiKey = undefined;
    this.currentJwt = undefined;
    this.httpClient.setApiKey(undefined);
    this.httpClient.setJwt(undefined);
    await this.storage.clear();
  }

  /**
   * Request a challenge nonce for wallet authentication
   */
  async challenge(params: {
    wallet: string;
    purpose?: string;
    namespace?: string;
  }): Promise<{
    nonce: string;
    wallet: string;
    namespace: string;
    expires_at: string;
  }> {
    const response = await this.httpClient.post("/v1/auth/challenge", {
      wallet: params.wallet,
      purpose: params.purpose || "authentication",
      namespace: params.namespace || "default",
    });
    return response;
  }

  /**
   * Verify wallet signature and get JWT token
   */
  async verify(params: {
    wallet: string;
    nonce: string;
    signature: string;
    namespace?: string;
    chain_type?: "ETH" | "SOL";
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
      wallet: params.wallet,
      nonce: params.nonce,
      signature: params.signature,
      namespace: params.namespace || "default",
      chain_type: params.chain_type || "ETH",
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

    return response as any;
  }

  /**
   * Get API key for wallet (creates namespace ownership)
   */
  async getApiKey(params: {
    wallet: string;
    nonce: string;
    signature: string;
    namespace?: string;
    chain_type?: "ETH" | "SOL";
  }): Promise<{
    api_key: string;
    namespace: string;
    wallet: string;
  }> {
    const response = await this.httpClient.post("/v1/auth/api-key", {
      wallet: params.wallet,
      nonce: params.nonce,
      signature: params.signature,
      namespace: params.namespace || "default",
      chain_type: params.chain_type || "ETH",
    });

    // Automatically set the API key
    this.setApiKey(response.api_key);

    return response;
  }
}
