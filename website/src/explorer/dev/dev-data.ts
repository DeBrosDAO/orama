import { addr, hashOf, homeJson, indexTx, valoper } from "../data/chain/fake-gateway";
import { dealJson, nodeJson, relayJson } from "../data/chain/fake-network";

/**
 * Rows for the dev gateway (src/explorer/dev/dev-gateway.ts): enough of a chain for every page of the
 * explorer to show its loaded state. Invented, and only ever served by a dev server started with
 * ORAMA_EXPLORER_DEV_GATEWAY=1.
 */

const ORAMA = 1_000_000_000n;
export const norama = (orama: bigint) => (orama * ORAMA).toString();

/** The newest closed epoch; the chain fixture says epoch 12 is in progress. */
export const CLOSED_EPOCHS = 11;
export const DEV_BLOCKS = 100;

export function epochRow(epoch: number, over: Record<string, unknown> = {}) {
  return {
    epoch,
    start_height: (epoch - 1) * 100 + 1,
    start_time: new Date(Date.UTC(2026, 9, epoch - 3)).toISOString(),
    end_height: epoch * 100,
    end_time: new Date(Date.UTC(2026, 9, epoch - 2)).toISOString(),
    blocks: 100,
    partial: false,
    max_mintable: norama(14_848n),
    minted_validators: "8908800000000",
    minted_service: "5000000000",
    minted_development: "0",
    minted_faucet: "0",
    minted_total: "8913800000000",
    burned: "300",
    fees_collected: "900",
    fees_burned: "300",
    fees_distributed: "600",
    rewards_recorded: true,
    rewards_credited: "8900000000000",
    rewards_commission: "890000000000",
    rewards_operator_share: "6500000000000",
    rewards_delegators: "1510000000000",
    rewards_force_bonded: "8800000000",
    ...over,
  };
}

export function supplyPoint(epoch: number) {
  return {
    epoch,
    height: epoch * 100,
    time: new Date(Date.UTC(2026, 9, epoch - 2)).toISOString(),
    total_supply: norama(41_000_000n + BigInt(epoch) * 9n),
    minted_to_date: norama(BigInt(epoch) * 9n),
    burned_to_date: String(epoch * 42),
    genesis_supply: norama(41_000_000n),
    bonded: norama(7_000n),
    unbonding: norama(1_000n),
    earnings_pools: norama(2_000n),
    storage_escrow: norama(3_000n),
    other_protocol: norama(4_000n),
    circulating: norama(40_983_000n + BigInt(epoch) * 9n),
  };
}

export const validatorEpoch = (epoch: number, n: number, over: Record<string, unknown> = {}) => ({
  epoch,
  operator: valoper(n),
  consensus_address: "oramavalcons1x",
  comet_power: n === 7 ? 600_000_000 : 400_000_000,
  signed_blocks: 95 - n,
  missed_blocks: 5 + n,
  uptime_bps: 9500 - n * 100,
  slashes: 0,
  slashed_burned: "0",
  jailed: false,
  status: "bonded",
  ...over,
});

export const validatorSummary = (n: number) => ({
  operator: valoper(n),
  moniker: `V${n}`,
  status: "bonded",
  jailed: false,
  tokens: "2000000000000",
  comet_power: n === 7 ? 600_000_000 : 400_000_000,
  updated_epoch: CLOSED_EPOCHS,
  last_epoch: validatorEpoch(CLOSED_EPOCHS, n),
});

export const reward = (epoch: number, n: number) => ({
  epoch,
  operator: valoper(n),
  amount: "1000000000",
  commission: "100000000",
  operator_share: "730000000",
  delegators: "270000000",
  force_bonded: "0",
});

export const accountCredit = (epoch: number) => ({
  epoch,
  validator: valoper(7),
  role: "delegator",
  amount: "12345678",
  force_bonded: "0",
});

export const blockRow = (height: number) => ({
  height,
  hash: "cd".repeat(32),
  time: new Date(Date.parse("2026-10-08T12:00:00Z") - (DEV_BLOCKS - height) * 2000).toISOString(),
  proposer: "ab".repeat(20),
  tx_count: height % 3,
  gas_used: 80000,
  burned: "100",
  commit_signed: 2,
  commit_size: 3,
});

/** A transaction in the three states a page shows: completed, failed, and a plain send. */
export function txByNumber(n: number) {
  if (n === 2) return indexTx(2, { code: 5, log: "insufficient funds: 3 < 5000000000" });
  return indexTx(n, { height: DEV_BLOCKS - (n % 3) });
}

export const devHome = () => homeJson({ txs: [txByNumber(1), txByNumber(2), txByNumber(3)] });

export const devNodes = () => [
  nodeJson(1),
  nodeJson(2, { roles: ["ROLE_VALIDATOR"], bonds: [{ role: "ROLE_VALIDATOR", amount: "600000000000" }] }),
  nodeJson(3, { roles: ["ROLE_STORAGE", "ROLE_RELAY"], status: "NODE_STATUS_ACTIVE" }),
];

export const devDeals = () => [dealJson(3), dealJson(2, { status: "DEAL_STATUS_EXPIRED" }), dealJson(1, { protocol: true })];

export const devRelays = () => [relayJson(1), relayJson(2, { exit: true }), relayJson(3, { jailed: true })];

export const devAccount = addr(1);
export const devTxHash = hashOf(1);
