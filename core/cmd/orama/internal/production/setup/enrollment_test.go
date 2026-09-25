package setup

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/inspector"
	"github.com/DeBrosOfficial/network/pkg/invite"
)

// Enrollment sends the VPS password over a connection whose host key the
// operator has confirmed, and installs the key without repeating it. These
// tests lock both properties in.

func TestInstallKeyArgs_NeverCarriesThePasswordAndPinsTheHostKey(t *testing.T) {
	const password = "sup3r-secret-vps-pass"
	args := installKeyArgs("1.2.3.4", "root", "/tmp/kh/known_hosts")

	joined := strings.Join(args, " ")
	if strings.Contains(joined, password) {
		t.Fatal("the password must never appear in argv: ps exposes it to every local process")
	}
	for _, forbidden := range []string{"-p", "StrictHostKeyChecking=no"} {
		for _, a := range args {
			if a == forbidden {
				t.Errorf("argument %q must not be used: %s", forbidden,
					map[string]string{
						"-p":                       "puts the password on the command line",
						"StrictHostKeyChecking=no": "accepts any host key on the connection that carries the password",
					}[forbidden])
			}
		}
	}

	mustContain(t, args, "-e")                                     // password read from SSHPASS
	mustContain(t, args, "StrictHostKeyChecking=yes")              // host key enforced
	mustContain(t, args, "UserKnownHostsFile=/tmp/kh/known_hosts") // against the pinned file
}

func mustContain(t *testing.T, args []string, want string) {
	t.Helper()
	for _, a := range args {
		if a == want {
			return
		}
	}
	t.Errorf("expected argument %q in %v", want, args)
}

// Re-running setup on a node that is already enrolled must not append the key
// again. The script is executed for real against a throwaway HOME so this
// tests the shell, not a description of it.
func TestInstallKeyScript_IsIdempotent(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not available")
	}
	home := t.TempDir()
	const pubKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExampleKeyMaterial orama@node"

	run := func() string {
		cmd := exec.Command(bash, "-c", installKeyScript)
		cmd.Env = append(os.Environ(), "HOME="+home)
		cmd.Stdin = strings.NewReader(pubKey + "\n")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("install script failed: %v (%s)", err, out)
		}
		return string(out)
	}

	for i := 0; i < 3; i++ {
		if out := run(); !strings.Contains(out, "key installed") {
			t.Fatalf("run %d did not confirm installation: %s", i+1, out)
		}
	}

	data, err := os.ReadFile(filepath.Join(home, ".ssh", "authorized_keys"))
	if err != nil {
		t.Fatalf("read authorized_keys: %v", err)
	}
	if got := strings.Count(string(data), pubKey); got != 1 {
		t.Fatalf("key present %d times after 3 runs, want exactly 1:\n%s", got, data)
	}
}

// A key whose comment contains shell metacharacters must be stored verbatim,
// not executed. The key travels on stdin precisely so this cannot happen.
func TestInstallKeyScript_DoesNotInterpretTheKey(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not available")
	}
	home := t.TempDir()
	canary := filepath.Join(home, "pwned")
	hostile := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExample '; touch " + canary + " ; echo '"

	cmd := exec.Command(bash, "-c", installKeyScript)
	cmd.Env = append(os.Environ(), "HOME="+home)
	cmd.Stdin = strings.NewReader(hostile + "\n")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("install script failed: %v (%s)", err, out)
	}

	if _, err := os.Stat(canary); err == nil {
		t.Fatal("the key was interpreted by the shell instead of being stored")
	}
	data, _ := os.ReadFile(filepath.Join(home, ".ssh", "authorized_keys"))
	if !strings.Contains(string(data), hostile) {
		t.Fatalf("key was not stored verbatim:\n%s", data)
	}
}

func TestHostKeyMatches_AcceptsEitherFingerprintForm(t *testing.T) {
	hk := &hostKey{lines: []string{"h ssh-ed25519 A", "h ecdsa B"}, fingerprints: []string{"SHA256:abc123", "SHA256:def456"}}

	for _, want := range []string{"SHA256:abc123", "abc123", "  SHA256:def456  "} {
		if !hk.matches(want) {
			t.Errorf("expected %q to match", want)
		}
	}
	for _, want := range []string{"", "SHA256:nope", "abc"} {
		if hk.matches(want) {
			t.Errorf("expected %q not to match", want)
		}
	}
}

