import type { ChainNode, NodeParams, NodeSet } from "./nodes";

/** Builders for tests of the node pages: a node with plausible defaults, and the set and parameters around it. */

export const OPERATOR = "orama1sx0x6xpv8wtgshrajsqgednruq26aes0h2jh07";

export const node = (id: string, over: Partial<ChainNode> = {}): ChainNode => ({
  id,
  operator: OPERATOR,
  roles: ["storage"],
  hotKey: "",
  services: ["ipfs"],
  endpoints: [],
  regionHint: "",
  bonds: [],
  declaredCapacityBytes: "0",
  reservedCapacityBytes: "0",
  status: "active",
  registeredAtHeight: 1,
  asn: 0,
  ...over,
});

export const nodeParams: NodeParams = {
  minBond: [
    { role: "storage", amount: "100" },
    { role: "validator", amount: "500" },
  ],
  unbondingSeconds: 86_400,
  bondPerGib: "10",
  probationCapacityBytes: "0",
};

export const nodeSet = (nodes: ChainNode[], over: Partial<NodeSet> = {}): NodeSet => ({ nodes, params: nodeParams, truncated: false, ...over });
