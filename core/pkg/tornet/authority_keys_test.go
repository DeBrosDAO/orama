package tornet

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func keysRequest(t *testing.T) AuthorityKeysRequest {
	return AuthorityKeysRequest{
		Spec: AuthoritySpec{Nickname: "OramaAuth01", Address: "57.129.166.16", ORPort: 31020, DirPort: 31021},
		Dir:  filepath.Join(t.TempDir(), "authority"),
		// The keys are made by whoever runs the test; the command's caller is root.
		Owner: os.Geteuid(),
	}
}

func TestEnsureAuthorityKeys_makesTheKeysWhereTheCeremonyPutsThem(t *testing.T) {
	f := &fakeTor{t: t}
	req := keysRequest(t)
	got, err := EnsureAuthorityKeys(context.Background(), f.run, req)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Created || got.Authority.Nickname != "OramaAuth01" || got.Authority.Address != "57.129.166.16" || got.Bundle != filepath.Join(req.Dir, "deploy", "OramaAuth01") {
		t.Fatalf("keys = %+v", got)
	}
	if err := got.Authority.validate(); err != nil {
		t.Fatalf("the identity does not validate: %v", err)
	}
	identity := filepath.Join(req.Dir, "offline", "OramaAuth01", KeyAuthorityIdentity)
	st, err := os.Stat(identity)
	if err != nil || st.Mode().Perm() != CeremonyOfflineKeyMode {
		t.Errorf("identity key = %v, %v, want mode %o", st, err, CeremonyOfflineKeyMode)
	}
	if _, err := os.Stat(filepath.Join(got.Bundle, "keys", KeyAuthorityIdentity)); err == nil {
		t.Error("the identity key is in the deploy bundle")
	}
	for _, k := range []string{KeyAuthoritySigning, KeyAuthorityCert, KeyRelayIdentity, KeyEd25519Master, KeyEd25519MasterPub} {
		if _, err := os.Stat(filepath.Join(got.Bundle, "keys", k)); err != nil {
			t.Errorf("the bundle lacks %s: %v", k, err)
		}
	}
}

func TestEnsureAuthorityKeys_thePassphraseIsGeneratedKeptReadOnlyAndNeverOnACommandLine(t *testing.T) {
	f := &fakeTor{t: t}
	req := keysRequest(t)
	if _, err := EnsureAuthorityKeys(context.Background(), f.run, req); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(req.Dir, "offline", "OramaAuth01", AuthorityPassphraseFile)
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	phrase := strings.TrimSuffix(string(raw), "\n")
	if len(phrase) < ceremonyPassphraseMin || strings.ContainsAny(phrase, "\r\n ") {
		t.Errorf("passphrase %q is shorter than %d characters or not one word", phrase, ceremonyPassphraseMin)
	}
	if st, _ := os.Stat(file); st.Mode().Perm() != CeremonyOfflineKeyMode {
		t.Errorf("passphrase file mode %o, want %o", st.Mode().Perm(), CeremonyOfflineKeyMode)
	}
	var gencert int
	for i, c := range f.calls {
		if strings.Contains(strings.Join(c, " "), phrase) {
			t.Errorf("the passphrase is on the command line of %v", c)
		}
		if c[0] == "tor-gencert" {
			gencert = i
		}
	}
	if f.stdins[gencert] != phrase {
		t.Errorf("tor-gencert read %q on stdin, want the generated passphrase", f.stdins[gencert])
	}
	if _, err := os.Stat(filepath.Join(req.Dir, "deploy", "OramaAuth01", "keys", AuthorityPassphraseFile)); err == nil {
		t.Error("the passphrase is in the deploy bundle")
	}
}

func TestEnsureAuthorityKeys_twoAuthoritiesGetTwoPassphrases(t *testing.T) {
	f := &fakeTor{t: t}
	a, b := keysRequest(t), keysRequest(t)
	b.Spec = AuthoritySpec{Nickname: "OramaAuth02", Address: "57.129.166.17", ORPort: 31020, DirPort: 31021}
	for _, r := range []AuthorityKeysRequest{a, b} {
		if _, err := EnsureAuthorityKeys(context.Background(), f.run, r); err != nil {
			t.Fatal(err)
		}
	}
	if f.stdins[1] == f.stdins[3] {
		t.Error("two authorities were given the same passphrase")
	}
}

