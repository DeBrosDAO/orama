import { createClient } from "../../src/index";
import { SDKError } from "../../src/errors";

export function getGatewayUrl(): string {
  return process.env.GATEWAY_BASE_URL || "http://localhost:6001";
}

export function getApiKey(): string | undefined {
  return process.env.GATEWAY_API_KEY;
}

export function getJwt(): string | undefined {
  return process.env.GATEWAY_JWT;
}

export function skipIfNoGateway() {
  const url = getGatewayUrl();
  const apiKey = getApiKey();

  if (!apiKey) {
    console.log("Skipping: GATEWAY_API_KEY not set");
    return true;
  }

  return false;
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
