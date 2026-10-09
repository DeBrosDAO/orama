import { bech32Rehrp } from "../../model/bech32";
import type { ValidatorRef } from "../../model/types";
import { shortAddress } from "../../model/units";
import { ChainReadError, chainPath } from "./client";
import type { ChainClient } from "./client";
import { clean } from "./text";
import { arr, bool, digits, optArr, optStr, rec, rpcResult, str } from "./wire";

/** Every validator the chain knows, joined across the staking module, CometBFT and x/power. */
export interface DirectoryValidator {
  ref: ValidatorRef;
  /** The oramavaloper address, as the staking module names the validator. */
  valoper: string;
  /** The consensus address CometBFT names the validator by, upper-case hex. */
  consensus: string;
  jailed: boolean;
  /** CometBFT voting power: 0 for a validator that is not in the active set. */
  power: bigint;
  /** A seat of the founding bootstrap committee. */
  committee: boolean;
}

export interface Directory {
  validators: DirectoryValidator[];
  /** The sum of the voting power of the active set. */
  totalPower: bigint;
  byConsensus: Map<string, DirectoryValidator>;
  byOperator: Map<string, DirectoryValidator>;
}

const ACCOUNT_HRP = "orama";
const ED25519_KEY_TYPE = "/cosmos.crypto.ed25519.PubKey";
const CONSENSUS_ADDRESS_BYTES = 20;
const MAX_MONIKER = 64;
const COMET_PAGE_SIZE = 100;
/** The proxy caps /validators at 100 per page and 100 pages; the active set is far below one page's worth of pages. */
const COMET_MAX_PAGES = 5;

const COMMITTEE_QUERY = "query/orama.power.v1.Query/BootstrapCommittee";

function toHex(bytes: Uint8Array): string {
  return Array.from(bytes, (b) => b.toString(16).padStart(2, "0")).join("").toUpperCase();
}

function fromBase64(text: string, what: string): Uint8Array<ArrayBuffer> {
  let binary: string;
  try {
    binary = atob(text);
  } catch {
    throw new ChainReadError(`The chain sent a malformed ${what}.`);
  }
  const bytes = new Uint8Array(binary.length);
  for (let i = 0; i < binary.length; i++) bytes[i] = binary.charCodeAt(i);
  return bytes;
}

/** CometBFT's address of an ed25519 key: the first 20 bytes of its SHA-256. */
export async function consensusAddress(keyBase64: string): Promise<string> {
  const digest = await crypto.subtle.digest("SHA-256", fromBase64(keyBase64, "validator key"));
  return toHex(new Uint8Array(digest).slice(0, CONSENSUS_ADDRESS_BYTES));
}

interface StakingValidator {
  valoper: string;
  operator: string;
  consensus: string;
  moniker: string;
  jailed: boolean;
}

async function readStaking(raw: unknown): Promise<StakingValidator[]> {
  const list = arr(rec(raw, "validator list").validators, "validator list");
  return Promise.all(
    list.map(async (item) => {
      const v = rec(item, "validator");
      const valoper = str(v.operator_address, "validator operator address", 120);
      const operator = bech32Rehrp(valoper, ACCOUNT_HRP);
      if (operator === null) throw new ChainReadError("The chain sent a malformed validator operator address.");
      const key = rec(v.consensus_pubkey, "validator key");
      if (key["@type"] !== ED25519_KEY_TYPE) throw new ChainReadError("The chain sent a validator key of a type this explorer cannot read.");
      const description = v.description === undefined ? {} : rec(v.description, "validator description");
      return {
        valoper,
        operator,
        consensus: await consensusAddress(str(key.key, "validator key", 128)),
        moniker: clean(optStr(description.moniker, "validator moniker", 256), MAX_MONIKER),
        jailed: bool(v.jailed, "validator jailed flag"),
      };
    }),
  );
}

interface CometValidator {
  power: bigint;
}

function readComet(raw: unknown): { power: Map<string, CometValidator>; total: number } {
  const result = rpcResult(raw, "validator set");
  const power = new Map<string, CometValidator>();
  for (const item of arr(result.validators, "validator set")) {
    const v = rec(item, "validator");
    power.set(str(v.address, "validator address", 64).toUpperCase(), { power: BigInt(digits(v.voting_power, "voting power")) });
  }
  return { power, total: Number(digits(result.total, "validator count")) };
}

async function readCometSet(client: ChainClient): Promise<Map<string, CometValidator>> {
  const all = new Map<string, CometValidator>();
  for (let page = 1; page <= COMET_MAX_PAGES; page++) {
    const { power, total } = readComet(await client.get(`validators?page=${page}&per_page=${COMET_PAGE_SIZE}`));
    power.forEach((v, address) => all.set(address, v));
    if (all.size >= total) return all;
  }
  throw new ChainReadError("The validator set is larger than this explorer reads.");
}

function readCommittee(raw: unknown): Set<string> {
  const members = optArr(rec(raw, "bootstrap committee").members, "bootstrap committee");
  return new Set(members.map((m) => str(rec(m, "committee member").operator_address, "committee member", 120).toLowerCase()));
}

/** Reads the validator directory from the proxy. */
export async function loadDirectory(client: ChainClient): Promise<Directory> {
  const [stakingRaw, comet, committeeRaw] = await Promise.all([
    client.get(chainPath("staking", "validators")),
    readCometSet(client),
    client.get(COMMITTEE_QUERY),
  ]);
  const staking = await readStaking(stakingRaw);
  const committee = readCommittee(committeeRaw);
  const validators: DirectoryValidator[] = staking.map((s) => ({
    ref: { moniker: s.moniker === "" ? shortAddress(s.operator) : s.moniker, operator: s.operator },
    valoper: s.valoper,
    consensus: s.consensus,
    jailed: s.jailed,
    power: comet.get(s.consensus)?.power ?? 0n,
    committee: committee.has(s.operator),
  }));
  const totalPower = [...comet.values()].reduce((sum, v) => sum + v.power, 0n);
  return {
    validators,
    totalPower,
    byConsensus: new Map(validators.map((v) => [v.consensus, v])),
    byOperator: new Map(validators.map((v) => [v.ref.operator, v])),
  };
}
