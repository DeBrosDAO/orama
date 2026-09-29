import * as archive from "./gen/orama/archive/v1/tx";
import * as cnft from "./gen/orama/cnft/v1/tx";
import * as houses from "./gen/orama/houses/v1/tx";
import * as market from "./gen/orama/market/v1/tx";
import * as nodes from "./gen/orama/nodes/v1/tx";
import * as relay from "./gen/orama/relay/v1/tx";
import * as storage from "./gen/orama/storage/v1/tx";
import * as token from "./gen/orama/token/v1/tx";
import { roleToJSON, keyTypeToJSON } from "./gen/orama/nodes/v1/nodes";
import { dealClassToJSON, releaseReasonToJSON } from "./gen/orama/storage/v1/storage";
import { voteOptionToJSON } from "./gen/orama/houses/v1/houses";
import { extensionToJSON } from "./gen/orama/token/v1/token";
import * as bank from "./gen/cosmos/bank/v1beta1/tx";
import * as staking from "./gen/cosmos/staking/v1beta1/tx";
import * as slashing from "./gen/cosmos/slashing/v1beta1/tx";
import * as distribution from "./gen/cosmos/distribution/v1beta1/tx";
import * as wasm from "./gen/cosmwasm/wasm/v1/tx";
import { defineMsg, desc, type MsgDef } from "./msg";
import { formatAmount, formatBps, formatCoins, shortHex } from "./format";

/** An amount string that a message carries in norama. */
const norama = (amount: string) => formatAmount(amount);

/** A CosmWasm message body: the JSON it holds, or a marker when it is not JSON. */
function wasmBody(msg: Uint8Array): string {
  const text = new TextDecoder("utf-8", { fatal: false }).decode(msg);
  try {
    return JSON.stringify(JSON.parse(text));
  } catch {
    return `(not JSON) ${shortHex(msg)}`;
  }
}

const list = (items: readonly string[]) => (items.length ? items.join(", ") : "none");

/**
 * Every message a wallet signs on Orama. Each entry has the type URL, the
 * generated protobuf codec, and the description an approval screen shows. The
 * Go side's list of the same messages is chain/client/tx/testdata/wallet_msgs.json;
 * a test fails when the two disagree.
 */
