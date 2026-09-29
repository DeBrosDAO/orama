package nsbackup

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/DeBrosOfficial/network/pkg/httputil"
	"github.com/DeBrosOfficial/network/pkg/secrets"
	"golang.org/x/crypto/nacl/box"
)

// RestoreKeyPurpose is the HKDF label a destination gateway derives a
// namespace's restore keypair under, from its cluster's current encryption
// root: RestoreKeyPurpose + ":" + namespace. The key is different for every
// namespace and changes when the root is rotated.
const RestoreKeyPurpose = "orama-restore-v1"

// ErrSecretMismatch means a sealed secret opened but names a different
// namespace or row than the one it was sent for: a secret moved or replayed.
var ErrSecretMismatch = errors.New("sealed secret does not belong where it was sent")

// WrappedSecret is a Secret sealed (nacl box.SealAnonymous) to the destination
// gateway's restore public key for the namespace. The box holds the namespace,
// table, column and ids as well as the value, and they are checked on open.
type WrappedSecret struct {
	Table  string   `json:"table"`
	Column string   `json:"column"`
	IDs    []string `json:"ids"`
	Sealed []byte   `json:"sealed"`
}

// boundSecret is what a WrappedSecret's box holds.
type boundSecret struct {
	Namespace string   `json:"namespace"`
	Table     string   `json:"table"`
	Column    string   `json:"column"`
	IDs       []string `json:"ids"`
	Value     string   `json:"value"`
}

// RestoreRequest is what the operator's machine sends a destination gateway:
// the opened backup, with every secret re-sealed for that gateway. It carries
// no backup key.
type RestoreRequest struct {
	Namespace   string
	Pins        []string
	StoredBytes int64
	Secrets     []WrappedSecret
	RQLite      []byte
}

type requestHeader struct {
	frameHeader
	Secrets []WrappedSecret `json:"secrets"`
}

// Rewrap seals every secret in p to dest, the destination gateway's restore
// public key for p's namespace.
func (p Payload) Rewrap(dest *[32]byte) (RestoreRequest, error) {
	if dest == nil {
		return RestoreRequest{}, fmt.Errorf("destination restore public key is required")
	}
	out := RestoreRequest{Namespace: p.Namespace, Pins: p.Pins, StoredBytes: p.StoredBytes, RQLite: p.RQLite,
		Secrets: make([]WrappedSecret, 0, len(p.Secrets))}
	for _, s := range p.Secrets {
		plain, err := json.Marshal(boundSecret{Namespace: p.Namespace, Table: s.Table, Column: s.Column, IDs: s.IDs, Value: s.Value})
		if err != nil {
			return RestoreRequest{}, fmt.Errorf("encode secret %s.%s %v: %w", s.Table, s.Column, s.IDs, err)
		}
		sealed, err := box.SealAnonymous(nil, plain, dest, rand.Reader)
		if err != nil {
			return RestoreRequest{}, fmt.Errorf("seal secret %s.%s %v for the destination: %w", s.Table, s.Column, s.IDs, err)
		}
		out.Secrets = append(out.Secrets, WrappedSecret{Table: s.Table, Column: s.Column, IDs: s.IDs, Sealed: sealed})
	}
	return out, nil
}

// Marshal validates r and encodes it as a restore request body.
func (r RestoreRequest) Marshal() ([]byte, error) {
	if err := validateFrame(r.Namespace, r.Pins, r.StoredBytes, r.RQLite); err != nil {
		return nil, err
	}
	for _, s := range r.Secrets {
		if err := validateSecretRef(s.Table, s.Column, s.IDs); err != nil {
			return nil, err
		}
	}
	h := requestHeader{frameHeader: newFrameHeader(r.Namespace, r.Pins, r.StoredBytes, r.RQLite), Secrets: r.Secrets}
	return writeFrame(requestMagic, h, r.RQLite)
}

// UnmarshalRestoreRequest decodes and validates a restore request body.
func UnmarshalRestoreRequest(b []byte) (RestoreRequest, error) {
	var h requestHeader
	db, err := readFrame(requestMagic, b, &h)
	if err != nil {
		return RestoreRequest{}, err
	}
	if err := checkFrame(h.frameHeader, db); err != nil {
		return RestoreRequest{}, err
	}
	for _, s := range h.Secrets {
		if err := validateSecretRef(s.Table, s.Column, s.IDs); err != nil {
			return RestoreRequest{}, fmt.Errorf("%w: %v", ErrCorrupt, err)
		}
	}
	return RestoreRequest{Namespace: h.Namespace, Pins: h.Pins, StoredBytes: h.StoredBytes, Secrets: h.Secrets, RQLite: db}, nil
}

// OpenSecrets opens every wrapped secret with the destination's restore
// private key and checks each names r's namespace and the row it was sent
// for. It is all or nothing, so a caller has decided nothing before it writes.
func (r RestoreRequest) OpenSecrets(priv *[32]byte) ([]Secret, error) {
	if priv == nil {
		return nil, fmt.Errorf("restore private key is required")
	}
	pub, ok := publicFromPrivate(priv)
	if !ok {
		return nil, fmt.Errorf("restore private key is not a valid X25519 scalar")
	}
	out := make([]Secret, 0, len(r.Secrets))
	for _, w := range r.Secrets {
		plain, ok := box.OpenAnonymous(nil, w.Sealed, pub, priv)
		if !ok {
			return nil, fmt.Errorf("secret %s.%s %v: %w", w.Table, w.Column, w.IDs, ErrNotForKey)
		}
		var b boundSecret
		dec := json.NewDecoder(bytes.NewReader(plain))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&b); err != nil {
			return nil, fmt.Errorf("secret %s.%s %v: %w: %v", w.Table, w.Column, w.IDs, ErrCorrupt, err)
		}
		if b.Namespace != r.Namespace || b.Table != w.Table || b.Column != w.Column || !slices.Equal(b.IDs, w.IDs) {
			return nil, fmt.Errorf("secret %s.%s %v: %w", w.Table, w.Column, w.IDs, ErrSecretMismatch)
		}
		out = append(out, Secret{Table: w.Table, Column: w.Column, IDs: w.IDs, Value: b.Value})
	}
	return out, nil
}

// RestoreKey derives a gateway's restore keypair for namespace from its
// encryption root. The public half is what an operator seals secrets to; the
// private half never leaves the gateway and is never stored.
func RestoreKey(root secrets.Root, namespace string) (pub, priv *[32]byte, err error) {
	if !httputil.ValidateNamespace(namespace) {
		return nil, nil, fmt.Errorf("derive the restore key: %q is not a valid namespace name", namespace)
	}
	k, err := secrets.DeriveKey(root.CurrentIKM, RestoreKeyPurpose+":"+namespace)
	if err != nil {
		return nil, nil, fmt.Errorf("derive the restore key: %w", err)
	}
	priv = new([32]byte)
	copy(priv[:], k)
	pub, ok := publicFromPrivate(priv)
	if !ok {
		return nil, nil, fmt.Errorf("derive the restore key: not a valid X25519 scalar")
	}
	return pub, priv, nil
}
