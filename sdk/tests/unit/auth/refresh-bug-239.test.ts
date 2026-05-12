import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { AuthClient } from "../../../src/auth/client";
import { HttpClient } from "../../../src/core/http";
import type { StorageAdapter } from "../../../src/auth/types";

/**
 * Bug #239 regression guard. Two pre-existing defects in
 * AuthClient.refresh() were demonstrated by this file in its pre-fix form
 * (the call carried no body and read the wrong response field, silently
 * corrupting the in-memory JWT to undefined). Both have been fixed and
 * the assertions below now lock in the correct behavior so the bug can't
 * silently come back.
 */
describe("Bug #239 — AuthClient.refresh() regression guard", () => {
  let fetchSpy: ReturnType<typeof vi.fn>;
  let storage: StorageAdapter;
  let memStore: Map<string, string>;

  function setupGoodResponse() {
    fetchSpy = vi.fn(async (_input: any, _init?: RequestInit) => {
      return new Response(
        // Server response shape matches
        // core/pkg/gateway/handlers/auth/jwt_handler.go:106-113.
        JSON.stringify({
          access_token: "NEW-JWT-FROM-SERVER",
          token_type: "Bearer",
          expires_in: 900,
          refresh_token: "rotated-refresh",
          subject: "0xabc",
          namespace: "anchat-test",
        }),
        { status: 200, headers: { "Content-Type": "application/json" } }
      );
    });
    vi.stubGlobal("fetch", fetchSpy);
  }

  beforeEach(() => {
    memStore = new Map<string, string>();
    storage = {
      get: async (k: string) => memStore.get(k),
      set: async (k: string, v: string) => {
        memStore.set(k, v);
      },
      delete: async (k: string) => {
        memStore.delete(k);
      },
      clear: async () => {
        memStore.clear();
      },
    };
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("sends { refresh_token, namespace } in the request body", async () => {
    setupGoodResponse();
    await storage.set("refreshToken", "stored-refresh-tok");
    await storage.set("namespace", "anchat-test");

    const http = new HttpClient({ baseURL: "https://example.invalid" });
    const auth = new AuthClient({ httpClient: http, storage });

    await auth.refresh();

    expect(fetchSpy).toHaveBeenCalledOnce();
    const init = fetchSpy.mock.calls[0][1] as RequestInit;
    expect(init?.body, "refresh() must send a JSON body").toBeDefined();

    const sentBody = JSON.parse(init!.body as string);
    expect(sentBody).toEqual({
      refresh_token: "stored-refresh-tok",
      namespace: "anchat-test",
    });
  });

  it("reads access_token from the response and propagates it as the new JWT", async () => {
    setupGoodResponse();
    await storage.set("refreshToken", "stored-refresh-tok");
    await storage.set("namespace", "anchat-test");

    const http = new HttpClient({ baseURL: "https://example.invalid" });
    const auth = new AuthClient({ httpClient: http, storage });

    const returned = await auth.refresh();

    expect(returned).toBe("NEW-JWT-FROM-SERVER");
    expect(auth.getToken()).toBe("NEW-JWT-FROM-SERVER");
  });

  it("rotates the stored refresh token when the server returns a new one", async () => {
    setupGoodResponse();
    await storage.set("refreshToken", "old-refresh");
    await storage.set("namespace", "anchat-test");

    const http = new HttpClient({ baseURL: "https://example.invalid" });
    const auth = new AuthClient({ httpClient: http, storage });

    await auth.refresh();

    expect(await storage.get("refreshToken")).toBe("rotated-refresh");
  });

  it("falls back to the 'default' namespace when none stored", async () => {
    setupGoodResponse();
    await storage.set("refreshToken", "stored-refresh-tok");
    // No namespace set.

    const http = new HttpClient({ baseURL: "https://example.invalid" });
    const auth = new AuthClient({ httpClient: http, storage });

    await auth.refresh();

    const sentBody = JSON.parse(
      (fetchSpy.mock.calls[0][1] as RequestInit)!.body as string
    );
    expect(sentBody.namespace).toBe("default");
  });

  it("throws (not silently undefined-ing the JWT) when no refresh token is stored", async () => {
    // No refresh token in storage. Server should never be called.
    fetchSpy = vi.fn();
    vi.stubGlobal("fetch", fetchSpy);

    const http = new HttpClient({ baseURL: "https://example.invalid" });
    const auth = new AuthClient({ httpClient: http, storage });

    await expect(auth.refresh()).rejects.toThrow(/no refresh token/i);
    expect(fetchSpy).not.toHaveBeenCalled();
    expect(auth.getToken()).toBeUndefined();
  });

  it("throws if the server response is missing access_token", async () => {
    // Server returns 200 but with malformed body (no access_token).
    fetchSpy = vi.fn(async () => {
      return new Response(JSON.stringify({ token_type: "Bearer" }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      });
    });
    vi.stubGlobal("fetch", fetchSpy);
    await storage.set("refreshToken", "stored-refresh-tok");

    const http = new HttpClient({ baseURL: "https://example.invalid" });
    const auth = new AuthClient({ httpClient: http, storage });

    await expect(auth.refresh()).rejects.toThrow(/no access_token/i);
    // In-memory JWT must NOT be set to undefined — pre-fix this is what
    // corrupted the auth state.
    expect(auth.getToken()).toBeUndefined();
  });
});
