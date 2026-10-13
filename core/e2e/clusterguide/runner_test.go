package clusterguide

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// fakeExec answers commands from memory and records what it was asked.
type fakeExec struct {
	calls   [][]string
	fail    string
	outputs map[string]string
}

func (f *fakeExec) Run(_ context.Context, argv []string) (string, error) {
	f.calls = append(f.calls, argv)
	line := strings.Join(argv, " ")
	if f.fail != "" && strings.Contains(line, f.fail) {
		return "boom", errors.New("exit status 1")
	}
	for prefix, out := range f.outputs {
		if strings.HasPrefix(line, prefix) {
			return out, nil
		}
	}
	return "", nil
}

func (f *fakeExec) lines() []string {
	var out []string
	for _, c := range f.calls {
		out = append(out, strings.Join(c, " "))
	}
	return out
}

func testFixture() *Fixture {
	return &Fixture{
		IPs:        []string{"198.51.100.1", "198.51.100.2", "198.51.100.3"},
		BaseDomain: "run.test.dev",
		EnvName:    "e2eguide",
		Archive:    "/build/orama.tar.gz",
		SiteDir:    "/tmp/site",
		HostKey:    func(ip string) (string, error) { return "SHA256:key-" + ip, nil },
		LookupNS:   func(string) ([]string, error) { return []string{"ns1.run.test.dev."}, nil },
		CertServed: func(string) error { return nil },
		Sleep:      func(time.Duration) {},
	}
}

const healthyJSON = `[{"host":"198.51.100.1","role":"nameserver","status":"healthy"},{"host":"198.51.100.2","role":"nameserver","status":"healthy"},{"host":"198.51.100.3","role":"nameserver","status":"healthy"}]`

func happyExec() *fakeExec {
	return &fakeExec{outputs: map[string]string{
		"orama node dns delegation": "NS run.test.dev\n",
		"orama status":              healthyJSON,
		"orama app list":            "www\n",
	}}
}

func guideCommands(t *testing.T) []Command {
	t.Helper()
	cmds, err := ParseGuide(readGuide(t))
	if err != nil {
		t.Fatal(err)
	}
	return CoveredCommands(cmds)
}

func TestExecute_runsTheGuideWithTheFixturesValues(t *testing.T) {
	ex := happyExec()
	fx := testFixture()
	fx.TokenFile = "/secrets/cf-token"
	results, err := Runner{Exec: ex, Fx: fx}.Execute(context.Background(), guideCommands(t), Plan())
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != len(Plan()) {
		t.Fatalf("%d results for %d steps", len(results), len(Plan()))
	}
	lines := ex.lines()
	if len(lines) == 0 || !strings.HasPrefix(lines[0], "orama node setup --ip 198.51.100.1 ") {
		t.Fatalf("first command = %v", lines)
	}
	genesis := lines[0]
	for _, want := range []string{"--genesis", "--env e2eguide", "--base-domain run.test.dev", "--archive /build/orama.tar.gz", "--host-key SHA256:key-198.51.100.1", "--acme-ca letsencrypt-staging"} {
		if !strings.Contains(genesis, want) {
			t.Errorf("genesis command lacks %q: %s", want, genesis)
		}
	}
	all := strings.Join(lines, "\n")
	for _, want := range []string{
		"--join-via root@198.51.100.1",
		"orama node setup --ip 198.51.100.2",
		"orama node setup --ip 198.51.100.3",
		"--cloudflare-token-file /secrets/cf-token",
		"orama network use e2eguide",
		"orama deploy static /tmp/site --name www",
		"orama status --env e2eguide --json",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("no command contains %q:\n%s", want, all)
		}
	}
	for _, leftover := range []string{"203.0.113", "example.com", "mycluster"} {
		if strings.Contains(all, leftover) {
			t.Errorf("an example value %q reached the executor:\n%s", leftover, all)
		}
	}
	if strings.Contains(all, "rw vault") || strings.Contains(all, "orama maint build") {
		t.Errorf("a step the fixture provides was executed:\n%s", all)
	}
}

