package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/DeBrosOfficial/network/e2e/harness/config"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

const testAddress = "0x852ad3DBB4A7da8b1D9C75Da2a7D35801a5A987e"

func newKey(t *testing.T) ssh.PublicKey {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	k, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// targetEnv is a fake owner's home with a known_hosts, the dev agent's ready
// file, and the keys each stagenet node offers.
type targetEnv struct {
	home  string
	in    targetInput
	nodes map[string]ssh.PublicKey
}

func newTargetEnv(t *testing.T, userLines func(env *targetEnv) []string) *targetEnv {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	e := &targetEnv{home: home, nodes: map[string]ssh.PublicKey{}}
	for _, n := range config.StagenetNodes {
		e.nodes[n.IP] = newKey(t)
	}
	write := func(rel, content string) {
		p := filepath.Join(home, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(config.StagenetKnownHostsRel, strings.Join(userLines(e), "\n")+"\n")
	write(config.StagenetRWReadyRel, `{"pid":1,"socket":"x","address":"`+testAddress+`"}`)
	e.in = targetInput{
		lay: layout{module: filepath.Join(home, "repo", "e2e"), repo: filepath.Join(home, "repo")}, realHome: home,
		out: filepath.Join(home, "out", "stagenet-state.json"), chainID: config.StagenetDefaultChainID,
		now: time.Date(2026, 9, 30, 10, 15, 0, 0, time.UTC),
		scan: func(_ context.Context, ip string) ([]ssh.PublicKey, error) {
			k, ok := e.nodes[ip]
			if !ok {
				return nil, errors.New("unexpected scan of " + ip)
			}
			return []ssh.PublicKey{k}, nil
		},
	}
	if err := os.MkdirAll(filepath.Dir(e.in.out), 0o700); err != nil {
		t.Fatal(err)
	}
	return e
}

// matching lists the owner's known_hosts entries for every node, the first
// one hashed as ssh does with HashKnownHosts.
func matching(e *targetEnv) []string {
	var lines []string
	for i, n := range config.StagenetNodes {
		host := n.IP
		if i == 0 {
			host = knownhosts.HashHostname(n.IP)
		}
		lines = append(lines, knownhosts.Line([]string{host}, e.nodes[n.IP]))
	}
	return lines
}

func TestWriteStagenetState_writesTheGuardedState(t *testing.T) {
	e := newTargetEnv(t, matching)
	if _, err := writeStagenetState(context.Background(), e.in); err != nil {
		t.Fatal(err)
	}
	st, err := fleet.Load(e.in.out)
	if err != nil {
		t.Fatal(err)
	}
	if err := fleet.CheckState(st, e.home); err != nil {
		t.Fatalf("the written state fails its own guards: %v", err)
	}
	if st.Target != config.TargetStagenet || st.RunID != "stagenet-20260930-101500" || st.OperatorAddress != testAddress ||
		st.ChainID != "orama-stagenet-1" || st.ChainRPC != "http://198.18.0.2:31001" || len(st.Nodes) != 3 {
		t.Fatalf("state %+v", st)
	}
	for i, want := range config.StagenetNodes {
		n := st.Nodes[i]
		if n.Name != want.Name || n.PublicIP != want.IP || n.SSHUser != want.User || n.WGIP != want.WGIP || n.Role != fleet.RoleNameserver {
			t.Errorf("node %d = %+v, want %+v", i, n, want)
		}
	}
	if st.OramaBin != filepath.Join(e.home, "repo", "core", "bin", "orama") || !strings.HasPrefix(st.ArtifactDir, filepath.Join(e.home, "repo", "e2e", "artifacts", "stagenet-")) {
		t.Fatalf("paths %s %s", st.OramaBin, st.ArtifactDir)
	}
	pinned, err := os.ReadFile(st.KnownHostsFile)
	if err != nil {
		t.Fatal(err)
	}
	for ip, key := range e.nodes {
		if !strings.Contains(string(pinned), ip+" "+key.Type()+" ") {
			t.Errorf("known_hosts does not pin %s:\n%s", ip, pinned)
		}
	}
}

func TestWriteStagenetState_refusals(t *testing.T) {
	cases := map[string]struct {
		lines  func(e *targetEnv) []string
		mutate func(e *targetEnv)
		want   string
	}{
		"changed host key": {
			lines: func(e *targetEnv) []string {
				l := matching(e)
				l[1] = knownhosts.Line([]string{config.StagenetNodes[1].IP}, newKey(t))
				return l
			},
			want: "HOST KEY MISMATCH",
		},
		"node missing from known_hosts": {
			lines: func(e *targetEnv) []string { return matching(e)[:2] },
			want:  "not in your known_hosts",
		},
		"known key of another type only": {
			lines: func(e *targetEnv) []string {
				l := matching(e)
				rsaLine := "57.128.226.141 ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAAAgQC7vbqajDw4o6gJy8UtmIbkcfu6/UrCa/rzWApN6E0kNXSXSJs0PB2vXo0Wrp2RHqwVbhWOfJ3Tj6MV+7kxRd0uOhGYNSvMiIhkGcL3pL7KwvxA4zqz/7oPJgxvSDUaYWn7wEwXhqW5kg0q0YQQsFO8CtiWY4hTVaJxDbsVW08mQw=="
				return append(l[:2], rsaLine)
			},
			want: "none of the host keys",
		},
		"scan fails": {
			lines: matching,
			mutate: func(e *targetEnv) {
				e.in.scan = func(context.Context, string) ([]ssh.PublicKey, error) { return nil, errors.New("timeout") }
			},
			want: "failed to scan",
		},
		"devnet chain id": {
			lines:  matching,
			mutate: func(e *targetEnv) { e.in.chainID = "orama-devnet-e2e-ab12" },
			want:   "chain id",
		},
		"agent not running": {
			lines:  matching,
			mutate: func(e *targetEnv) { _ = os.Remove(filepath.Join(e.home, config.StagenetRWReadyRel)) },
			want:   "ready file",
		},
		"not an address": {
			lines: matching,
			mutate: func(e *targetEnv) {
				_ = os.WriteFile(filepath.Join(e.home, config.StagenetRWReadyRel), []byte(`{"address":"nope"}`), 0o600)
			},
			want: "not an EVM address",
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			e := newTargetEnv(t, c.lines)
			if c.mutate != nil {
				c.mutate(e)
			}
			_, err := writeStagenetState(context.Background(), e.in)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err %v, want %q", err, c.want)
			}
			if _, statErr := os.Stat(e.in.out); statErr == nil {
				t.Fatal("a refused target still wrote a state file")
			}
		})
	}
}

func TestParseKeyscan(t *testing.T) {
	k := newKey(t)
	out := []byte("# 1.2.3.4:22 SSH-2.0-OpenSSH_9\n" + knownhosts.Line([]string{"1.2.3.4"}, k) + "\n")
	keys, err := parseKeyscan(out)
	if err != nil || len(keys) != 1 || string(keys[0].Marshal()) != string(k.Marshal()) {
		t.Fatalf("keys %v err %v", keys, err)
	}
	for _, bad := range []string{"", "# only a comment\n", "not a known_hosts line\n"} {
		if _, err := parseKeyscan([]byte(bad)); err == nil {
			t.Errorf("%q parsed", bad)
		}
	}
}

func TestCmdTarget_usage(t *testing.T) {
	for name, args := range map[string][]string{
		"no target":      nil,
		"other target":   {"devnet", "--out", "/tmp/x.json"},
		"no out":         {"stagenet"},
		"stray argument": {"stagenet", "--out", "/tmp/x.json", "extra"},
	} {
		t.Run(name, func(t *testing.T) {
			code, err := cmdTarget(context.Background(), args)
			if code != exitUsage || !errors.Is(err, errUsage) {
				t.Fatalf("code %d err %v", code, err)
			}
		})
	}
}

func writeState(t *testing.T, st *fleet.State) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "state.json")
	if err := st.Save(p); err != nil {
		t.Fatal(err)
	}
	return p
}

