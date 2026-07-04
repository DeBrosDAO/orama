import { describe, it, expect, vi } from "vitest";
import { HttpClient } from "../../../src/core/http";

/**
 * Bugboard #149 — the SDK dropped the wallet JWT on /v1/proxy/*.
 *
 * The gateway enforces a per-user (SIWE wallet) JWT on proxy operations
 * (layer-1, #148): an API key alone is rejected 401 "requires a logged-in
 * user". HttpClient.getAuthHeaders() bucketed /v1/proxy/ into the api-key-ONLY
 * branch (alongside rqlite/pubsub/cache), so proxyAnon went out without the
 * Authorization header even when a valid wallet JWT was set — while /v1/storage/*
 * (which is NOT in that branch) correctly sent both. Same client, same JWT,
 * attached on storage, dropped on proxy. This locks in that proxy sends BOTH.
 */
describe("Bug #149 — HttpClient attaches wallet JWT on /v1/proxy/*", () => {
  function captureHeaders() {
    const seen: Record<string, any> = {};
    const fetchImpl = vi.fn(async (url: any, options: any) => {
      seen.url = String(url);
      seen.headers = options?.headers ?? {};
      return new Response(JSON.stringify({ ok: true }), {
        status: 200,
        headers: { "content-type": "application/json" },
      });
    });
    const client = new HttpClient({
      baseURL: "https://gw.example",
      maxRetries: 0,
      timeout: 5000,
      fetch: fetchImpl as any,
    });
    client.setApiKey("ak_runtime:anchat-test");
    client.setJwt("eyJhbGciOi.wallet.jwt");
    return { client, seen };
  }

  it("sends BOTH X-API-Key and Bearer JWT on POST /v1/proxy/anon", async () => {
    const { client, seen } = captureHeaders();
    await client.post("/v1/proxy/anon", { url: "https://x" });
    expect(seen.headers["X-API-Key"]).toBe("ak_runtime:anchat-test");
    expect(seen.headers["Authorization"]).toBe("Bearer eyJhbGciOi.wallet.jwt");
  });

  it("storage (the working reference) also sends both — proxy must match it", async () => {
    const { client, seen } = captureHeaders();
    await client.post("/v1/storage/upload", { data: "x" });
    expect(seen.headers["X-API-Key"]).toBe("ak_runtime:anchat-test");
    expect(seen.headers["Authorization"]).toBe("Bearer eyJhbGciOi.wallet.jwt");
  });

  it("rqlite/pubsub/cache stay api-key-only (JWT intentionally NOT attached)", async () => {
    for (const path of ["/v1/rqlite/query", "/v1/pubsub/publish", "/v1/cache/get"]) {
      const { client, seen } = captureHeaders();
      await client.post(path, {});
      expect(seen.headers["X-API-Key"]).toBe("ak_runtime:anchat-test");
      expect(seen.headers["Authorization"]).toBeUndefined();
    }
  });
});
