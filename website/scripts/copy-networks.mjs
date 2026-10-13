import { createHash } from "node:crypto";
import { cpSync, existsSync, mkdirSync, readdirSync, readFileSync, rmSync, statSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

// The repository's networks/ directory is what the orama CLI embeds and what a
// joining operator fetches from https://orama.network/networks/<name>/. The site
// serves a copy of it, so a copy that differs from networks/ would put a
// different network on the web than in the binary. This copies it into the built
// site and fails if anything in the copy differs from the source.

const PUBLISHED = ["manifest.json", "release-root.json", "genesis.json", "tor-network.json"];

const sha256 = (file) => createHash("sha256").update(readFileSync(file)).digest("hex");

function listFiles(dir) {
  return readdirSync(dir, { recursive: true, withFileTypes: true })
    .filter((e) => e.isFile())
    .map((e) => join(e.parentPath, e.name).slice(dir.length + 1))
    .sort();
}

/** Throws unless the network directory holds a manifest whose digests match its files. */
export function verifyNetworkDir(dir) {
  const manifestFile = join(dir, "manifest.json");
  const rootFile = join(dir, "release-root.json");
  if (!existsSync(manifestFile) || !existsSync(rootFile)) {
    throw new Error(`${dir}: a network needs manifest.json and release-root.json`);
  }
  const manifest = JSON.parse(readFileSync(manifestFile, "utf8"));
  if (manifest.release_root_sha256 !== sha256(rootFile)) {
    throw new Error(`${dir}: release-root.json does not match release_root_sha256 in the manifest`);
  }
  const genesisFile = join(dir, "genesis.json");
  if (existsSync(genesisFile) && manifest.genesis_sha256 !== sha256(genesisFile)) {
    throw new Error(`${dir}: genesis.json does not match genesis_sha256 in the manifest`);
  }
  const torFile = join(dir, "tor-network.json");
  if (manifest.tor_network_sha256 && !existsSync(torFile)) {
    throw new Error(`${dir}: the manifest pins tor-network.json (tor_network_sha256) but the file is missing`);
  }
  if (manifest.tor_network_sha256 && manifest.tor_network_sha256 !== sha256(torFile)) {
    throw new Error(`${dir}: tor-network.json does not match tor_network_sha256 in the manifest`);
  }
  if (!manifest.tor_network_sha256 && existsSync(torFile)) {
    throw new Error(`${dir}: tor-network.json is there but the manifest does not pin it (tor_network_sha256)`);
  }
  const extra = listFiles(dir).filter((f) => !PUBLISHED.includes(f));
  if (extra.length > 0) {
    throw new Error(`${dir}: unexpected files ${extra.join(", ")}; a network directory holds ${PUBLISHED.join(", ")}`);
  }
}

/**
 * Copies every network under `source` to `<dist>/networks/` and returns the
 * names copied. Throws if a source network is inconsistent or a copy differs.
 */
export function copyNetworks(source, dist) {
  const target = join(dist, "networks");
  rmSync(target, { recursive: true, force: true });
  const names = readdirSync(source, { withFileTypes: true })
    .filter((e) => e.isDirectory())
    .map((e) => e.name)
    .sort();
  for (const name of names) {
    verifyNetworkDir(join(source, name));
    mkdirSync(join(target, name), { recursive: true });
    cpSync(join(source, name), join(target, name), { recursive: true });
  }
  for (const name of names) {
    const want = listFiles(join(source, name));
    const got = listFiles(join(target, name));
    if (want.join("\n") !== got.join("\n")) {
      throw new Error(`networks/${name}: the copy holds [${got}] but the source holds [${want}]`);
    }
    for (const file of want) {
      if (sha256(join(source, name, file)) !== sha256(join(target, name, file))) {
        throw new Error(`networks/${name}/${file}: the copy differs from the source`);
      }
    }
  }
  return names;
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const here = dirname(fileURLToPath(import.meta.url));
  const source = resolve(here, "..", "..", "networks");
  const dist = resolve(here, "..", "dist");
  if (!existsSync(source) || !statSync(source).isDirectory()) {
    throw new Error(`${source} is missing: the repository's networks/ directory is published with the site`);
  }
  const names = copyNetworks(source, dist);
  console.log(`copy-networks: ${names.length} network(s) copied to dist/networks (${names.join(", ") || "none"})`);
}