func TestConfirmHostKey_RefusesAMismatchedPin(t *testing.T) {
	hk := &hostKey{lines: []string{"h ssh-ed25519 A"}, fingerprints: []string{"SHA256:actual"}}
	var out bytes.Buffer

	_, err := confirmHostKey(hk, "1.2.3.4", "SHA256:expected", strings.NewReader(""), &out)
	if err == nil {
		t.Fatal("a fingerprint mismatch must stop enrollment before the password is sent")
	}
	if !strings.Contains(err.Error(), "mismatch") {
		t.Fatalf("error should name the mismatch, got: %v", err)
	}
}

func TestConfirmHostKey_AcceptsAMatchingPinWithoutPrompting(t *testing.T) {
	hk := &hostKey{lines: []string{"h ssh-ed25519 A"}, fingerprints: []string{"SHA256:actual"}}
	var out bytes.Buffer

	// Empty stdin: a prompt here would fail, proving none was shown.
	if _, err := confirmHostKey(hk, "1.2.3.4", "SHA256:actual", strings.NewReader(""), &out); err != nil {
		t.Fatalf("matching pin should be accepted, got: %v", err)
	}
}

func TestConfirmHostKey_InteractiveAnswerDecides(t *testing.T) {
	hk := &hostKey{lines: []string{"h ssh-ed25519 A"}, fingerprints: []string{"SHA256:actual"}}

	for _, tc := range []struct {
		answer string
		wantOK bool
	}{
		{"y\n", true},
		{"yes\n", true},
		{"n\n", false},
		{"\n", false},
		{"", false}, // no input at all must not be read as consent
	} {
		var out bytes.Buffer
		_, err := confirmHostKey(hk, "1.2.3.4", "", strings.NewReader(tc.answer), &out)
		if tc.wantOK && err != nil {
			t.Errorf("answer %q should continue, got: %v", tc.answer, err)
		}
		if !tc.wantOK && err == nil {
			t.Errorf("answer %q must not be read as confirmation", tc.answer)
		}
		if !strings.Contains(out.String(), "SHA256:actual") {
			t.Errorf("the fingerprint must be shown to the operator, got: %s", out.String())
		}
	}
}

// Key-only VPS images (a non-root user with sudo, no password login) enroll
// over the key that opens them today. That connection carries the same trust
// as the password one, so it must pin the host key the same way and must not
// let the operator's ssh config substitute another policy or identity.
func TestInstallKeyWithKeyArgs_PinsTheHostKeyAndOffersOnlyThatKey(t *testing.T) {
	args := installKeyWithKeyArgs("1.2.3.4", "ubuntu", "/keys/boot", "/tmp/kh/known_hosts")

	mustContain(t, args, "StrictHostKeyChecking=yes")
	mustContain(t, args, "UserKnownHostsFile=/tmp/kh/known_hosts")
	mustContain(t, args, "IdentitiesOnly=yes")
	mustContain(t, args, "BatchMode=yes")
	mustContain(t, args, "ubuntu@1.2.3.4")
	mustContain(t, args, installKeyScript)

	if i := indexOf(args, "-F"); i < 0 || args[i+1] != "/dev/null" {
		t.Errorf("the operator's ssh config must be ignored (-F /dev/null): %v", args)
	}
	if i := indexOf(args, "-i"); i < 0 || args[i+1] != "/keys/boot" {
		t.Errorf("the bootstrap key must be passed with -i: %v", args)
	}
	for _, a := range args {
		if a == "StrictHostKeyChecking=no" || a == "StrictHostKeyChecking=accept-new" {
			t.Errorf("%q would trust an unpinned host key", a)
		}
	}
}

func indexOf(args []string, want string) int {
	for i, a := range args {
		if a == want && i+1 < len(args) {
			return i
		}
	}
	return -1
}

