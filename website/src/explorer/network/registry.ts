import { CUSTOM_NETWORK, isPageKind } from "../model/routes";
import { ManifestError, gatewayOrigin, isCreated, parseManifest } from "./manifest";
import type { NetworkManifest } from "./manifest";

/** A network of the registry the explorer can read: its chain exists and a gateway is named. */
export interface RegistryNetwork {
  manifest: NetworkManifest;
  /** The public gateway's https origin. */
  gateway: string;
}

export interface Registry {
  /** Created networks by name. */
  networks: readonly RegistryNetwork[];
  /** The network /explorer shows with no network in its URL. */
  defaultName: string;
}

/** The network a bare /explorer opens, when the registry has it; else the first by name. */
export const PREFERRED_DEFAULT = "stagenet";

const MANIFEST_FILE = /(?:^|\/)([^/]+)\/manifest\.json$/;

/**
 * The registry from the manifests of networks/<name>/manifest.json, keyed by
 * their path. A network that is only announced has no chain to read and is
 * left out. A manifest the explorer cannot read, or whose name differs from its
 * directory, or that is named like a page of the explorer (it would take the
 * place of that page in the URL), throws: the registry is part of the build.
 */
export function buildRegistry(files: Readonly<Record<string, unknown>>): Registry {
  const networks: RegistryNetwork[] = [];
  for (const [path, raw] of Object.entries(files)) {
    const dir = MANIFEST_FILE.exec(path)?.[1];
    if (dir === undefined) throw new ManifestError(`${path}: not a networks/<name>/manifest.json`);
    const manifest = parseManifest(raw, path);
    if (manifest.name !== dir) throw new ManifestError(`${path}: the manifest names the network "${manifest.name}"`);
    if (manifest.name === CUSTOM_NETWORK || isPageKind(manifest.name)) {
      throw new ManifestError(`${path}: "${manifest.name}" is a page of the explorer and cannot name a network`);
    }
    if (!isCreated(manifest)) continue;
    networks.push({ manifest, gateway: gatewayOrigin(manifest) });
  }
  networks.sort((a, b) => a.manifest.name.localeCompare(b.manifest.name));
  const first = networks[0];
  if (first === undefined) throw new ManifestError("the network registry holds no created network");
  const preferred = networks.find((n) => n.manifest.name === PREFERRED_DEFAULT);
  return { networks, defaultName: (preferred ?? first).manifest.name };
}

/** The repository's networks/ directory, which the site build also publishes at /networks/. */
const SHIPPED = import.meta.glob<unknown>("../../../../networks/*/manifest.json", { eager: true, import: "default" });

export const REGISTRY: Registry = buildRegistry(SHIPPED);

export function findNetwork(registry: Registry, name: string): RegistryNetwork | undefined {
  return registry.networks.find((n) => n.manifest.name === name);
}
