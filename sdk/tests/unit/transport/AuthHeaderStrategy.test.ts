import { describe, it, expect } from "vitest";
import { PathBasedAuthStrategy } from "../../../src/core/transport/AuthHeaderStrategy";

const KEY = "ak_test:anchat-test";
const JWT = "eyJhbGciOi.jwt.sig";

function headers(path: string, apiKey = KEY, jwt = JWT) {
  return new PathBasedAuthStrategy(apiKey, jwt).getHeaders({ path, method: "POST" });
}

describe("PathBasedAuthStrategy", () => {
  // Regression (bugboard #148): the gateway enforces a per-user JWT on
  // /v1/proxy/* (layer-1). The SDK MUST send the Bearer JWT alongside the API
  // key or the request is rejected 401 "requires a logged-in user".
  it("sends BOTH api key and Bearer JWT on /v1/proxy/anon", () => {
    const h = headers("/v1/proxy/anon");
    expect(h["X-API-Key"]).toBe(KEY);
    expect(h["Authorization"]).toBe(`Bearer ${JWT}`);
  });

  it("proxy with no JWT set sends only the api key (degrades cleanly)", () => {
    const h = new PathBasedAuthStrategy(KEY, undefined).getHeaders({
      path: "/v1/proxy/anon",
      method: "POST",
    });
    expect(h["X-API-Key"]).toBe(KEY);
    expect(h["Authorization"]).toBeUndefined();
  });

  // Data-plane paths not explicitly ruled fall to the default "both", so the
  // wallet JWT already rides along (layer-1 satisfied).
  it.each(["/v1/storage/upload", "/v1/push/devices", "/v1/webrtc/signal"])(
    "sends both api key and Bearer JWT on %s (default both)",
    (path) => {
      const h = headers(path);
      expect(h["X-API-Key"]).toBe(KEY);
      expect(h["Authorization"]).toBe(`Bearer ${JWT}`);
    }
  );

  // Non-layer-1 namespace paths remain api-key-only (no JWT needed/attached).
  it.each(["/v1/rqlite/query", "/v1/pubsub/publish", "/v1/cache/get"])(
    "sends only the api key on %s (api-key-only)",
    (path) => {
      const h = headers(path);
      expect(h["X-API-Key"]).toBe(KEY);
      expect(h["Authorization"]).toBeUndefined();
    }
  );
});
