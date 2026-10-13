/** Whatever was thrown, as an Error a page can show; an Error passes through untouched. */
export function toError(thrown: unknown): Error {
  return thrown instanceof Error ? thrown : new Error(String(thrown));
}
