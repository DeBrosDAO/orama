# Post-Quantum Crypto Integration Guide

## Current Status

The PQ modules (`pq_kem.zig`, `pq_sig.zig`) are **stub implementations**:

- **ML-KEM-768 (pq_kem.zig):** Uses HMAC-based key derivation instead of real lattice-based KEM. Provides classical security via HMAC-SHA256 but zero post-quantum security.
- **ML-DSA-65 (pq_sig.zig):** Uses SHA-256 hash-based signatures instead of real lattice-based signatures. Provides tamper detection but zero post-quantum security.

Both stubs preserve the correct interface (key sizes, function signatures) so callers don't need to change when liboqs is integrated.

**Important:** The stubs now **fail-closed** — `verify()` rejects tampered messages/signatures, and `encaps()`/`decaps()` produce deterministic (though incompatible) shared secrets. The previous stub where `verify()` always succeeded has been fixed.

## Integration Plan

### Prerequisites

1. **liboqs** — Open Quantum Safe library (C)
   - Source: https://github.com/open-quantum-safe/liboqs
   - Required version: 0.10.0+
   - Algorithms needed: ML-KEM-768 (FIPS 203), ML-DSA-65 (FIPS 204)

### Step 1: Build liboqs as a Static Library

```bash
# Clone
git clone https://github.com/open-quantum-safe/liboqs.git
cd liboqs

# Build for Linux x86_64 (production target)
mkdir build && cd build
cmake -DCMAKE_INSTALL_PREFIX=/opt/orama/lib/liboqs \
      -DBUILD_SHARED_LIBS=OFF \
      -DOQS_MINIMAL_BUILD="KEM_ml_kem_768;SIG_ml_dsa_65" \
      -DCMAKE_C_COMPILER=zig-cc \
      ..
make -j$(nproc)
make install
```

For cross-compilation with Zig:
```bash
# Use Zig as the C compiler for cross-compilation
CC="zig cc -target x86_64-linux-musl" cmake ...
```

### Step 2: Update build.zig

Add liboqs as a system dependency:

```zig
// In build.zig, after creating the module:
root_mod.addIncludePath(.{ .cwd_relative = "/opt/orama/lib/liboqs/include" });
root_mod.addLibraryPath(.{ .cwd_relative = "/opt/orama/lib/liboqs/lib" });
root_mod.linkSystemLibrary("oqs");
```

### Step 3: Replace Stubs

**pq_kem.zig** — Replace stub functions with:

```zig
const oqs = @cImport({
    @cInclude("oqs/oqs.h");
});

pub fn keygen() KEMError!Keypair {
    var kp = Keypair{ .public_key = undefined, .secret_key = undefined };
    const kem = oqs.OQS_KEM_new(oqs.OQS_KEM_alg_ml_kem_768) orelse return KEMError.KeygenFailed;
    defer oqs.OQS_KEM_free(kem);

    if (oqs.OQS_KEM_keypair(kem, &kp.public_key, &kp.secret_key) != oqs.OQS_SUCCESS) {
        return KEMError.KeygenFailed;
    }
    return kp;
}

pub fn encaps(public_key: [PK_SIZE]u8) KEMError!EncapsulationResult {
    var result = EncapsulationResult{ .ciphertext = undefined, .shared_secret = undefined };
    const kem = oqs.OQS_KEM_new(oqs.OQS_KEM_alg_ml_kem_768) orelse return KEMError.EncapsFailed;
    defer oqs.OQS_KEM_free(kem);

    if (oqs.OQS_KEM_encaps(kem, &result.ciphertext, &result.shared_secret, &public_key) != oqs.OQS_SUCCESS) {
        return KEMError.EncapsFailed;
    }
    return result;
}

pub fn decaps(ciphertext: [CT_SIZE]u8, secret_key: [SK_SIZE]u8) KEMError![SS_SIZE]u8 {
    const kem = oqs.OQS_KEM_new(oqs.OQS_KEM_alg_ml_kem_768) orelse return KEMError.DecapsFailed;
    defer oqs.OQS_KEM_free(kem);

    var ss: [SS_SIZE]u8 = undefined;
    if (oqs.OQS_KEM_decaps(kem, &ss, &ciphertext, &secret_key) != oqs.OQS_SUCCESS) {
        return KEMError.DecapsFailed;
    }
    return ss;
}
```

**pq_sig.zig** — Replace stub functions similarly using `OQS_SIG_ml_dsa_65`.

### Step 4: Test

```bash
zig build test  # Unit tests with real PQ operations
```

Key test: `keygen() → encaps(pk) → decaps(ct, sk)` must produce matching shared secrets.

### Step 5: Wire into Protocol

Once stubs are replaced:
1. **Hybrid key exchange:** X25519 + ML-KEM-768 for guardian-to-guardian and client-to-guardian (see `crypto/hybrid.zig`)
2. **PQ auth:** ML-DSA-65 signatures on challenge-response tokens
3. **Key rotation:** Re-key PQ parameters on each session

## Security Note

The current system is still secure without PQ crypto because:
- **Shamir SSS** is information-theoretic (immune to quantum)
- **AES-256-GCM** has 128-bit post-quantum security (Grover's algorithm halves the key strength)
- **WireGuard** provides transport encryption (ChaCha20-Poly1305)
- **HMAC-SHA256** has 128-bit post-quantum security

PQ crypto adds protection against quantum attacks on:
- **Key exchange** (currently X25519 — vulnerable to Shor's algorithm)
- **Digital signatures** (currently Ed25519 — vulnerable to Shor's algorithm)

The urgency is moderate: harvest-now-decrypt-later attacks are a concern for long-lived secrets, but Shamir shares are re-generated on each sync push.
