package tornet

import (
	"bytes"
	"context"
	"crypto/sha1" // #nosec G505 -- Tor identifies a relay by the SHA-1 of its identity key; this is a name, not a signature.
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Ceremony output layout, below the output directory.
const (
	CeremonyOfflineDir    = "offline"
	CeremonyDeployDir     = "deploy"
	CeremonyTranscript    = "TRANSCRIPT.txt"
	ceremonyKeysDir       = "keys"
	ceremonyDirMode       = 0o700
	ceremonyPublicMode    = 0o644
	ceremonyPassphraseMin = 16

	// Files tor and tor-gencert write below a DataDirectory's keys/ directory.
	KeyAuthorityIdentity = "authority_identity_key"
	KeyAuthoritySigning  = "authority_signing_key"
	KeyAuthorityCert     = "authority_certificate"
	KeyRelayIdentity     = "secret_id_key"
	KeyEd25519Master     = "ed25519_master_id_secret_key"
	KeyEd25519MasterPub  = "ed25519_master_id_public_key"

	// ed25519PublicHeaderLen is the fixed header tor writes before a 32-byte
	// ed25519 public key in its key files.
	ed25519PublicHeaderLen = 32
	ed25519KeyLen          = 32
	ed25519PublicTag       = "== ed25519v1-public: type0 =="
)

// Runner runs an external program with stdin and returns its combined output.
type Runner func(ctx context.Context, stdin []byte, name string, args ...string) ([]byte, error)

// AuthoritySpec is one authority to create.
type AuthoritySpec struct {
	Nickname string
	Address  string
	ORPort   int
	DirPort  int
}

// CeremonyRequest is one key ceremony: the authorities to create and the
// network they form.
type CeremonyRequest struct {
	Network Network // Authorities is ignored; Specs makes them.
	Specs   []AuthoritySpec
	OutDir  string
	// Passphrase encrypts every authority identity key. Use one per authority
	// in a real ceremony by running it once per custodian; a stagenet ceremony
	// may share one.
	Passphrase []byte
	// TorBinary and GencertBinary are the upstream programs; empty means the
	// names "tor" and "tor-gencert" on PATH.
	TorBinary     string
	GencertBinary string
	// Now is the clock used for the transcript.
	Now time.Time
}

// CeremonyResult is what a ceremony produced.
type CeremonyResult struct {
	Network Network
	// Expires is each authority's signing certificate expiry, by nickname.
	Expires map[string]time.Time
}

// RunCeremony generates the keys of a directory-authority set with the
// upstream tor and tor-gencert, on the machine it runs on. That machine should
// be air-gapped and have tor installed; docs/TOR_NETWORK.md has the procedure.
//
// For each authority it writes below OutDir:
//   - offline/<nickname>/authority_identity_key: the identity key, encrypted
//     with the passphrase. It signs certificates and nothing else. Move it to
//     offline media and delete it from this machine.
//   - deploy/<nickname>/keys/: what the authority host installs (the signing
//     key and its 12-month certificate, the relay identity keys).
//
// and, once, tor-network.json (public: the authority list every relay and
// client needs) and a transcript with the fingerprints to read aloud and sign.
// OutDir must not exist or be empty: a ceremony never overwrites keys.
func RunCeremony(ctx context.Context, run Runner, req CeremonyRequest) (CeremonyResult, error) {
	if err := req.check(); err != nil {
		return CeremonyResult{}, err
	}
	if err := prepareOutDir(req.OutDir); err != nil {
		return CeremonyResult{}, err
	}
	net := req.Network
	net.Authorities = nil
	expires := map[string]time.Time{}
	for _, spec := range req.Specs {
		a, exp, err := req.createAuthority(ctx, run, spec)
		if err != nil {
			return CeremonyResult{}, fmt.Errorf("authority %s: %w (%s now holds a partial ceremony: remove it and run again; nothing in it is usable)", spec.Nickname, err, req.OutDir)
		}
		net.Authorities = append(net.Authorities, a)
		expires[spec.Nickname] = exp
	}
	body, err := net.Marshal()
	if err != nil {
		return CeremonyResult{}, err
	}
	if err := os.WriteFile(filepath.Join(req.OutDir, "tor-network.json"), body, ceremonyPublicMode); err != nil {
		return CeremonyResult{}, fmt.Errorf("write tor-network.json: %w", err)
	}
	if err := os.WriteFile(filepath.Join(req.OutDir, CeremonyTranscript), []byte(transcript(net, expires, req.Now)), ceremonyPublicMode); err != nil {
		return CeremonyResult{}, fmt.Errorf("write the transcript: %w", err)
	}
	return CeremonyResult{Network: net, Expires: expires}, nil
}

func (r CeremonyRequest) check() error {
	if len(r.Specs) < MinAuthorities {
		return fmt.Errorf("a ceremony creates at least %d authorities, got %d", MinAuthorities, len(r.Specs))
	}
	if len(r.Passphrase) < ceremonyPassphraseMin {
		return fmt.Errorf("the identity-key passphrase must be at least %d characters", ceremonyPassphraseMin)
	}
	if strings.ContainsAny(string(r.Passphrase), "\r\n") {
		return errors.New("the identity-key passphrase must be one line")
	}
	if r.OutDir == "" {
		return errors.New("no output directory")
	}
	// Reuse Network's own checks on a network with placeholder identities.
	probe := r.Network
	probe.Authorities = nil
	for i, s := range r.Specs {
		probe.Authorities = append(probe.Authorities, Authority{
			Nickname: s.Nickname, Address: s.Address, ORPort: s.ORPort, DirPort: s.DirPort,
			V3Ident: fmt.Sprintf("%040X", i+1), Fingerprint: fmt.Sprintf("%040X", i+101),
			Ed25519ID: base64.RawStdEncoding.EncodeToString(append(make([]byte, ed25519KeyLen-1), byte(i+1))),
		})
	}
	return probe.Validate()
}

func prepareOutDir(dir string) error {
	entries, err := os.ReadDir(dir)
	switch {
	case err == nil && len(entries) > 0:
		return fmt.Errorf("%s is not empty: a ceremony never writes over keys; use a new directory", dir)
	case err != nil && !errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("read %s: %w", dir, err)
	}
	if err := os.MkdirAll(dir, ceremonyDirMode); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	return nil
}

// createAuthority makes one authority's relay keys with tor and its identity,
// signing key and certificate with tor-gencert, and reads back what the
// network file publishes.
func (r CeremonyRequest) createAuthority(ctx context.Context, run Runner, s AuthoritySpec) (Authority, time.Time, error) {
	deploy := filepath.Join(r.OutDir, CeremonyDeployDir, s.Nickname)
	offline := filepath.Join(r.OutDir, CeremonyOfflineDir, s.Nickname)
	keys := filepath.Join(deploy, ceremonyKeysDir)
	for _, d := range []string{deploy, offline} {
		if err := os.MkdirAll(d, ceremonyDirMode); err != nil {
			return Authority{}, time.Time{}, fmt.Errorf("create %s: %w", d, err)
		}
	}
	torBin, gencert := orDefault(r.TorBinary, "tor"), orDefault(r.GencertBinary, "tor-gencert")
	out, err := run(ctx, nil, torBin, "--defaults-torrc", "/dev/null", "-f", "/dev/null",
		"--DisableNetwork", "1", "--list-fingerprint", "--hush",
		"--DataDirectory", deploy, "--Nickname", s.Nickname, "--ORPort", fmt.Sprint(s.ORPort))
	if err != nil {
		return Authority{}, time.Time{}, fmt.Errorf("tor --list-fingerprint: %w\n%s", err, strings.TrimSpace(string(out)))
	}
	fingerprint, err := relayFingerprintFile(filepath.Join(keys, KeyRelayIdentity))
	if err != nil {
		return Authority{}, time.Time{}, err
	}
	if !strings.Contains(strings.ToUpper(strings.ReplaceAll(string(out), " ", "")), fingerprint) {
		return Authority{}, time.Time{}, fmt.Errorf("tor printed %q, but the relay identity key it wrote hashes to %s", strings.TrimSpace(string(out)), fingerprint)
	}
	edPub, err := os.ReadFile(filepath.Join(keys, KeyEd25519MasterPub))
	if err != nil {
		return Authority{}, time.Time{}, fmt.Errorf("tor wrote no ed25519 identity: %w", err)
	}
	ed, err := Ed25519Identity(edPub)
	if err != nil {
		return Authority{}, time.Time{}, err
	}
	out, err = run(ctx, r.Passphrase, gencert, "--create-identity-key", "--passphrase-fd", "0",
		"-m", fmt.Sprint(CertMonths), "-a", fmt.Sprintf("%s:%d", s.Address, s.DirPort),
		"-i", filepath.Join(offline, KeyAuthorityIdentity),
		"-s", filepath.Join(keys, KeyAuthoritySigning),
		"-c", filepath.Join(keys, KeyAuthorityCert))
	if err != nil {
		return Authority{}, time.Time{}, fmt.Errorf("tor-gencert: %w\n%s", err, strings.TrimSpace(string(out)))
	}
	identity, err := os.ReadFile(filepath.Join(offline, KeyAuthorityIdentity))
	if err != nil {
		return Authority{}, time.Time{}, fmt.Errorf("tor-gencert wrote no identity key: %w", err)
	}
	if !bytes.Contains(identity, []byte("ENCRYPTED")) {
		return Authority{}, time.Time{}, errors.New("tor-gencert left the authority identity key unencrypted, which this ceremony never accepts: the passphrase did not reach it")
	}
	cert, err := os.ReadFile(filepath.Join(keys, KeyAuthorityCert))
	if err != nil {
		return Authority{}, time.Time{}, fmt.Errorf("tor-gencert wrote no certificate: %w", err)
	}
	v3, expires, err := ParseAuthorityCertificate(string(cert))
	if err != nil {
		return Authority{}, time.Time{}, err
	}
	return Authority{Nickname: s.Nickname, Address: s.Address, ORPort: s.ORPort, DirPort: s.DirPort,
		V3Ident: v3, Fingerprint: fingerprint, Ed25519ID: ed}, expires, nil
}

func orDefault(v, d string) string {
	if v == "" {
		return d
	}
	return v
}

func relayFingerprintFile(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("tor wrote no relay identity key: %w", err)
	}
	return RelayFingerprint(raw)
}

