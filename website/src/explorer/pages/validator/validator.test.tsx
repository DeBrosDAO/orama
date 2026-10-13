import { renderToStaticMarkup } from "react-dom/server";
import type { ReactNode } from "react";
import { MemoryRouter, Route, Routes } from "react-router";
import { describe, expect, it } from "vitest";
import { ExplorerApp } from "../../explorer-app";
import type { QueryState } from "../../data/query-state";
import { LOADING } from "../../data/query-state";
import type { Handover, OperatorGroups, Validator, ValidatorDetail, ValidatorEpoch, ValidatorHistory, ValidatorSigning } from "../../model/types";
import { HandoverCard } from "../validators/handover-card";
import { OperatorsCard } from "../validators/operators-card";
import { summarize } from "../validators/summary";
import { ValidatorTable } from "../validators/validator-table";
import { ValidatorHeader } from "./validator-header";
import { ValidatorHistoryView } from "./validator-history";
import { ValidatorStats } from "./validator-stats";

const OPERATOR = "orama1sx0x6xpv8wtgshrajsqgednruq26aes0h2jh07";
const render = (ui: ReactNode) => renderToStaticMarkup(<MemoryRouter>{ui}</MemoryRouter>);
const ready = <T,>(data: T): QueryState<T> => ({ status: "ready", data, error: null });
const failed = <T,>(message: string): QueryState<T> => ({ status: "error", data: null, error: new Error(message) });

const validator = (over: Partial<Validator> = {}): Validator => ({
  ref: { moniker: "Alpha", operator: OPERATOR },
  type: "committee",
  power: 0.333,
  jailed: false,
  status: "bonded",
  commission: 0.1,
  tokens: "2000000000000",
  ...over,
});

const detail = (over: Partial<ValidatorDetail> = {}): ValidatorDetail => ({
  validator: validator(),
  website: "",
  details: "",
  commission: { rate: 0.1, maxRate: 0.2, maxChangePerDay: 0.01, updatedAt: null },
  minSelfDelegation: "1000000000",
  selfBond: "1500000000000",
  delegatedByOthers: "500000000000",
  unbondsAt: null,
  power: { cometPower: "300000000", operator: { kind: "registered", address: "orama1operator" } },
  ...over,
});

const epoch = (n: number, over: Partial<ValidatorEpoch> = {}): ValidatorEpoch => ({
  epoch: n,
  cometPower: 300_000_000 + n,
  signedBlocks: 95,
  missedBlocks: 5,
  uptime: 0.95,
  slashes: 0,
  slashedBurned: "0",
  jailed: false,
  status: "bonded",
  ...over,
});