func TestExecute_withoutATokenTheCloudflareStepIsSkipped(t *testing.T) {
	ex := happyExec()
	results, err := Runner{Exec: ex, Fx: testFixture()}.Execute(context.Background(), guideCommands(t), Plan())
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range ex.lines() {
		if strings.Contains(line, "--cloudflare-token-file") {
			t.Errorf("token step ran without a token: %s", line)
		}
	}
	var skipped []string
	for _, r := range results {
		if r.Skipped != "" {
			skipped = append(skipped, r.Step)
		}
	}
	if !contains(skipped, "write the delegation at Cloudflare") {
		t.Errorf("skipped = %v", skipped)
	}
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func TestExecute_useOnlySkipsInstallAndChecksTheRest(t *testing.T) {
	ex := happyExec()
	fx := testFixture()
	fx.UseOnly, fx.IPs, fx.Archive = true, nil, ""
	if _, err := (Runner{Exec: ex, Fx: fx}).Execute(context.Background(), guideCommands(t), Plan()); err != nil {
		t.Fatal(err)
	}
	for _, line := range ex.lines() {
		if strings.Contains(line, "node setup") || strings.Contains(line, "dns delegation") {
			t.Errorf("an install step ran on a use-only run: %s", line)
		}
	}
	if len(ex.lines()) != 7 {
		t.Errorf("ran %d commands, want the 5 Use steps and 2 Check steps:\n%v", len(ex.lines()), ex.lines())
	}
}

func TestExecute_stopsAtTheFirstFailureAndNamesTheStep(t *testing.T) {
	ex := happyExec()
	ex.fail = "orama node setup --ip 198.51.100.2"
	results, err := Runner{Exec: ex, Fx: testFixture()}.Execute(context.Background(), guideCommands(t), Plan())
	if err == nil || !strings.Contains(err.Error(), "join the second node") || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err = %v", err)
	}
	for _, line := range ex.lines() {
		if strings.Contains(line, "198.51.100.3") {
			t.Errorf("a later step ran after the failure: %s", line)
		}
	}
	if last := results[len(results)-1]; last.Step != "join the second node" {
		t.Errorf("last result = %s", last.Step)
	}
}

func TestExecute_healthChecks(t *testing.T) {
	tests := map[string]string{
		"a degraded node": `[{"host":"a","status":"healthy"},{"host":"b","status":"healthy"},{"host":"c","status":"degraded","error":"rqlite"}]`,
		"too few nodes":   `[{"host":"a","status":"healthy"}]`,
		"not a node list": `everything is fine`,
	}
	for name, out := range tests {
		t.Run(name, func(t *testing.T) {
			ex := happyExec()
			ex.outputs["orama status"] = out
			_, err := Runner{Exec: ex, Fx: testFixture()}.Execute(context.Background(), guideCommands(t), Plan())
			if err == nil || !strings.Contains(err.Error(), "every node is healthy") {
				t.Fatalf("err = %v, want the health step to fail", err)
			}
		})
	}
}

func TestExecute_deploymentMustBeListed(t *testing.T) {
	ex := happyExec()
	ex.outputs["orama app list"] = "no deployments\n"
	_, err := Runner{Exec: ex, Fx: testFixture()}.Execute(context.Background(), guideCommands(t), Plan())
	if err == nil || !strings.Contains(err.Error(), "www") {
		t.Fatalf("err = %v", err)
	}
}

func TestExecute_delegationOutputMustNameTheDomain(t *testing.T) {
	ex := happyExec()
	ex.outputs["orama node dns delegation"] = "nothing to publish\n"
	_, err := Runner{Exec: ex, Fx: testFixture()}.Execute(context.Background(), guideCommands(t), Plan())
	if err == nil || !strings.Contains(err.Error(), "base domain") {
		t.Fatalf("err = %v", err)
	}
}

