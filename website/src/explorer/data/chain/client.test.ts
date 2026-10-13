import { describe, expect, it } from "vitest";
import { ChainIndexUnavailable, ChainNotFound, ChainReadError, CHAIN_BASE, DEFAULT_GATEWAY_ORIGIN, chainBase, createClient } from "./client";

describe("chainBase", () => {
  it("TestChainBase_puts_the_chain_proxy_on_the_gateways_origin", () => {
    expect(chainBase("https://stagenet.orama.network")).toBe("https://stagenet.orama.network/v1/chain");
    expect(chainBase("https://gw.example.org:8443")).toBe("https://gw.example.org:8443/v1/chain");
  });

  it("TestChainBase_default_is_a_gateway_not_the_page_origin", () => {
    // The page is served from orama.network, which has no chain behind it: a relative base would read the site shell.
    expect(CHAIN_BASE).toBe(`${DEFAULT_GATEWAY_ORIGIN}/v1/chain`);
    expect(CHAIN_BASE.startsWith("https://")).toBe(true);
  });

  it("TestChainBase_allows_a_gateway_on_this_machine_over_http", () => {
    expect(chainBase("http://localhost:6001")).toBe("http://localhost:6001/v1/chain");
    expect(chainBase("http://127.0.0.1:6001")).toBe("http://127.0.0.1:6001/v1/chain");
    expect(chainBase("https://localhost")).toBe("https://localhost/v1/chain");
  });

  it.each([
    ["an empty string", ""],
    ["a bare host", "stagenet.orama.network"],
    ["clear http on a public host", "http://stagenet.orama.network"],
    ["a trailing slash", "https://stagenet.orama.network/"],
    ["a path", "https://stagenet.orama.network/v1/chain"],
    ["a query", "https://stagenet.orama.network?x=1"],
    ["credentials in the address", "https://user:pw@stagenet.orama.network"],
    ["another scheme", "ftp://stagenet.orama.network"],
    ["a lookalike of localhost over http", "http://localhost.evil.example"],
    ["a lookalike of 127.0.0.1 over http", "http://127.0.0.1.evil.example"],
    ["credentials on a local address", "http://user:pw@localhost:6001"],
    ["an upper-case host", "https://Stagenet.orama.network"],
    ["an explicit default port", "https://stagenet.orama.network:443"],
    ["a fragment", "https://stagenet.orama.network#x"],
  ])("TestChainBase_refuses_%s", (_name, origin) => {
    expect(() => chainBase(origin)).toThrow(ChainReadError);
  });
});

