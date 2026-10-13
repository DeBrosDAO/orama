import { describe, it, expect, vi, afterEach } from "vitest";
import { HttpClient } from "../../../src/core/http";
import { createClient } from "../../../src/index";

/**
 * Bugboard #325 — the SDK must never change the process's TLS settings.
 *
 * It used to set `NODE_TLS_REJECT_UNAUTHORIZED = "0"` whenever `NODE_ENV` was
 * anything other than "production", which is the default in most apps. That
 * disabled certificate verification for *every* HTTPS client in the importing
 * process, not just for calls to Orama, and it happened merely on constructing
 * a client.
 *
 * Reaching a gateway with an untrusted certificate is done by injecting a
 * `fetch` that relaxes verification for that connection alone.
 */
describe("Bug #325 — the SDK leaves process TLS settings alone", () => {
  const originalReject = process.env.NODE_TLS_REJECT_UNAUTHORIZED;
  const originalNodeEnv = process.env.NODE_ENV;

  afterEach(() => {
    restore("NODE_TLS_REJECT_UNAUTHORIZED", originalReject);
    restore("NODE_ENV", originalNodeEnv);
  });

  function restore(key: string, value: string | undefined) {
    if (value === undefined) {
      delete process.env[key];
    } else {
      process.env[key] = value;
    }
  }

  it("does not disable certificate verification when NODE_ENV is unset", () => {
    delete process.env.NODE_ENV;
    delete process.env.NODE_TLS_REJECT_UNAUTHORIZED;

    createClient({ baseURL: "https://gw.example", apiKey: "ak_test:default" });

    expect(process.env.NODE_TLS_REJECT_UNAUTHORIZED).toBeUndefined();
  });

  it("does not disable certificate verification in development", () => {
    process.env.NODE_ENV = "development";
    delete process.env.NODE_TLS_REJECT_UNAUTHORIZED;

    new HttpClient({ baseURL: "https://gw.example" });

    expect(process.env.NODE_TLS_REJECT_UNAUTHORIZED).toBeUndefined();
  });

  it("leaves an existing value exactly as the host application set it", () => {
    process.env.NODE_ENV = "development";
    process.env.NODE_TLS_REJECT_UNAUTHORIZED = "1";

    createClient({ baseURL: "https://gw.example", apiKey: "ak_test:default" });

    expect(process.env.NODE_TLS_REJECT_UNAUTHORIZED).toBe("1");
  });

  it("uses the caller's fetch, which is how an untrusted certificate is handled", async () => {
    const injected = vi.fn(async () =>
      new Response(JSON.stringify({ ok: true }), {
        status: 200,
        headers: { "content-type": "application/json" },
      })
    );

    const client = new HttpClient({
      baseURL: "https://gw.example",
      fetch: injected as unknown as typeof fetch,
    });
    await client.get("/v1/health");

    expect(injected).toHaveBeenCalledOnce();
  });
});
