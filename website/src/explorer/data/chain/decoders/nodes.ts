import { enumText } from "./format";
import { define, field, sp, table } from "./kit";
import type { Reader } from "./kit";

const MODULE = "Nodes";

export const ROLES = [
  ["UNSPECIFIED", "unspecified"],
  ["VALIDATOR", "validator"],
  ["STORAGE", "storage"],
  ["RELAY", "relay"],
  ["EXIT", "exit"],
  ["DIRAUTH", "directory authority"],
  ["ARCHIVER", "archiver"],
] as const;

const FUND_SOURCES = [
  ["EARNINGS", "their earnings"],
  ["BANK", "their bank balance"],
] as const;

const role = (r: Reader, key = "role") => r.enumName(key, "ROLE_", ROLES) || "unspecified";
const roles = (r: Reader) =>
  r
    .items("roles")
    .map((v) => enumText(v, "ROLE_", ROLES))
    .filter((v) => v !== "")
    .join(", ");
const node = (r: Reader) => r.name("node_id");

/** Where a hot-key top-up comes from. The proto's zero value, and so an absent field, is earnings. */
function fundSource(r: Reader): string {
  const source = r.enumName("source", "FUND_SOURCE_", FUND_SOURCES);
  if (source === "") return FUND_SOURCES[0][1];
  return FUND_SOURCES.some(([, label]) => label === source) ? source : `an unrecognised source (${source})`;
}

function updatedFields(r: Reader) {
  const bindings = r.items("bindings").length;
  return [
    field("Node ID", node(r)),
    field("New hot key", r.text("hot_key"), "address"),
    field("Bindings replaced", bindings > 0 ? String(bindings) : ""),
    field("Endpoints", r.bool("set_endpoints") ? r.list("endpoints") || "none" : ""),
    field("Region hint", r.bool("set_region_hint") ? r.text("region_hint") || "cleared" : ""),
    field("ASN", r.bool("set_asn") ? (r.digits("asn") === "0" ? "undeclared" : r.uint("asn")) : ""),
  ];
}

export const NODES_DECODERS = table(
  define("/orama.nodes.v1.MsgRegisterOperator", {
    module: MODULE,
    tag: "Register operator",
    actor: "operator",
    verb: ["registered", "register"],
    object: () => ["as a node operator"],
  }),
  define("/orama.nodes.v1.MsgRegisterNode", {
    module: MODULE,
    tag: "Register node",
    actor: "operator",
    verb: ["registered", "register"],
    object: (r) => sp`node ${node(r)}${roles(r) === "" ? "" : ` with ${r.items("roles").length > 1 ? "roles" : "role"} ${roles(r)}`}`,
    fields: (r) => [
      field("Node ID", node(r)),
      field("Roles", roles(r)),
      field("Hot key", r.text("hot_key"), "address"),
      field("Endpoints", r.list("endpoints")),
      field("Region hint", r.text("region_hint")),
      field("ASN", r.digits("asn") === "0" ? "" : r.uint("asn")),
    ],
  }),
  define("/orama.nodes.v1.MsgUpdateNode", {
    module: MODULE,
    tag: "Update node",
    actor: "operator",
    verb: ["updated", "update"],
    object: (r) => sp`node ${node(r)}`,
    fields: updatedFields,
  }),
  define("/orama.nodes.v1.MsgRetireNode", {
    module: MODULE,
    tag: "Retire node",
    actor: "operator",
    verb: ["retired", "retire"],
    object: (r) => sp`node ${node(r)}`,
    fields: (r) => [field("Node ID", node(r))],
  }),
  define("/orama.nodes.v1.MsgBondNode", {
    module: MODULE,
    tag: "Bond node",
    category: "staking",
    actor: "operator",
    verb: ["bonded", "bond"],
    object: (r) => sp`${r.amountPart("amount")} on node ${node(r)} for the ${role(r)} role`,
    amount: (r) => r.norama("amount"),
    fields: (r) => [field("Node ID", node(r)), field("Role", role(r)), field("Amount", r.norama("amount") ?? "", "norama")],
  }),
  define("/orama.nodes.v1.MsgUnbondNode", {
    module: MODULE,
    tag: "Unbond node",
    category: "staking",
    actor: "operator",
    verb: ["unbonded", "unbond"],
    object: (r) => sp`${r.amountPart("amount")} from node ${node(r)} for the ${role(r)} role`,
    amount: (r) => r.norama("amount"),
    fields: (r) => [field("Node ID", node(r)), field("Role", role(r)), field("Amount", r.norama("amount") ?? "", "norama")],
  }),
  define("/orama.nodes.v1.MsgDeclareCapacity", {
    module: MODULE,
    tag: "Declare capacity",
    actor: "operator",
    verb: ["declared", "declare"],
    object: (r) => sp`${r.bytes("capacity_bytes")} of storage capacity on node ${node(r)}`,
    fields: (r) => [field("Node ID", node(r)), field("Declared capacity", r.bytes("capacity_bytes"))],
  }),
  define("/orama.nodes.v1.MsgFundHotKey", {
    module: MODULE,
    tag: "Fund hot key",
    actor: "operator",
    verb: ["funded", "fund"],
    object: (r) => sp`the hot key of node ${node(r)} with ${r.amountPart("amount")} from ${fundSource(r)}`,
    amount: (r) => r.norama("amount"),
    fields: (r) => [field("Node ID", node(r)), field("Amount", r.norama("amount") ?? "", "norama"), field("Taken from", fundSource(r))],
  }),
  define("/orama.nodes.v1.MsgRegisterCluster", {
    module: MODULE,
    tag: "Register cluster",
    actor: "operator",
    verb: ["registered", "register"],
    object: (r) => sp`cluster ${r.name("cluster_id")}`,
    fields: clusterFields,
  }),
  define("/orama.nodes.v1.MsgUpdateCluster", {
    module: MODULE,
    tag: "Update cluster",
    actor: "operator",
    verb: ["updated", "update"],
    object: (r) => sp`cluster ${r.name("cluster_id")}`,
    fields: clusterFields,
  }),
  define("/orama.nodes.v1.MsgRetireCluster", {
    module: MODULE,
    tag: "Retire cluster",
    actor: "operator",
    verb: ["retired", "retire"],
    object: (r) => sp`cluster ${r.name("cluster_id")}`,
    fields: (r) => [field("Cluster ID", r.name("cluster_id"))],
  }),
  define("/orama.nodes.v1.MsgClaimNodeName", {
    module: MODULE,
    tag: "Claim node name",
    actor: "operator",
    verb: ["claimed", "claim"],
    object: (r) => sp`the name “${r.name("name")}” for node ${node(r)}`,
    fields: (r) => [field("Node ID", node(r)), field("Name", r.name("name"))],
  }),
  define("/orama.nodes.v1.MsgReleaseNodeName", {
    module: MODULE,
    tag: "Release node name",
    actor: "operator",
    verb: ["released", "release"],
    object: (r) => sp`the name of node ${node(r)}`,
    fields: (r) => [field("Node ID", node(r))],
  }),
);

function clusterFields(r: Reader) {
  return [
    field("Cluster ID", r.name("cluster_id")),
    field("Base domain", r.text("base_domain")),
    field("Public endpoints", r.list("public_endpoints")),
    field("Metadata URI", r.text("metadata_uri")),
  ];
}