describe("createClient", () => {
  const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status });

  it("TestCreateClient_leaves_caching_to_the_gateways_cache_control", async () => {
    let seen: RequestInit | undefined;
    const client = createClient(async (_url, init) => {
      seen = init;
      return json({ ok: true });
    });
    await client.get("index/epochs/3");
    expect(seen?.cache).toBe("default");
    expect(seen?.credentials).toBe("omit");
  });

  it("TestCreateClient_reads_from_the_gateway_origin_without_credentials", async () => {
    const seen: { url: string; init?: RequestInit }[] = [];
    const client = createClient(async (url, init) => {
      seen.push({ url, init });
      return json({ ok: true });
    });
    expect(await client.get("status")).toEqual({ ok: true });
    expect(seen[0]?.url).toBe(`${DEFAULT_GATEWAY_ORIGIN}/v1/chain/status`);
    // Nothing of the reader's (cookies, tokens) goes to another origin, and the request stays a simple one: no preflight.
    expect(seen[0]?.init?.credentials).toBe("omit");
    expect(seen[0]?.init?.method).toBe("GET");
    expect(seen[0]?.init?.headers).toEqual({ Accept: "application/json" });
  });

  it("TestCreateClient_takes_another_networks_base", async () => {
    let url = "";
    const client = createClient(async (u) => {
      url = u;
      return json({});
    }, chainBase("https://testnet.example.org"));
    await client.get("block?height=1");
    expect(url).toBe("https://testnet.example.org/v1/chain/block?height=1");
  });

  it("TestCreateClient_a_404_is_not_found_and_other_failures_are_read_errors", async () => {
    const notFound = createClient(async () => new Response("", { status: 404 }));
    await expect(notFound.get("index/txs/ab")).rejects.toBeInstanceOf(ChainNotFound);
    const limited = createClient(async () => new Response("rate limit exceeded", { status: 429 }));
    await expect(limited.get("status")).rejects.toThrow("HTTP 429");
  });

  it("TestCreateClient_a_503_from_a_node_with_no_indexer_is_told_apart_from_any_other_503", async () => {
    const noIndexer = createClient(
      async () => new Response("this node runs no chain indexer; install it with orama global install --services chain,indexer\n", { status: 503 }),
    );
    await expect(noIndexer.get("index/epochs")).rejects.toBeInstanceOf(ChainIndexUnavailable);
    await expect(noIndexer.get("index/epochs")).rejects.not.toThrow(/orama global install/);
    const busy = createClient(async () => new Response("too many chain queries in flight", { status: 503 }));
    const error = await busy.get("query/orama.fees.v1.Query/BaseFee").catch((e: unknown) => e);
    expect(error).toBeInstanceOf(ChainReadError);
    expect(error).not.toBeInstanceOf(ChainIndexUnavailable);
    expect((error as Error).message).toContain("HTTP 503");
  });

  it("TestCreateClient_a_blocked_or_unreachable_gateway_names_the_gateway_and_the_likely_causes", async () => {
    // A CORS refusal, an untrusted certificate and a dead gateway are the same to a script: fetch rejects.
    const client = createClient(
      async () => {
        throw new TypeError("net::ERR_CERT_AUTHORITY_INVALID");
      },
      "https://stagenet.orama.network/v1/chain",
    );
    const error = (await client.get("status").catch((e: unknown) => e)) as Error;
    expect(error).toBeInstanceOf(ChainReadError);
    expect(error.message).toContain("The chain could not be reached.");
    expect(error.message).toContain("the gateway at stagenet.orama.network");
    expect(error.message).toMatch(/down/);
    expect(error.message).toMatch(/TLS certificate .* staging certificate/);
    expect(error.message).toMatch(/CORS/);
    expect(error.message).toContain("https://stagenet.orama.network/v1/chain/status");
    // What the browser reported stays out of the reader's message.
    expect(error.message).not.toContain("ERR_CERT");
  });

  it("TestCreateClient_a_gateway_that_does_not_answer_in_time_says_how_long_it_waited", async () => {
    const client = createClient(async () => {
      throw new DOMException("signal timed out", "TimeoutError");
    }, "https://gw.example.org:8443/v1/chain");
    await expect(client.get("status")).rejects.toThrow("The gateway at gw.example.org:8443 did not answer within 15 seconds.");
  });

  it("TestCreateClient_the_unreachable_message_lists_rate_limiting_among_the_causes", async () => {
    const error = (await createClient(async () => {
      throw new TypeError("Failed to fetch");
    }, "https://stagenet.orama.network/v1/chain").get("status").catch((e: unknown) => e)) as Error;
    expect(error.message).toMatch(/limiting how fast this address may read/);
    expect(error.message).toMatch(/a normal answer to CORS or the rate limit/);
  });

  it("TestCreateClient_the_status_page_it_points_to_keeps_the_scheme_of_the_gateway", async () => {
    // A gateway on this machine is http: the address to open must not be turned into https.
    const failing = async () => {
      throw new TypeError("Failed to fetch");
    };
    const local = (await createClient(failing, "http://localhost:5173/v1/chain").get("status").catch((e: unknown) => e)) as Error;
    expect(local.message).toContain("Open http://localhost:5173/v1/chain/status in a new tab");
    expect(local.message).not.toContain("https://localhost");
    const remote = (await createClient(failing, "https://gw.example.org:8443/v1/chain").get("status").catch((e: unknown) => e)) as Error;
    expect(remote.message).toContain("Open https://gw.example.org:8443/v1/chain/status in a new tab");
  });

  it("TestCreateClient_a_rate_limited_read_says_to_wait", async () => {
    const base = "https://stagenet.orama.network/v1/chain";
    await expect(createClient(async () => new Response("slow down", { status: 429 }), base).get("status")).rejects.toThrow(
      "The chain could not be read (HTTP 429). stagenet.orama.network is limiting how fast this browser may read; wait a minute and try again.",
    );
  });

  it("TestCreateClient_a_502_is_not_only_a_dead_node_it_can_be_an_address_the_indexer_refused", async () => {
    const base = "https://stagenet.orama.network/v1/chain";
    const error = (await createClient(async () => new Response("chain request failed", { status: 502 }), base).get("index/accounts/x").catch((e: unknown) => e)) as Error;
    expect(error.message).toContain("HTTP 502");
    expect(error.message).toMatch(/refused part of this request \(check that the address or id is valid\)/);
    expect(error.message).toMatch(/chain node/);
  });

  it("TestCreateClient_a_503_that_is_not_the_missing_indexer_says_busy_not_a_missing_indexer", async () => {
    const base = "https://stagenet.orama.network/v1/chain";
    for (const body of ["the chain index is busy; try again", "too many chain queries in flight"]) {
      const error = (await createClient(async () => new Response(body, { status: 503 }), base).get("status").catch((e: unknown) => e)) as Error;
      expect(error).not.toBeInstanceOf(ChainIndexUnavailable);
      expect(error.message).toContain("HTTP 503");
      expect(error.message).toMatch(/busy or cannot serve this read just now/);
      expect(error.message).not.toMatch(/chain node/);
    }
  });

  it("TestCreateClient_a_504_says_the_node_was_too_slow_and_any_other_status_is_plain", async () => {
    const base = "https://stagenet.orama.network/v1/chain";
    await expect(createClient(async () => new Response("late", { status: 504 }), base).get("status")).rejects.toThrow(/HTTP 504\)\. stagenet\.orama\.network did not get an answer from its chain node in time/);
    await expect(createClient(async () => new Response("boom", { status: 500 }), base).get("status")).rejects.toThrow(/^The chain could not be read \(HTTP 500\)\.$/);
  });
});
