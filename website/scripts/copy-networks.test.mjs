import { createHash } from "node:crypto";
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { afterAll, describe, expect, it } from "vitest";
import { copyNetworks, verifyNetworkDir } from "./copy-networks.mjs";

const root = mkdtempSync(join(tmpdir(), "copy-networks-"));
afterAll(() => rmSync(root, { recursive: true, force: true }));

const sha = (text) => createHash("sha256").update(text).digest("hex");

/** Writes networks/<name>/ under a fresh source dir; `genesis` is optional. */
function makeSource(label, { name = "teststage", genesis = '{"chain_id":"x"}', rootText = '{"signed":1}', genesisSha = sha('{"chain_id":"x"}') } = {}) {
  const source = join(root, label, "networks");
  mkdirSync(join(source, name), { recursive: true });
  const manifest = {
    name,
    chain_id: "x",
    genesis_sha256: genesisSha,
    release_root_sha256: sha(rootText),
  };
  writeFileSync(join(source, name, "manifest.json"), JSON.stringify(manifest));
  writeFileSync(join(source, name, "release-root.json"), rootText);
  if (genesis !== null) writeFileSync(join(source, name, "genesis.json"), genesis);
  return { source, dist: join(root, label, "dist") };
}

describe("copyNetworks", () => {
  it("TestCopyNetworks_copiesEveryFile", () => {
    const { source, dist } = makeSource("ok");
    expect(copyNetworks(source, dist)).toEqual(["teststage"]);
    expect(readFileSync(join(dist, "networks", "teststage", "genesis.json"), "utf8")).toBe('{"chain_id":"x"}');
  });

  it("TestCopyNetworks_aManifestWithoutAGenesisYet", () => {
    const { source, dist } = makeSource("nogenesis", { genesis: null });
    expect(copyNetworks(source, dist)).toEqual(["teststage"]);
  });

  it("TestCopyNetworks_anAnnouncedNetworkHasNoGenesis", () => {
    const { source, dist } = makeSource("announced", { genesis: null, genesisSha: "" });
    expect(copyNetworks(source, dist)).toEqual(["teststage"]);
  });

  it("TestCopyNetworks_noNetworksIsNotAnError", () => {
    const source = join(root, "empty", "networks");
    mkdirSync(source, { recursive: true });
    expect(copyNetworks(source, join(root, "empty", "dist"))).toEqual([]);
  });

  it("TestCopyNetworks_staleCopyIsReplaced", () => {
    const { source, dist } = makeSource("stale");
    mkdirSync(join(dist, "networks", "gone"), { recursive: true });
    writeFileSync(join(dist, "networks", "gone", "manifest.json"), "old");
    copyNetworks(source, dist);
    expect(() => readFileSync(join(dist, "networks", "gone", "manifest.json"))).toThrow();
  });
});

describe("verifyNetworkDir", () => {
  it("TestVerifyNetworkDir_rootThatIsNotPinned", () => {
    const { source } = makeSource("badroot");
    writeFileSync(join(source, "teststage", "release-root.json"), "tampered");
    expect(() => verifyNetworkDir(join(source, "teststage"))).toThrow(/release_root_sha256/);
  });

  it("TestVerifyNetworkDir_genesisThatIsNotPinned", () => {
    const { source } = makeSource("badgenesis", { genesis: '{"chain_id":"y"}' });
    expect(() => verifyNetworkDir(join(source, "teststage"))).toThrow(/genesis_sha256/);
  });

  it("TestVerifyNetworkDir_anAnnouncementThatCarriesAGenesis", () => {
    const { source } = makeSource("announcedgenesis", { genesisSha: "" });
    expect(() => verifyNetworkDir(join(source, "teststage"))).toThrow(/genesis_sha256/);
  });

  it("TestVerifyNetworkDir_missingRootAndStrayFiles", () => {
    const { source } = makeSource("missing");
    writeFileSync(join(source, "teststage", "notes.txt"), "x");
    expect(() => verifyNetworkDir(join(source, "teststage"))).toThrow(/unexpected files notes.txt/);
    rmSync(join(source, "teststage", "release-root.json"));
    expect(() => verifyNetworkDir(join(source, "teststage"))).toThrow(/needs manifest.json and release-root.json/);
  });
});
