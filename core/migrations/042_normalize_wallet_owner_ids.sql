-- Normalize wallet-keyed owner ids.
--
-- namespace_ownership stored the wallet exactly as the client sent it, while
-- wallet_api_keys lowercased it. The same wallet therefore owned a namespace
-- under one spelling (EIP-55 checksummed) and was looked up under another
-- (lowercase), so an ownership check could fail for the actual owner, and two
-- rows could accumulate for one wallet.
--
-- Writes are normalised from bug-329 onward; this brings existing rows in line.
-- Idempotent: re-running changes nothing once every row is already lowercase.

-- Drop rows that would collide with an already-normalised row for the same
-- namespace, so the UPDATE below cannot violate uniqueness.
DELETE FROM namespace_ownership
 WHERE owner_type = 'wallet'
   AND owner_id <> LOWER(owner_id)
   AND EXISTS (
       SELECT 1 FROM namespace_ownership AS keep
        WHERE keep.namespace_id = namespace_ownership.namespace_id
          AND keep.owner_type = 'wallet'
          AND keep.owner_id = LOWER(namespace_ownership.owner_id)
   );

UPDATE namespace_ownership
   SET owner_id = LOWER(owner_id)
 WHERE owner_type = 'wallet'
   AND owner_id <> LOWER(owner_id);

-- Same treatment for the wallet -> api_key linkage, which is lowercased on
-- write today but may hold rows from before that.
DELETE FROM wallet_api_keys
 WHERE wallet <> LOWER(wallet)
   AND EXISTS (
       SELECT 1 FROM wallet_api_keys AS keep
        WHERE keep.namespace_id = wallet_api_keys.namespace_id
          AND keep.wallet = LOWER(wallet_api_keys.wallet)
          AND keep.api_key_id = wallet_api_keys.api_key_id
   );

UPDATE wallet_api_keys
   SET wallet = LOWER(wallet)
 WHERE wallet <> LOWER(wallet);
