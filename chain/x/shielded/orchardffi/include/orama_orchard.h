#ifndef ORAMA_ORCHARD_H
#define ORAMA_ORCHARD_H

#include <stddef.h>
#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

/* Result codes of orama_orchard_verify. Stable: the Go side maps each one to an error. */
#define ORAMA_ORCHARD_OK 0
#define ORAMA_ORCHARD_MALFORMED 1
#define ORAMA_ORCHARD_BAD_PROOF_LENGTH 2
#define ORAMA_ORCHARD_PROOF_REJECTED 3
#define ORAMA_ORCHARD_SIGNATURE_REJECTED 4
#define ORAMA_ORCHARD_PANIC 5
#define ORAMA_ORCHARD_BAD_ARGUMENT 6

/* Bytes in the sighash the caller supplies. */
#define ORAMA_ORCHARD_SIGHASH_LEN 32

/*
 * Verify one Ironwood bundle in the canonical Zcash v6 encoding: the Halo 2 proof, every
 * spend-authorization signature and the binding signature over `sighash`.
 * The verifying key is built once, for OrchardCircuitVersion::PostNu6_3 only.
 * Never unwinds across this boundary.
 */
int32_t orama_orchard_verify(const uint8_t *bundle, size_t bundle_len, const uint8_t *sighash);

/*
 * Build the verifying key now instead of on the first bundle. Returns ORAMA_ORCHARD_OK, or
 * ORAMA_ORCHARD_PANIC if key generation panicked. Safe to call more than once and concurrently.
 */
int32_t orama_orchard_warm(void);

/* Largest encoded note-commitment frontier: position (8) + leaf (32) + 32 ommers (32 each). */
#define ORAMA_ORCHARD_MAX_FRONTIER_LEN 1064

/*
 * Append `n_commitments` note commitments (32 bytes each, canonical) to a note-commitment
 * tree frontier and return the new frontier and its root. `frontier` is empty (len 0) for an
 * empty tree. Encoding: position u64 LE, leaf (32), one ommer (32) per set bit of the position.
 * Writes the new frontier to `out_frontier` (capacity `out_cap`, at least
 * ORAMA_ORCHARD_MAX_FRONTIER_LEN), its length to `out_len` and the 32-byte root to `out_root`.
 * Returns ORAMA_ORCHARD_OK, ORAMA_ORCHARD_MALFORMED (bad frontier or commitment, or a full
 * tree), ORAMA_ORCHARD_PANIC or ORAMA_ORCHARD_BAD_ARGUMENT.
 */
int32_t orama_orchard_tree_append(const uint8_t *frontier, size_t frontier_len,
                                  const uint8_t *commitments, size_t n_commitments,
                                  uint8_t *out_frontier, size_t out_cap, size_t *out_len,
                                  uint8_t *out_root);

#ifdef __cplusplus
}
#endif

#endif