describe("ValidatorHeader", () => {
  it("TestValidatorHeader_names_the_validator_its_state_and_operator", () => {
    const html = render(<ValidatorHeader detail={detail()} />);
    expect(html).toContain("Alpha");
    expect(html).toContain("Active");
    expect(html).toContain("Founding committee");
    expect(html).toContain(OPERATOR);
    expect(html).toContain(`href="/explorer/wallet/${OPERATOR}"`);
    expect(html).not.toContain("jailed");
  });

  it("TestValidatorHeader_a_jailed_validator_says_it_signs_nothing", () => {
    const html = render(<ValidatorHeader detail={detail({ validator: validator({ jailed: true, status: "unbonding" }) })} />);
    expect(html).toContain("Jailed");
    expect(html).toContain("signs no blocks until it is unjailed");
  });

  it("TestValidatorHeader_an_unbonding_validator_says_when_it_finishes", () => {
    const html = render(<ValidatorHeader detail={detail({ validator: validator({ status: "unbonding" }), unbondsAt: "2026-10-20T00:00:00.000Z" })} />);
    expect(html).toContain("not in the active set");
    expect(html).toContain("2026-10-20 00:00:00 UTC");
  });

  it("TestValidatorHeader_text_the_validator_wrote_is_text_and_its_website_is_never_a_link", () => {
    const html = render(
      <ValidatorHeader
        detail={detail({
          validator: validator({ ref: { moniker: "<img src=x onerror=alert(1)>", operator: OPERATOR } }),
          website: "javascript:alert(1)",
          details: "<script>alert(2)</script>",
        })}
      />,
    );
    expect(html).not.toContain("<img");
    expect(html).not.toContain("<script");
    expect(html).toContain("&lt;img src=x onerror=alert(1)&gt;");
    expect(html).toContain("javascript:alert(1)");
    expect(html).not.toMatch(/href="javascript/);
  });

  it("TestValidatorHeader_links_stay_inside_the_network_being_read", () => {
    expect(render(<ValidatorHeader detail={detail()} />)).toContain('href="/explorer/wallet/');
  });
});

describe("ValidatorStats", () => {
  it("TestValidatorStats_shows_power_commission_self_bond_and_total_delegated", () => {
    const html = render(<ValidatorStats detail={detail()} />);
    expect(html).toContain("33.3%");
    expect(html).toContain("300,000,000 CometBFT power");
    expect(html).toContain("10.0%");
    expect(html).toContain("max 20.0%, moves at most 1.0% a day");
    expect(html).toContain("1,500");
    expect(html).toContain("2,000");
    expect(html).toContain("500 ORAMA is from other delegators");
  });

  it("TestValidatorStats_a_validator_x_power_does_not_list_says_so_instead_of_a_power", () => {
    expect(render(<ValidatorStats detail={detail({ power: null })} />)).toContain("x/power does not list it");
  });

  it("TestValidatorStats_a_changed_commission_names_the_day", () => {
    const html = render(<ValidatorStats detail={detail({ commission: { rate: 0.05, maxRate: 1, maxChangePerDay: 0.01, updatedAt: "2026-10-01T00:00:00.000Z" } })} />);
    expect(html).toContain("changed 1 Oct 2026");
  });
});

describe("ValidatorHistoryView", () => {
  const history = (over: Partial<ValidatorHistory> = {}): ValidatorHistory => ({ epochs: [epoch(2), epoch(1, { missedBlocks: 15, signedBlocks: 85, uptime: 0.85 })], slashes: [], jails: [], ...over });

  it("TestValidatorHistory_loading_is_announced_as_a_status", () => {
    expect(render(<ValidatorHistoryView state={LOADING} onRetry={() => undefined} />)).toContain('role="status"');
  });

  it("TestValidatorHistory_a_node_without_an_indexer_says_what_is_missing_and_why_and_offers_a_retry", () => {
    const html = render(<ValidatorHistoryView state={failed("The chain could not be read (HTTP 503).")} onRetry={() => undefined} />);
    expect(html).toContain('role="alert"');
    expect(html).toContain("come from the chain indexer");
    expect(html).toContain("The chain could not be read (HTTP 503).");
    expect(html).toContain("Try again");
  });

  it("TestValidatorHistory_a_validator_the_index_has_not_seen_says_so", () => {
    expect(render(<ValidatorHistoryView state={ready(null)} onRetry={() => undefined} />)).toContain("has not seen this validator yet");
  });

  it("TestValidatorHistory_shows_uptime_for_the_last_epoch_and_the_window_and_the_epoch_table", () => {
    const html = render(<ValidatorHistoryView state={ready(history())} onRetry={() => undefined} />);
    expect(html).toContain("95.0%");
    expect(html).toContain("5 of 100 blocks missed");
    expect(html).toContain("90.0%");
    expect(html).toContain("20 blocks missed in all");
    expect(html).toContain("<caption");
    expect(html).toContain('scope="col"');
  });

  it("TestValidatorHistory_no_slash_and_no_jail_is_a_statement_not_a_blank", () => {
    expect(render(<ValidatorHistoryView state={ready(history())} onRetry={() => undefined} />)).toContain("No slash and no jail period recorded since the index began.");
  });

  it("TestValidatorHistory_lists_a_slash_with_its_reason_and_burned_amount_and_a_jail_period", () => {
    const html = render(
      <ValidatorHistoryView
        state={ready(
          history({
            epochs: [epoch(5, { slashes: 1, jailed: true })],
            slashes: [{ height: 210, time: "2026-10-05T10:00:00.000Z", epoch: 5, reason: "double_sign", power: 3, burned: "5000000000" }],
            jails: [{ jailedHeight: 210, jailedTime: null, reason: "double_sign", unjailedHeight: null, unjailedTime: null }],
          }),
        )}
        onRetry={() => undefined}
      />,
    );
    expect(html).toContain("1 slash, 5 ORAMA burned in all");
    expect(html).toContain("Signed two blocks at one height");
    expect(html).toContain("Jailed at block 210, and is still jailed.");
    expect(html).toContain("1 slash");
  });

  it("TestValidatorHistory_an_epoch_with_no_commit_shows_a_dash_not_zero_percent", () => {
    const html = render(<ValidatorHistoryView state={ready(history({ epochs: [epoch(3, { uptime: null, signedBlocks: 0, missedBlocks: 0 })] }))} onRetry={() => undefined} />);
    expect(html).not.toContain("0.0%");
    expect(html).toContain("–");
  });

  it("TestValidatorHistory_a_long_history_shows_a_few_epochs_and_offers_the_rest", () => {
    const epochs = Array.from({ length: 12 }, (_, i) => epoch(12 - i));
    const html = render(<ValidatorHistoryView state={ready(history({ epochs }))} onRetry={() => undefined} />);
    expect(html).toContain("Show all 12 epochs");
    expect(html).toContain('aria-expanded="false"');
  });

  it("TestValidatorHistory_a_validator_with_no_closed_epoch_says_so", () => {
    expect(render(<ValidatorHistoryView state={ready(history({ epochs: [] }))} onRetry={() => undefined} />)).toContain("No closed epoch yet.");
  });
});

describe("the validators page parts", () => {
  const set = [validator(), validator({ ref: { moniker: "Beta", operator: "orama1beta" }, type: "community", power: 0.1, jailed: true, status: "unbonding", commission: 0.05, tokens: "1500000000000" })];

  it("TestValidatorTable_rows_open_the_validator_page_and_show_commission_and_staked", () => {
    const html = render(<ValidatorTable validators={set} signing={LOADING} />);
    expect(html).toContain(`href="/explorer/validator/${OPERATOR}"`);
    expect(html).toContain("10.0%");
    expect(html).toContain("2,000");
    expect(html).toContain("jailed");
    expect(html).toContain("<caption");
  });

  it("TestValidatorTable_uptime_comes_from_the_indexer_per_validator", () => {
    const signing: ValidatorSigning[] = [{ operator: OPERATOR, epoch: 12, uptime: 0.9, missedBlocks: 100, slashes: 0 }];
    const html = render(<ValidatorTable validators={set} signing={ready(signing)} />);
    expect(html).toContain("90.0%");
    expect(html).toContain("100 blocks missed in epoch 12");
  });

  it("TestValidatorTable_an_indexer_failure_leaves_the_table_and_says_why_uptime_is_missing", () => {
    const html = render(<ValidatorTable validators={set} signing={failed("The chain could not be read (HTTP 503).")} />);
    expect(html).toContain("Alpha");
    expect(html).toContain("n/a");
    expect(html).toContain("Uptime comes from the chain indexer and could not be read: The chain could not be read (HTTP 503).");
  });

  const handover: Handover = {
    lambda: 0.0369,
    stake: { bonded: "10000000000000", exit: "271000000000000" },
    epochs: { current: 7, deadline: 365 },
    preGateCap: 0.95,
    cap: { current: 0.05, normal: 0.05, reduced: 0.03, stepDownCount: 60 },
  };

  it("TestHandoverCard_shows_what_moves_lambda_as_labelled_progress_bars", () => {
    const html = render(<HandoverCard summary={summarize({ lambda: 0.0369, nakamoto: 1, totalStaked: "0", jailed: 0, validators: set, handover })} handover={handover} />);
    expect(html).toContain('role="progressbar"');
    expect(html).toContain('aria-label="Stake"');
    expect(html).toContain('aria-valuenow="4"');
    expect(html).toContain('aria-label="Time"');
    expect(html).toContain("epoch 7 of 365");
    expect(html).toContain("at least 40 stake-backed validators");
    expect(html).toContain("The chain does not say whether that has happened yet.");
    expect(html).toContain("λ = 0.04");
  });

  const groups: OperatorGroups = {
    groups: [
      { operator: { kind: "registered", address: "orama1operator" }, validators: set, power: 0.433 },
      { operator: { kind: "unlinked" }, validators: [validator({ ref: { moniker: "Gamma", operator: "orama1gamma" } })], power: 0.1 },
    ],
    unresolved: 3,
  };

  it("TestOperatorsCard_groups_validators_by_operator_with_the_cap_and_names_the_unlinked_bucket", () => {
    const html = render(<OperatorsCard state={ready(groups)} onRetry={() => undefined} cap={handover.cap} />);
    expect(html).toContain("2 validators");
    expect(html).toContain("43.3%");
    expect(html).toContain("No registered node");
    expect(html).toContain("at most 5.0%");
    expect(html).toContain("3 smaller validators are not");
    expect(html).toContain(`href="/explorer/validator/${OPERATOR}"`);
  });

  it("TestOperatorsCard_loading_and_failure_are_states_of_the_card", () => {
    expect(render(<OperatorsCard state={LOADING} onRetry={() => undefined} cap={handover.cap} />)).toContain('role="status"');
    const html = render(<OperatorsCard state={failed("The chain could not be reached.")} onRetry={() => undefined} cap={handover.cap} />);
    expect(html).toContain("The chain could not be reached.");
    expect(html).toContain("Try again");
  });

  it("TestOperatorsCard_no_active_validator_is_an_empty_state", () => {
    expect(render(<OperatorsCard state={ready({ groups: [], unresolved: 0 })} onRetry={() => undefined} cap={handover.cap} />)).toContain("No active validator has an operator to group.");
  });
});

/** The explorer mounted the way the site mounts it: under explorer/*. */
const app = (path: string) =>
  renderToStaticMarkup(
    <MemoryRouter initialEntries={[path]}>
      <Routes>
        <Route path="explorer/*" element={<ExplorerApp />} />
      </Routes>
    </MemoryRouter>,
  );

describe("the validator page route", () => {
  it("TestValidatorPage_an_address_that_is_not_a_wallet_is_not_found_without_asking_the_chain", () => {
    const html = app("/explorer/validator/not-an-address");
    expect(html).toContain("Validator not found");
    expect(html).toContain("does not look like an Orama wallet address");
  });

  it("TestValidatorPage_a_valid_operator_starts_in_the_loading_state", () => {
    const html = app(`/explorer/validator/${OPERATOR}`);
    expect(html).toContain('role="status"');
    expect(html).not.toContain("Validator not found");
  });
});
