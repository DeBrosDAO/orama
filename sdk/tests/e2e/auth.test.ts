import { describe, it, expect, beforeAll } from "vitest";
import { createTestClient, skipIfNoGateway } from "./setup";

describe("Auth", () => {
  beforeAll(() => {
    if (skipIfNoGateway()) {
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
    const client = await createTestClient();

    // Set API key
    const apiKey = process.env.GATEWAY_API_KEY;
    if (apiKey) {
      client.auth.setApiKey(apiKey);
      expect(client.auth.getToken()).toBe(apiKey);
    }

    // Set JWT (even if invalid, should update the token)
    const testJwt = "test-jwt-token";
    client.auth.setJwt(testJwt);
    expect(client.auth.getToken()).toBe(testJwt);
  });

  it("should handle logout", async () => {
    const client = await createTestClient();
    await client.auth.logout();
    expect(client.auth.getToken()).toBeUndefined();
  });
});
