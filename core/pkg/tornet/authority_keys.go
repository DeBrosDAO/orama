package tornet

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const (
	// AuthorityPassphraseFile is the file, below offline/<nickname>/, that holds the
	// passphrase of the authority identity key when the keys were made on the
	// authority's own machine (EnsureAuthorityKeys).
	AuthorityPassphraseFile = "passphrase"
	// authorityPassphraseBytes is the entropy of a generated passphrase: far above
	// the ceremony's 16-character floor, and the key it protects is a legacy PEM
	// that a stolen copy attacks offline.
	authorityPassphraseBytes = 32
	// secretFileOthers is every permission bit group and others must not have on a secret.
	secretFileOthers = 0o077
)

// AuthorityKeys is one authority's keys as they stand on the machine that makes
// them, and the public identity the network file lists for it.
type AuthorityKeys struct {
	// Created says this call made the keys; false says it read back keys an
	// earlier call made.
	Created   bool      `json:"created"`
	Authority Authority `json:"authority"`
	// Expires is when the signing certificate stops being valid.
	Expires time.Time `json:"expires"`
	// Bundle is the directory to give `orama global install --services dirauth
	// --tor-authority-keys`: deploy/<nickname> below the keys home.
	Bundle string `json:"bundle"`
}

// AuthorityKeysRequest is one authority whose keys are made where it will run.
type AuthorityKeysRequest struct {
	Spec AuthoritySpec
	// Dir is the keys home, a directory only root can enter. The same layout as a
	// ceremony's output goes below it: offline/<nickname>/ and deploy/<nickname>/keys/.
	Dir string
	// TorBinary and GencertBinary are the upstream programs; empty means the
	// names "tor" and "tor-gencert" on PATH.
	TorBinary     string
	GencertBinary string
	// Owner is the uid that must own the identity key and its passphrase when they are read
	// back: root, 0, for the command; a test sets its own.
	Owner int
	// Random is the source of the passphrase; nil means crypto/rand.
	Random io.Reader
}

// Validate checks the authority s describes the way a network file checks it,
// before any key exists.
func (s AuthoritySpec) Validate() error {
	if err := s.placeholder(0).validate(); err != nil {
		return fmt.Errorf("authority %q: %w", s.Nickname, err)
	}
	return nil
}

// EnsureAuthorityKeys makes the keys of ONE directory authority on the machine it
// will run on, with the same programs and the same key handling as the ceremony
// (RunCeremony): the relay identity with tor, and the authority identity key,
// the signing key and a 12-month certificate with tor-gencert. The identity key
// is encrypted with a passphrase, mode 0400, and lives in offline/<nickname>/,
// outside every tor DataDirectory and out of the deploy bundle that is installed
// for tor's account; the signing key, the certificate and the relay identity are
// in deploy/<nickname>/keys/, which is all `orama global install --services
// dirauth` reads. Nothing secret leaves the machine.
//
// The passphrase is generated here and kept beside the identity key
// (offline/<nickname>/passphrase, mode 0400), which is what a test network's
// authority needs and a production network's must not do: a production network
// runs the ceremony on an offline machine and keeps the identity key there.
//
// It is idempotent: when the keys exist they are read back and checked against
// Spec, never made again, so a run that stopped can be run again. A directory
// that holds only part of a set of keys is an error, because making new keys
// could replace an identity another file already names.
func EnsureAuthorityKeys(ctx context.Context, run Runner, req AuthorityKeysRequest) (AuthorityKeys, error) {
	if err := req.Spec.Validate(); err != nil {
		return AuthorityKeys{}, err
	}
	if req.Dir == "" {
		return AuthorityKeys{}, errors.New("no keys directory")
	}
	deploy := filepath.Join(req.Dir, CeremonyDeployDir, req.Spec.Nickname)
	offline := filepath.Join(req.Dir, CeremonyOfflineDir, req.Spec.Nickname)
	state, err := authorityKeyState(deploy, offline)
	if err != nil {
		return AuthorityKeys{}, err
	}
	switch state {
	case keysComplete:
		return req.readBack(deploy, offline)
	case keysPartial:
		return AuthorityKeys{}, fmt.Errorf("%s or %s holds part of the keys of authority %s: remove both if the authority was never installed, or finish by hand; keys are never made over keys", deploy, offline, req.Spec.Nickname)
	}
	return req.make(ctx, run, deploy, offline)
}

type keyState int

const (
	keysNone keyState = iota
	keysPartial
	keysComplete
)

// authorityKeyState says what the two directories hold: nothing, a finished set
// (the certificate is what tor-gencert writes last) or something in between.
func authorityKeyState(deploy, offline string) (keyState, error) {
	cert := filepath.Join(deploy, ceremonyKeysDir, KeyAuthorityCert)
	identity := filepath.Join(offline, KeyAuthorityIdentity)
	have := 0
	for _, p := range []string{cert, identity} {
		_, err := os.Lstat(p)
		switch {
		case err == nil:
			have++
		case !errors.Is(err, fs.ErrNotExist):
			return keysNone, fmt.Errorf("look for %s: %w", p, err)
		}
	}
	if have == 2 {
		return keysComplete, nil
	}
	for _, d := range []string{deploy, offline} {
		entries, err := os.ReadDir(d)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return keysNone, fmt.Errorf("read %s: %w", d, err)
		}
		if len(entries) > 0 {
			return keysPartial, nil
		}
	}
	return keysNone, nil
}

