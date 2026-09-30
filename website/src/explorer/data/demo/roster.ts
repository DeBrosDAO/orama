import type { ValidatorRef, WalletRef } from "../../model/types";
import { addressFor } from "./ids";

export const FOUNDATION_LABEL = "Orama Foundation";

export interface ValidatorSeed {
  moniker: string;
  type: "committee" | "community";
  /** Relative weight inside its group. */
  weight: number;
  /** Jailed for missing blocks; still a validator, with no power. */
  jailed: boolean;
}

export const VALIDATOR_SEEDS: readonly ValidatorSeed[] = [
  { moniker: "val-1", type: "committee", weight: 0.34, jailed: false },
  { moniker: "val-2", type: "committee", weight: 0.33, jailed: false },
  { moniker: "val-3", type: "committee", weight: 0.33, jailed: false },
  { moniker: "val-4", type: "community", weight: 1, jailed: false },
  { moniker: "val-5", type: "community", weight: 1, jailed: true },
  { moniker: "val-6", type: "community", weight: 1, jailed: false },
  { moniker: "val-7", type: "community", weight: 1, jailed: false },
];

export const PROVIDER_LABELS = ["Storage node fra-1", "Storage node ams-2", "Storage node sgp-1"] as const;

export const foundationRef = (): WalletRef => ({
  address: addressFor("foundation"),
  label: FOUNDATION_LABEL,
  verified: true,
});

export const operatorRef = (moniker: string): WalletRef => ({
  address: addressFor(`operator:${moniker}`),
  label: `${moniker} operator`,
});

export const validatorRef = (moniker: string): ValidatorRef => ({
  moniker,
  operator: addressFor(`operator:${moniker}`),
});

export const providerRef = (label: string): WalletRef => ({ address: addressFor(`provider:${label}`), label });

export const userRef = (n: number): WalletRef => ({ address: addressFor(`user:${n}`) });
