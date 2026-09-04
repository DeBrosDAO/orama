# Orama Vault -- Security Model

## Threat Model

| Threat | Severity | Mitigation | Status |
|--------|----------|------------|--------|
| **Single node compromise** | Medium | Shamir SSS: a single share reveals zero information about the secret. Attacker gets one share out of N; needs K to reconstruct. | Implemented |
| **K-1 node collusion** | High | Information-theoretic security: K-1 shares provide exactly zero bits of information about the secret. This is not computational -- it is mathematically proven. | Implemented |
| **All N nodes collude** | Critical | Not defended. If all N guardians collude, they can reconstruct the secret. Mitigated by: (1) nodes operated by different parties, (2) geographic distribution, (3) proactive re-sharing (not yet active at runtime) invalidates old shares. | By design |
| **Quantum adversary** | Future | Post-quantum KEM (ML-KEM-768) and signatures (ML-DSA-65) interfaces are defined. Hybrid key exchange (X25519 + ML-KEM-768) is implemented. | Stub (Phase 2) |
| **Replay attack on push** | Medium | Monotonic version counter. Each push must have a version strictly greater than the stored version. Replaying an old push is rejected. | Implemented |
| **Rollback attack** | Medium | Anti-rollback via monotonic version counter stored in each identity's `meta.json`. Attacker cannot downgrade a share to an older version; violations are rejected with 409 Conflict. | Implemented |
| **Disk corruption** | Medium | HMAC-SHA256 checksum per share file. On read, the checksum is verified before returning data. Corruption is detected and surfaced as an error. | Implemented |
| **Disk tampering** | Medium | Same HMAC integrity check. An attacker who modifies share.bin on disk cannot forge a valid checksum without the integrity key. | Implemented |
| **Network eavesdropping** | High | All inter-node traffic uses WireGuard (encrypted tunnel). Client-to-guardian will use TLS in Phase 3. | Partial (WireGuard: yes, TLS: Phase 3) |
| **Timing side-channels** | Low | All HMAC verifications and auth token checks use constant-time comparison (`diff |= x ^ y` accumulator). | Implemented |
| **Memory disclosure** | Low | Secure memory: `secureZero` (volatile zero-fill that cannot be optimized away), `mlock` (prevents swap to disk), `SecureBuffer` RAII wrapper. Server secret zeroed on Guardian deinit. | Implemented |
| **Resource exhaustion** | Medium | Request body size limits (1 MiB push, 4 KiB pull), share size limit (512 KiB), peer protocol max payload (1 MiB). Systemd MemoryMax=512M. | Implemented |
| **Man-in-the-middle (peer)** | High | WireGuard provides authenticated encryption between peers on the overlay. Note: in v0.1.0 the peer listener is never started, so nothing accepts connections on port 7501 -- there is no active peer protocol to attack. | Peer protocol not active (v0.1.0) |
| **Man-in-the-middle (client)** | High | TLS termination planned for Phase 3. Currently plain TCP on port 7500. In production, the Orama gateway provides TLS. | Gateway-level |
| **Unauthorized push/pull** | Medium | Two layers, both enforced by the push/pull handlers: (1) a session token (HMAC challenge-response via `/v1/vault/auth/challenge` + `/v1/vault/auth/session`, sent as `X-Session-Token`), and (2) an Ed25519 ownership proof — identity = SHA-256(pubkey), with a signature over the push message (binding the version) or pull message (binding a timestamp, max 120s skew). Requests without both get 401. | Implemented |
| **Share epoch mixing** | High | After proactive re-sharing, old and new shares are algebraically independent. Mixing shares from different epochs does NOT reconstruct the secret. Tested and verified. | Implemented |

---

## Shamir Secret Sharing Security

### Information-Theoretic Security

Shamir's Secret Sharing provides **perfect secrecy** -- this is the strongest possible security guarantee:

- **K shares** can reconstruct the secret (Lagrange interpolation at x=0).
- **K-1 shares** provide exactly **zero** information about the secret.
- This is not a computational assumption. It holds against adversaries with unlimited computing power, including quantum computers.