// make creates the keys: the passphrase first, so that no identity key is ever
// written that cannot be opened again.
func (r AuthorityKeysRequest) make(ctx context.Context, run Runner, deploy, offline string) (AuthorityKeys, error) {
	if err := os.MkdirAll(offline, ceremonyDirMode); err != nil {
		return AuthorityKeys{}, fmt.Errorf("create %s: %w", offline, err)
	}
	phrase, err := r.passphrase()
	if err != nil {
		return AuthorityKeys{}, err
	}
	if err := os.WriteFile(filepath.Join(offline, AuthorityPassphraseFile), []byte(phrase+"\n"), CeremonyOfflineKeyMode); err != nil {
		return AuthorityKeys{}, fmt.Errorf("write the passphrase of authority %s: %w", r.Spec.Nickname, err)
	}
	c := CeremonyRequest{OutDir: r.Dir, Passphrase: []byte(phrase), TorBinary: r.TorBinary, GencertBinary: r.GencertBinary}
	a, expires, err := c.createAuthority(ctx, run, r.Spec)
	if err != nil {
		return AuthorityKeys{}, fmt.Errorf("authority %s: %w (%s and %s now hold part of its keys: remove both if the authority was never installed)", r.Spec.Nickname, err, deploy, offline)
	}
	return AuthorityKeys{Created: true, Authority: a, Expires: expires, Bundle: deploy}, nil
}

func (r AuthorityKeysRequest) passphrase() (string, error) {
	src := r.Random
	if src == nil {
		src = rand.Reader
	}
	raw := make([]byte, authorityPassphraseBytes)
	if _, err := io.ReadFull(src, raw); err != nil {
		return "", fmt.Errorf("make a passphrase for the authority identity key: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// readAuthorityKeys reads back the identity of keys an earlier call made and
// checks it is the authority spec describes: the same checks the installer makes
// of the bundle, plus the identity key still being encrypted.
func (r AuthorityKeysRequest) readBack(deploy, offline string) (AuthorityKeys, error) {
	spec := r.Spec
	keys := filepath.Join(deploy, ceremonyKeysDir)
	for _, secret := range []string{KeyAuthorityIdentity, AuthorityPassphraseFile} {
		if err := checkSecretFile(filepath.Join(offline, secret), r.Owner); err != nil {
			return AuthorityKeys{}, err
		}
	}
	identity, err := os.ReadFile(filepath.Join(offline, KeyAuthorityIdentity))
	if err != nil {
		return AuthorityKeys{}, fmt.Errorf("read the authority identity key: %w", err)
	}
	if !bytes.Contains(identity, []byte("ENCRYPTED")) {
		return AuthorityKeys{}, fmt.Errorf("the authority identity key in %s is not encrypted, which the ceremony never accepts", offline)
	}
	fingerprint, err := relayFingerprintFile(filepath.Join(keys, KeyRelayIdentity))
	if err != nil {
		return AuthorityKeys{}, err
	}
	edPub, err := os.ReadFile(filepath.Join(keys, KeyEd25519MasterPub))
	if err != nil {
		return AuthorityKeys{}, fmt.Errorf("read the ed25519 identity: %w", err)
	}
	ed, err := Ed25519Identity(edPub)
	if err != nil {
		return AuthorityKeys{}, err
	}
	cert, err := os.ReadFile(filepath.Join(keys, KeyAuthorityCert))
	if err != nil {
		return AuthorityKeys{}, fmt.Errorf("read the certificate: %w", err)
	}
	v3, expires, err := ParseAuthorityCertificate(string(cert))
	if err != nil {
		return AuthorityKeys{}, err
	}
	a := Authority{Nickname: spec.Nickname, Address: spec.Address, ORPort: spec.ORPort, DirPort: spec.DirPort, V3Ident: v3, Fingerprint: fingerprint, Ed25519ID: ed}
	return AuthorityKeys{Authority: a, Expires: expires, Bundle: deploy}, nil
}

// checkSecretFile refuses a secret that is not what the command left: it must exist, be a
// regular file (a link could point root's reader or the install at another file), belong
// to owner, and be closed to group and others. Reading it back is only safe then.
func checkSecretFile(path string, owner int) error {
	st, err := os.Lstat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("%s is missing: the keys were made here, and an authority identity key without its passphrase (or the reverse) cannot be used", path)
	case err != nil:
		return fmt.Errorf("look at %s: %w", path, err)
	case !st.Mode().IsRegular():
		return fmt.Errorf("%s is not a regular file (%s): a secret is never read through a link or a device", path, st.Mode().Type())
	case st.Mode().Perm()&secretFileOthers != 0:
		return fmt.Errorf("%s has mode %o, which lets others in: it was made 0%o", path, st.Mode().Perm(), CeremonyOfflineKeyMode)
	}
	if sys, ok := st.Sys().(*syscall.Stat_t); ok && int(sys.Uid) != owner {
		return fmt.Errorf("%s belongs to uid %d, want %d", path, sys.Uid, owner)
	}
	return nil
}

// ParseAuthorityKeys reads the document `orama maint global tor authority-keys`
// prints, from a machine this process does not control: unknown fields are an
// error and the identity is checked as a network file checks it.
func ParseAuthorityKeys(data []byte) (AuthorityKeys, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var k AuthorityKeys
	if err := dec.Decode(&k); err != nil {
		return AuthorityKeys{}, fmt.Errorf("parse the authority keys document: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return AuthorityKeys{}, errors.New("parse the authority keys document: trailing data after the object")
	}
	k.Authority.V3Ident, k.Authority.Fingerprint = strings.ToUpper(k.Authority.V3Ident), strings.ToUpper(k.Authority.Fingerprint)
	if err := k.Authority.validate(); err != nil {
		return AuthorityKeys{}, fmt.Errorf("authority %q: %w", k.Authority.Nickname, err)
	}
	if k.Expires.IsZero() {
		return AuthorityKeys{}, errors.New("the authority keys document has no certificate expiry")
	}
	return k, nil
}
