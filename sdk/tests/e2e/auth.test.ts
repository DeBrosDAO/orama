import { describe, it, expect, beforeAll } from "vitest";
import { createClient, MemoryStorage } from "../../src/index";
import { createTestClient, getGatewayUrl, hasGateway } from "./setup";

describe.skipIf(!hasGateway())("Auth", () => {
  beforeAll(() => {
    if (hasGateway()) {
      console.log("Skipping auth tests");
    }
  });

  it("should get whoami", async () => {
    const client = await createTestClient();
    const whoami = await client.auth.whoami();
    expect(whoami).toBeDefined();
    expect(whoami.authenticated).toBe(true);
  });

  it("should switch API key and JWT", async () => {
    // A key is never a request's credential: it is exchanged for a token, and
    // the token is what getToken() reports. So a client holding only a key has
    // no token until the exchange, and a JWT set on it is the token at once.
    const apiKey = process.env.GATEWAY_API_KEY;
    const client = createClient({ baseURL: getGatewayUrl(), apiKey });
    if (apiKey) {
      client.auth.setApiKey(apiKey);
      expect(client.auth.getToken()).toBeUndefined();
    }

    // Set JWT (even if invalid, should update the token)
    const testJwt = "test-jwt-token";
    client.auth.setJwt(testJwt);
    expect(client.auth.getToken()).toBe(testJwt);
  });

  it("should handle logout", async () => {
    // Logging out revokes the session it names. The suite's shared credential
    // (GATEWAY_JWT, the namespace owner's) must stay alive for every test
    // after this one, so a run that provides a session of its own for this
    // (E2E_LOGOUT_JWT) ends that; without one there is no session to end.
    const jwt = process.env.E2E_LOGOUT_JWT;
    const storage = new MemoryStorage();
    if (process.env.E2E_LOGOUT_REFRESH) {
      await storage.set("refreshToken", process.env.E2E_LOGOUT_REFRESH);
    }
    const client = createClient({ baseURL: getGatewayUrl(), jwt, storage });
    await client.auth.logout();
    expect(client.auth.getToken()).toBeUndefined();
  });
});
