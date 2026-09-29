package nsbackup

import (
	"crypto/rand"
	"fmt"

	"github.com/DeBrosOfficial/network/pkg/secrets"
	"golang.org/x/crypto/nacl/box"
)

// RestoreKeyPurpose is the HKDF label a destination gateway derives its
// restore keypair under, from its cluster's current encryption root. The
// restore key therefore changes when that root is rotated.
const RestoreKeyPurpose = "orama-restore-v1"

// WrappedSecret is a Secret whose value is sealed (nacl box.SealAnonymous) to
// the destination gateway's restore public key. Only that gateway can open it.
type WrappedSecret struct {
	Table  string   `json:"table"`
	Column string   `json:"column"`
	IDs    []string `json:"ids"`
	Sealed []byte   `json:"sealed"`
}

// RestoreRequest is what the operator's machine sends a destination gateway:
// the opened backup, with every secret re-sealed for that gateway. It carries
// no backup key.
type RestoreRequest struct {
	Namespace string
	Pins      []string
	Secrets   []WrappedSecret
	RQLite    []byte
}

type requestHeader struct {
	frameHeader
	Secrets []WrappedSecret `json:"secrets"`
}

// Rewrap seals every secret in p to dest, the destination gateway's restore
// public key.
func (p Payload) Rewrap(dest *[32]byte) (RestoreRequest, error) {
	if dest == nil {
		return RestoreRequest{}, fmt.Errorf("destination restore public key is required")
	}
	out := RestoreRequest{Namespace: p.Namespace, Pins: p.Pins, RQLite: p.RQLite,
		Secrets: make([]WrappedSecret, 0, len(p.Secrets))}
	for _, s := range p.Secrets {
		sealed, err := box.SealAnonymous(nil, []byte(s.Value), dest, rand.Reader)
		if err != nil {
			return RestoreRequest{}, fmt.Errorf("seal secret %s.%s %v for the destination: %w", s.Table, s.Column, s.IDs, err)
		}
		out.Secrets = append(out.Secrets, WrappedSecret{Table: s.Table, Column: s.Column, IDs: s.IDs, Sealed: sealed})
	}
	return out, nil
}

// Marshal validates r and encodes it as a restore request body.
func (r RestoreRequest) Marshal() ([]byte, error) {
	if err := validateFrame(r.Namespace, r.Pins, r.RQLite); err != nil {
		return nil, err
	}
	for _, s := range r.Secrets {
		if err := validateSecretRef(s.Table, s.Column, s.IDs); err != nil {
			return nil, err
		}
	}
	h := requestHeader{frameHeader: newFrameHeader(r.Namespace, r.Pins, r.RQLite), Secrets: r.Secrets}
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
	return RestoreRequest{Namespace: h.Namespace, Pins: h.Pins, Secrets: h.Secrets, RQLite: db}, nil
}

// OpenSecrets opens every wrapped secret with the destination's restore
// private key. It is all or nothing: one secret that does not open fails the
// whole call, so a caller has decided nothing before it writes.
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
		out = append(out, Secret{Table: w.Table, Column: w.Column, IDs: w.IDs, Value: string(plain)})
	}
	return out, nil
}

// RestoreKey derives a gateway's restore keypair from its encryption root.
// The public half is what an operator seals secrets to; the private half never
// leaves the gateway and is never stored.
func RestoreKey(root secrets.Root) (pub, priv *[32]byte, err error) {
	k, err := secrets.DeriveKey(root.CurrentIKM, RestoreKeyPurpose)
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