// run, provision, teardown, sweep and every hook refuse a stagenet state
// before they read a credential, a broker or a cloud API.
func TestServerCommands_refuseTheStagenetTarget(t *testing.T) {
	t.Setenv(config.EnvState, writeState(t, &fleet.State{Target: config.TargetStagenet}))
	ctx := context.Background()
	cmds := map[string]func() (int, error){
		"run":       func() (int, error) { return cmdRun(ctx, nil) },
		"provision": func() (int, error) { return cmdProvision(ctx, nil) },
		"teardown":  func() (int, error) { return cmdTeardown(ctx, nil) },
		"sweep":     func() (int, error) { return cmdSweep(ctx, []string{"--max-age", "48h"}) },
		"hook destroy": func() (int, error) {
			return cmdHook(ctx, []string{"destroy", "37.59.116.212"})
		},
		"hook break": func() (int, error) { return cmdHook(ctx, []string{"break", "37.59.116.212"}) },
		"hook provision": func() (int, error) {
			return cmdHook(ctx, []string{"provision"})
		},
	}
	for name, run := range cmds {
		t.Run(name, func(t *testing.T) {
			code, err := run()
			if code != exitFail || err == nil || !strings.Contains(err.Error(), "not available on the stagenet target") || !strings.Contains(err.Error(), "e2e-fleet "+name) {
				t.Fatalf("code %d err %v", code, err)
			}
		})
	}
}

func TestRefuseStagenetEnv_otherStates(t *testing.T) {
	t.Setenv(config.EnvState, "")
	if err := refuseStagenetEnv("run"); err != nil {
		t.Fatalf("unset state: %v", err)
	}
	t.Setenv(config.EnvState, writeState(t, &fleet.State{RunID: "ab12", Env: "e2e-ab12"}))
	if err := refuseStagenetEnv("teardown"); err != nil {
		t.Fatalf("fleet state: %v", err)
	}
	t.Setenv(config.EnvState, filepath.Join(t.TempDir(), "missing.json"))
	if err := refuseStagenetEnv("run"); err != nil {
		t.Fatalf("missing state: %v", err)
	}
}

// Stagenet's nodes are shared VPSs: its packages run two tests at a time unless the flag says otherwise;
// the fleet keeps go's default.
func TestTestParallel_defaultsPerTarget(t *testing.T) {
	stagenet := &fleet.State{Target: config.TargetStagenet}
	if got := testParallel(stagenet, -1); got != stagenetTestParallel {
		t.Errorf("stagenet default = %d, want %d", got, stagenetTestParallel)
	}
	if got := testParallel(stagenet, 6); got != 6 {
		t.Errorf("stagenet with --parallel 6 = %d", got)
	}
	if got := testParallel(&fleet.State{}, -1); got != 0 {
		t.Errorf("fleet default = %d, want 0 (go's default)", got)
	}
}
