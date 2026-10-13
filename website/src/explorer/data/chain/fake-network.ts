import { withQuery } from "./client";
import { addr } from "./fake-gateway";

/** Stand-ins for the answers of the list queries of x/nodes, x/storage, x/archive and x/relay, as the gateway's module-query route writes them (every field present, 64-bit numbers as strings, enums by name). */

const json = (value: unknown) => JSON.stringify(value);

export const NODES_METHOD = "query/orama.nodes.v1.Query/Nodes";
export const DEALS_METHOD = "query/orama.storage.v1.Query/Deals";
export const RELAYS_METHOD = "query/orama.relay.v1.Query/Relays";

/** The path of a list query's page, as listAll builds it. */
export const pagePath = (method: string, key = "", reverse = false) =>
  withQuery(method, { json: json({ pagination: { limit: 100, ...(key === "" ? {} : { key }), ...(reverse ? { reverse: true } : {}) } }) });

export const byId = (method: string, field: string, id: string) => withQuery(method, { json: json({ [field]: id }) });

export const page = (field: string, items: unknown[], nextKey = "") => ({ [field]: items, pagination: { next_key: nextKey, total: "0" } });

export const NODE_PARAMS = {
  params: {
    min_bond: [
      { role: "ROLE_VALIDATOR", amount: "500000000000" },
      { role: "ROLE_STORAGE", amount: "100000000000" },
    ],
    bond_per_gib: "1000000000",
    unbonding_seconds: "86400",
    deposit_per_byte: "1",
    probation_capacity_bytes: "1073741824",
    max_endpoints: 8,
    max_bindings: 8,
    network_identity_lock_seconds: "0",
    name_deposit: "1000000000",
  },
};

export function nodeJson(n: number, over: Record<string, unknown> = {}) {
  return {
    node_id: `node-${n}`,
    operator: addr(n),
    roles: ["ROLE_STORAGE"],
    hot_key: addr(n + 100),
    bindings: [{ service: "ipfs", key_type: "KEY_TYPE_ED25519", pubkey: "AAEC", signature: "AAEC" }],
    endpoints: [`https://node-${n}.example.org:443`],
    region_hint: "eu-1",
    bonds: [{ role: "ROLE_STORAGE", amount: "150000000000" }],
    declared_capacity_bytes: "2147483648",
    reserved_capacity_bytes: "536870912",
    status: "NODE_STATUS_ACTIVE",
    deposit_bytes: "0",
    deposit_parts: 0,
    capacity_indexed: false,
    capacity_class: 0,
    registered_at_height: "5",
    asn: 24940,
    identity_since_unix: "0",
    ...over,
  };
}

export function dealJson(id: number, over: Record<string, unknown> = {}) {
  return {
    id: String(id),
    class: "DEAL_CLASS_PUBLIC_PIN",
    protocol: false,
    client: addr(1),
    granter: "",
    repair_delegate: "",
    deal_nonce: "",
    replicas: 3,
    price_per_epoch: "2000000000",
    duration_epochs: "30",
    start_epoch: "10",
    end_epoch: "40",
    escrow: "60000000000",
    status: "DEAL_STATUS_ACTIVE",
    assign_at_height: "0",
    created_height: "50",
    ...over,
  };
}

export const STORAGE_PARAMS = {
  params: {
    min_deal_bytes: "1024",
    deal_fee: "1000000",
    miss_threshold: 2,
    protocol_every_epochs: "7",
    protocol_piece_bytes: "1048576",
    protocol_price_per_epoch: "500000000",
    protocol_duration_epochs: "365",
  },
};

export function slotJson(dealId: number, index: number, over: Record<string, unknown> = {}) {
  return {
    slot: { deal_id: String(dealId), index, node_id: `node-${index + 1}`, status: "SLOT_STATUS_ACTIVE", consecutive_misses: 0, piece_bytes: "1048576", accepted: true, ...over },
  };
}

export const ARCHIVE_PARAMS = { params: { retention_window_blocks: "1000", max_piece_bytes: "1048576", max_candidates_per_range: 4, range_blocks: "100" } };

export const retainJson = (over: Record<string, unknown> = {}) => ({
  retain_height: "400",
  tip: "1450",
  last_archived_height: "400",
  retention_window_blocks: "1000",
  ...over,
});

export function rangeJson(start: number, end: number, over: Record<string, unknown> = {}) {
  return {
    range: {
      start_height: String(start),
      end_height: String(end),
      bundle_cid: "",
      deal_ids: [],
      archivers: [],
      archived: false,
      operators: [],
      decided: false,
      candidates: [],
      ...over,
    },
  };
}

export const rangePath = (start: number, end: number) =>
  withQuery("query/orama.archive.v1.Query/Range", { json: json({ start_height: String(start), end_height: String(end) }) });

export const FINGERPRINT_B64 = (mark: number) => btoa(String.fromCharCode(...new Uint8Array(20).fill(mark)));

export function relayJson(n: number, over: Record<string, unknown> = {}) {
  return {
    node_id: `node-${n}`,
    rsa_fingerprint: FINGERPRINT_B64(n),
    ed25519_id: "",
    operator: addr(n),
    prefix16: `10.${n}.0.0/16`,
    exit: false,
    jailed: false,
    ...over,
  };
}

export const RELAY_PARAMS = {
  params: {
    min_reporters_quorum: 2,
    min_uptime_fraction: "800000000000000000",
    exit_multiplier: "2000000000000000000",
    per_relay_cap: "1000000000",
    per_operator_cap: "3000000000",
    per_prefix16_cap: "5000000000",
  },
};
