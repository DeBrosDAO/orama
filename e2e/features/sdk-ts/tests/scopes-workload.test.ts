// The SDK's scope constants against the live gateway, and the workload
// client's refusals outside a deployment.
import { KEY_PROFILES, PROFILE_SCOPES, SDKError, createWorkloadClient, isScope } from "../../../../sdk/src/index";
import { http, need, nsURL, onFleet } from "./fleet";

interface MintedKey {
  id: number;
  api_key: string;
  scopes: string;
}

const keysPath = "/v1/namespace/keys";

function scopeSet(stored: string): string[] {
  return stored.split(/[\s,]+/).filter(Boolean).sort();
}

describe.skipIf(!onFleet)("sdk scopes and workload", () => {
  it("every key profile the SDK lists is one the gateway mints, granting exactly the SDK's scopes", async () => {
    const owner = http(nsURL(), need("GATEWAY_JWT"));
    for (const profile of KEY_PROFILES) {
      const key = await owner.post<MintedKey>(keysPath, { scope: profile, label: `e2e-sdk-${profile}` });
      try {
        expect(key.api_key).toBeTruthy();
        expect(scopeSet(key.scopes)).toEqual([...PROFILE_SCOPES[profile]].sort());
        for (const s of scopeSet(key.scopes)) expect(isScope(s)).toBe(true);
      } finally {
        await owner.request("DELETE", `${keysPath}/${key.id}`);
      }
    }
  });

  it("a profile the SDK does not list is refused by the gateway", async () => {
    const owner = http(nsURL(), need("GATEWAY_JWT"));
    await expect(owner.post(keysPath, { scope: "e2e-not-a-profile", label: "e2e" })).rejects.toMatchObject({
      httpStatus: 400,
    });
  });

  it("the workload client refuses to start outside a deployment", async () => {
    await expect(createWorkloadClient({ baseURL: undefined, tokenFile: undefined })).rejects.toMatchObject({
      code: "WORKLOAD_NO_GATEWAY",
    });
    await expect(createWorkloadClient({ baseURL: nsURL() })).rejects.toBeInstanceOf(SDKError);
  });

  it("a workload token file with no token in it is refused, and a forged one cannot reach the gateway", async () => {
    await expect(
      createWorkloadClient({ baseURL: nsURL(), tokenFile: "/token", readFile: async () => "" })
    ).rejects.toBeInstanceOf(SDKError);
    const client = await createWorkloadClient({
      baseURL: nsURL(),
      tokenFile: "/token",
      readFile: async () => "e2e.forged.workload-token",
    }).catch((e) => e);
    if (client instanceof Error) {
      expect(client).toBeInstanceOf(SDKError);
      return;
    }
    const who = await client.auth.whoami();
    expect(who.authenticated).toBe(false);
  });
});
