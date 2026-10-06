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

// Decrypt opens a stored value. A versioned envelope is tried with the matching
// generation; a legacy envelope is tried with current then previous.
func (k Keyset) Decrypt(ciphertext string) (string, error) {
	env, err := ParseEnvelope(ciphertext)
	if err != nil {
		return "", err
	}
	if env.Version == 1 {
		key := k.keyFor(env.KeyID)
		if len(key) == 0 {
			return "", fmt.Errorf("no key for id %q", env.KeyID)
		}
		return open(env.Data, key)
	}
	return DecryptAny(ciphertext, k.Current, k.Previous)
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
func Seal(holder *Holder, purpose string, fallback []byte, plaintext string) (string, error) {
	if holder != nil {
		ks, err := holder.Keyset(purpose)
		if err == nil {
			return ks.Encrypt(plaintext)
		}
	}
	if len(fallback) == 0 {
		return "", fmt.Errorf("no encryption key")
	}
	return Encrypt(plaintext, fallback)
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