// RelayFingerprint is the SHA-1 of the DER RSAPublicKey of a relay identity key
// file (tor's secret_id_key, a PEM RSA private key): the 40 hex digits a
// DirAuthority line and a relay descriptor name.
func RelayFingerprint(secretIDKey []byte) (string, error) {
	block, _ := pem.Decode(secretIDKey)
	if block == nil {
		return "", errors.New("the relay identity key is not a PEM file")
	}
	key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		return "", fmt.Errorf("parse the relay identity key: %w", err)
	}
	sum := sha1.Sum(x509.MarshalPKCS1PublicKey(&key.PublicKey)) // #nosec G401
	return strings.ToUpper(hex.EncodeToString(sum[:])), nil
}

// Ed25519Identity is the unpadded base64 of the ed25519 public key in the
// contents of a tor key file (ed25519_master_id_public_key: a 32-byte header,
// then the key): a relay's ed25519 identity.
func Ed25519Identity(keyFile []byte) (string, error) {
	if len(keyFile) != ed25519PublicHeaderLen+ed25519KeyLen || !strings.HasPrefix(string(keyFile), ed25519PublicTag) {
		return "", errors.New("not a tor ed25519 public key file")
	}
	return base64.RawStdEncoding.EncodeToString(keyFile[ed25519PublicHeaderLen:]), nil
}

