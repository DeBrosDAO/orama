package releasecmd

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/pkg/releasepub"
	"github.com/DeBrosOfficial/network/pkg/releasepub/pubtest"
	"github.com/DeBrosOfficial/network/pkg/rwagent"
)

var testNow = time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)

// agent signs with a TEST key and counts approvals.
type agent struct {
	priv      ed25519.PrivateKey
	approvals int
}

func (a *agent) ReleaseKey(context.Context) (ed25519.PublicKey, error) {
	return a.priv.Public().(ed25519.PublicKey), nil
}

func (a *agent) SignForPurpose(_ context.Context, message, _, _ string) (*rwagent.WalletSignData, error) {
	if err := releasepub.CheckSignable([]byte(message)); err != nil {
		return nil, err
	}
	a.approvals++
	return &rwagent.WalletSignData{Signature: hex.EncodeToString(ed25519.Sign(a.priv, []byte(message)))}, nil
}

func newAgent(t *testing.T) *agent {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return &agent{priv: priv}
}

// exec runs `release <args>` with the agent and runner and returns the output
// and error.
func exec(t *testing.T, a releasepub.Agent, run releasepub.Runner, args ...string) (string, error) {
	t.Helper()
	cmd := newCommand(deps{agent: func() releasepub.Agent { return a }, run: run, now: func() time.Time { return testNow }})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	err := cmd.ExecuteContext(t.Context())
	return out.String(), err
}

func archiveFile(t *testing.T, version, arch string) string {
	t.Helper()
	return pubtest.Archive(t, version, arch, "bytes of "+version+arch)
}

func TestRelease_theWholeFlowFromRootToPublish(t *testing.T) {
	a := newAgent(t)
	dir := filepath.Join(t.TempDir(), "repo")
	out, err := exec(t, a, nil, "init-root", "--dir", dir)
	if err != nil || !strings.Contains(out, "root sha256: ") {
		t.Fatalf("init-root: %v\n%s", err, out)
	}
	out, err = exec(t, a, nil, "cut", "--dir", dir, "--channel", "nightly", "--archive", archiveFile(t, "0.3.1", "amd64"), "--archive", archiveFile(t, "0.3.1", "arm64"))
	if err != nil {
		t.Fatalf("cut: %v\n%s", err, out)
	}
	for _, want := range []string{"Approval 1 of 3", "Approval 3 of 3", "list    nightly/orama-0.3.1-linux-arm64.tar.gz", "GitHub release release-nightly-0.3.1"} {
		if !strings.Contains(out, want) {
			t.Errorf("cut output lacks %q:\n%s", want, out)
		}
	}
	out, err = exec(t, a, nil, "publish", "--dir", dir, "--dry-run", "--metadata-dest", "host:/srv/")
	if err != nil || !strings.Contains(out, "gh release upload release-nightly-0.3.1") || !strings.Contains(out, "Dry run") {
		t.Fatalf("publish --dry-run: %v\n%s", err, out)
	}
	if out, err = exec(t, a, nil, "refresh-timestamp", "--dir", dir, "--channel", "nightly"); err != nil || !strings.Contains(out, "timestamp version 2") {
		t.Fatalf("refresh-timestamp: %v\n%s", err, out)
	}
	if out, err = exec(t, a, nil, "renew-root", "--dir", dir); err == nil {
		t.Fatalf("a renewal on the day the root was made succeeded:\n%s", out)
	}
}

func TestCut_dryRunNeverContactsTheWallet(t *testing.T) {
	a := newAgent(t)
	dir := filepath.Join(t.TempDir(), "repo")
	if _, err := exec(t, a, nil, "init-root", "--dir", dir); err != nil {
		t.Fatal(err)
	}
	approved := a.approvals
	cmd := newCommand(deps{agent: func() releasepub.Agent { t.Fatal("the dry run asked for the agent"); return nil }, now: func() time.Time { return testNow }})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"cut", "--dir", dir, "--channel", "dev/Feat/X", "--archive", archiveFile(t, "0.3.1", "amd64"), "--dry-run"})
	if err := cmd.ExecuteContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	if a.approvals != approved || !strings.Contains(out.String(), "Would cut dev/feat-x 0.3.1") || !strings.Contains(out.String(), "Nothing was signed") {
		t.Fatalf("approvals %d -> %d\n%s", approved, a.approvals, out.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "targets.json")); err == nil {
		t.Fatal("a dry run wrote targets.json")
	}
}

func TestCut_usageErrorsCarryTheUsageExitCode(t *testing.T) {
	a := newAgent(t)
	dir := filepath.Join(t.TempDir(), "repo")
	for name, args := range map[string][]string{
		"no archive":  {"cut", "--dir", dir, "--channel", "nightly"},
		"bad channel": {"cut", "--dir", dir, "--channel", "stable", "--archive", "x"},
		"no channel":  {"cut", "--dir", dir, "--archive", "x"},
		"refresh bad": {"refresh-timestamp", "--dir", dir, "--channel", "Stable"},
	} {
		_, err := exec(t, a, nil, args...)
		if err == nil {
			t.Errorf("%s: no error", name)
			continue
		}
		if name != "no channel" && clierr.CodeOf(err) != clierr.CodeUsage {
			t.Errorf("%s: exit code %d, want usage", name, clierr.CodeOf(err))
		}
	}
}

func TestCut_aRepositoryWithNoRootSaysHowToMakeOne(t *testing.T) {
	_, err := exec(t, newAgent(t), nil, "cut", "--dir", t.TempDir(), "--channel", "nightly", "--archive", archiveFile(t, "0.3.1", "amd64"))
	if err == nil || !strings.Contains(err.Error(), "init-root") {
		t.Fatalf("err = %v", err)
	}
}

func TestPublish_runsTheCommandsThroughTheRunner(t *testing.T) {
	a := newAgent(t)
	dir := filepath.Join(t.TempDir(), "repo")
	if _, err := exec(t, a, nil, "init-root", "--dir", dir); err != nil {
		t.Fatal(err)
	}
	if _, err := exec(t, a, nil, "cut", "--dir", dir, "--channel", "main", "--archive", archiveFile(t, "0.3.0", "amd64")); err != nil {
		t.Fatal(err)
	}
	var calls []string
	run := func(_ context.Context, name string, args ...string) ([]byte, error) {
		calls = append(calls, name+" "+args[0])
		if name == "gh" && args[1] == "view" {
			return []byte(`{"assets":[]}`), nil
		}
		return nil, nil
	}
	if _, err := exec(t, a, run, "publish", "--dir", dir, "--github-repo", "o/r", "--metadata-dest", "h:/d/"); err != nil {
		t.Fatal(err)
	}
	if strings.Join(calls, ";") != "gh release;gh release;rsync -a;rsync -a" {
		t.Fatalf("calls = %q", calls)
	}
}

func TestNewCommand_hasTheFiveSubcommands(t *testing.T) {
	var names []string
	for _, c := range NewCommand().Commands() {
		names = append(names, c.Name())
	}
	for _, want := range []string{"init-root", "renew-root", "cut", "refresh-timestamp", "publish"} {
		found := false
		for _, n := range names {
			found = found || n == want
		}
		if !found {
			t.Errorf("no %s subcommand (have %v)", want, names)
		}
	}
}
