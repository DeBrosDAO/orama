const TYPE_KEY = "@type";

function isRecord(v: unknown): v is Record<string, unknown> {
  return typeof v === "object" && v !== null && !Array.isArray(v);
}

/**
 * The message type URLs inside a transaction's raw JSON body, in order. Empty
 * when the text is not JSON or carries no messages; the raw JSON is shown
 * beside it, so a reader can always see why.
 */
export function messageTypeUrls(rawJson: string): string[] {
  let parsed: unknown;
  try {
    parsed = JSON.parse(rawJson);
  } catch {
    return [];
  }
  if (!isRecord(parsed) || !isRecord(parsed.body) || !Array.isArray(parsed.body.messages)) return [];
  const urls: string[] = [];
  for (const m of parsed.body.messages) {
    if (isRecord(m) && typeof m[TYPE_KEY] === "string") urls.push(m[TYPE_KEY]);
  }
  return urls;
}