func TestEnsureAuthorityKeys_runAgainReadsBackAndMakesNothing(t *testing.T) {
	f := &fakeTor{t: t}
	req := keysRequest(t)
	first, err := EnsureAuthorityKeys(context.Background(), f.run, req)
	if err != nil {
		t.Fatal(err)
	}
	calls := len(f.calls)
	again, err := EnsureAuthorityKeys(context.Background(), f.run, req)
	if err != nil {
		t.Fatal(err)
	}
	if again.Created || again.Authority != first.Authority || !again.Expires.Equal(first.Expires) {
		t.Errorf("second call = %+v, want the first call's identity with Created false", again)
	}
	if len(f.calls) != calls {
		t.Errorf("the second call ran %v: keys are never made over keys", f.calls[calls:])
	}
}

func TestEnsureAuthorityKeys_refusesWhatItCannotTrust(t *testing.T) {
	cases := []struct {
		name  string
		prep  func(t *testing.T, req AuthorityKeysRequest)
		spec  func(*AuthoritySpec)
		fake  func(*fakeTor)
		want  string
		calls bool
	}{
		{name: "a private address", spec: func(s *AuthoritySpec) { s.Address = "10.0.0.5" }, want: "not a public address"},
		{name: "a bad nickname", spec: func(s *AuthoritySpec) { s.Nickname = "has space" }, want: "nickname"},
		{name: "tor failing", fake: func(f *fakeTor) { f.failTor = true }, want: "tor --list-fingerprint", calls: true},
		{name: "an identity key tor-gencert left in the clear", fake: func(f *fakeTor) { f.plainIdentity = true }, want: "unencrypted", calls: true},
		{
			name: "part of a set of keys",
			prep: func(t *testing.T, req AuthorityKeysRequest) {
				if err := os.MkdirAll(filepath.Join(req.Dir, "deploy", "OramaAuth01", "keys"), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(req.Dir, "deploy", "OramaAuth01", "keys", KeyRelayIdentity), []byte("x"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
			want: "holds part of the keys",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := &fakeTor{t: t}
			if c.fake != nil {
				c.fake(f)
			}
			req := keysRequest(t)
			if c.spec != nil {
				c.spec(&req.Spec)
			}
			if c.prep != nil {
				c.prep(t, req)
			}
			_, err := EnsureAuthorityKeys(context.Background(), f.run, req)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want %q", err, c.want)
			}
			if !c.calls && len(f.calls) != 0 {
				t.Errorf("ran %v before refusing", f.calls)
			}
		})
	}
}

func TestEnsureAuthorityKeys_aFailedMakeLeavesAPartialSetThatIsNotMadeOver(t *testing.T) {
	f := &fakeTor{t: t, failCert: true}
	req := keysRequest(t)
	if _, err := EnsureAuthorityKeys(context.Background(), f.run, req); err == nil || !strings.Contains(err.Error(), "remove both") {
		t.Fatalf("err = %v, want the partial set named", err)
	}
	calls := len(f.calls)
	f.failCert = false
	if _, err := EnsureAuthorityKeys(context.Background(), f.run, req); err == nil || !strings.Contains(err.Error(), "holds part of the keys") {
		t.Fatalf("err = %v, want a refusal to make keys over a partial set", err)
	}
	if len(f.calls) != calls {
		t.Errorf("a partial set was made over: %v", f.calls[calls:])
	}
}

func TestEnsureAuthorityKeys_aReadBackKeyThatIsNoLongerEncryptedIsRefused(t *testing.T) {
	f := &fakeTor{t: t}
	req := keysRequest(t)
	if _, err := EnsureAuthorityKeys(context.Background(), f.run, req); err != nil {
		t.Fatal(err)
	}
	identity := filepath.Join(req.Dir, "offline", "OramaAuth01", KeyAuthorityIdentity)
	if err := os.Chmod(identity, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(identity, []byte("-----BEGIN RSA PRIVATE KEY-----\nMIIB\n-----END RSA PRIVATE KEY-----\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureAuthorityKeys(context.Background(), f.run, req); err == nil || !strings.Contains(err.Error(), "not encrypted") {
		t.Fatalf("err = %v, want the unencrypted key refused", err)
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("no entropy") }

func TestEnsureAuthorityKeys_noEntropyMakesNoKeys(t *testing.T) {
	f := &fakeTor{t: t}
	req := keysRequest(t)
	req.Random = failingReader{}
	if _, err := EnsureAuthorityKeys(context.Background(), f.run, req); err == nil || !strings.Contains(err.Error(), "no entropy") {
		t.Fatalf("err = %v", err)
	}
	if len(f.calls) != 0 {
		t.Errorf("ran %v without a passphrase", f.calls)
	}
}

func TestParseAuthorityKeys(t *testing.T) {
	f := &fakeTor{t: t}
	keys, err := EnsureAuthorityKeys(context.Background(), f.run, keysRequest(t))
	if err != nil {
		t.Fatal(err)
	}
	doc, err := json.Marshal(keys)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseAuthorityKeys(doc)
	if err != nil || got.Authority != keys.Authority || !got.Created {
		t.Fatalf("round trip = %+v, %v", got, err)
	}
	bad := map[string]string{
		"an unknown field":    strings.Replace(string(doc), `"created"`, `"extra":1,"created"`, 1),
		"trailing data":       string(doc) + "{}",
		"a bad fingerprint":   strings.Replace(string(doc), keys.Authority.Fingerprint, "ZZ", 1),
		"a private address":   strings.Replace(string(doc), "57.129.166.16", "10.1.2.3", 1),
		"not an object":       "[]",
		"nothing":             "",
		"a zero expiry value": strings.Replace(string(doc), keys.Expires.Format("2006-01-02T15:04:05Z07:00"), "0001-01-01T00:00:00Z", 1),
	}
	for name, body := range bad {
		if _, err := ParseAuthorityKeys([]byte(body)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// A secret that is read back must be what the command left: a regular file of the owner's,
// closed to group and others, and the identity key must have its passphrase beside it.
func TestEnsureAuthorityKeys_readBackRefusesASecretThatIsNotWhatWasLeft(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(t *testing.T, req *AuthorityKeysRequest, offline string)
		want   string
	}{
		{"an identity key a group can read", func(t *testing.T, _ *AuthorityKeysRequest, offline string) {
			chmod(t, filepath.Join(offline, KeyAuthorityIdentity), 0o440)
		}, "lets others in"},
		{"a passphrase the world can read", func(t *testing.T, _ *AuthorityKeysRequest, offline string) {
			chmod(t, filepath.Join(offline, AuthorityPassphraseFile), 0o444)
		}, "lets others in"},
		{"no passphrase", func(t *testing.T, _ *AuthorityKeysRequest, offline string) {
			if err := os.Remove(filepath.Join(offline, AuthorityPassphraseFile)); err != nil {
				t.Fatal(err)
			}
		}, "passphrase is missing"},
		{"an identity key that is a link", func(t *testing.T, _ *AuthorityKeysRequest, offline string) {
			path := filepath.Join(offline, KeyAuthorityIdentity)
			real := filepath.Join(offline, "real")
			if err := os.Rename(path, real); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(real, path); err != nil {
				t.Fatal(err)
			}
		}, "not a regular file"},
		{"a passphrase that is a directory", func(t *testing.T, _ *AuthorityKeysRequest, offline string) {
			path := filepath.Join(offline, AuthorityPassphraseFile)
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(path, 0o700); err != nil {
				t.Fatal(err)
			}
		}, "not a regular file"},
		{"a secret of another account", func(_ *testing.T, req *AuthorityKeysRequest, _ string) {
			req.Owner = os.Geteuid() + 1
		}, "belongs to uid"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := &fakeTor{t: t}
			req := keysRequest(t)
			if _, err := EnsureAuthorityKeys(context.Background(), f.run, req); err != nil {
				t.Fatal(err)
			}
			c.mutate(t, &req, filepath.Join(req.Dir, "offline", "OramaAuth01"))
			calls := len(f.calls)
			_, err := EnsureAuthorityKeys(context.Background(), f.run, req)
			if err == nil || !strings.Contains(err.Error(), c.want) || !strings.Contains(err.Error(), filepath.Join(req.Dir, "offline", "OramaAuth01")) && c.want != "belongs to uid" {
				t.Fatalf("err = %v, want %q with the path", err, c.want)
			}
			if len(f.calls) != calls {
				t.Errorf("keys were made over a set that was refused: %v", f.calls[calls:])
			}
		})
	}
}

func chmod(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}
