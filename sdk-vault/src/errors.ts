import type { GuardianResult } from "./types";

/**
 * A write did not reach enough guardians to be durable, or a read did not
 * find enough shares of one version to reconstruct.
 *
 * `store` and `delete` used to return `{ quorumMet: false }` and resolve
 * normally. A caller that did not inspect that field — the obvious way to use a
 * promise that resolves — believed a secret had been saved when it had not, and
 * would find it unrecoverable later, with nothing at the time of the write to
 * say so.
 */
export class QuorumError extends Error {
  /** Guardians that acknowledged. */
  readonly ackCount: number;
  /** Guardians the operation needed. */
  readonly required: number;
  /** Guardians it tried. */
  readonly totalContacted: number;
  /** What each guardian did, including the ones that failed to authenticate. */
  readonly guardianResults: GuardianResult[];

  constructor(
    message: string,
    detail: {
      ackCount: number;
      required: number;
      totalContacted: number;
      guardianResults?: GuardianResult[];
    },
  ) {
    super(message);
    this.name = "QuorumError";
    this.ackCount = detail.ackCount;
    this.required = detail.required;
    this.totalContacted = detail.totalContacted;
    this.guardianResults = detail.guardianResults ?? [];
  }
}
