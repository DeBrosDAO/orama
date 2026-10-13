import { afterEach, describe, expect, it, vi } from "vitest";
import { confirmGateway, forgetConfirmedGateways, isConfirmed } from "./gate";
import { detectReset } from "./reset";

describe("detectReset", () => {
  it("TestDetectReset_nothing_to_say_while_the_gateway_has_not_answered", () => {
    expect(detectReset({ expected: "a-1", live: null, pinned: "a-0" })).toBeNull();
  });

  it("TestDetectReset_all_three_agree", () => {
    expect(detectReset({ expected: "a-1", live: "a-1", pinned: "a-1" })).toBeNull();
  });

  it("TestDetectReset_no_pin_and_a_custom_gateway_have_nothing_to_disagree_with", () => {
    expect(detectReset({ expected: "a-1", live: "a-1", pinned: null })).toBeNull();
    expect(detectReset({ expected: null, live: "x-9", pinned: null })).toBeNull();
    expect(detectReset({ expected: null, live: "x-9", pinned: "x-9" })).toBeNull();
  });

  it("TestDetectReset_the_registry_lists_another_chain_than_the_gateway_reports", () => {
    expect(detectReset({ expected: "a-2", live: "a-1", pinned: null })).toEqual({ kind: "registry", expected: "a-2", live: "a-1" });
    expect(detectReset({ expected: "a-2", live: "a-1", pinned: "a-1" })).toEqual({ kind: "registry", expected: "a-2", live: "a-1" });
  });

  it("TestDetectReset_a_link_made_on_another_chain_is_a_reset_even_when_the_registry_agrees_with_the_gateway", () => {
    expect(detectReset({ expected: "a-2", live: "a-2", pinned: "a-1" })).toEqual({ kind: "link", pinned: "a-1", live: "a-2" });
  });

  it("TestDetectReset_the_link_takes_precedence_over_the_registry_because_its_page_cannot_be_shown", () => {
    expect(detectReset({ expected: "a-3", live: "a-2", pinned: "a-1" })?.kind).toBe("link");
  });

  it("TestDetectReset_a_custom_gateway_that_was_reset_is_found_by_the_link_alone", () => {
    expect(detectReset({ expected: null, live: "x-2", pinned: "x-1" })).toEqual({ kind: "link", pinned: "x-1", live: "x-2" });
  });
});

describe("the gateway agreement", () => {
  afterEach(forgetConfirmedGateways);

  it("TestConfirmGateway_covers_only_the_gateway_the_reader_agreed_to", () => {
    expect(isConfirmed("https://a.example.org")).toBe(false);
    confirmGateway("https://a.example.org");
    expect(isConfirmed("https://a.example.org")).toBe(true);
    expect(isConfirmed("https://b.example.org")).toBe(false);
    expect(isConfirmed("https://a.example.org.evil.example")).toBe(false);
  });

  it("TestForgetConfirmedGateways_asks_again", () => {
    confirmGateway("https://a.example.org");
    forgetConfirmedGateways();
    expect(isConfirmed("https://a.example.org")).toBe(false);
  });

  it("TestConfirmGateway_survives_a_reload_through_the_tabs_storage_and_ignores_a_damaged_one", () => {
    const store = new Map<string, string>();
    vi.stubGlobal("sessionStorage", {
      getItem: (k: string) => store.get(k) ?? null,
      setItem: (k: string, v: string) => void store.set(k, v),
      removeItem: (k: string) => void store.delete(k),
    });
    try {
      confirmGateway("https://a.example.org");
      // A page load that has not made the agreement itself: only the storage remembers it.
      store.set("orama-explorer-confirmed-gateways", JSON.stringify(["https://c.example.org", 7, null]));
      expect(isConfirmed("https://c.example.org")).toBe(true);
      expect(isConfirmed("https://a.example.org")).toBe(true);
      store.set("orama-explorer-confirmed-gateways", "{not json");
      expect(isConfirmed("https://d.example.org")).toBe(false);
    } finally {
      vi.unstubAllGlobals();
    }
  });

  it("TestConfirmGateway_a_storage_that_refuses_writes_keeps_the_agreement_for_the_page", () => {
    vi.stubGlobal("sessionStorage", {
      getItem: () => null,
      setItem: () => {
        throw new Error("blocked");
      },
      removeItem: () => undefined,
    });
    try {
      confirmGateway("https://a.example.org");
      expect(isConfirmed("https://a.example.org")).toBe(true);
    } finally {
      vi.unstubAllGlobals();
    }
  });
});
