package decommission

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

func bashPath(t *testing.T) string {
	t.Helper()
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not available")
	}
	return bash
}

// fakeBin writes executables into a fresh directory and returns it.
func fakeBin(t *testing.T, scripts map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range scripts {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// inRoot points the absolute paths of a script block into root, so that the block can run
// on a test machine.
func inRoot(block, root string) string {
	pathList := regexp.MustCompile(`(?m)^for path in (.*); do$`)
	block = pathList.ReplaceAllStringFunc(block, func(line string) string {
		return regexp.MustCompile(` (/[^ ;]+)`).ReplaceAllString(line, " "+root+"$1")
	})
	return strings.ReplaceAll(block, "find /etc/systemd/system", "find "+root+"/etc/systemd/system")
}

// checkedPaths are the paths the check's loop looks at.
func checkedPaths(check string) []string {
	m := regexp.MustCompile(`(?m)^for path in (.*); do$`).FindStringSubmatch(check)
	if m == nil {
		return nil
	}
	return strings.Fields(m[1])
}

// removalPart is the script before its leftover check.
func removalPart(script string) string {
	i := strings.Index(script, "# What is left.")
	if i < 0 {
		return script
	}
	return script[:i]
}

// Every path the check looks at is a path the wipe removes, and the other way round for the
// paths listed here: a path in only one of the two is a wipe that cannot know it failed, or a
// check that fails a clean machine.
func TestWipeScript_leftoverCheckAndRemovalCoverTheSamePaths(t *testing.T) {
	for _, path := range wipedPaths() {
		if !strings.Contains(removalPart(wipeScript(false)), path) {
			t.Errorf("the check looks at %s, which the wipe does not remove", path)
		}
		if !slices.Contains(checkedPaths(leftoverCheck(false)), path) {
			t.Errorf("the check does not look at %s", path)
		}
	}
	for _, path := range nuclearPaths() {
		if !strings.Contains(removalPart(wipeScript(true)), path) {
			t.Errorf("the nuclear check looks at %s, which a nuclear wipe does not remove", path)
		}
		if !slices.Contains(checkedPaths(leftoverCheck(true)), path) || slices.Contains(checkedPaths(leftoverCheck(false)), path) {
			t.Errorf("%s is checked only with --nuclear", path)
		}
	}
}

// The drop-in directories of deployments and the leftover .wants links go, whatever the
// namespace in their name; nothing that is not orama's does.
func TestWipeScript_removesEveryOramaNamedEntryUnderTheUnitDirectory(t *testing.T) {
	root := t.TempDir()
	unitDir := filepath.Join(root, "etc/systemd/system")
	oramas := []string{
		"orama-deploy-build@ns1.service.d", "orama-deploy-clean@ns1.service.d", "orama-deploy-go@ns2.service.d",
		"orama-deploy-node@ns2.service.d", "orama-namespace-gateway@index.service.d", "multi-user.target.wants/orama-node.service",
	}
	others := []string{"ssh.service.d", "tailscaled.service", "multi-user.target.wants/ssh.service", "caddy-ops.service.d"}
	for _, name := range append(append([]string{}, oramas...), others...) {
		path := filepath.Join(unitDir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if strings.HasSuffix(name, ".d") {
			err := os.MkdirAll(path, 0o755)
			if err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.WriteFile(path, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	line := ""
	for _, l := range strings.Split(wipeScript(false), "\n") {
		if strings.HasPrefix(l, "find /etc/systemd/system -maxdepth 2 -name") {
			line = l
		}
	}
	if line == "" {
		t.Fatal("the wipe script does not sweep the unit directory")
	}
	if out, err := exec.Command(bashPath(t), "-c", strings.Replace(line, "/etc/systemd/system", unitDir, 1)).CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, name := range oramas {
		if _, err := os.Lstat(filepath.Join(unitDir, name)); err == nil {
			t.Errorf("%s was left", name)
		}
	}
	for _, name := range others {
		if _, err := os.Lstat(filepath.Join(unitDir, name)); err != nil {
			t.Errorf("%s is not orama's and was removed: %v", name, err)
		}
	}
}

// A transient unit that outlives its file is stopped and its failed state reset, then systemd
// reloads, in that order and after the unit files are gone.
func TestWipeScript_stopsAndForgetsUnitsThatOutliveTheirFile(t *testing.T) {
	script := wipeScript(false)
	rmAt := strings.Index(script, `find /etc/systemd/system -maxdepth 2`)
	sweepAt := strings.Index(script, `systemctl list-units --all --plain --no-legend "orama-*" | while`)
	stopAt := strings.Index(script[sweepAt:], `systemctl stop "$unit"`)
	resetAt := strings.Index(script[sweepAt:], `systemctl reset-failed "$unit"`)
	reloadAt := strings.Index(script[sweepAt:], "systemctl daemon-reload")
	if rmAt < 0 || sweepAt < rmAt || stopAt < 0 || resetAt < stopAt || reloadAt < resetAt {
		t.Errorf("sweep order wrong: files %d, list %d, stop %d, reset-failed %d, reload %d", rmAt, sweepAt, stopAt, resetAt, reloadAt)
	}
}

func TestWipeScript_removesTheInstallersIptablesRule(t *testing.T) {
	if !strings.Contains(wipeScript(false), "iptables -D INPUT -i wg0 -s 10.0.0.0/24 -j ACCEPT") {
		t.Error("the accept rule for the mesh survives a wipe")
	}
}

// runCheck runs the firewall section and the leftover check on a test machine rooted at root.
// units is what `systemctl list-units` prints; ufwStatus is `ufw status numbered`.
func runCheck(t *testing.T, root string, nuclear bool, units, ufwStatus string, extra map[string]string) (string, int) {
	t.Helper()
	status := filepath.Join(t.TempDir(), "status")
	if err := os.WriteFile(status, []byte(ufwStatus), 0o600); err != nil {
		t.Fatal(err)
	}
	fakes := map[string]string{
		"systemctl": `[ "$1" = list-units ] && printf '%s' '` + units + `'; exit 0`,
		"ufw":       `[ "$1" = status ] && cat "` + status + `"; exit 0`,
		"sshd":      "echo port 22",
		"ip":        "exit 1",
		"iptables":  "exit 1",
		"getent":    "exit 0",
	}
	for k, v := range extra {
		fakes[k] = v
	}
	bin := fakeBin(t, fakes)
	fw := firewallBlock(t)
	// Only the function and ssh_ports of the firewall section are needed, not its deletions.
	fw = fw[:strings.Index(fw, "if [ -z")]
	script := fw + "\n" + inRoot(leftoverCheck(nuclear), root)
	if nuclear {
		script = purgeFunctionsOnly() + script
	}
	cmd := exec.Command(bashPath(t), "-c", script)
	cmd.Env = []string{"PATH=" + bin + ":/usr/bin:/bin"}
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatal(err)
		}
		code = ee.ExitCode()
	}
	return string(out), code
}

// purgeFunctionsOnly is the account listing functions of the purge block, without its deletions.
func purgeFunctionsOnly() string {
	b := purgeAccountsBlock()
	return b[:strings.Index(b, "    for name in $(orama_accounts); do\n        pkill")] + "\n"
}

func TestWipeLeftoverCheck_cleanMachinePasses(t *testing.T) {
	out, code := runCheck(t, t.TempDir(), false, "", "Status: active\n[ 1] 22/tcp ALLOW IN Anywhere # orama\n", nil)
	if code != 0 || !strings.Contains(out, "Node wiped") || strings.Contains(out, "LEFTOVER") {
		t.Fatalf("exit %d:\n%s", code, out)
	}
}

func TestWipeLeftoverCheck_listsEachLeftoverAndFails(t *testing.T) {
	root := t.TempDir()
	unitDir := filepath.Join(root, "etc/systemd/system/orama-deploy-go@ns1.service.d")
	for _, d := range []string{unitDir, filepath.Join(root, "etc/orama")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	units := "orama-namespace-wireguard@index.service not-found active exited Namespace WireGuard\n"
	status := "Status: active\n[ 1] 22/tcp ALLOW IN Anywhere # orama\n[ 2] 26656/tcp ALLOW IN Anywhere # orama-global\n"
	out, code := runCheck(t, root, false, units, status, nil)
	if code == 0 {
		t.Fatalf("a machine with leftovers passed:\n%s", out)
	}
	for _, want := range []string{
		"LEFTOVER: " + root + "/etc/orama", "LEFTOVER: " + unitDir, "LEFTOVER: systemd unit orama-namespace-wireguard@index.service",
		"LEFTOVER: ufw rule number 2", "wipe INCOMPLETE: 4 orama-related item(s)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "ufw rule number 1") || strings.Contains(out, "Node wiped") {
		t.Errorf("the SSH rule is a leftover, or success was claimed:\n%s", out)
	}
}

// Rules that were not removed because sshd's ports are unknown are reported, not forgotten.
func TestWipeLeftoverCheck_reportsRulesLeftBecauseSSHPortsAreUnknown(t *testing.T) {
	out, code := runCheck(t, t.TempDir(), false, "", "Status: active\n[ 1] 443/tcp ALLOW IN Anywhere # orama\n", map[string]string{"sshd": "exit 1"})
	if code == 0 || !strings.Contains(out, "LEFTOVER: ufw rules tagged orama") {
		t.Fatalf("exit %d:\n%s", code, out)
	}
}

func TestWipeLeftoverCheck_nuclearAlsoChecksAccountsAndBinaries(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "usr/local/bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "usr/local/bin/rqlited"), nil, 0o755); err != nil {
		t.Fatal(err)
	}
	passwd := "root:x:0:0:root:/root:/bin/bash\norama:x:998:998::/opt/orama:/usr/sbin/nologin\n"
	group := "root:x:0:\norama-sfu:x:990:orama\n"
	getent := `case "$1" in passwd) printf '%s' '` + passwd + `';; group) printf '%s' '` + group + `';; esac`
	out, code := runCheck(t, root, true, "", "Status: active\n", map[string]string{"getent": getent})
	for _, want := range []string{"LEFTOVER: " + root + "/usr/local/bin/rqlited", "LEFTOVER: system user orama", "LEFTOVER: system group orama-sfu"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if code == 0 {
		t.Error("a nuclear wipe that left accounts passed")
	}
	// Without --nuclear the accounts are expected to stay.
	out, code = runCheck(t, t.TempDir(), false, "", "Status: active\n", map[string]string{"getent": getent})
	if code != 0 || strings.Contains(out, "system user") {
		t.Errorf("a plain wipe fails on the accounts it keeps (exit %d):\n%s", code, out)
	}
}

// Only the system accounts Orama created are deleted: the orama user, orama-* and ntfy below
// the first regular uid, and their groups. A person's account is never touched, whatever it
// is called.
func TestPurgeAccounts_deletesOnlyOramaSystemAccounts(t *testing.T) {
	log := filepath.Join(t.TempDir(), "calls")
	passwd := "root:x:0:0:r:/root:/bin/bash\norama:x:998:998::/:/s\norama-sfu:x:997:996::/:/s\norama-chain:x:995:995::/:/s\nntfy:x:994:994::/:/s\n" +
		"ubuntu:x:1000:1000::/home/ubuntu:/bin/bash\norama-alice:x:1001:1001::/home/a:/bin/bash\ndaemon:x:1:1::/:/s\n"
	group := "root:x:0:\norama:x:998:\norama-sfu:x:996:orama\norama-ipfs-pub-rpc:x:993:\nubuntu:x:1000:\norama-alice:x:1001:\nadm:x:4:\n"
	bin := fakeBin(t, map[string]string{
		"getent":   `case "$1" in passwd) printf '%s' '` + passwd + `';; group) printf '%s' '` + group + `';; esac`,
		"userdel":  `echo "userdel $*" >> "` + log + `"`,
		"groupdel": `echo "groupdel $*" >> "` + log + `"`,
		"pkill":    `echo "pkill $*" >> "` + log + `"`,
	})
	cmd := exec.Command(bashPath(t), "-c", purgeAccountsBlock())
	cmd.Env = []string{"PATH=" + bin + ":/usr/bin:/bin"}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Split(strings.TrimSpace(string(data)), "\n")
	want := []string{
		"pkill -9 -u orama", "userdel orama", "pkill -9 -u orama-sfu", "userdel orama-sfu", "pkill -9 -u orama-chain", "userdel orama-chain",
		"pkill -9 -u ntfy", "userdel ntfy", "groupdel orama", "groupdel orama-sfu", "groupdel orama-ipfs-pub-rpc",
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("calls:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// The accounts go only with --nuclear: the block sits inside the nuclear branch, and the wipe
// removes processes' accounts after the binaries.
func TestWipeScript_accountsAreRemovedOnlyByNuclear(t *testing.T) {
	script := wipeScript(false)
	nuclearAt := strings.Index(script, `if [ -n "$NUCLEAR" ]`)
	userdelAt := strings.Index(script, "userdel ")
	fiAt := strings.Index(script[nuclearAt:], "\nfi\n")
	if nuclearAt < 0 || userdelAt < nuclearAt || userdelAt > nuclearAt+fiAt {
		t.Errorf("userdel (%d) is not inside the nuclear branch (%d..%d)", userdelAt, nuclearAt, nuclearAt+fiAt)
	}
	if !strings.Contains(wipeScript(true), "NUCLEAR=1") || strings.Contains(wipeScript(false), "NUCLEAR=1") {
		t.Error("only a nuclear wipe sets NUCLEAR")
	}
}

// A nuclear wipe deletes the ntfy account, so it must take the ntfy binary and config with
// it: on stagenet a wiped machine kept /usr/local/bin/ntfy, and the next install saw ntfy as
// already installed and failed to chown its data directory to an account that was gone.
func TestWipeScript_nuclearRemovesNtfyWithItsAccount(t *testing.T) {
	nuclear := removalPart(wipeScript(true))
	for _, path := range []string{"/usr/local/bin/ntfy", "/etc/ntfy", "/var/lib/ntfy"} {
		if !strings.Contains(nuclear, path) {
			t.Errorf("a nuclear wipe does not remove %s", path)
		}
		if !slices.Contains(checkedPaths(leftoverCheck(true)), path) {
			t.Errorf("the nuclear leftover check does not look at %s", path)
		}
	}
	if slices.Contains(checkedPaths(leftoverCheck(false)), "/usr/local/bin/ntfy") {
		t.Error("a plain wipe keeps the shared binaries, so its check must not look at the ntfy binary")
	}
}
