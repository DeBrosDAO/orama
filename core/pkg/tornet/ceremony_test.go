package tornet

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeTor stands in for tor and tor-gencert: it writes the files the real
// ones write (the keys tor makes, the certificate tor-gencert makes) so the
// ceremony's reading of them is exercised without the binaries.
type fakeTor struct {
	t       *testing.T
	calls   [][]string
	stdins  []string
	failTor bool
	// plainIdentity makes tor-gencert leave the identity key unencrypted.
	plainIdentity bool
	failCert      bool
	badPrint      bool
	certExtra     string
	seq           byte
}

func (f *fakeTor) run(_ context.Context, stdin []byte, name string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, append([]string{name}, args...))
	f.stdins = append(f.stdins, string(stdin))
	switch name {
	case "tor":
		return f.tor(args)
	case "tor-gencert":
		return f.gencert(args)
	}
	return nil, fmt.Errorf("unexpected program %s", name)
}

func argAfter(args []string, flag string) string {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func (f *fakeTor) tor(args []string) ([]byte, error) {
	if f.failTor {
		return []byte("Failed to parse/validate config"), errors.New("exit status 1")
	}
	dir, nick := argAfter(args, "--DataDirectory"), argAfter(args, "--Nickname")
	keys := filepath.Join(dir, "keys")
	if err := os.MkdirAll(keys, 0o700); err != nil {
		f.t.Fatal(err)
	}
	key, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		f.t.Fatal(err)
	}
	pemKey := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	f.write(filepath.Join(keys, KeyRelayIdentity), pemKey)
	f.seq++
	pub := append([]byte(ed25519PublicTag), make([]byte, ed25519PublicHeaderLen-len(ed25519PublicTag))...)
	pub = append(pub, append(make([]byte, ed25519KeyLen-1), f.seq)...)
	f.write(filepath.Join(keys, KeyEd25519MasterPub), pub)
	f.write(filepath.Join(keys, KeyEd25519Master), []byte("secret"))
	fp, _ := relayFingerprintFile(filepath.Join(keys, KeyRelayIdentity))
	if f.badPrint {
		fp = strings.Repeat("0", 40)
	}
	var spaced []string
	for i := 0; i < len(fp); i += 4 {
		spaced = append(spaced, fp[i:i+4])
	}
	return []byte(nick + " " + strings.Join(spaced, " ") + "\n"), nil
}

func (f *fakeTor) gencert(args []string) ([]byte, error) {
	if f.failCert {
		return []byte("bad passphrase"), errors.New("exit status 1")
	}
	identity := "-----BEGIN RSA PRIVATE KEY-----\nProc-Type: 4,ENCRYPTED\nDEK-Info: DES-EDE3-CBC,00\n-----END RSA PRIVATE KEY-----\n"
	if f.plainIdentity {
		identity = "-----BEGIN RSA PRIVATE KEY-----\nMIIB\n-----END RSA PRIVATE KEY-----\n"
	}
	f.write(argAfter(args, "-i"), []byte(identity))
	f.write(argAfter(args, "-s"), []byte("signing key"))
	cert := fmt.Sprintf("dir-key-certificate-version 3\nfingerprint %040X\ndir-key-published 2026-10-08 00:00:00\ndir-key-expires 2027-10-08 00:00:00\n%s", 0xD0+int(f.seq), f.certExtra)
	f.write(argAfter(args, "-c"), []byte(cert))
	return nil, nil
}

func (f *fakeTor) write(path string, data []byte) {
	f.t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		f.t.Fatal(err)
	}
}

func ceremonyRequest(t *testing.T) CeremonyRequest {
	return CeremonyRequest{
		Network: Network{Name: "orama-teststage", Private: true, VotingIntervalMinutes: 30, VoteDelaySeconds: 300, DistDelaySeconds: 300, AllowExit: true},
		Specs: []AuthoritySpec{
			{Nickname: "OramaAuth1", Address: "57.129.166.16", ORPort: 31020, DirPort: 31021},
			{Nickname: "OramaAuth2", Address: "57.129.166.17", ORPort: 31020, DirPort: 31021},
			{Nickname: "OramaAuth3", Address: "161.97.184.199", ORPort: 31020, DirPort: 31021},
		},
		OutDir:     filepath.Join(t.TempDir(), "ceremony"),
		Passphrase: []byte("correct horse battery staple"),
		Now:        time.Date(2026, 10, 8, 9, 0, 0, 0, time.UTC),
	}
}

