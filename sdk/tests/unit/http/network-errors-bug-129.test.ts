import { describe, it, expect, vi } from "vitest";
import { HttpClient } from "../../../src/core/http";
import { SDKError } from "../../../src/errors";

/**
 * Bugboard #129 — typed network errors.
 *
 * Before this fix the HttpClient re-threw the raw platform error on a
 * network-level failure, so a caller (e.g. AnChat's JwtSession) could only
 * tell "couldn't reach the gateway" apart from a real HTTP error by
 * string-matching `TypeError: Network request failed`. These guards lock in
 * that every transport failure surfaces as a typed SDKError with httpStatus 0
 * and a stable `.code`, while genuine HTTP errors keep their status/code.
 */
describe("Bug #129 — HttpClient surfaces typed network errors", () => {
  function client(fetchImpl: typeof fetch, onNetworkError?: any) {
    return new HttpClient({
      baseURL: "https://gw.example",
      maxRetries: 0,
      timeout: 5000,
      fetch: fetchImpl,
      onNetworkError,
    });
  }

  it("maps a fetch TypeError (connection failure) to SDKError NETWORK_ERROR / status 0", async () => {
    const fetchSpy = vi.fn(async () => {
      throw new TypeError("Network request failed");
    });
    const err = await client(fetchSpy as any)
      .post("/v1/auth/refresh", { refresh_token: "x" })
      .catch((e) => e);

    expect(err).toBeInstanceOf(SDKError);
    expect(err.code).toBe("NETWORK_ERROR");
    expect(err.httpStatus).toBe(0);
    // Original platform message is preserved for diagnostics.
    expect(err.message).toContain("Network request failed");
  });

  it("maps an AbortError (timeout) to SDKError TIMEOUT / status 0", async () => {
    const fetchSpy = vi.fn(async () => {
      const e = new Error("aborted");
      e.name = "AbortError";
      throw e;
    });
    const err = await client(fetchSpy as any)
      .get("/v1/auth/challenge")
      .catch((e) => e);

    expect(err).toBeInstanceOf(SDKError);
    expect(err.code).toBe("TIMEOUT");
    expect(err.httpStatus).toBe(0);
    expect(err.message).toContain("5000ms");
  });

  it("passes a real HTTP error through unchanged (not masked as NETWORK_ERROR)", async () => {
    const fetchSpy = vi.fn(
      async () =>
        new Response(JSON.stringify({ error: "nope", code: "BAD_TOKEN" }), {
          status: 401,
          headers: { "content-type": "application/json" },
        })
    );
    const err = await client(fetchSpy as any)
      .post("/v1/auth/refresh", { refresh_token: "x" })
      .catch((e) => e);

    expect(err).toBeInstanceOf(SDKError);
    expect(err.httpStatus).toBe(401);
    expect(err.code).toBe("BAD_TOKEN");
  });

  it("hands the typed error (not the raw TypeError) to the onNetworkError callback", async () => {
    const seen: SDKError[] = [];
    const fetchSpy = vi.fn(async () => {
      throw new TypeError("Failed to fetch");
    });
    await client(fetchSpy as any, (e: SDKError) => seen.push(e))
      .get("/v1/db/read")
      .catch(() => {});

    expect(seen).toHaveLength(1);
    expect(seen[0]).toBeInstanceOf(SDKError);
    expect(seen[0].code).toBe("NETWORK_ERROR");
    expect(seen[0].httpStatus).toBe(0);
  });
});