// ParseAuthorityCertificate reads the authority identity digest and the expiry
// from a tor-gencert certificate ("fingerprint <40 hex>", "dir-key-expires <time>").
func ParseAuthorityCertificate(cert string) (v3ident string, expires time.Time, err error) {
	for _, line := range strings.Split(cert, "\n") {
		kw, rest, _ := strings.Cut(line, " ")
		switch kw {
		case "fingerprint":
			v3ident = strings.ToUpper(strings.TrimSpace(rest))
		case "dir-key-expires":
			if expires, err = parseConsensusTime(rest); err != nil {
				return "", time.Time{}, fmt.Errorf("certificate expiry: %w", err)
			}
		}
	}
	if err := checkFingerprint("certificate fingerprint", v3ident); err != nil {
		return "", time.Time{}, err
	}
	if expires.IsZero() {
		return "", time.Time{}, errors.New("the certificate has no dir-key-expires line")
	}
	return v3ident, expires, nil
}

func transcript(n Network, expires map[string]time.Time, now time.Time) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Orama Tor network %s: authority key ceremony\n", n.Name)
	if !now.IsZero() {
		fmt.Fprintf(&b, "Date (UTC): %s\n", now.UTC().Format(time.RFC3339))
	}
	b.WriteString("Read each fingerprint aloud against the authority host's own output before installing, and keep this file with the signed record of the ceremony.\n\n")
	for _, a := range n.Authorities {
		fmt.Fprintf(&b, "%s  %s:%d (dir %d)\n  relay RSA fingerprint  %s\n  v3 identity            %s\n  ed25519 identity       %s\n  signing certificate expires %s\n\n",
			a.Nickname, a.Address, a.ORPort, a.DirPort, a.Fingerprint, a.V3Ident, a.Ed25519ID, expires[a.Nickname].Format(time.RFC3339))
	}
	return b.String()
}
