import { renderToStaticMarkup } from "react-dom/server";
import { MemoryRouter } from "react-router";
import { describe, expect, it } from "vitest";
import { ALICE, decode } from "../../data/chain/decoders/fixtures";
import { Sentence } from "../../ui/sentence";
import { MessageFields } from "./message-fields";

const NODE = "/orama.nodes.v1.";
const HTML = "<img src=x onerror=alert(1)><script>alert(2)</script>";

describe("MessageFields", () => {
  it("TestMessageFields_renders_chain_text_as_text_never_markup", () => {
    const m = decode(`${NODE}MsgRegisterCluster`, { operator: ALICE, cluster_id: "c1", base_domain: HTML, metadata_uri: HTML });
    const html = renderToStaticMarkup(<MessageFields message={m} />);
    expect(html).not.toContain("<img");
    expect(html).not.toContain("<script");
    expect(html).toContain("&lt;img src=x onerror=alert(1)&gt;");
  });

  it("TestMessageFields_the_sentence_is_text_too", () => {
    const m = decode(`${NODE}MsgRegisterNode`, { operator: ALICE, node_id: HTML });
    const html = renderToStaticMarkup(
      <MemoryRouter>
        <Sentence message={m} />
      </MemoryRouter>,
    );
    expect(html).not.toContain("<script");
    expect(html).not.toContain("<img");
    expect(html).toContain("&lt;img");
  });

  it("TestMessageFields_shows_a_norama_value_as_ORAMA_and_labels_every_row", () => {
    const m = decode(`${NODE}MsgBondNode`, { operator: ALICE, node_id: "n1", role: "ROLE_STORAGE", amount: "2500000000" });
    const html = renderToStaticMarkup(<MessageFields message={m} />);
    expect(html).toContain("<dt");
    expect(html).toContain("2.5 ORAMA");
    expect(html).toContain("Node ID");
  });

  it("TestMessageFields_renders_nothing_for_a_message_without_fields", () => {
    expect(renderToStaticMarkup(<MessageFields message={{ type: "unknown", typeUrl: "/x", signer: null }} />)).toBe("");
    const bare = decode("/orama.houses.v1.MsgUnlockHouseBond", { signer: ALICE });
    expect(renderToStaticMarkup(<MessageFields message={bare} />)).toBe("");
  });
});