func TestRunCeremony_producesALoadableNetworkAndSplitsTheKeys(t *testing.T) {
	f := &fakeTor{t: t}
	req := ceremonyRequest(t)
	res, err := RunCeremony(context.Background(), f.run, req)
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(req.OutDir, "tor-network.json"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseNetwork(body)
	if err != nil {
		t.Fatalf("the network file the ceremony wrote does not load: %v", err)
	}
	if !got.Private || len(got.Authorities) != 3 || !got.AllowExit || got.Authorities[1].V3Ident != fmt.Sprintf("%040X", 0xD2) {
		t.Fatalf("network = %+v", got)
	}
	if want := time.Date(2027, 10, 8, 0, 0, 0, 0, time.UTC); !res.Expires["OramaAuth1"].Equal(want) {
		t.Errorf("expiry = %v", res.Expires["OramaAuth1"])
	}
	for _, a := range got.Authorities {
		identity := filepath.Join(req.OutDir, CeremonyOfflineDir, a.Nickname, KeyAuthorityIdentity)
		if _, err := os.Stat(identity); err != nil {
			t.Errorf("%s: no offline identity key: %v", a.Nickname, err)
		}
		keys := filepath.Join(req.OutDir, CeremonyDeployDir, a.Nickname, "keys")
		for _, k := range []string{KeyAuthoritySigning, KeyAuthorityCert, KeyRelayIdentity, KeyEd25519Master, KeyEd25519MasterPub} {
			if _, err := os.Stat(filepath.Join(keys, k)); err != nil {
				t.Errorf("%s: deploy bundle lacks %s", a.Nickname, k)
			}
		}
		if _, err := os.Stat(filepath.Join(keys, KeyAuthorityIdentity)); err == nil {
			t.Errorf("%s: the identity key is in the deploy bundle", a.Nickname)
		}
	}
	tr, _ := os.ReadFile(filepath.Join(req.OutDir, CeremonyTranscript))
	if !strings.Contains(string(tr), got.Authorities[0].Fingerprint) || strings.Contains(string(tr), string(req.Passphrase)) {
		t.Errorf("transcript:\n%s", tr)
	}
}

func TestRunCeremony_passphraseGoesOnStdinNotTheCommandLine(t *testing.T) {
	f := &fakeTor{t: t}
	req := ceremonyRequest(t)
	if _, err := RunCeremony(context.Background(), f.run, req); err != nil {
		t.Fatal(err)
	}
	gencerts := 0
	for i, call := range f.calls {
		if strings.Contains(strings.Join(call, " "), string(req.Passphrase)) {
			t.Errorf("the passphrase is on a command line: %v", call)
		}
		if call[0] == "tor-gencert" {
			gencerts++
			if f.stdins[i] != string(req.Passphrase) || argAfter(call, "--passphrase-fd") != "0" || argAfter(call, "-m") != "12" {
				t.Errorf("tor-gencert call = %v stdin %q", call, f.stdins[i])
			}
			if !strings.Contains(strings.Join(call, " "), "-a 57.129.166.") && !strings.Contains(strings.Join(call, " "), "-a 161.97.184.199:31021") {
				t.Errorf("tor-gencert was not given the authority address: %v", call)
			}
		}
	}
	if gencerts != 3 {
		t.Fatalf("%d tor-gencert runs", gencerts)
	}
}

func TestRunCeremony_refusals(t *testing.T) {
	cases := map[string]func(*CeremonyRequest){
		"two authorities":  func(r *CeremonyRequest) { r.Specs = r.Specs[:2] },
		"short passphrase": func(r *CeremonyRequest) { r.Passphrase = []byte("short") },
		"multiline phrase": func(r *CeremonyRequest) { r.Passphrase = []byte("aaaaaaaaaaaaaaaaaaaa\nbbbb") },
		"no out dir":       func(r *CeremonyRequest) { r.OutDir = "" },
		"private address":  func(r *CeremonyRequest) { r.Specs[0].Address = "10.1.2.3" },
		"duplicate nick":   func(r *CeremonyRequest) { r.Specs[1].Nickname = r.Specs[0].Nickname },
		"bad schedule":     func(r *CeremonyRequest) { r.Network.VotingIntervalMinutes = 7 },
		"bad network name": func(r *CeremonyRequest) { r.Network.Name = "" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			f := &fakeTor{t: t}
			req := ceremonyRequest(t)
			mutate(&req)
			if _, err := RunCeremony(context.Background(), f.run, req); err == nil {
				t.Fatal("accepted")
			}
			if len(f.calls) != 0 {
				t.Fatalf("tor ran before the request was checked: %v", f.calls)
			}
		})
	}
}

func TestRunCeremony_neverWritesOverKeys(t *testing.T) {
	f := &fakeTor{t: t}
	req := ceremonyRequest(t)
	if err := os.MkdirAll(req.OutDir, 0o700); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(req.OutDir, "old-key"), []byte("x"))
	if _, err := RunCeremony(context.Background(), f.run, req); err == nil || !strings.Contains(err.Error(), "not empty") {
		t.Fatalf("err = %v", err)
	}
	if len(f.calls) != 0 {
		t.Fatal("tor ran in a directory that held keys")
	}
}

