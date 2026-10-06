// Sessions through the SDK against the live fleet: verify, whoami, refresh
// (rotating), refresh-on-401 replaying the request, logout, and renew being
// for workloads only (docs/TS_SDK.md, docs/AUTH.md).
import { createClient, MemoryStorage, SDKError } from "../../../../sdk/src/index";
import { mainURL, need, nsClient, onFleet } from "./fleet";

describe.skipIf(!onFleet)("sdk auth sessions", () => {
  it("verify signs the lobby wallet in and whoami answers for it, refresh rotates, logout ends it", async () => {
    const storage = new MemoryStorage();
    const client = createClient({ baseURL: mainURL(), storage });
    const session = await client.auth.verify({
      message: need("E2E_SIWE_MESSAGE"),
      signature: need("E2E_SIWE_SIGNATURE"),
    });
    expect(session.access_token).toBeTruthy();
    expect(session.refresh_token).toBeTruthy();
    expect(session.namespace).toBe("default");
    expect((await storage.get("refreshToken")) ?? "").toBe(session.refresh_token);

    const who = await client.auth.whoami();
    expect(who.authenticated).toBe(true);
    expect(who.subject?.toLowerCase()).toBe(need("E2E_SIWE_WALLET").toLowerCase());

    const renewed = await client.auth.refresh();
    expect(renewed).toBeTruthy();
    expect(renewed).not.toBe(session.access_token);
    expect(client.auth.getToken()).toBe(renewed);
    expect(await storage.get("refreshToken")).not.toBe(session.refresh_token);

    await client.auth.logout();
    expect(client.auth.getToken()).toBeUndefined();
    const stale = createClient({ baseURL: mainURL(), jwt: renewed });
    const after = await stale.auth.whoami();
    expect(after.authenticated).toBe(false);
  });

  it("a replayed nonce is refused", async () => {
    const client = createClient({ baseURL: mainURL() });
    const err = await client.auth
      .verify({ message: need("E2E_SIWE_MESSAGE"), signature: need("E2E_SIWE_SIGNATURE") })
      .catch((e) => e);
    expect(err).toBeInstanceOf(SDKError);
    expect([400, 401]).toContain((err as SDKError).httpStatus);
  });

  it("a 401 renews the session with the stored refresh token and replays the request", async () => {
    const storage = new MemoryStorage();
    await storage.set("refreshToken", need("E2E_MEMBER_REFRESH"));
    await storage.set("namespace", need("E2E_NAMESPACE"));
    const client = nsClient({ jwt: "e2e.expired.token", storage });
    const who = await client.auth.whoami();
    expect(who.authenticated).toBe(true);
    expect(client.auth.getToken()).not.toBe("e2e.expired.token");
    expect((await storage.get("refreshToken")) ?? "").not.toBe("");
  });

  it("refresh without a stored refresh token fails loudly, not with undefined", async () => {
    const client = nsClient({ jwt: need("GATEWAY_JWT") });
    await expect(client.auth.refresh()).rejects.toThrow(/no refresh token/);
  });

  it("renew is for a workload's token: a user session cannot mint its own successor", async () => {
    const client = nsClient({ jwt: need("GATEWAY_JWT") });
    const err = await client.auth.renew().catch((e) => e);
    expect(err).toBeInstanceOf(SDKError);
    expect([401, 403]).toContain((err as SDKError).httpStatus);
  });

  it("listDevices answers an array for a session bound to no device", async () => {
    const client = nsClient({ jwt: need("GATEWAY_JWT") });
    const devices = await client.auth.listDevices();
    expect(Array.isArray(devices)).toBe(true);
  });

  it("whoami with no credential is an answer, not an error", async () => {
    const client = nsClient();
    const who = await client.auth.whoami();
    expect(who.authenticated).toBe(false);
  });
});
