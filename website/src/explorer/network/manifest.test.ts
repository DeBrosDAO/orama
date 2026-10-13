import { describe, expect, it } from "vitest";
import { DEFAULT_GATEWAY_ORIGIN } from "../data/chain/client";
import { ManifestError, gatewayOrigin, isCreated, parseManifest } from "./manifest";

const SHA = "84ee8d46628343b85c464185e1009974beb34f91c99477a75a5dfb66a5918b67";

/** The shape networks/stagenet/manifest.json has, with the fields the explorer ignores. */
const stagenet = {
  name: "stagenet",
  chain_id: "orama-stagenet-7",
  genesis_sha256: SHA,
  seeds: ["ns1.stagenet.orama.network", "ns2.stagenet.orama.network", "ns3.stagenet.orama.network"],
  channel: "nightly",
  min_version: "0.3.0",
  release_repo: "https://test-releases.orama.network",
  release_root_sha256: SHA,
  faucet: true,
};

describe("parseManifest", () => {
  it("TestParseManifest_reads_the_fields_the_explorer_uses", () => {
    expect(parseManifest(stagenet)).toEqual({
      name: "stagenet",
      chainId: "orama-stagenet-7",
      genesisSha256: SHA,
      seeds: stagenet.seeds,
    });
  });

  it("TestParseManifest_an_announcement_has_no_genesis_and_is_not_created", () => {
    const m = parseManifest({ ...stagenet, name: "futurenet", genesis_sha256: "", seeds: [] });
    expect(isCreated(m)).toBe(false);
    expect(isCreated(parseManifest(stagenet))).toBe(true);
  });

  it.each([
    ["not an object", null],
    ["an array", []],
    ["a name with capitals", { ...stagenet, name: "Stagenet" }],
    ["a name that starts with a digit", { ...stagenet, name: "1net" }],
    ["a chain id with a slash", { ...stagenet, chain_id: "orama/7" }],
    ["a chain id that ends with a dash", { ...stagenet, chain_id: "orama-" }],
    ["a genesis digest in upper case", { ...stagenet, genesis_sha256: SHA.toUpperCase() }],
    ["a short genesis digest", { ...stagenet, genesis_sha256: "abc" }],
    ["seeds that are not a list", { ...stagenet, seeds: "ns1.stagenet.orama.network" }],
    ["a seed that is an address with a scheme", { ...stagenet, seeds: ["https://ns1.stagenet.orama.network"] }],
    ["a seed that is one label", { ...stagenet, seeds: ["localhost"] }],
    ["a name that is missing", { ...stagenet, name: undefined }],
  ])("TestParseManifest_refuses_%s", (_what, raw) => {
    expect(() => parseManifest(raw)).toThrow(ManifestError);
  });
});

describe("gatewayOrigin", () => {
  it("TestGatewayOrigin_stagenet_is_its_public_gateway", () => {
    expect(gatewayOrigin(parseManifest(stagenet))).toBe("https://stagenet.orama.network");
    expect(gatewayOrigin(parseManifest(stagenet))).toBe(DEFAULT_GATEWAY_ORIGIN);
  });

  it("TestGatewayOrigin_nameservers_of_two_domains_do_not_make_a_domain_so_the_first_seed_is_the_gateway", () => {
    const m = parseManifest({ ...stagenet, seeds: ["ns1.alpha.example.org", "ns2.beta.example.org"] });
    expect(gatewayOrigin(m)).toBe("https://ns1.alpha.example.org");
  });

  it("TestGatewayOrigin_a_seed_that_is_not_a_nameserver_is_a_gateway_itself", () => {
    const m = parseManifest({ ...stagenet, seeds: ["gw.example.org", "gw2.example.org"] });
    expect(gatewayOrigin(m)).toBe("https://gw.example.org");
  });

  it("TestGatewayOrigin_one_nameserver_seed_names_its_domain", () => {
    expect(gatewayOrigin(parseManifest({ ...stagenet, seeds: ["ns1.testnet.orama.network"] }))).toBe("https://testnet.orama.network");
  });

  it("TestGatewayOrigin_no_seed_names_no_gateway", () => {
    expect(() => gatewayOrigin(parseManifest({ ...stagenet, seeds: [] }))).toThrow(ManifestError);
  });
});