func TestRunCeremony_toolFailuresAreNamedAndStopTheCeremony(t *testing.T) {
	t.Run("tor fails", func(t *testing.T) {
		f := &fakeTor{t: t, failTor: true}
		_, err := RunCeremony(context.Background(), f.run, ceremonyRequest(t))
		if err == nil || !strings.Contains(err.Error(), "OramaAuth1") || !strings.Contains(err.Error(), "tor --list-fingerprint") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("tor-gencert fails", func(t *testing.T) {
		f := &fakeTor{t: t, failCert: true}
		_, err := RunCeremony(context.Background(), f.run, ceremonyRequest(t))
		if err == nil || !strings.Contains(err.Error(), "tor-gencert") || !strings.Contains(err.Error(), "bad passphrase") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("tor prints another fingerprint than its key hashes to", func(t *testing.T) {
		f := &fakeTor{t: t, badPrint: true}
		_, err := RunCeremony(context.Background(), f.run, ceremonyRequest(t))
		if err == nil || !strings.Contains(err.Error(), "hashes to") {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestParseAuthorityCertificate(t *testing.T) {
	v3, exp, err := ParseAuthorityCertificate("dir-key-certificate-version 3\nfingerprint " + strings.Repeat("ab", 20) + "\ndir-key-expires 2027-01-02 03:04:05\n")
	if err != nil || v3 != strings.Repeat("AB", 20) || exp.Year() != 2027 {
		t.Fatalf("%q %v %v", v3, exp, err)
	}
	for _, bad := range []string{"", "fingerprint abcd\ndir-key-expires 2027-01-02 03:04:05\n", "fingerprint " + strings.Repeat("ab", 20) + "\n", "fingerprint " + strings.Repeat("ab", 20) + "\ndir-key-expires soon\n"} {
		if _, _, err := ParseAuthorityCertificate(bad); err == nil {
			t.Errorf("certificate %q accepted", bad)
		}
	}
}

func TestEd25519Identity(t *testing.T) {
	good := append([]byte(ed25519PublicTag), make([]byte, ed25519PublicHeaderLen-len(ed25519PublicTag))...)
	good = append(good, make([]byte, ed25519KeyLen)...)
	id, err := Ed25519Identity(good)
	if err != nil || len(id) != ed25519IDLen {
		t.Fatalf("%q %v", id, err)
	}
	if _, err := Ed25519Identity(good[:40]); err == nil {
		t.Fatal("a truncated key file was accepted")
	}
	if _, err := Ed25519Identity(nil); err == nil {
		t.Fatal("an empty key file was accepted")
	}
	wrongTag := append([]byte("== something else =============="), good[ed25519PublicHeaderLen-2:]...)
	if _, err := Ed25519Identity(wrongTag); err == nil {
		t.Fatal("a file with another header was accepted")
	}
}

// If tor-gencert did not take the passphrase, the identity key is on the
// ceremony machine in the clear. The ceremony refuses to go on.
func TestRunCeremony_refusesAnUnencryptedIdentityKey(t *testing.T) {
	f := &fakeTor{t: t, plainIdentity: true}
	_, err := RunCeremony(context.Background(), f.run, ceremonyRequest(t))
	if err == nil || !strings.Contains(err.Error(), "unencrypted") {
		t.Fatalf("err = %v", err)
	}
}

func TestRunCeremony_aFailureSaysWhereTheLeftoversAreAndWhatToDo(t *testing.T) {
	f := &fakeTor{t: t, failCert: true}
	req := ceremonyRequest(t)
	_, err := RunCeremony(context.Background(), f.run, req)
	if err == nil || !strings.Contains(err.Error(), req.OutDir) || !strings.Contains(err.Error(), "remove it and run again") {
		t.Fatalf("err = %v", err)
	}
}

// tor refuses a character device as its configuration on macOS ("Unable to
// open configuration file /dev/null"), so --list-fingerprint must be given a
// regular empty file, and that file must not end up in a bundle.
func TestRunCeremony_givesTorARegularEmptyConfigOutsideTheBundle(t *testing.T) {
	f := &fakeTor{t: t}
	req := ceremonyRequest(t)
	var configs []string
	run := func(ctx context.Context, stdin []byte, name string, args ...string) ([]byte, error) {
		if name == "tor" {
			for _, flag := range []string{"-f", "--defaults-torrc"} {
				path := argAfter(args, flag)
				st, err := os.Lstat(path)
				if err != nil || !st.Mode().IsRegular() || st.Size() != 0 {
					t.Errorf("tor %s %q: want an existing regular empty file, got %v, %v", flag, path, st, err)
				}
				if strings.HasPrefix(path, req.OutDir) {
					t.Errorf("tor %s %q is inside the ceremony output", flag, path)
				}
				configs = append(configs, path)
			}
		}
		return f.run(ctx, stdin, name, args...)
	}
	if _, err := RunCeremony(context.Background(), run, req); err != nil {
		t.Fatal(err)
	}
	if len(configs) == 0 {
		t.Fatal("tor was never run")
	}
	for _, path := range configs {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("the empty torrc %s was left behind (%v)", path, err)
		}
	}
}
