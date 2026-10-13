import { chainBase } from "../data/chain/client";

/**
 * A network of the registry (networks/<name>/manifest.json in the repository,
 * served at https://orama.network/networks/<name>/manifest.json; the schema is
 * core/pkg/netregistry/manifest.go). Only what the explorer uses is kept.
 */
export interface NetworkManifest {
  name: string;
  chainId: string;
  /** Empty while the network is announced and its chain does not exist yet. */
  genesisSha256: string;
  seeds: string[];
}

const NAME = /^[a-z][a-z0-9-]{0,31}$/;
const CHAIN_ID = /^[a-z0-9]([a-z0-9-]{0,46}[a-z0-9])?$/;
const SHA256_HEX = /^[0-9a-f]{64}$/;
const DNS_LABEL = /^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$/;
const MAX_SEEDS = 64;
/** A seed is ns<N>.<domain>: the cluster's nameservers, on the domain whose gateway the cluster runs. */
const NAMESERVER_SEED = /^ns[1-9][0-9]*\.(.+)$/;

/** A manifest the registry holds that this explorer cannot read. Thrown at load, so a bad one fails the tests rather than a reader's page. */
export class ManifestError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "ManifestError";
  }
}

function field(obj: Record<string, unknown>, key: string, where: string): string {
  const v = obj[key];
  if (typeof v !== "string") throw new ManifestError(`${where}: ${key} is not a string`);
  return v;
}

function isDnsName(s: string): boolean {
  const labels = s.split(".");
  return s.length <= 253 && labels.length >= 2 && labels.every((l) => DNS_LABEL.test(l));
}

/** Reads a manifest the way netregistry.ParseManifest does, for the fields the explorer uses. */
export function parseManifest(raw: unknown, where = "manifest"): NetworkManifest {
  if (raw === null || typeof raw !== "object" || Array.isArray(raw)) throw new ManifestError(`${where}: not an object`);
  const obj = raw as Record<string, unknown>;
  const name = field(obj, "name", where);
  const chainId = field(obj, "chain_id", where);
  const genesisSha256 = field(obj, "genesis_sha256", where);
  if (!NAME.test(name)) throw new ManifestError(`${where}: name "${name}" is not a network name`);
  if (!CHAIN_ID.test(chainId)) throw new ManifestError(`${where}: chain_id "${chainId}" is not a chain id`);
  if (genesisSha256 !== "" && !SHA256_HEX.test(genesisSha256)) {
    throw new ManifestError(`${where}: genesis_sha256 is not 64 lowercase hex characters`);
  }
  const seeds = obj.seeds;
  if (!Array.isArray(seeds) || seeds.length > MAX_SEEDS || !seeds.every((s) => typeof s === "string" && isDnsName(s))) {
    throw new ManifestError(`${where}: seeds is not a list of DNS names`);
  }
  return { name, chainId, genesisSha256, seeds: seeds as string[] };
}

/** True once the network's chain exists: an announcement has no genesis yet, so there is nothing to read. */
export function isCreated(m: NetworkManifest): boolean {
  return m.genesisSha256 !== "";
}

/**
 * The origin of the network's public gateway. The manifest has no field for it:
 * its seeds are what name the network's machines. A created network's seeds are
 * the nameservers ns1..nsN of its cluster's domain (orama setup --create-network),
 * and that domain is where the cluster's gateway answers (setup records
 * https://<domain>) and reaches any node: stagenet's seeds ns1..ns5.stagenet.orama.network
 * give https://stagenet.orama.network. Seeds of another shape are gateways
 * themselves (the CLI asks the faucet at https://<seed>), so the first one is used.
 * The result goes through chainBase: an https origin, or it throws.
 */
export function gatewayOrigin(m: NetworkManifest): string {
  const seed = m.seeds[0];
  if (seed === undefined) throw new ManifestError(`${m.name}: the manifest lists no seed, so it names no gateway`);
  const domains = new Set(m.seeds.map((s) => NAMESERVER_SEED.exec(s)?.[1] ?? null));
  const domain = domains.size === 1 ? [...domains][0] : null;
  const origin = `https://${domain ?? seed}`;
  chainBase(origin);
  return origin;
}
