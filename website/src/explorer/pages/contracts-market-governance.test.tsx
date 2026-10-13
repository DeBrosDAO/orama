import { describe, expect, it } from "vitest";
import { addr } from "../data/chain/fake-gateway";
import type { CodeInfo, HistoryEntry, StateEntry } from "../model/contracts";
import type { Governance, Proposal } from "../model/governance";
import type { IdPage } from "../model/paging";
import { failedPaged, loadingPaged, readyPaged, render } from "./asset-fixtures";
import { HistoryCard } from "./contract/history-card";
import { StateCard } from "./contract/state-card";
import { ContractAddressList } from "./contracts/address-list";
import { CodeList } from "./contracts/code-list";
import { ProposalList } from "./governance/proposal-list";
import { EnactedCard, ParamsCard } from "./governance/rules-cards";
import { StatusBadge } from "./governance/status-badge";
import { BidAnswer } from "./listing/bid-lookup";
import { TallyCard } from "./proposal/tally-card";
import { VoteAnswer } from "./proposal/vote-lookup";

const code = (id: number, over: Partial<CodeInfo> = {}): CodeInfo => ({ codeId: id, creator: addr(4), checksum: "ab".repeat(32), permission: "everybody", allowed: [], ...over });

describe("contracts", () => {
  it("TestCodeList_links_each_code_and_says_who_may_instantiate_it", () => {
    const html = render(<CodeList paged={readyPaged([code(1), code(2, { permission: "nobody" })], "a2V5")} />);
    expect(html).toContain('href="/explorer/code/1"');
    expect(html).toContain("Anyone may instantiate it");
    expect(html).toContain("No one may instantiate it");
    expect(html).toContain("abababababab…");
    expect(html).toContain("Show more codes");
  });

  it("TestCodeList_empty_loading_and_error_states", () => {
    expect(render(<CodeList paged={readyPaged<CodeInfo>([])} />)).toContain("No contract code has been stored on this chain yet.");
    expect(render(<CodeList paged={loadingPaged<CodeInfo>()} />)).toContain('aria-label="Loading"');
    expect(render(<CodeList paged={failedPaged<CodeInfo>("The chain could not be reached.")} />)).toContain("The chain could not be reached.");
  });

  it("TestContractAddressList_opens_each_contract_and_says_when_there_are_none", () => {
    const list = render(<ContractAddressList title="Contracts" empty="None." paged={readyPaged([addr(5), addr(6)])} />);
    expect(list).toContain(`href="/explorer/contract/${addr(5)}"`);
    expect(render(<ContractAddressList title="Contracts" empty="No contract here." paged={readyPaged<string>([])} />)).toContain("No contract here.");
  });

  it("TestHistoryCard_shows_each_operation_its_code_block_and_message", () => {
    const entries: HistoryEntry[] = [
      { operation: "init", codeId: 3, height: 120, message: '{"owner":"x"}' },
      { operation: "migrate", codeId: 4, height: 0, message: null },
    ];
    const html = render(<HistoryCard paged={readyPaged(entries)} />);
    expect(html).toContain("Instantiated");
    expect(html).toContain("Migrated");
    expect(html).toContain('href="/explorer/block/120"');
    expect(html).toContain("{&quot;owner&quot;:&quot;x&quot;}");
    expect(render(<HistoryCard paged={readyPaged<HistoryEntry>([])} />)).toContain("keeps no history");
  });

  it("TestStateCard_shows_raw_state_as_text_and_says_when_a_contract_stores_nothing", () => {
    const entries: StateEntry[] = [{ key: "config", value: '{"admin":"a"}' }, { key: "0x0001", value: "3 bytes, not text" }];
    const html = render(<StateCard paged={readyPaged(entries)} />);
    expect(html).toContain("config");
    expect(html).toContain("3 bytes, not text");
    expect(render(<StateCard paged={readyPaged<StateEntry>([])} />)).toContain("stores nothing");
    expect(render(<StateCard paged={failedPaged<StateEntry>("no node")} />)).toContain("no node");
  });
});

describe("market", () => {
  it("TestBidAnswer_shows_the_bidder_and_escrow_or_says_the_bid_is_not_held", () => {
    const html = render(<BidAnswer bid={{ id: 9, listingId: 5, bidder: addr(3), amount: "2500000000" }} id={9} />);
    expect(html).toContain("2.5 ORAMA");
    expect(html).toContain(`href="/explorer/wallet/${addr(3)}"`);
    expect(render(<BidAnswer bid={null} id={9} />)).toContain("holds no bid 9");
  });
});

