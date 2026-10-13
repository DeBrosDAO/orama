// Device linking through the SDK: a device with no wallet starts a link for
// the namespace and, until one of the account's devices approves it, the
// claim is pending. Every device action needs the platform's key.
import { generateKeyPairSync, sign } from "node:crypto";
import { SDKError, deviceIdOf, normalizeUserCode } from "../../../../sdk/src/index";
import type { DeviceSigner } from "../../../../sdk/src/index";
import { namespace, nsClient, onFleet } from "./fleet";

function ed25519Signer(): DeviceSigner {
  const { publicKey, privateKey } = generateKeyPairSync("ed25519");
  const jwk = publicKey.export({ format: "jwk" }) as Record<string, string>;
  return {
    publicJwk: { kty: jwk.kty, crv: jwk.crv, x: jwk.x },
    sign: async (message: string) => sign(null, Buffer.from(message, "utf8"), privateKey).toString("base64url"),
  };
}

describe.skipIf(!onFleet)("sdk device link", () => {
  it("starting a link returns codes for this device, and the claim is pending until approved", async () => {
    const signer = ed25519Signer();
    const client = nsClient();
    client.auth.setDeviceSigner(signer);
    const link = await client.auth.startDeviceLink({ namespace: namespace(), label: "e2e sdk device" });
    expect(link.device_id).toBe(await deviceIdOf(signer.publicJwk));
    expect(normalizeUserCode(link.user_code)).toBe(link.user_code);
    expect(link.device_code).toBeTruthy();
    expect(link.expires_in).toBeGreaterThan(0);
    const claim = client.auth.claimDeviceLink(link.device_code, namespace());
    await expect(claim).rejects.toBeInstanceOf(SDKError);
    // RFC 8628: {"error":"authorization_pending", "error_description": ...}.
    await expect(claim).rejects.toThrow(/authorization_pending/);
  });

  it("device actions without a signer fail locally with DEVICE_PROOF_REQUIRED", async () => {
    const client = nsClient();
    await expect(client.auth.startDeviceLink({ namespace: namespace() })).rejects.toMatchObject({
      code: "DEVICE_PROOF_REQUIRED",
    });
  });

  it("a malformed user code is refused before any request", () => {
    expect(() => normalizeUserCode("abc")).toThrow(/8 characters/);
  });
});
