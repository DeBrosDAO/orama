import { addr } from "./fake-gateway";

export const nanos = (iso: string) => String(BigInt(Date.parse(iso)) * 1_000_000n);
const NO_CONTENT = {
  parameter_change: null,
  software_upgrade: null,
  emission_split: null,
  development_spend: null,
  power_bounds: null,
  relay_reporters: null,
  allow_list: null,
};

export function proposalJson(id: number, content: Record<string, unknown>, over: Record<string, unknown> = {}) {
  return {
    id: String(id),
    proposer: addr(1),
    submit_unix_nano: nanos("2026-10-01T00:00:00Z"),
    voting_end_unix_nano: nanos("2026-10-15T00:00:00Z"),
    veto_end_unix_nano: "0",
    timelock_end_unix_nano: "0",
    status: "VOTING",
    content: { ...NO_CONTENT, ...content },
    token_yes: "700",
    token_no: "100",
    token_abstain: "0",
    operator_yes: "3",
    operator_no: "1",
    operator_abstain: "0",
    eligible_operators: "5",
    fail_reason: "",
    advance_failures: 0,
    ...over,
  };
}