func TestEnrollKey_PasswordAndBootstrapKeyAreExclusive(t *testing.T) {
	err := enrollKey(Options{IP: "1.2.3.4", User: "root", Password: "p", BootstrapKey: "/k"}, "ssh-ed25519 AAAA", "/kh")
	if err == nil || !strings.Contains(err.Error(), "alternatives") {
		t.Fatalf("expected an error naming both flags as alternatives, got %v", err)
	}
}

// With no credential, enrollment is skipped and checkNodeAccess reports what
// to pass if the key turns out not to be installed.
func TestEnrollKey_NoCredentialSkipsEnrollment(t *testing.T) {
	if err := enrollKey(Options{IP: "1.2.3.4", User: "root"}, "ssh-ed25519 AAAA", "/kh"); err != nil {
		t.Fatalf("no credential must not attempt enrollment, got %v", err)
	}
}

func TestInstallPublicKeyWithKey_MissingKeyFileIsNamed(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no-such-key")
	err := installPublicKeyWithKey("1.2.3.4", "ubuntu", missing, "ssh-ed25519 AAAA", "/tmp/kh")
	if err == nil || !strings.Contains(err.Error(), "--bootstrap-key") || !strings.Contains(err.Error(), missing) {
		t.Fatalf("expected an error naming --bootstrap-key and the path, got %v", err)
	}
}

// Re-running setup replaced binaries by extracting over /opt/orama, so an
// older build's files — a manifest.sig that no longer matches, a binary a newer
// build dropped — survived next to the new ones. The node's data lives in the
// same directory and must survive instead.
func TestExtractArchiveCommand_ClearsArchiveFilesButNotNodeData(t *testing.T) {
	cmd := extractArchiveCommand("/tmp/orama-archive.AbC12345")
	for _, want := range []string{"rm -rf /opt/orama/bin", "/opt/orama/manifest.sig", "tar xzf /tmp/orama-archive.AbC12345/archive.tar.gz -C /tmp/orama-archive.AbC12345/extract", "cp -a /tmp/orama-archive.AbC12345/extract/. /opt/orama/", "rm -rf /tmp/orama-archive.AbC12345'", "set -e"} {
		if !strings.Contains(cmd, want) {
			t.Errorf("extract command missing %q: %s", want, cmd)
		}
	}
	for _, forbidden := range []string{"rm -rf /opt/orama;", "rm -rf /opt/orama ", "/opt/orama/.orama", "/opt/orama/*"} {
		if strings.Contains(cmd, forbidden) {
			t.Errorf("extract command must not touch %q: %s", forbidden, cmd)
		}
	}
}

func TestJoinArguments_JoinViaAndGatewayAreExclusive(t *testing.T) {
	_, err := joinArguments(Options{JoinVia: "debian@1.2.3.4", Gateway: "https://x"})
	if err == nil || !strings.Contains(err.Error(), "alternatives") {
		t.Fatalf("expected an alternatives error, got %v", err)
	}
}

func TestMintInviteOverSSH_RejectsMalformedJoinVia(t *testing.T) {
	for _, v := range []string{"1.2.3.4", "@1.2.3.4", "debian@", "", "-oProxyCommand=x@1.2.3.4", "debian@-oProxyCommand=x", "debian@host.example"} {
		_, err := mintInviteOverSSH(v)
		if err == nil || !strings.Contains(err.Error(), "want user@ip") {
			t.Errorf("--join-via %q: expected a user@ip error, got %v", v, err)
		}
	}
}

// Genesis records the cluster under https://<base domain>; a plain-HTTP IP
// URL would carry the operator's bearer token in the clear.
func TestRecordEnvironment_NeedsABaseDomain(t *testing.T) {
	err := recordEnvironment(Options{Env: "stagenet", IP: "1.2.3.4"})
	if err == nil || !strings.Contains(err.Error(), "--base-domain") {
		t.Fatalf("expected a --base-domain error, got %v", err)
	}
}