**Proof sketch:** A polynomial of degree K-1 is uniquely determined by K points. With only K-1 points, there are exactly 256 (= |GF(2^8)|) distinct polynomials passing through those points, one for each possible value of the constant term (the secret byte). Each is equally likely. Therefore, the conditional probability distribution of the secret given K-1 shares is uniform over GF(2^8) -- identical to the prior distribution. No information is gained.

For a multi-byte secret of length L, this applies independently to each byte position, since each byte uses an independent random polynomial.

### Threshold and Share Count

The system uses an adaptive read threshold and a write quorum derived from it:

```
K = max(2, floor(N/3))
W = min(N, max(K + 1, ceil(2N/3)))
```

Where N is the number of alive guardians. K is the number of shares a read must
collect; W is the number of guardians that must acknowledge before a write is
reported successful.

| Alive Nodes (N) | Threshold (K) | Write Quorum (W) | Read Fault Tolerance (N-K) |
|------------------|---------------|------------------|-----------------------------|
| 3 | 2 | 3 | 1 |
| 5 | 2 | 4 | 3 |
| 9 | 3 | 6 | 6 |
| 10 | 3 | 7 | 7 |
| 14 | 4 | 10 | 10 |
| 50 | 16 | 34 | 34 |
| 100 | 33 | 67 | 67 |

Two is the smallest threshold that keeps the secret secret: with K = 1 a single
guardian holds enough to reconstruct on its own.

`W > K` is the durability guarantee. A write reported successful has persisted
strictly more shares than a read requires, so it is always recoverable and
survives the loss of at least one guardian. The floor used to be 3, which broke
that guarantee in the other direction: at N = 3 it gave K = 3 against W = 2, so
a write the system called successful had stored fewer shares than a read needed
and was permanently unrecoverable, with nothing at the time of the write to say
so.

The three implementations — `vault/src/membership/quorum.zig`,
`core/pkg/shamir/shamir.go` and `sdk-vault/src/quorum.ts` — must agree exactly.
A client that believes a write needed two acknowledgements where the guardian
required three reports a write as successful that the guardian refused.

---

## GF(2^8) Choice Rationale

The finite field GF(2^8) = GF(256) was chosen for Shamir arithmetic:

1. **Same field as AES.** The irreducible polynomial x^8 + x^4 + x^3 + x + 1 (0x11B) is the AES field polynomial. This is the most studied and battle-tested GF(2^8) instantiation in cryptography.

2. **Byte-aligned.** Each field element is exactly one byte. No encoding overhead, no multi-precision arithmetic, no serialization complexity.

3. **O(1) arithmetic.** Precomputed exp/log tables (512 + 256 = 768 bytes total, generated at Zig comptime) give constant-time multiplication, inversion, and division via table lookups. The generator element is 3 (0x03), a primitive element of the multiplicative group of order 255.

4. **255 distinct evaluation points.** Shares are evaluated at x = 1, 2, ..., N (never x=0, which would reveal the secret). This supports up to 255 shares per secret, far exceeding the Orama network size.

5. **Exhaustively verified.** The implementation includes tests that verify:
   - All 256x256 multiplication pairs produce valid results.
   - Multiplicative identity: 1 * a = a for all a.
   - Multiplicative inverse: a * inv(a) = 1 for all nonzero a.
   - Commutativity, associativity, and distributivity (sampled).
   - The exp table generates all 255 nonzero elements exactly once (confirming 3 is a primitive element).

---

## Key Wrapping (Planned Architecture)

> **Status:** The key wrapping scheme is designed but not yet fully implemented. The crypto primitives (AES-256-GCM, HKDF-SHA256) are implemented and tested.

The planned key hierarchy:

```
User Secret (root seed / mnemonic)
    |
    +-- DEK (Data Encryption Key) -- random 256-bit AES key
    |     |
    |     +-- Encrypts the secret via AES-256-GCM
    |
    +-- KEK1 (Key Encryption Key 1) -- derived from mnemonic via HKDF
    |     |
    |     +-- Wraps DEK (AES-256-GCM)
    |     +-- Stored alongside the encrypted secret
    |
    +-- KEK2 (Key Encryption Key 2) -- derived from username+passphrase via HKDF
          |
          +-- Wraps DEK (AES-256-GCM)
          +-- Stored alongside the encrypted secret
```

**Recovery Path A (Mnemonic):**
1. User provides mnemonic.
2. Derive KEK1 = HKDF(mnemonic, "orama-kek1-v1").
3. Unwrap DEK from wrapped_dek1.bin.
4. Decrypt secret with DEK.

**Recovery Path B (Username + Passphrase):**
1. User provides username + passphrase.
2. Derive identity = SHA-256(username).
3. Pull K shares from guardians.
4. Reconstruct encrypted blob via Lagrange interpolation.
5. Derive KEK2 = HKDF(passphrase, "orama-kek2-v1").
6. Unwrap DEK from wrapped_dek2.bin.
7. Decrypt secret with DEK.

---

## HMAC Integrity

Every stored share has an associated HMAC-SHA256 checksum:

```
checksum = HMAC-SHA256(integrity_key, share_data)
```

On read, the checksum is recomputed and compared in constant time:

```zig
fn constantTimeEqual(a: []const u8, b: []const u8) bool {
    if (a.len != b.len) return false;
    var diff: u8 = 0;
    for (a, b) |x, y| {
        diff |= x ^ y;
    }
    return diff == 0;
}
```

