/** A transaction hash is SHA-256, written as 64 hexadecimal characters. */
export const TX_HASH_LENGTH = 64;

const HEX_HASH = new RegExp(`^[0-9a-fA-F]{${TX_HASH_LENGTH}}$`);

/**
 * The canonical (upper-case) form of a hash from the URL, or null when the
 * text cannot be a transaction hash at all.
 */
export function normalizeTxHash(raw: string | undefined): string | null {
  if (raw === undefined || !HEX_HASH.test(raw)) return null;
  return raw.toUpperCase();
}
