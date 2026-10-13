package secrets

import (
	"fmt"
	"sync"
)

// Keyset is the pair of AES keys for one purpose, derived from the current
// (and, during a rotate, previous) encryption root.
type Keyset struct {
	Current        []byte
	CurrentID      string
	Previous       []byte
	PreviousID     string
	WriteVersioned bool
	WriteBound     bool
}

// Encrypt seals plaintext under the current key. After an operator rotate it
// writes the versioned envelope; until then it writes the legacy enc: form so
// a mixed-version rolling upgrade can still read new rows.
func (k Keyset) Encrypt(plaintext string) (string, error) {
	if len(k.Current) == 0 {
		return "", fmt.Errorf("no current encryption key")
	}
	if k.WriteVersioned {
		return EncryptVersioned(plaintext, k.CurrentID, k.Current)
	}
	return Encrypt(plaintext, k.Current)
}

// EncryptBound seals plaintext to aad (the row it is stored in) once the
// operator has enabled bound writes, and otherwise exactly as Encrypt does.
// Until then a node that has not been upgraded would be handed an envelope it
// cannot open.
func (k Keyset) EncryptBound(plaintext string, aad []byte) (string, error) {
	if !k.WriteBound {
		return k.Encrypt(plaintext)
	}
	if len(k.Current) == 0 {
		return "", fmt.Errorf("no current encryption key")
	}
	return EncryptBound(plaintext, k.CurrentID, k.Current, aad)
}

// Decrypt opens a stored value. A versioned envelope is tried with the matching
// generation; a legacy envelope is tried with current then previous. A bound
// envelope is refused: it opens through DecryptBound.
func (k Keyset) Decrypt(ciphertext string) (string, error) {
	return k.DecryptBound(ciphertext, nil)
}

// DecryptBound is Decrypt for a column whose rows are sealed to aad. Envelopes
// written before the binding existed carry none and open as they always did.
func (k Keyset) DecryptBound(ciphertext string, aad []byte) (string, error) {
	env, err := ParseEnvelope(ciphertext)
	if err != nil {
		return "", err
	}
	if env.Version == 0 {
		return k.decryptLegacy(ciphertext, aad)
	}
	key := k.keyFor(env.KeyID)
	if len(key) == 0 {
		return "", fmt.Errorf("no key for id %q", env.KeyID)
	}
	return DecryptBound(ciphertext, key, aad)
}

func (k Keyset) decryptLegacy(ciphertext string, aad []byte) (string, error) {
	var last error
	tried := 0
	for _, key := range [][]byte{k.Current, k.Previous} {
		if len(key) == 0 {
			continue
		}
		tried++
		plain, err := DecryptBound(ciphertext, key, aad)
		if err == nil {
			return plain, nil
		}
		last = err
	}
	if tried == 0 {
		return "", fmt.Errorf("no decryption key")
	}
	return "", last
}

func (k Keyset) keyFor(id string) []byte {
	if id == k.CurrentID {
		return k.Current
	}
	if id == k.PreviousID {
		return k.Previous
	}
	return nil
}

// Root is one generation of the encryption-root IKM, plus the previous
// generation while a rotate is in flight.
type Root struct {
	CurrentID      string
	CurrentIKM     string
	PreviousID     string
	PreviousIKM    string
	WriteVersioned bool
	// WriteBound is set by the operator once every gateway can open an enc:v2:
	// envelope. Until then rows that could be bound are written unbound.
	WriteBound bool
}

// Keyset derives the purpose-separated AES keys from this root.
func (r Root) Keyset(purpose string) (Keyset, error) {
	if r.CurrentIKM == "" {
		return Keyset{}, fmt.Errorf("encryption root is empty")
	}
	cur, err := DeriveKey(r.CurrentIKM, purpose)
	if err != nil {
		return Keyset{}, err
	}
	ks := Keyset{
		Current:        cur,
		CurrentID:      r.CurrentID,
		WriteVersioned: r.WriteVersioned,
		WriteBound:     r.WriteBound,
		PreviousID:     r.PreviousID,
	}
	if r.PreviousIKM != "" {
		prev, err := DeriveKey(r.PreviousIKM, purpose)
		if err != nil {
			return Keyset{}, err
		}
		ks.Previous = prev
	}
	return ks, nil
}

// Holder is the process-wide encryption root. Stores read it on each
// encrypt/decrypt so a rotate takes effect without restarting the gateway.
type Holder struct {
	mu sync.RWMutex
	r  Root
}

// NewHolder wraps r.
func NewHolder(r Root) *Holder { return &Holder{r: r} }

// Get returns a snapshot of the root.
func (h *Holder) Get() Root {
	if h == nil {
		return Root{}
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.r
}

// Swap replaces the root. Called after a rotate lands in the registry.
func (h *Holder) Swap(r Root) {
	if h == nil {
		return
	}
	h.mu.Lock()
	h.r = r
	h.mu.Unlock()
}

// Keyset is Get().Keyset(purpose).
func (h *Holder) Keyset(purpose string) (Keyset, error) {
	return h.Get().Keyset(purpose)
}

// Seal encrypts with the holder's current keyset, or fallback if holder is nil.
// A holder whose root cannot make the keyset is an error, never a reason to
// seal under the static key: that row would be unreadable to every gateway that
// has the root.
func Seal(holder *Holder, purpose string, fallback []byte, plaintext string) (string, error) {
	if holder != nil {
		ks, err := holder.Keyset(purpose)
		if err != nil {
			return "", fmt.Errorf("derive the %s encryption keys: %w", purpose, err)
		}
		return ks.Encrypt(plaintext)
	}
	if len(fallback) == 0 {
		return "", fmt.Errorf("no encryption key")
	}
	return Encrypt(plaintext, fallback)
}

// SealBound is Seal for a row that can be bound to aad. Without a holder there
// is no operator state to say every node can read the bound form, so it seals
// unbound.
func SealBound(holder *Holder, purpose string, fallback, aad []byte, plaintext string) (string, error) {
	if holder != nil {
		ks, err := holder.Keyset(purpose)
		if err != nil {
			return "", fmt.Errorf("derive the %s encryption keys: %w", purpose, err)
		}
		return ks.EncryptBound(plaintext, aad)
	}
	return Seal(nil, purpose, fallback, plaintext)
}

// OpenBound is Open for a row that can be bound to aad.
func OpenBound(holder *Holder, purpose string, fallback, aad []byte, ciphertext string) (string, error) {
	if holder != nil {
		ks, err := holder.Keyset(purpose)
		if err == nil {
			return ks.DecryptBound(ciphertext, aad)
		}
	}
	if len(fallback) == 0 {
		return "", fmt.Errorf("no decryption key")
	}
	return DecryptBound(ciphertext, fallback, aad)
}

// Open decrypts with the holder's keyset (current and previous), or fallback.
func Open(holder *Holder, purpose string, fallback []byte, ciphertext string) (string, error) {
	if holder != nil {
		ks, err := holder.Keyset(purpose)
		if err == nil {
			return ks.Decrypt(ciphertext)
		}
	}
	if len(fallback) == 0 {
		return "", fmt.Errorf("no decryption key")
	}
	return Decrypt(ciphertext, fallback)
}
