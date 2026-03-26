import { describe, it, expect, beforeAll } from "vitest";
import { createTestClient, skipIfNoGateway } from "./setup";

describe("Network", () => {
  beforeAll(() => {
    if (skipIfNoGateway()) {
      console.log("Skipping network tests");
    }
  });

  it("should check health", async () => {
    const client = await createTestClient();
    const healthy = await client.network.health();
    expect(typeof healthy).toBe("boolean");
  });

  it("should get network status", async () => {
    const client = await createTestClient();
    const status = await client.network.status();
    expect(status).toBeDefined();
    expect(typeof status.connected).toBe("boolean");
    expect(typeof status.peer_count).toBe("number");
  });

  it("should list peers", async () => {
    const client = await createTestClient();
    const peers = await client.network.peers();
    expect(Array.isArray(peers)).toBe(true);
  });

  it("should proxy request through Anyone network", async () => {
    const client = await createTestClient();

    // Test with a simple GET request
    const response = await client.network.proxyAnon({
      url: "https://httpbin.org/get",
      method: "GET",
      headers: {
        "User-Agent": "DeBros-SDK-Test/1.0",
      },
    });

    expect(response).toBeDefined();
    expect(response.status_code).toBe(200);
    expect(response.body).toBeDefined();
    expect(typeof response.body).toBe("string");
  });

  it("should handle proxy errors gracefully", async () => {
    const client = await createTestClient();

    // Test with invalid URL
    await expect(
      client.network.proxyAnon({
        url: "http://localhost:1/invalid",
        method: "GET",
      })
    ).rejects.toThrow();
  });
});
