// What the Go runner (e2e/features/sdk-ts/runner_test.go) hands the
// TypeScript suite, and the clients built from it. The suite runs only under
// that runner: it sets E2E_FLEET=1 and every variable read here.
//
// vitest runs with `globals: true` from the SDK's own node_modules, so these
// files import nothing from vitest: describe/it/expect are globals.
import { createClient, HttpClient, MemoryStorage } from "../../../../sdk/src/index";
import type { Client, ClientConfig } from "../../../../sdk/src/index";

export const onFleet = process.env.E2E_FLEET === "1";

/** A variable the runner must have set. */
export function need(name: string): string {
  const v = process.env[name];
  if (!v) {
    throw new Error(`${name} is not set: this suite runs only under e2e/features/sdk-ts`);
  }
  return v;
}

/** The namespace gateway, https://ns-<name>.<base>. */
export const nsURL = () => need("GATEWAY_BASE_URL");
/** The public gateway, where the lobby signs in. */
export const mainURL = () => need("E2E_MAIN_GATEWAY_URL");
export const namespace = () => need("E2E_NAMESPACE");

/** A client for the namespace gateway with the given credentials. */
export function nsClient(extra: Partial<ClientConfig> = {}): Client {
  return createClient({
    baseURL: nsURL(),
    functionsConfig: { namespace: namespace() },
    storage: new MemoryStorage(),
    ...extra,
  });
}

/** The namespace owner's session. */
export const ownerClient = () => nsClient({ jwt: need("GATEWAY_JWT") });

/** A bare HTTP client for routes the SDK has no method for. */
export function http(baseURL: string, jwt?: string): HttpClient {
  const c = new HttpClient({ baseURL, maxRetries: 0 });
  if (jwt) {
    c.setJwt(jwt);
  }
  return c;
}

/** A unique, identifier-safe name for tables and topics. */
export function unique(prefix: string): string {
  return `${prefix}_${Date.now()}_${Math.random().toString(36).slice(2, 8)}`;
}

/** Wait until check() is true, polling every intervalMs, for at most timeoutMs. */
export async function until(what: string, check: () => boolean, timeoutMs: number, intervalMs = 250): Promise<void> {
  const deadline = Date.now() + timeoutMs;
  while (!check()) {
    if (Date.now() > deadline) {
      throw new Error(`timed out after ${timeoutMs}ms waiting for ${what}`);
    }
    await new Promise((resolve) => setTimeout(resolve, intervalMs));
  }
}
