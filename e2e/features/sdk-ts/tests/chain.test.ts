// The read-only chain proxy (/v1/chain/*, docs/whitepaper/technical-reference/appendices/i-api-surface.md: "Read-only
// proxy of CometBFT status, blocks, transactions, validators, norama supply,
// and the staking pool. Open. The handler refuses every other path."). The
// SDK has no method for it, so it is read through the SDK's HttpClient.
import { SDKError } from "../../../../sdk/src/index";
import { http, mainURL, onFleet } from "./fleet";

const hasChain = process.env.E2E_CHAIN === "1";

describe.skipIf(!onFleet || !hasChain)("chain read proxy", () => {
  const client = () => http(mainURL());

  it("status, validators, supply and the staking pool answer without a credential", async () => {
    for (const path of ["status", "validators", "supply/norama", "staking/pool"]) {
      const body = await client().get(`/v1/chain/${path}`);
      expect(body).toBeTruthy();
    }
  });

  it("the latest block is served, and the height advances", async () => {
    const first = await client().get<any>("/v1/chain/status");
    const height = Number(first?.result?.sync_info?.latest_block_height ?? first?.sync_info?.latest_block_height);
    expect(height).toBeGreaterThan(0);
    const block = await client().get(`/v1/chain/block?height=${height}`);
    expect(block).toBeTruthy();
  });

  it("every other path, method and query is refused", async () => {
    // Each is given its handler as it is made: they run at once, and one
    // refused before the loop below reached it was an unhandled rejection.
    const bad = [
      client().get("/v1/chain/abci_query"),
      client().get("/v1/chain/broadcast_tx_commit?tx=0x00"),
      // A "../" segment is not sent: fetch resolves it before the request
      // leaves (/v1/chain/../status arrives as /v1/status, a public route
      // that answers 200). The handler's traversal refusal is a Go unit test
      // (chainread handler_test.go).
      client().get("/v1/chain/status/extra"),
      client().get("/v1/chain/block?height=abc"),
      client().get("/v1/chain/status?unexpected=1"),
      client().post("/v1/chain/status", {}),
    ].map((p) => p.catch((e) => e));
    for (const p of bad) {
      const err = await p;
      expect(err).toBeInstanceOf(SDKError);
      expect([400, 404, 405]).toContain((err as SDKError).httpStatus);
    }
  });
});
