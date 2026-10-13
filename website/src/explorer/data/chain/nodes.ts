import { NODE_ROLES, NODE_STATUSES } from "../../model/nodes";
import type { ChainNode, NodeDetail, NodeParams, NodeRole, NodeSet, NodeUnbonding, RoleBond } from "../../model/nodes";
import { ChainNotFound, ChainReadError, withQuery } from "./client";
import type { ChainClient } from "./client";
import { listAll } from "./paging";
import { clean } from "./text";
import { account, arr, digits, enumName, optArr, optStr, optUint, rec, str, uint } from "./wire";

const NODES = "query/orama.nodes.v1.Query/Nodes";
const NODE = "query/orama.nodes.v1.Query/Node";
const NODE_PARAMS = "query/orama.nodes.v1.Query/Params";
const NAME_OF_NODE = "query/orama.nodes.v1.Query/NameOfNode";
const NODE_UNBONDINGS = "query/orama.nodes.v1.Query/NodeUnbondings";

/** Pages of 100 read for the node list: a network of 500 nodes shows whole, a larger one says it is cut. */
const NODE_PAGES = 5;
const MAX_TEXT = 200;
const MAX_NAME = 64;
/** x/nodes holds a node id to this shape (chain/x/nodes/types/validate.go idPattern). */
export const NODE_ID = /^[A-Za-z0-9][A-Za-z0-9._:-]{0,63}$/;

const MS_PER_SECOND = 1000;
/** The latest time a JS Date holds. */
const MAX_DATE_MS = 8_640_000_000_000_000;

/** A Unix time in seconds as a UTC ISO string. */
function unixTime(value: unknown, what: string): string {
  const ms = Number(digits(value, what)) * MS_PER_SECOND;
  if (!Number.isFinite(ms) || ms > MAX_DATE_MS) throw new ChainReadError(`The chain sent a malformed ${what}.`);
  return new Date(ms).toISOString();
}

const ROLE_PREFIX = "ROLE_";
const STATUS_PREFIX = "NODE_STATUS_";

const readRole = (value: unknown): NodeRole => enumName(value, ROLE_PREFIX, NODE_ROLES, "node role");

function readBond(raw: unknown): RoleBond {
  const bond = rec(raw, "node bond");
  return { role: readRole(bond.role), amount: digits(bond.amount, "node bond") };
}

export function readNode(raw: unknown): ChainNode {
  const n = rec(raw, "node");
  const id = str(n.node_id, "node id", 64);
  if (!NODE_ID.test(id)) throw new ChainReadError("The chain sent a malformed node id.");
  return {
    id,
    operator: account(n.operator, "node operator"),
    roles: optArr(n.roles, "node roles").map(readRole),
    hotKey: n.hot_key === undefined || n.hot_key === null || n.hot_key === "" ? "" : account(n.hot_key, "node hot key"),
    services: optArr(n.bindings, "node bindings").map((b) => clean(str(rec(b, "node binding").service, "binding service", 32), 32)),
    endpoints: optArr(n.endpoints, "node endpoints").map((e) => clean(str(e, "node endpoint", MAX_TEXT), MAX_TEXT)),
    regionHint: clean(optStr(n.region_hint, "node region", 64), 64),
    bonds: optArr(n.bonds, "node bonds").map(readBond),
    declaredCapacityBytes: digits(n.declared_capacity_bytes, "declared capacity"),
    reservedCapacityBytes: digits(n.reserved_capacity_bytes, "reserved capacity"),
    status: enumName(n.status, STATUS_PREFIX, NODE_STATUSES, "node status"),
    registeredAtHeight: uint(n.registered_at_height, "registration height"),
    asn: optUint(n.asn, "node ASN"),
  };
}

export function readNodeParams(raw: unknown): NodeParams {
  const p = rec(rec(raw, "node parameters").params, "node parameters");
  return {
    minBond: optArr(p.min_bond, "minimum bonds").map(readBond),
    unbondingSeconds: uint(p.unbonding_seconds, "unbonding time"),
    bondPerGib: digits(p.bond_per_gib, "bond per GiB"),
    probationCapacityBytes: digits(p.probation_capacity_bytes, "probation capacity"),
  };
}

async function loadNodeParams(client: ChainClient): Promise<NodeParams> {
  return readNodeParams(await client.get(NODE_PARAMS));
}

/** Every node x/nodes holds, whatever its status, with the module's parameters. */
export async function loadNodeSet(client: ChainClient): Promise<NodeSet> {
  const [listed, params] = await Promise.all([listAll(client, NODES, "nodes", readNode, { maxPages: NODE_PAGES }), loadNodeParams(client)]);
  return { nodes: listed.items, params, truncated: listed.more };
}

function readUnbonding(raw: unknown): NodeUnbonding {
  const u = rec(raw, "node unbonding");
  return {
    id: uint(u.id, "unbonding id"),
    role: readRole(u.role),
    amount: digits(u.amount, "unbonding amount"),
    completesAt: unixTime(u.completion_unix, "unbonding completion"),
  };
}

/** An absent answer (the chain says 404) is null; any other failure stays a failure. */
async function orNull<T>(read: Promise<T>): Promise<T | null> {
  try {
    return await read;
  } catch (err) {
    if (err instanceof ChainNotFound) return null;
    throw err;
  }
}

/** One node with its name and unbonding bonds; null for an id the chain does not hold. */
export async function loadNodeDetail(client: ChainClient, id: string): Promise<NodeDetail | null> {
  if (!NODE_ID.test(id)) return null;
  const request = (method: string) => withQuery(method, { json: JSON.stringify({ node_id: id }) });
  const nodeRaw = await orNull(client.get(request(NODE)));
  if (nodeRaw === null) return null;
  const [params, nameRaw, unbondingsRaw] = await Promise.all([
    loadNodeParams(client),
    orNull(client.get(request(NAME_OF_NODE))),
    client.get(request(NODE_UNBONDINGS)),
  ]);
  return {
    node: readNode(rec(nodeRaw, "node").node),
    params,
    name: nameRaw === null ? null : clean(str(rec(rec(nameRaw, "node name").name, "node name").name, "node name", MAX_NAME), MAX_NAME),
    unbondings: arr(rec(unbondingsRaw, "node unbondings").unbondings, "node unbondings").map(readUnbonding),
  };
}