// A scan returns every key the answering host offers. Trusting all of them once
// one matched --host-key let an on-path attacker offer the real key alongside
// its own and pass the pin; only the matching entry may be trusted.
func TestConfirmHostKey_PinTrustsOnlyTheMatchingKey(t *testing.T) {
	hk := &hostKey{
		lines:        []string{"1.2.3.4 ssh-ed25519 REAL", "1.2.3.4 ecdsa-sha2-nistp256 ATTACKER"},
		fingerprints: []string{"SHA256:real", "SHA256:attacker"},
	}
	trusted, err := confirmHostKey(hk, "1.2.3.4", "SHA256:real", strings.NewReader(""), &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if len(trusted) != 1 || trusted[0] != "1.2.3.4 ssh-ed25519 REAL" {
		t.Fatalf("trusted %v, want only the matching key", trusted)
	}
}

// Every connection of a setup run uses the pinned file with strict checking;
// accept-new would trust whoever answers the upload or the install.
func TestHostKeyOptions_PinnedNodeIsStrict(t *testing.T) {
	pinned := inspector.Node{KnownHostsFile: "/tmp/kh/known_hosts"}
	got := strings.Join(pinned.HostKeyOptions(), " ")
	if got != "-o StrictHostKeyChecking=yes -o UserKnownHostsFile=/tmp/kh/known_hosts -o GlobalKnownHostsFile=/dev/null -o KnownHostsCommand=none" {
		t.Errorf("pinned node options = %q", got)
	}
	if got := strings.Join(inspector.Node{}.HostKeyOptions(), " "); got != "-o StrictHostKeyChecking=accept-new" {
		t.Errorf("unpinned node options = %q", got)
	}
}

func TestUploadDirPattern_OnlyMktempOutputPasses(t *testing.T) {
	if !uploadDirPattern.MatchString("/tmp/orama-archive.AbC12345") {
		t.Error("real mktemp output must pass")
	}
	for _, bad := range []string{"/tmp/orama-archive.AbC1234", "/tmp/orama-archive.AbC12345; rm -rf /", "/tmp/x", "", "/tmp/orama-archive.AbC1234'"} {
		if uploadDirPattern.MatchString(bad) {
			t.Errorf("%q must be refused: it is interpolated into a root shell", bad)
		}
	}
}

func TestRedactToken_HidesTheInvite(t *testing.T) {
	got := redactToken("sudo /opt/orama/bin/orama node install --vps-ip 1.2.3.4 --token SECRETINVITE --nameserver")
	if strings.Contains(got, "SECRETINVITE") || !strings.Contains(got, "--token '<invite>'") {
		t.Errorf("redactToken = %q", got)
	}
}

// A corrupt archive must fail before anything installed is removed.
func TestExtractArchiveCommand_UnpacksBeforeRemoving(t *testing.T) {
	cmd := extractArchiveCommand("/tmp/orama-archive.AbC12345")
	if strings.Index(cmd, "tar xzf") > strings.Index(cmd, "rm -rf /opt/orama/bin") {
		t.Errorf("tar must run before the installed files are removed: %s", cmd)
	}
}

// The invite comes from another machine and lands in a root command on the new
// VPS: anything but the invite format is refused, whatever it contains.
func TestValidateInvite_OnlyTheInviteFormatPasses(t *testing.T) {
	good, err := invite.Encode(invite.Invite{JoinURL: "https://stagenet.example", Token: strings.Repeat("ab", 32), CAFingerprint: "x"})
	if err != nil {
		t.Fatal(err)
	}
	for _, ok := range []string{good, strings.Repeat("0f", 32)} {
		if err := validateInvite(ok); err != nil {
			t.Errorf("%q refused: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "orama1_$(id)", "orama1_abc;reboot", good + " --force", "orama1_AAAA", strings.Repeat("0f", 31), "'" + good + "'"} {
		if err := validateInvite(bad); err == nil {
			t.Errorf("%q must be refused", bad)
		}
	}
}

func TestShellQuote_SurvivesQuotesAndMetacharacters(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not available")
	}
	for _, v := range []string{"plain", "it's", "$(id)", "a b;c", `"\`} {
		out, err := exec.Command(bash, "-c", "printf %s "+shellQuote(v)).Output()
		if err != nil || string(out) != v {
			t.Errorf("shellQuote(%q) round-tripped to %q (%v)", v, out, err)
		}
	}
}
