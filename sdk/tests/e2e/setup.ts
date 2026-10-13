import { createClient } from "../../src/index";

export function getGatewayUrl(): string {
  // The default is a node's index gateway on this machine. It used to be a
  // port no gateway has listened on since the port block moved to 10100.
  return process.env.GATEWAY_BASE_URL || "http://localhost:10104";
}

export function getApiKey(): string | undefined {
  return process.env.GATEWAY_API_KEY;
}

export function getJwt(): string | undefined {
  return process.env.GATEWAY_JWT;
}

/**
 * Whether the end-to-end suite has a gateway to talk to.
 *
 * Use it as `describe.skipIf(!hasGateway())`, not as an `if` inside a describe
 * block. Every e2e file used to do:
 *
 *     describe("Database", () => {
 *       if (skipIfNoGateway()) { console.log("Skipping database tests"); }
 *       it("should create a table", ...)
 *     });
 *
 * which logs a line and then registers and runs every test anyway. That is 27
 * of the 30 failures in a checkout with no gateway, and QUICKSTART.md said the
 * suite skips gracefully.
 */
export function hasGateway(): boolean {
  return Boolean(getApiKey());
}

export async function createTestClient() {
  const client = createClient({
    baseURL: getGatewayUrl(),
    apiKey: getApiKey(),
    jwt: getJwt(),
  });

  return client;
}

export function generateTableName(): string {
  return `test_${Date.now()}_${Math.random().toString(36).substring(7)}`;
}

export async function delay(ms: number) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

export async function isGatewayReady(): Promise<boolean> {
  try {
    const client = await createTestClient();
    const healthy = await client.network.health();
    return healthy;
  } catch {
    return false;
  }
}
