import { describe, it, expect } from "vitest";
import { createTestClient, hasGateway } from "./setup";

describe.skipIf(!hasGateway())("Network", () => {
  it("should check health", async () => {
    const client = await createTestClient();
    const healthy = await client.network.health();
    expect(typeof healthy).toBe("boolean");
  });

  // The peer map is an operator's: the operator grant and a wallet on the
  // cluster's operator list (docs/API_SURFACE.md). A namespace's owner or key
  // is neither on the list nor meant to be, so the suite's credential is
  // refused — a 403, not an answer and not a 503 from a gateway that cannot
  // read the list. An operator's reads are the fleet's network-routes feature.
  it("should refuse network status to a namespace credential", async () => {
    const client = await createTestClient();
    await expect(client.network.status()).rejects.toMatchObject({
      httpStatus: 403,
      code: expect.stringMatching(/^(NOT_AN_OPERATOR|INSUFFICIENT_SCOPE)$/),
    });
  });

  it("should refuse the peer list to a namespace credential", async () => {
    const client = await createTestClient();
    await expect(client.network.peers()).rejects.toMatchObject({
      httpStatus: 403,
      code: expect.stringMatching(/^(NOT_AN_OPERATOR|INSUFFICIENT_SCOPE)$/),
    });
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
