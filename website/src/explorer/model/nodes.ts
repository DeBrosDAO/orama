import type { Norama } from "./types";

/**
 * The global layer's nodes as x/nodes keeps them: who registered them, what
 * they run, what they have bonded and what they declare about themselves.
 * What an operator declares (the ASN, the region, the endpoints) is the
 * operator's word: the chain cannot check it.
 */

export const NODE_ROLES = ["validator", "storage", "relay", "exit", "dirauth", "archiver"] as const;
export type NodeRole = (typeof NODE_ROLES)[number];

export const ROLE_INFO: Record<NodeRole, { label: string; blurb: string }> = {
  validator: { label: "Validator", blurb: "signs blocks" },
  storage: { label: "Storage provider", blurb: "holds replicas of storage deals and proves them" },
  relay: { label: "Tor relay", blurb: "carries Tor circuits" },
  exit: { label: "Tor exit", blurb: "carries Tor traffic out to the internet" },
  dirauth: { label: "Directory authority", blurb: "votes on the Tor consensus" },
  archiver: { label: "Archiver", blurb: "bundles and attests ranges of old blocks" },
};

export const NODE_STATUSES = ["registered", "active", "jailed", "retired", "tombstoned"] as const;
export type NodeStatus = (typeof NODE_STATUSES)[number];

export interface RoleBond {
  role: NodeRole;
  amount: Norama;
}

export interface ChainNode {
  /** The id the operator registered the node under; the node's page is /node/<this>. */
  id: string;
  /** The operator's wallet address (orama1…). */
  operator: string;
  roles: NodeRole[];
  /** The account the node signs its chain transactions from. */
  hotKey: string;
  /** The names of the service keys the operator bound to the node ("tor", "ipfs", …). */
  services: string[];
  /** As declared by the operator. Text, never a link. */
  endpoints: string[];
  regionHint: string;
  bonds: RoleBond[];
  declaredCapacityBytes: string;
  reservedCapacityBytes: string;
  status: NodeStatus;
  registeredAtHeight: number;
  /** The autonomous system number the operator declared; 0 when it declared none. */
  asn: number;
}

export interface NodeParams {
  minBond: RoleBond[];
  unbondingSeconds: number;
  /** Norama that must be bonded on the storage role per GiB of declared capacity. */
  bondPerGib: Norama;
  /** The storage capacity a node may declare while its storage bond is zero. */
  probationCapacityBytes: string;
}

export interface NodeSet {
  nodes: ChainNode[];
  params: NodeParams;
  /** True when the chain holds more nodes than the explorer read. */
  truncated: boolean;
}

export interface NodeUnbonding {
  id: number;
  role: NodeRole;
  amount: Norama;
  /** When the escrowed bond is released. */
  completesAt: string;
}

export interface NodeDetail {
  node: ChainNode;
  params: NodeParams;
  /** The identification name the operator claimed for the node, or null. */
  name: string | null;
  unbondings: NodeUnbonding[];
}

/** A node that still serves: not retired and not tombstoned. */
export function isLive(node: ChainNode): boolean {
  return node.status !== "retired" && node.status !== "tombstoned";
}

export function hasRole(node: ChainNode, role: NodeRole): boolean {
  return node.roles.includes(role);
}

export function bondOf(node: ChainNode, role: NodeRole): Norama {
  return node.bonds.find((b) => b.role === role)?.amount ?? "0";
}

export function totalBond(node: ChainNode): Norama {
  return node.bonds.reduce((n, b) => n + BigInt(b.amount), 0n).toString();
}

export function minBondOf(params: NodeParams, role: NodeRole): Norama {
  return params.minBond.find((b) => b.role === role)?.amount ?? "0";
}

/** True when the role is bonded at or above its floor, which is what makes the role active. */
export function roleBonded(node: ChainNode, params: NodeParams, role: NodeRole): boolean {
  return BigInt(bondOf(node, role)) >= BigInt(minBondOf(params, role));
}

export function roleCounts(nodes: readonly ChainNode[]): Record<NodeRole, number> {
  const counts = Object.fromEntries(NODE_ROLES.map((r) => [r, 0])) as Record<NodeRole, number>;
  for (const node of nodes.filter(isLive)) for (const role of node.roles) counts[role] += 1;
  return counts;
}

export function statusCounts(nodes: readonly ChainNode[]): Record<NodeStatus, number> {
  const counts = Object.fromEntries(NODE_STATUSES.map((s) => [s, 0])) as Record<NodeStatus, number>;
  for (const node of nodes) counts[node.status] += 1;
  return counts;
}

export function operatorCount(nodes: readonly ChainNode[]): number {
  return new Set(nodes.filter(isLive).map((n) => n.operator)).size;
}

export interface AsnShare {
  /** 0 is the nodes that declared none. */
  asn: number;
  nodes: number;
  share: number;
}

/** The live nodes grouped by the ASN they declared, the largest group first; the undeclared group last. */
export function asnShares(nodes: readonly ChainNode[]): AsnShare[] {
  const live = nodes.filter(isLive);
  const counts = new Map<number, number>();
  for (const node of live) counts.set(node.asn, (counts.get(node.asn) ?? 0) + 1);
  return [...counts.entries()]
    .map(([asn, n]) => ({ asn, nodes: n, share: live.length === 0 ? 0 : n / live.length }))
    .sort((a, b) => (a.asn === 0 ? 1 : b.asn === 0 ? -1 : b.nodes - a.nodes || a.asn - b.asn));
}

export interface CapacityTotals {
  declared: string;
  reserved: string;
}

/** What the live storage providers declare and have reserved for deals. */
export function capacityTotals(nodes: readonly ChainNode[]): CapacityTotals {
  const providers = nodes.filter((n) => isLive(n) && hasRole(n, "storage"));
  return {
    declared: providers.reduce((n, p) => n + BigInt(p.declaredCapacityBytes), 0n).toString(),
    reserved: providers.reduce((n, p) => n + BigInt(p.reservedCapacityBytes), 0n).toString(),
  };
}

const SHARE_SCALE = 10_000n;

/** The share of a node's declared capacity that deals have reserved, or null when it declares none. */
export function utilisation(node: ChainNode): number | null {
  const declared = BigInt(node.declaredCapacityBytes);
  if (declared === 0n) return null;
  return Number((BigInt(node.reservedCapacityBytes) * SHARE_SCALE) / declared) / Number(SHARE_SCALE);
}