export const MSG = {
  // ---- bank, staking, slashing, distribution ----
  bankSend: defineMsg("/cosmos.bank.v1beta1.MsgSend", bank.MsgSend, (m) =>
    desc("Send", `Send ${formatCoins(m.amount)} from ${m.fromAddress} to ${m.toAddress}`, [
      "The chain refuses public user-to-user norama sends; a payment goes through the shielded path.",
    ]),
  ),
  stakingCreateValidator: defineMsg("/cosmos.staking.v1beta1.MsgCreateValidator", staking.MsgCreateValidator, (m) =>
    desc(
      "Create validator",
      `Create validator "${m.description?.moniker ?? ""}" at ${m.validatorAddress} with a self-delegation of ${formatCoins(m.value ? [m.value] : [])}`,
      [
        `Commission ${m.commission?.rate ?? "?"} (max ${m.commission?.maxRate ?? "?"}, max change ${m.commission?.maxChangeRate ?? "?"} per day)`,
        `Minimum self-delegation ${m.minSelfDelegation}`,
      ],
      true,
    ),
  ),
  stakingEditValidator: defineMsg("/cosmos.staking.v1beta1.MsgEditValidator", staking.MsgEditValidator, (m) =>
    desc("Edit validator", `Edit validator ${m.validatorAddress}`, [
      m.commissionRate ? `New commission rate ${m.commissionRate}` : "Commission rate unchanged",
    ]),
  ),
  stakingDelegate: defineMsg("/cosmos.staking.v1beta1.MsgDelegate", staking.MsgDelegate, (m) =>
    desc("Delegate", `Delegate ${formatCoins(m.amount ? [m.amount] : [])} from ${m.delegatorAddress} to ${m.validatorAddress}`),
  ),
  stakingUndelegate: defineMsg("/cosmos.staking.v1beta1.MsgUndelegate", staking.MsgUndelegate, (m) =>
    desc("Undelegate", `Undelegate ${formatCoins(m.amount ? [m.amount] : [])} from ${m.validatorAddress}`, [
      "The amount is locked for the unbonding period before it is spendable.",
    ]),
  ),
  stakingBeginRedelegate: defineMsg("/cosmos.staking.v1beta1.MsgBeginRedelegate", staking.MsgBeginRedelegate, (m) =>
    desc("Redelegate", `Move ${formatCoins(m.amount ? [m.amount] : [])} from ${m.validatorSrcAddress} to ${m.validatorDstAddress}`),
  ),
  slashingUnjail: defineMsg("/cosmos.slashing.v1beta1.MsgUnjail", slashing.MsgUnjail, (m) =>
    desc("Unjail validator", `Unjail validator ${m.validatorAddr}`),
  ),
  distributionWithdrawReward: defineMsg(
    "/cosmos.distribution.v1beta1.MsgWithdrawDelegatorReward",
    distribution.MsgWithdrawDelegatorReward,
    (m) => desc("Withdraw staking rewards", `Withdraw ${m.delegatorAddress}'s rewards from ${m.validatorAddress}`),
  ),

  // ---- CosmWasm ----
  wasmExecute: defineMsg("/cosmwasm.wasm.v1.MsgExecuteContract", wasm.MsgExecuteContract, (m) =>
    desc(
      "Execute contract",
      `Call contract ${m.contract} with ${wasmBody(m.msg)}`,
      [`Funds sent with the call: ${formatCoins(m.funds)}`],
      m.funds.length > 0,
    ),
  ),
  wasmInstantiate: defineMsg("/cosmwasm.wasm.v1.MsgInstantiateContract", wasm.MsgInstantiateContract, (m) =>
    desc(
      "Instantiate contract",
      `Instantiate code ${m.codeId} as "${m.label}" with ${wasmBody(m.msg)}`,
      [`Admin: ${m.admin || "none"}`, `Funds sent: ${formatCoins(m.funds)}`],
      m.admin !== "" || m.funds.length > 0,
    ),
  ),

  // ---- x/token ----
  tokenCreate: defineMsg("/orama.token.v1.MsgCreateToken", token.MsgCreateToken, (m) => {
    const powers: string[] = [];
    if (m.mint) powers.push("the creator can mint more supply");
    if (m.freeze) powers.push("the creator can freeze any holder");
    if (m.pause) powers.push("the creator can pause all transfers");
    if (m.permanentDelegate) powers.push(`${m.permanentDelegate} can move any holder's tokens`);
    if (m.transferHook) powers.push("every transfer runs a hook");
    if (m.transferFeeBps > 0) powers.push(`every transfer pays a ${formatBps(m.transferFeeBps)} fee`);
    if (m.nonTransferable) powers.push("holders cannot transfer it");
    return desc(
      "Create token",
      `Create token factory/${m.creator}/${m.subdenom} (${m.symbol}, "${m.name}")`,
      [`Powers: ${list(powers)}`, "Powers are fixed for the life of the token except by renouncing them."],
      powers.length > 0,
    );
  }),
  tokenMint: defineMsg("/orama.token.v1.MsgMint", token.MsgMint, (m) =>
    desc("Mint token", `Mint ${m.amount} ${m.denom} to ${m.recipient}`),
  ),
  tokenBurn: defineMsg("/orama.token.v1.MsgBurn", token.MsgBurn, (m) =>
    desc("Burn token", `Burn ${m.amount} ${m.denom} from ${m.sender}`),
  ),
  tokenTransfer: defineMsg("/orama.token.v1.MsgTransfer", token.MsgTransfer, (m) =>
    desc(
      "Transfer token",
      `Transfer ${m.amount} ${m.denom} from ${m.from} to ${m.to}`,
      m.from !== m.sender ? [`Signed by ${m.sender}, the token's permanent delegate, not by the holder`] : [],
      m.from !== m.sender,
    ),
  ),
  tokenSetFrozen: defineMsg("/orama.token.v1.MsgSetFrozen", token.MsgSetFrozen, (m) =>
    desc(m.frozen ? "Freeze holder" : "Unfreeze holder", `${m.frozen ? "Freeze" : "Unfreeze"} ${m.account} for ${m.denom}`, [], m.frozen),
  ),
  tokenSetPaused: defineMsg("/orama.token.v1.MsgSetPaused", token.MsgSetPaused, (m) =>
    desc(m.paused ? "Pause token" : "Unpause token", `${m.paused ? "Pause" : "Unpause"} all transfers of ${m.denom}`, [], m.paused),
  ),
  tokenRenounce: defineMsg("/orama.token.v1.MsgRenounce", token.MsgRenounce, (m) =>
    desc("Renounce power", `Give up the ${extensionToJSON(m.extension)} power of ${m.denom}`, ["This cannot be undone."]),
  ),
  tokenSetShieldable: defineMsg("/orama.token.v1.MsgSetShieldable", token.MsgSetShieldable, (m) =>
    desc("Make token shieldable", `Allow ${m.denom} to enter the shielded pool`, ["This cannot be undone."]),
  ),
  tokenDelete: defineMsg("/orama.token.v1.MsgDeleteToken", token.MsgDeleteToken, (m) =>
    desc("Delete token", `Delete zero-supply token ${m.denom} and release its metadata deposit`),
  ),

  // ---- x/nodes ----
  nodesRegisterOperator: defineMsg("/orama.nodes.v1.MsgRegisterOperator", nodes.MsgRegisterOperator, (m) =>
    desc("Register operator", `Register ${m.operator} as a node operator`),
  ),
  nodesRegisterNode: defineMsg("/orama.nodes.v1.MsgRegisterNode", nodes.MsgRegisterNode, (m) =>
    desc(
      "Register node",
      `Register node ${m.nodeId} for ${m.operator} with roles ${list(m.roles.map(roleToJSON))}`,
      [
        `Hot key: ${m.hotKey}`,
        `Service keys bound: ${list(m.bindings.map((b) => `${b.service} (${keyTypeToJSON(b.keyType)})`))}`,
        `Endpoints: ${list(m.endpoints)}`,
      ],
    ),
  ),
  nodesUpdateNode: defineMsg("/orama.nodes.v1.MsgUpdateNode", nodes.MsgUpdateNode, (m) => {
    const changes: string[] = [];
    if (m.hotKey) changes.push(`hot key becomes ${m.hotKey}`);
    if (m.bindings.length > 0) changes.push(`service-key bindings replaced by ${m.bindings.length}`);
    if (m.setEndpoints) changes.push(`endpoints become ${list(m.endpoints)}`);
    if (m.setRegionHint) changes.push(`region becomes "${m.regionHint}"`);
    if (m.setAsn) changes.push(`ASN becomes ${m.asn}`);
    return desc("Update node", `Update node ${m.nodeId}: ${list(changes)}`, [
      "Rotating the hot key or the service keys changes who controls the node.",
    ], true);
  }),
  nodesRetireNode: defineMsg("/orama.nodes.v1.MsgRetireNode", nodes.MsgRetireNode, (m) =>
    desc("Retire node", `Retire node ${m.nodeId}`, ["A retired node's service keys can never be bound again."], true),
  ),
  nodesBondNode: defineMsg("/orama.nodes.v1.MsgBondNode", nodes.MsgBondNode, (m) =>
    desc("Bond node", `Bond ${norama(m.amount)} to the ${roleToJSON(m.role)} role of node ${m.nodeId}`, [
      "Earnings in the operator's earnings account fund any shortfall first.",
    ]),
  ),
  nodesUnbondNode: defineMsg("/orama.nodes.v1.MsgUnbondNode", nodes.MsgUnbondNode, (m) =>
    desc("Unbond node", `Start unbonding ${norama(m.amount)} from the ${roleToJSON(m.role)} role of node ${m.nodeId}`, [
      "The amount is paid back after the unbonding period.",
    ]),
  ),
  nodesDeclareCapacity: defineMsg("/orama.nodes.v1.MsgDeclareCapacity", nodes.MsgDeclareCapacity, (m) =>
    desc("Declare capacity", `Declare ${m.capacityBytes} bytes of storage capacity on node ${m.nodeId}`),
  ),
  nodesFundHotKey: defineMsg("/orama.nodes.v1.MsgFundHotKey", nodes.MsgFundHotKey, (m) =>
    desc("Fund node hot key", `Send ${norama(m.amount)} from ${m.operator} to node ${m.nodeId}'s hot key`),
  ),
  nodesRegisterCluster: defineMsg("/orama.nodes.v1.MsgRegisterCluster", nodes.MsgRegisterCluster, (m) =>
    desc("Register cluster", `Register public cluster ${m.clusterId} at ${m.baseDomain}`, [
      `Public endpoints: ${list(m.publicEndpoints)}`,
      m.metadataUri ? `Metadata: ${m.metadataUri}` : "No metadata URI",
    ]),
  ),
  nodesUpdateCluster: defineMsg("/orama.nodes.v1.MsgUpdateCluster", nodes.MsgUpdateCluster, (m) =>
    desc("Update cluster", `Update public cluster ${m.clusterId}: ${m.baseDomain}`, [`Public endpoints: ${list(m.publicEndpoints)}`]),
  ),
  nodesRetireCluster: defineMsg("/orama.nodes.v1.MsgRetireCluster", nodes.MsgRetireCluster, (m) =>
    desc("Retire cluster", `Retire public cluster ${m.clusterId}`),
  ),

  // ---- x/storage ----
  storageCreateDeal: defineMsg("/orama.storage.v1.MsgCreateDeal", storage.MsgCreateDeal, (m) => {
    const total = BigInt(m.pricePerEpoch || "0") * BigInt(m.durationEpochs) * BigInt(m.replicas);
    return desc(
      "Create storage deal",
      `${dealClassToJSON(m.class)} deal: ${m.replicas} replicas, ${m.pieces.length} pieces, ${m.durationEpochs} epochs at ${norama(m.pricePerEpoch)} per epoch`,
      [
        `Maximum cost: ${norama(total.toString())}`,
        m.granter ? `Paid from ${m.granter}'s DealAuthorization` : "Paid from the signer's own funds",
        m.repairDelegate ? `Repair delegate: ${m.repairDelegate}` : "No repair delegate",
      ],
      m.repairDelegate !== "",
    );
  }),
  storageExtendDeal: defineMsg("/orama.storage.v1.MsgExtendDeal", storage.MsgExtendDeal, (m) =>
    desc("Extend storage deal", `Extend deal ${m.dealId} by ${m.extraEpochs} epochs`),
  ),
  storageGrantDealAuthorization: defineMsg(
    "/orama.storage.v1.MsgGrantDealAuthorization",
    storage.MsgGrantDealAuthorization,
    (m) =>
      desc(
        "Grant deal authorization",
        `Let ${m.grantee} create deals paid by ${m.signer}, spending up to ${norama(m.spendLimit)} per ${m.periodEpochs}-epoch period`,
        [
          `Expires at epoch ${m.expiryEpoch}`,
          `Per deal: at most ${m.maxPieceBytes} bytes per piece, ${m.maxDurationEpochs} epochs, ${m.replicas} replicas`,
          "The grantee can spend these funds without asking again until it expires or is revoked.",
        ],
        true,
      ),
  ),
  storageRevokeDealAuthorization: defineMsg(
    "/orama.storage.v1.MsgRevokeDealAuthorization",
    storage.MsgRevokeDealAuthorization,
    (m) => desc("Revoke deal authorization", `Revoke ${m.grantee}'s authorization to create deals for ${m.signer}`),
  ),
  storageAcceptDeal: defineMsg("/orama.storage.v1.MsgAcceptDeal", storage.MsgAcceptDeal, (m) =>
    desc("Accept deal", `Node ${m.nodeId} accepts slot ${m.slot} of deal ${m.dealId}`),
  ),
  storageDeclineDeal: defineMsg("/orama.storage.v1.MsgDeclineDeal", storage.MsgDeclineDeal, (m) =>
    desc("Decline deal", `Node ${m.nodeId} declines slot ${m.slot} of deal ${m.dealId}: ${m.reason}`),
  ),
  storageSubmitProofs: defineMsg("/orama.storage.v1.MsgSubmitProofs", storage.MsgSubmitProofs, (m) =>
    desc("Submit storage proofs", `Node ${m.nodeId} submits ${m.proofs.length} storage proofs`),
  ),
  storageReleaseReplica: defineMsg("/orama.storage.v1.MsgReleaseReplica", storage.MsgReleaseReplica, (m) =>
    desc("Release replica", `Node ${m.nodeId} releases slot ${m.slot} of deal ${m.dealId}: ${releaseReasonToJSON(m.reason)}`),
  ),

  // ---- x/houses ----
  housesSubmitProposal: defineMsg("/orama.houses.v1.MsgSubmitProposal", houses.MsgSubmitProposal, (m) => {
    const kinds = Object.entries(m.content ?? {})
      .filter(([, v]) => v !== undefined)
      .map(([k]) => k);
    return desc("Submit proposal", `Submit a ${list(kinds)} proposal as ${m.proposer}`, [
      "Proposals pass in the token house and then face the operator house.",
    ]);
  }),
  housesVoteToken: defineMsg("/orama.houses.v1.MsgVoteToken", houses.MsgVoteToken, (m) =>
    desc("Vote (token house)", `Vote ${voteOptionToJSON(m.option)} on proposal ${m.proposalId} in the token house`),
  ),
  housesVoteOperator: defineMsg("/orama.houses.v1.MsgVoteOperator", houses.MsgVoteOperator, (m) =>
    desc(
      "Vote (operator house)",
      `Vote ${voteOptionToJSON(m.option)} on proposal ${m.proposalId} in the operator house`,
      ["A NO vote from enough of the operator house vetoes a parameter proposal."],
    ),
  ),
  housesLockHouseBond: defineMsg("/orama.houses.v1.MsgLockHouseBond", houses.MsgLockHouseBond, (m) =>
    desc("Lock house bond", `Lock ${norama(m.amount)} as a house bond`),
  ),
  housesUnlockHouseBond: defineMsg("/orama.houses.v1.MsgUnlockHouseBond", houses.MsgUnlockHouseBond, (m) =>
    desc("Unlock house bond", `Unlock ${m.signer}'s house bond`),
  ),
  housesExecuteProposal: defineMsg("/orama.houses.v1.MsgExecuteProposal", houses.MsgExecuteProposal, (m) =>
    desc("Execute proposal", `Execute passed proposal ${m.proposalId}`),
  ),

  // ---- x/cnft ----
  cnftCreateCollection: defineMsg("/orama.cnft.v1.MsgCreateCollection", cnft.MsgCreateCollection, (m) =>
    desc("Create collection", `Create collection "${m.name}"`, [`Royalty on every sale: ${formatBps(m.royaltyBps)}`]),
  ),
  cnftCreateTree: defineMsg("/orama.cnft.v1.MsgCreateTree", cnft.MsgCreateTree, (m) =>
    desc("Create tree", `Create a depth-${m.depth} tree for collection ${m.collectionId} (buffer ${m.buffer}, canopy ${m.canopy})`),
  ),
  cnftMint: defineMsg("/orama.cnft.v1.MsgMint", cnft.MsgMint, (m) =>
    desc("Mint cNFTs", `Mint ${m.leaves.length} cNFTs into tree ${m.treeId}`, [
      `Owners: ${list([...new Set(m.leaves.map((l) => l.owner))])}`,
    ]),
  ),
  cnftTransfer: defineMsg("/orama.cnft.v1.MsgTransfer", cnft.MsgTransfer, (m) =>
    desc("Transfer cNFT", `Transfer asset ${shortHex(m.current?.assetId ?? new Uint8Array())} in tree ${m.treeId} to ${m.newOwner}`, [
      m.newDelegate ? `New delegate: ${m.newDelegate}` : "No delegate",
    ]),
  ),
  cnftBurn: defineMsg("/orama.cnft.v1.MsgBurn", cnft.MsgBurn, (m) =>
    desc("Burn cNFT", `Burn asset ${shortHex(m.current?.assetId ?? new Uint8Array())} in tree ${m.treeId}`, ["This cannot be undone."]),
  ),
  cnftUpdateMetadata: defineMsg("/orama.cnft.v1.MsgUpdateMetadata", cnft.MsgUpdateMetadata, (m) =>
    desc("Update cNFT metadata", `Point asset ${shortHex(m.current?.assetId ?? new Uint8Array())} at ${m.newMetadataCid}`),
  ),
  cnftDecompress: defineMsg("/orama.cnft.v1.MsgDecompress", cnft.MsgDecompress, (m) =>
    desc("Decompress cNFT", `Decompress asset ${shortHex(m.current?.assetId ?? new Uint8Array())} in tree ${m.treeId}`),
  ),
  cnftCompress: defineMsg("/orama.cnft.v1.MsgCompress", cnft.MsgCompress, (m) =>
    desc("Compress cNFT", `Compress asset ${shortHex(m.assetId)}`),
  ),
  cnftRecordSnapshot: defineMsg("/orama.cnft.v1.MsgRecordSnapshot", cnft.MsgRecordSnapshot, (m) =>
    desc("Record tree snapshot", `Record snapshot ${m.cid} of tree ${m.treeId}`),
  ),

  // ---- x/market ----
  marketList: defineMsg("/orama.market.v1.MsgList", market.MsgList, (m) =>
    desc("List cNFT", `List asset ${shortHex(m.leaf?.assetId ?? new Uint8Array())} in tree ${m.treeId} for ${norama(m.price)}`, [
      "The collection's royalty is taken from the sale price when it settles.",
    ]),
  ),
  marketCancelListing: defineMsg("/orama.market.v1.MsgCancelListing", market.MsgCancelListing, (m) =>
    desc("Cancel listing", `Cancel listing ${m.listingId}`),
  ),
  marketBid: defineMsg("/orama.market.v1.MsgBid", market.MsgBid, (m) =>
    desc("Bid", `Bid ${norama(m.amount)} on listing ${m.listingId}`, ["The bid amount is escrowed until it is cancelled or settled."]),
  ),
  marketCancelBid: defineMsg("/orama.market.v1.MsgCancelBid", market.MsgCancelBid, (m) =>
    desc("Cancel bid", `Cancel bid ${m.bidId} on listing ${m.listingId}`),
  ),
  marketSettle: defineMsg("/orama.market.v1.MsgSettle", market.MsgSettle, (m) =>
    desc("Settle sale", `Settle listing ${m.listingId} with bid ${m.bidId}`, ["Royalties are paid to the collection creator from the price."]),
  ),

  // ---- x/archive, x/relay ----
  archiveAttest: defineMsg("/orama.archive.v1.MsgAttest", archive.MsgAttest, (m) =>
    desc("Attest archive", `Attest blocks ${m.startHeight}-${m.endHeight} as bundle ${m.bundleCid} for node ${m.nodeId}`),
  ),
  archiveAttachReplicas: defineMsg("/orama.archive.v1.MsgAttachReplicas", archive.MsgAttachReplicas, (m) =>
    desc("Attach archive replicas", `Attach ${m.dealIds.length} deals to blocks ${m.startHeight}-${m.endHeight}`),
  ),
  relayRegisterRelay: defineMsg("/orama.relay.v1.MsgRegisterRelay", relay.MsgRegisterRelay, (m) =>
    desc("Register relay", `Register ${m.exit ? "exit relay" : "relay"} for node ${m.nodeId}`),
  ),
  relayReportEpoch: defineMsg("/orama.relay.v1.MsgReportEpoch", relay.MsgReportEpoch, (m) =>
    desc("Report relay epoch", `Report ${m.entries.length} relay observations for epoch ${m.epoch} (chunk ${m.chunkIndex + 1} of ${m.chunkCount})`),
  ),
  relayUpdateReporters: defineMsg("/orama.relay.v1.MsgUpdateReporters", relay.MsgUpdateReporters, (m) =>
    desc("Update relay reporters", `Replace the relay reporter set with ${list(m.reporters)}`, [], true),
  ),
} as const;

/** Every message the registry holds, by type URL. */
// eslint-disable-next-line @typescript-eslint/no-explicit-any
export const MESSAGE_REGISTRY: ReadonlyMap<string, MsgDef<any>> = new Map(
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  Object.values(MSG).map((def) => [def.typeUrl, def as MsgDef<any>]),
);