This detects:
- Accidental disk corruption (bit flips, sector failures).
- Intentional tampering by an attacker with disk access.
- Partial writes (if the share was updated but checksum wasn't, or vice versa).

---

## Anti-Rollback Protection

Each identity has a monotonic version counter stored (with the Shamir threshold) in `<data_dir>/shares/<identity>/meta.json`. On push:

1. Read the current version from `meta.json`.
2. If metadata exists and the new version is <= the stored version, reject with 409 Conflict (distinguishable from a 400 malformed request, so clients can re-read and bump).
3. If the new version is strictly greater, proceed with the write.
4. Write `meta.json` atomically (temp + rename), last -- it doubles as the commit marker for the multi-file write.

This prevents an attacker from replacing a current share with an older version, which could be part of an attack to force reconstruction with a known set of shares.

---

## Timing Attack Prevention

All security-sensitive comparisons use constant-time operations:

1. **HMAC verification** (`src/crypto/hmac.zig`): `constantTimeEqual` with XOR accumulator.
2. **Challenge verification** (`src/auth/challenge.zig`): `timingSafeEqual` with same pattern.
3. **Session token verification** (`src/auth/session.zig`): `timingSafeEqual` with same pattern.

The pattern:
```zig
var diff: u8 = 0;
for (a, b) |x, y| {
    diff |= x ^ y;
}
return diff == 0;
```

This ensures the comparison takes the same time regardless of where (or whether) the bytes differ. An attacker cannot learn partial information about expected values by measuring response times.

---

## Secure Memory

The `src/crypto/secure_mem.zig` module provides:

### secureZero

```zig
pub fn secureZero(buf: []u8) void {
    std.crypto.secureZero(u8, @as([]volatile u8, @volatileCast(buf)));
}
```

Uses volatile semantics to prevent the compiler from optimizing away the zero-fill. This is critical for erasing keys, secrets, and intermediate cryptographic material from memory.

### mlock / munlock

```zig
pub fn mlock(ptr: [*]const u8, len: usize) void {
    if (builtin.os.tag == .linux) {
        const result = std.posix.mlock(ptr[0..len]);
        // Non-fatal on failure
    }
}
```

Locks memory pages so they are never written to swap. This prevents key material from being persisted to disk in a swap partition. Requires either `CAP_IPC_LOCK` capability or sufficient `RLIMIT_MEMLOCK`.

The systemd service file sets `LimitMEMLOCK=67108864` (64 MiB) to allow mlock.

### SecureBuffer

RAII wrapper that combines allocation, mlock, and automatic zeroing:

```zig
pub const SecureBuffer = struct {
    data: []u8,
    allocator: std.mem.Allocator,

    pub fn deinit(self: *SecureBuffer) void {
        secureZero(self.data);       // volatile zero
        munlock(self.data.ptr, ...); // unlock pages
        self.allocator.free(self.data);
    }
};
```

Used for all key material that has a defined lifetime.

### Server Secret Zeroing

The `Guardian.deinit()` method zeroes the 32-byte server secret:

```zig
pub fn deinit(self: *Guardian) void {
    self.nodes.deinit();
    @memset(&self.server_secret, 0);
}
```

### Share Zeroing

All `Share.deinit()` calls zero the share data before freeing:

```zig
pub fn deinit(self: Share, allocator: std.mem.Allocator) void {
    const mutable: []u8 = @constCast(self.y);
    @memset(mutable, 0);
    allocator.free(mutable);
}
```

Similarly, the `split` operation zeros the coefficient buffer (which contains the secret as `coeffs[0]`) on cleanup.

---

## Post-Quantum Roadmap

### Current State: Stubs

The post-quantum modules exist with correct interfaces but provide **zero post-quantum security** (no real lattice operations):

- **ML-KEM-768** (`src/crypto/pq_kem.zig`): `keygen()` returns random bytes. `encaps()` generates a random ciphertext and derives the shared secret as HMAC-SHA256(public_key, ciphertext); `decaps()` derives HMAC-SHA256(secret_key, ciphertext). Because the stub keys are independent random bytes, encaps and decaps do NOT produce matching secrets -- the stub preserves the interface only.

- **ML-DSA-65** (`src/crypto/pq_sig.zig`): `keygen()` returns random bytes. `sign()` places SHA-256(message) in the signature as a placeholder. `verify()` is **fail-closed**: it recomputes SHA-256(message), compares in constant time, and returns `SigError.VerifyFailed` on mismatch. This rejects tampered messages/signatures but is NOT real post-quantum verification.

Both modules log a one-time warning when first used:
```
pq_kem: STUB implementation — uses HMAC-based KEM, NOT real post-quantum security. Install liboqs for ML-KEM-768.
pq_sig: STUB implementation — uses HMAC-based signatures, NOT real post-quantum security. Install liboqs for ML-DSA-65.
```

See [PQ_INTEGRATION.md](PQ_INTEGRATION.md) for details.

### Planned Implementation (Phase 2)

Replace stubs with liboqs-backed implementations:

| Algorithm | Standard | Security Level | Key Sizes |
|-----------|----------|---------------|-----------|
| ML-KEM-768 | FIPS 203 | ~192-bit post-quantum | PK: 1184, SK: 2400, CT: 1088, SS: 32 |
| ML-DSA-65 | FIPS 204 | ~192-bit post-quantum | PK: 1952, SK: 4032, Sig: 3309 max |

Integration plan:
1. Link liboqs as a C dependency via Zig's `@cImport`.
2. Replace random byte generation with actual `OQS_KEM_ml_kem_768_*` and `OQS_SIG_ml_dsa_65_*` calls.
3. The hybrid module (`src/crypto/hybrid.zig`) already combines X25519 + ML-KEM correctly -- once the ML-KEM stub is replaced, hybrid key exchange will provide real post-quantum protection.

### Hybrid Key Exchange

The hybrid module (`src/crypto/hybrid.zig`) implements X25519 + ML-KEM-768:

```
shared_secret = HKDF-SHA256(X25519_SS || ML-KEM_SS, salt=0^32, info="orama-hybrid-v1")
```

This ensures:
- If X25519 is broken (quantum computer), ML-KEM still protects.
- If ML-KEM is broken (unknown classical attack), X25519 still protects.
- Both must be broken simultaneously to compromise the shared secret.

The X25519 portion is fully functional using Zig's `std.crypto.dh.X25519`. Only the ML-KEM portion is currently a stub.

---

## WireGuard Transport Security

Guardian-to-guardian communication (port 7501) is designed to be restricted to the WireGuard overlay network (10.0.0.x addresses).

> **Status (v0.1.0):** The peer protocol is not active -- `main()` never starts the peer listener, so nothing accepts connections on port 7501 (the module is exercised only by tests). The heartbeat sender runs, but with the empty stubbed node list it has no peers to contact. The properties below apply once the peer protocol is wired in.

WireGuard provides:

1. **Authenticated encryption:** ChaCha20-Poly1305 with per-peer keys derived from Noise IK handshake.
2. **Perfect forward secrecy:** New ephemeral keys every 2 minutes or 2^64 messages.
3. **Mutual authentication:** Only nodes with authorized public keys can join the overlay.
4. **Replay protection:** Built-in counter-based replay rejection.

An attacker who does not have a valid WireGuard private key cannot:
- Connect to port 7501 on any guardian.
- Observe peer-to-peer traffic contents.
- Inject or replay messages.

This is defense-in-depth: even if the binary peer protocol had vulnerabilities, the WireGuard layer prevents exploitation from outside the cluster.

---

## Proactive Re-sharing Security

The Herzberg-Jarecki-Krawczyk-Yung re-sharing protocol ensures:

1. **Forward secrecy for shares.** After re-sharing, old shares are algebraically independent from new shares. An attacker who compromises old shares (before re-sharing) and new shares (after re-sharing) from *different* guardians cannot combine them.

2. **Secret preservation.** The secret itself does not change during re-sharing. Only the polynomial representation changes. `sum(q_i(0)) = 0` ensures the constant term (secret) is preserved.

3. **Epoch isolation.** Tested and verified: mixing one new share with K-1 old shares does NOT reconstruct the original secret. The test in `src/sss/reshare.zig` confirms this with high probability.

4. **No secret reconstruction.** At no point during re-sharing does any single party learn the secret. Each guardian only processes deltas and updates its own share.

> **Status (v0.1.0):** The re-sharing math is implemented and tested (`src/sss/reshare.zig`), but no runtime code invokes it -- the repair/re-share modules are referenced only from tests. The planned triggers (Phase 2) are:
> - On node topology changes (join/leave detected by discovery module).
> - Periodically every 24 hours.
> - When alive count drops below the safety threshold (K+1).

---

## Resource Limits

| Resource | Limit | Where Enforced |
|----------|-------|----------------|
| Process memory | 512 MiB | systemd `MemoryMax=512M` |
| mlock memory | 64 MiB | systemd `LimitMEMLOCK=67108864` |
| Push request body | 1 MiB | `handler_push.zig` `MAX_BODY_SIZE` |
| Pull request body | 4 KiB | `handler_pull.zig` `MAX_BODY_SIZE` |
| Decoded share size | 512 KiB | `handler_push.zig` `MAX_SHARE_SIZE` |
| Peer protocol payload | 1 MiB | `protocol.zig` `MAX_PAYLOAD_SIZE` |
| HTTP read buffer | 64 KiB | `listener.zig` `READ_BUF_SIZE` |
| Share file read | 1 MiB / 10 MiB | `handler_pull.zig` / `file_store.zig` |

---

## Systemd Security Hardening

The systemd service file applies defense-in-depth:

```ini
PrivateTmp=yes              # Isolated /tmp
ProtectSystem=strict        # Read-only filesystem except explicit paths
ReadWritePaths=/opt/orama/.orama/data/vault  # Only data dir is writable
NoNewPrivileges=yes         # Cannot gain new privileges (no setuid, no capabilities)
LimitMEMLOCK=67108864       # Allow mlock for secure memory
MemoryMax=512M              # Hard memory limit
```

This means even if the guardian process is compromised, the attacker:
- Cannot write to the filesystem outside the data directory.
- Cannot escalate privileges.
- Cannot consume unbounded memory.
- Has isolated temporary file access.