const proposal = (id: number, over: Partial<Proposal> = {}): Proposal => ({
  id,
  proposer: addr(1),
  status: "voting",
  submittedAt: "2026-10-01T00:00:00.000Z",
  votingEndsAt: "2026-10-15T00:00:00.000Z",
  vetoEndsAt: null,
  timelockEndsAt: null,
  content: { kind: "Software upgrade", facts: [{ label: "Upgrade name", value: "v2", kind: "text" }] },
  tokens: { yes: "700000000000", no: "100000000000", abstain: "0" },
  operators: { yes: 3, no: 1, abstain: 0, eligible: 5 },
  failReason: "",
  ...over,
});
const idPage = (items: Proposal[], next: number | null): IdPage<Proposal> => ({ newest: 9, items, next });

describe("governance", () => {
  it("TestProposalList_shows_kind_status_proposer_and_pages_older", () => {
    const html = render(<ProposalList page={idPage([proposal(9), proposal(8, { status: "executed" })], 7)} atNewest />);
    expect(html).toContain('href="/explorer/proposal/9"');
    expect(html).toContain("Software upgrade");
    expect(html).toContain("Voting");
    expect(html).toContain("Executed");
    expect(html).toContain("?from=7");
  });

  it("TestProposalList_no_proposals_and_a_page_past_the_oldest_say_different_things", () => {
    expect(render(<ProposalList page={idPage([], null)} atNewest />)).toContain("No proposal has been submitted on this chain yet.");
    expect(render(<ProposalList page={idPage([], null)} atNewest={false} />)).toContain("no proposal at that number");
  });

  it("TestStatusBadge_reads_each_status_in_words", () => {
    expect(render(<StatusBadge status="veto_window" />)).toContain("Veto window");
    expect(render(<StatusBadge status="failed" />)).toContain("Failed");
  });

  it("TestTallyCard_shows_both_houses_and_says_the_chain_lists_no_voters", () => {
    const html = render(<TallyCard proposal={proposal(1)} />);
    expect(html).toContain("700 ORAMA");
    expect(html).toContain("Token house");
    expect(html).toContain("Operator house");
    expect(html).toContain("the chain lists no voters");
  });

  it("TestVoteAnswer_names_the_option_or_says_there_is_no_vote", () => {
    expect(render(<VoteAnswer vote={{ proposalId: 1, voter: addr(1), option: "yes" }} />)).toContain("voted yes");
    expect(render(<VoteAnswer vote={{ proposalId: 1, voter: addr(1), option: "abstain" }} />)).toContain("abstained");
    expect(render(<VoteAnswer vote={null} />)).toContain("holds no vote from this address");
  });

  const governance: Governance = {
    params: {
      bootstrapExitStake: "271000000000000",
      tokenQuorum: 0.334,
      tokenPassThreshold: 0.5,
      votingPeriodSeconds: 1_209_600,
      houseBond: "5000000000",
      maxEligiblePerPrefix16: 2,
      maxEligiblePerAsn: 3,
      minHouseSize: 5,
      vetoWindowSeconds: 604_800,
      parameterTimelockSeconds: 1_209_600,
      upgradeTimelockSeconds: 5_184_000,
      spendTimelockSeconds: 604_800,
    },
    enacted: { scheduledUpgrade: null, emissionSplit: null, powerBoundsActive: false, relayReporters: [], codeUploadAllow: [addr(2)], adapterAllow: [] },
  };

  it("TestParamsCard_states_the_rules_in_words", () => {
    const html = render(<ParamsCard params={governance.params} />);
    expect(html).toContain("14 days");
    expect(html).toContain("33.4% of bonded stake must take part");
    expect(html).toContain("at most 2 in one /16 network and 3 in one ASN");
    expect(html).toContain("60 days");
  });

  it("TestEnactedCard_says_when_nothing_was_enacted_and_lists_what_was", () => {
    const html = render(<EnactedCard enacted={governance.enacted} />);
    expect(html).toContain("The genesis split stands");
    expect(html).toContain("Not activated");
    expect(html).toContain(`href="/explorer/wallet/${addr(2)}"`);
    const set = render(
      <EnactedCard enacted={{ ...governance.enacted, scheduledUpgrade: { name: "v2", height: 5000 }, emissionSplit: { validator: 55, storage: 30, relay: 10, development: 5 } }} />,
    );
    expect(set).toContain("v2 at block 5,000");
    expect(set).toContain("55% validators");
  });
});