func TestAwaitDelegation(t *testing.T) {
	t.Run("waits until the records and the certificate exist", func(t *testing.T) {
		fx := testFixture()
		polls := 0
		fx.LookupNS = func(string) ([]string, error) {
			if polls++; polls < 3 {
				return nil, nil
			}
			return []string{"ns1."}, nil
		}
		if err := awaitDelegation(context.Background(), fx); err != nil {
			t.Fatal(err)
		}
		if polls != 3 {
			t.Errorf("polled %d times", polls)
		}
	})
	t.Run("gives up with the last reason", func(t *testing.T) {
		fx := testFixture()
		fx.DelegationWait = time.Millisecond
		fx.CertServed = func(string) error { return errors.New("handshake failure") }
		fx.Sleep = func(time.Duration) { time.Sleep(2 * time.Millisecond) }
		err := awaitDelegation(context.Background(), fx)
		if err == nil || !strings.Contains(err.Error(), "handshake failure") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("a lookup error is reported", func(t *testing.T) {
		fx := testFixture()
		fx.DelegationWait = time.Millisecond
		fx.LookupNS = func(string) ([]string, error) { return nil, errors.New("SERVFAIL") }
		fx.Sleep = func(time.Duration) { time.Sleep(2 * time.Millisecond) }
		if err := awaitDelegation(context.Background(), fx); err == nil || !strings.Contains(err.Error(), "SERVFAIL") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("a cancelled context stops the wait", func(t *testing.T) {
		fx := testFixture()
		fx.LookupNS = func(string) ([]string, error) { return nil, nil }
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := awaitDelegation(ctx, fx); !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v", err)
		}
	})
}

// mutate returns the guide's commands after edit, to model a guide that
// drifted from the plan.
func mutate(t *testing.T, edit func([]Command) []Command) error {
	t.Helper()
	cmds := edit(append([]Command(nil), guideCommands(t)...))
	return Runner{Exec: happyExec(), Fx: testFixture()}.Verify(cmds, Plan())
}

func TestVerify_catchesAGuideThatDiverged(t *testing.T) {
	setupIdx := func(cmds []Command, nth int) int {
		for i, c := range cmds {
			if len(c.Argv) > 2 && c.Argv[2] == "setup" {
				if nth == 0 {
					return i
				}
				nth--
			}
		}
		t.Fatal("no such setup command")
		return -1
	}
	dropFlag := func(c *Command, flag string) {
		var argv []string
		skip := false
		for _, a := range c.Argv {
			switch {
			case skip:
				skip = false
			case a == flag:
				skip = flag != "--genesis" && flag != "--password"
			default:
				argv = append(argv, a)
			}
		}
		c.Argv = argv
	}
	tests := []struct {
		name string
		edit func([]Command) []Command
		want string
	}{
		{"a flag the step needs was dropped", func(c []Command) []Command {
			dropFlag(&c[setupIdx(c, 0)], "--acme-ca")
			return c
		}, "lacks --acme-ca"},
		{"the genesis flag was dropped", func(c []Command) []Command {
			dropFlag(&c[setupIdx(c, 0)], "--genesis")
			return c
		}, "lacks --genesis"},
		{"a joiner gained --genesis", func(c []Command) []Command {
			i := setupIdx(c, 1)
			c[i].Argv = append(append([]string(nil), c[i].Argv...), "--genesis")
			return c
		}, "must not"},
		{"a command was removed", func(c []Command) []Command { return append(c[:3:3], c[4:]...) }, "is not step"},
		{"a command was added at the end", func(c []Command) []Command {
			return append(c, Command{Section: SectionCheck, Line: "orama node remove", Argv: []string{"orama", "node", "remove"}})
		}, "does not run"},
		{"the guide ends early", func(c []Command) []Command { return c[:len(c)-1] }, "ends before"},
		{"two commands swapped", func(c []Command) []Command {
			c[len(c)-1], c[len(c)-2] = c[len(c)-2], c[len(c)-1]
			return c
		}, "is not step"},
		{"a command moved to another section", func(c []Command) []Command {
			c[len(c)-1].Section = SectionUse
			return c
		}, "under"},
		{"a command was renamed", func(c []Command) []Command {
			c[len(c)-1].Argv = []string{"orama", "app", "ls"}
			return c
		}, "is not step"},
		{"an example value the fixture cannot bind", func(c []Command) []Command {
			i := setupIdx(c, 0)
			c[i].Argv = append(append([]string(nil), c[i].Argv...), "--gateway", "https://other.example.com")
			return c
		}, "example value"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := mutate(t, tc.edit)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to contain %q", err, tc.want)
			}
		})
	}
}

func TestFixtureValidate(t *testing.T) {
	good := testFixture()
	if err := good.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, edit := range map[string]func(*Fixture){
		"two machines": func(f *Fixture) { f.IPs = f.IPs[:2] },
		"no domain":    func(f *Fixture) { f.BaseDomain = " " },
		"no archive":   func(f *Fixture) { f.Archive = "" },
		"no env name":  func(f *Fixture) { f.EnvName = "" },
		"no site dir":  func(f *Fixture) { f.SiteDir = "" },
	} {
		f := testFixture()
		edit(f)
		if err := f.Validate(); err == nil {
			t.Errorf("%s: fixture accepted", name)
		}
	}
	useOnly := testFixture()
	useOnly.UseOnly, useOnly.IPs, useOnly.Archive = true, nil, ""
	if err := useOnly.Validate(); err != nil {
		t.Errorf("a use-only fixture needs neither machines nor an archive: %v", err)
	}
}

func TestBind_replacesEveryExampleAndRefusesTheRest(t *testing.T) {
	fx := testFixture()
	got, err := fx.Bind(Command{Section: SectionInstall, Argv: []string{"orama", "x", "root@203.0.113.10", "cluster.example.com", "mycluster"}})
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(got) != "[orama x root@198.51.100.1 run.test.dev e2eguide]" {
		t.Errorf("Bind = %v", got)
	}
	if _, err := fx.Bind(Command{Section: SectionInstall, Argv: []string{"203.0.113.99"}}); err == nil {
		t.Error("an unbound example address was accepted")
	}
}

func TestHostKeyArgNeedsAnIP(t *testing.T) {
	if _, err := hostKeyArg(context.Background(), testFixture(), []string{"orama", "node", "setup"}); err == nil {
		t.Error("a setup command with no --ip got a host key")
	}
	fx := testFixture()
	fx.HostKey = func(string) (string, error) { return "", errors.New("no sshd") }
	if _, err := hostKeyArg(context.Background(), fx, []string{"--ip", "1.2.3.4"}); err == nil || !strings.Contains(err.Error(), "no sshd") {
		t.Errorf("err = %v", err)
	}
}
