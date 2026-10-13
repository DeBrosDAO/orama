import { classify } from "../../model/search";

/**
 * The wallet address in a `/wallet/:address` URL, lower-cased, or null when the
 * text is not a valid Orama wallet address (bad characters, bad checksum, or
 * another kind of identifier). Checked before any query, so junk in the URL
 * never reaches the data source.
 */
export function walletParam(raw: string | undefined): string | null {
  const lower = (raw ?? "").toLowerCase();
  const hit = classify(lower);
  return hit?.kind === "wallet" && hit.address === lower ? hit.address : null;
}
