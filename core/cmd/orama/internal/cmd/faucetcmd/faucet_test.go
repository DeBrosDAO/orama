package faucetcmd

import (
	"bytes"
	"encoding/json"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/cmdmeta"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/printer"
	"github.com/DeBrosOfficial/network/pkg/chainfaucet"
	"github.com/DeBrosOfficial/network/pkg/constants"
)

// ourselves makes every account lookup answer the user running the test.
func ourselves(t *testing.T) {
	t.Helper()
	old := lookupUser
	t.Cleanup(func() { lookupUser = old })
	lookupUser = func(name string) (*user.User, error) {
		return &user.User{Username: name, Uid: strconv.Itoa(os.Geteuid()), Gid: strconv.Itoa(os.Getegid())}, nil
	}
}

func TestInitKey_createsAKeyForTheOwnerAndNeverReplacesIt(t *testing.T) {
	ourselves(t)
	path := filepath.Join(t.TempDir(), "secrets", chainfaucet.KeyFileName)

	first, err := initKey(path, "orama")

	if err != nil || !first.Created || first.KeyFile != path || !strings.HasPrefix(first.Address, "orama1") {
		t.Fatalf("initKey = %+v, %v", first, err)
	}
	if key, err := chainfaucet.LoadKey(path); err != nil || key.Address() != first.Address {
		t.Fatalf("the file does not hold the account reported: %v", err)
	}
	again, err := initKey(path, "orama")
	if err != nil || again.Created || again.Address != first.Address {
		t.Fatalf("a second run = %+v, %v; want the same account and nothing created", again, err)
	}
}

func TestInitKey_refusesWhatItCannotDoSafely(t *testing.T) {
	ourselves(t)
	dir := t.TempDir()
	notAKey := filepath.Join(dir, "other")
	if err := os.WriteFile(notAKey, []byte("something else\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := initKey(notAKey, "orama"); clierr.CodeOf(err) != clierr.CodeFailure {
		t.Errorf("a file that is not a key: %v", err)
	}
	if got, _ := os.ReadFile(notAKey); string(got) != "something else\n" {
		t.Error("a file that is not a key was changed")
	}

	lookupUser = func(string) (*user.User, error) { return nil, user.UnknownUserError("ghost") }
	if _, err := initKey(filepath.Join(dir, "k"), "ghost"); clierr.CodeOf(err) != clierr.CodeUsage || !strings.Contains(err.Error(), "ghost") {
		t.Errorf("an unknown owner: %v", err)
	}
	lookupUser = func(string) (*user.User, error) { return &user.User{Uid: "x", Gid: "1"}, nil }
	if _, err := initKey(filepath.Join(dir, "k"), "odd"); err == nil {
		t.Error("an account with a user id that is not a number was accepted")
	}
	if _, err := os.Stat(filepath.Join(dir, "k")); err == nil {
		t.Error("a key was written for an account that could not be resolved")
	}
}

// Not root, and not the owner: the key file could not be handed over, so it is not started.
func TestInitKey_anotherAccountsKeyNeedsRoot(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: nothing is refused")
	}
	old := lookupUser
	t.Cleanup(func() { lookupUser = old })
	lookupUser = func(string) (*user.User, error) {
		return &user.User{Uid: strconv.Itoa(os.Geteuid() + 1), Gid: "1"}, nil
	}
	path := filepath.Join(t.TempDir(), "k")
	if _, err := initKey(path, "orama"); clierr.CodeOf(err) != clierr.CodeUsage || !strings.Contains(err.Error(), "root") {
		t.Fatalf("err = %v, want a usage error saying to run as root", err)
	}
	if _, err := os.Stat(path); err == nil {
		t.Error("a key file was made before the refusal")
	}
}

func TestInitCmd_runsOnTheNodeWithNoOperatorEnvironment(t *testing.T) {
	if !cmdmeta.IsNodeLocal(MaintCmd.Commands()[0]) {
		t.Error("init needs the operator's CA files and environment on a node that has none")
	}
}

func TestPrintInit(t *testing.T) {
	var out bytes.Buffer
	p := printer.New(&out, &out)
	rep := initReport{Address: "orama1abc", KeyFile: constants.ChainFaucetKeyFile, Created: true}
	if err := printInit(p, rep); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"orama1abc", "chain.faucet.enabled", "orama node restart"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "key_file") {
		t.Errorf("the default key file needs no key_file setting:\n%s", out.String())
	}

	out.Reset()
	other := initReport{Address: "orama1abc", KeyFile: "/etc/f.key"}
	_ = printInit(p, other)
	if !strings.Contains(out.String(), "chain.faucet.key_file: /etc/f.key") || !strings.Contains(out.String(), "already exists") {
		t.Errorf("a key kept in another file, already made:\n%s", out.String())
	}

	out.Reset()
	if err := printInit(p.WithJSON(true), rep); err != nil {
		t.Fatal(err)
	}
	var got initReport
	if err := json.Unmarshal(out.Bytes(), &got); err != nil || got != rep {
		t.Errorf("json = %s, %v", out.String(), err)
	}
}
