package setup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	psetup "github.com/DeBrosOfficial/network/cmd/orama/internal/production/setup"
)

const (
	testCLISum2    = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	testFetchedDir = "/var/tmp/orama-archive.AbC12345"
)

func testRef(body string) *ReleaseRef {
	sum := sha256.Sum256([]byte(body))
	return &ReleaseRef{
		Version: "0.3.1", Arch: "amd64", URL: "https://releases.example/targets/nightly/orama-0.3.1-linux-amd64.tar.gz",
		SHA256: hex.EncodeToString(sum[:]), Length: int64(len(body)),
	}
}

// ---- the script a machine runs to download the release

func TestFetchReleaseScript_downloadsOnlyTheSignedFile(t *testing.T) {
	ref := testRef("archive bytes")
	script, err := fetchReleaseScript(testFetchedDir, ref)
	if err != nil {
		t.Fatal(err)
	}
	order := []string{
		`command -v curl`,
		"dir=" + testFetchedDir,
		`mkdir -m 700 "$dir"`,
		`trap '[ "$ok" = 1 ] || rm -rf "$dir"' EXIT`,
		`df -Pk "$dir"`,
		"ulimit -f 2;",
		"curl -q --fail --silent --show-error --location --globoff --proto '=https' --proto-redir '=https' --max-redirs 5",
		"--max-filesize 13",
		"'" + ref.URL + "'",
		`[ "$size" = 13 ]`,
		`[ "$sum" = ` + ref.SHA256 + ` ]`,
		"ok=1",
		markReleaseSum,
	}
	last := -1
	for _, want := range order {
		i := strings.Index(script, want)
		if i < 0 || i < last {
			t.Fatalf("missing or out of order %q in:\n%s", want, script)
		}
		last = i
	}
	for _, forbidden := range []string{"tar -", "eval", "chmod", "| sh", "| bash", "mktemp"} {
		if strings.Contains(script, forbidden) {
			t.Errorf("the download script must not %q before the archive is checked:\n%s", forbidden, script)
		}
	}
	assertBashSyntax(t, script)
}

func TestFetchReleaseScript_namesThePackageOfEveryToolItRuns(t *testing.T) {
	script, err := fetchReleaseScript(testFetchedDir, testRef("x"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`command -v curl >/dev/null 2>&1 || { echo "this machine has no curl; install it (apt-get install curl) and run setup again"`,
		`this machine has no sha256sum; install it (apt-get install coreutils)`,
		`this machine has no find; install it (apt-get install findutils)`,
		`this machine has no tar;`, `this machine has no gzip;`, `this machine has no awk;`, `this machine has no stat;`,
	} {
		if !strings.Contains(script, want) {
			t.Errorf("missing %q in:\n%s", want, script)
		}
	}
}

func TestFetchReleaseScript_aPlainHTTPRepositoryIsFetchedOverHTTPAndMayOnlyRedirectToHTTPS(t *testing.T) {
	ref := testRef("x")
	ref.URL = "http://192.0.2.7:8080/targets/nightly/orama-0.3.1-linux-amd64.tar.gz"
	script, err := fetchReleaseScript(testFetchedDir, ref)
	if err != nil || !strings.Contains(script, "--proto '=http' --proto-redir '=https'") {
		t.Fatalf("%v:\n%s", err, script)
	}
}

func TestFetchReleaseScript_refusesWhatCouldNotGoIntoARootShellCommand(t *testing.T) {
	cases := map[string]func(r *ReleaseRef) string{
		"a URL that is not http(s)":    func(r *ReleaseRef) string { r.URL = "ftp://releases.example/x"; return testFetchedDir },
		"a URL with no host":           func(r *ReleaseRef) string { r.URL = "https:///x"; return testFetchedDir },
		"text that is no URL":          func(r *ReleaseRef) string { r.URL = "; reboot"; return testFetchedDir },
		"a digest that is not hex":     func(r *ReleaseRef) string { r.SHA256 = "abc; reboot"; return testFetchedDir },
		"an upper case digest":         func(r *ReleaseRef) string { r.SHA256 = strings.ToUpper(r.SHA256); return testFetchedDir },
		"a digest of the wrong length": func(r *ReleaseRef) string { r.SHA256 = r.SHA256[:63]; return testFetchedDir },
		"a length of zero":             func(r *ReleaseRef) string { r.Length = 0; return testFetchedDir },
		"a negative length":            func(r *ReleaseRef) string { r.Length = -5; return testFetchedDir },
		"a directory outside /var/tmp": func(r *ReleaseRef) string { return "/etc/orama-archive.AbC12345" },
		"a directory with a quote":     func(r *ReleaseRef) string { return "/var/tmp/orama-archive.AbC1'234" },
	}
	for name, change := range cases {
		ref := testRef("archive")
		dir := change(ref)
		if script, err := fetchReleaseScript(dir, ref); err == nil {
			t.Errorf("%s was accepted:\n%s", name, script)
		}
	}
}

func TestFetchReleaseScript_aURLWithAQuoteStaysInsideItsQuotes(t *testing.T) {
	ref := testRef("archive")
	ref.URL = "https://releases.example/a'b$(reboot)"
	script, err := fetchReleaseScript(testFetchedDir, ref)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(script, `'https://releases.example/a'"'"'b$(reboot)'`) {
		t.Errorf("the URL is not quoted:\n%s", script)
	}
	assertBashSyntax(t, script)
}

func assertBashSyntax(t *testing.T, script string) {
	t.Helper()
	sh, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no bash to check the script with")
	}
	if out, err := exec.Command(sh, "-n", "-c", script).CombinedOutput(); err != nil {
		t.Fatalf("the script does not parse: %v\n%s\n%s", err, out, script)
	}
}

// stubbedTools is a directory of stand-ins for the programs the download script
// runs on a node (curl, GNU stat, sha256sum), so the script itself is run here.
// The stand-in curl writes $CURL_BODY to its --output, or fails when CURL_FAIL is
// set, and records its arguments in $CURL_ARGS. withCurl false leaves curl out.
func stubbedTools(t *testing.T, withCurl bool) string {
	t.Helper()
	dir := t.TempDir()
	tools := map[string]string{
		"stat": "#!/bin/sh\nwc -c < \"$3\" | tr -d ' '\n",
	}
	if withCurl {
		tools["curl"] = `#!/bin/sh
echo "$*" > "$CURL_ARGS"
out=""
while [ $# -gt 0 ]; do
  if [ "$1" = "--output" ]; then out="$2"; fi
  shift
done
if [ -n "$CURL_FAIL" ]; then echo "curl: (22) The requested URL returned error: 404" >&2; exit 22; fi
printf %s "$CURL_BODY" > "$out"
`
	}
	if _, err := exec.LookPath("sha256sum"); err != nil {
		tools["sha256sum"] = "#!/bin/sh\nexec shasum -a 256 \"$@\"\n"
	}
	for name, body := range tools {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// fetchRun is one run of the download script with the stand-in tools.
type fetchRun struct {
	dir                  string
	stdout, stderr, args string
	err                  error
}

// runFetchScript runs the download script for ref in a new archive directory with
// the stand-in tools; body is what the repository serves.
func runFetchScript(t *testing.T, ref *ReleaseRef, body string, fail, withCurl bool) fetchRun {
	t.Helper()
	sh, lookErr := exec.LookPath("bash")
	if lookErr != nil {
		t.Skip("no bash to run the script with")
	}
	dir, err := psetup.NewArchiveDir()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return runFetchScriptIn(t, sh, dir, ref, body, fail, withCurl)
}

func runFetchScriptIn(t *testing.T, sh, dir string, ref *ReleaseRef, body string, fail, withCurl bool) fetchRun {
	t.Helper()
	script, err := fetchReleaseScript(dir, ref)
	if err != nil {
		t.Fatal(err)
	}
	argsFile := filepath.Join(t.TempDir(), "curl-args")
	cmd := exec.Command(sh, "-c", script)
	path := stubbedTools(t, withCurl) + string(os.PathListSeparator) + os.Getenv("PATH")
	if !withCurl {
		path = stubbedTools(t, false)
	}
	cmd.Env = append(os.Environ(), "PATH="+path, "CURL_BODY="+body, "CURL_ARGS="+argsFile)
	if fail {
		cmd.Env = append(cmd.Env, "CURL_FAIL=1")
	}
	var out, errOut strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err = cmd.Run()
	args, _ := os.ReadFile(argsFile)
	return fetchRun{dir: dir, stdout: out.String(), stderr: errOut.String(), args: string(args), err: err}
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func TestFetchReleaseScript_runsAndKeepsTheDirectoryOfTheSignedFile(t *testing.T) {
	ref := testRef("archive bytes")
	run := runFetchScript(t, ref, "archive bytes", false, true)
	if run.err != nil {
		t.Fatalf("%v\n%s", run.err, run.stderr)
	}
	if got := strings.TrimSpace(splitMarked(run.stdout, markReleaseSum)[markReleaseSum]); got != ref.SHA256 {
		t.Fatalf("digest %q, want %q", got, ref.SHA256)
	}
	got, err := os.ReadFile(filepath.Join(run.dir, downloadedName))
	if err != nil || string(got) != "archive bytes" {
		t.Fatalf("the download is %q (%v)", got, err)
	}
	if !strings.HasPrefix(run.args, "-q ") || !strings.Contains(run.args, "--globoff") || !strings.Contains(run.args, ref.URL) || !strings.Contains(run.args, "--max-filesize 13") {
		t.Errorf("curl was run with %q: -q must come first, so a ~/.curlrc is never read", run.args)
	}
}

func TestFetchReleaseScript_aFileThatIsNotTheSignedOneIsRefusedAndRemoved(t *testing.T) {
	for name, c := range map[string]struct{ body, want string }{
		"another file of the same length": {"archive BYTES", "the download has sha256"},
		"a longer file":                   {"archive bytes and more", "the download is 22 bytes"},
		"a shorter file":                  {"archive", "the download is 7 bytes"},
	} {
		run := runFetchScript(t, testRef("archive bytes"), c.body, false, true)
		if run.err == nil || !strings.Contains(run.stderr, c.want) {
			t.Errorf("%s: err %v, stderr %q, want %q", name, run.err, run.stderr, c.want)
		}
		if exists(run.dir) {
			t.Errorf("%s: the directory of a refused download was kept", name)
		}
	}
}

func TestFetchReleaseScript_aServerThatSendsMoreThanItSaidIsCutAtTheCap(t *testing.T) {
	ref := testRef("archive bytes")
	run := runFetchScript(t, ref, strings.Repeat("x", 1<<20), false, true)
	if run.err == nil {
		t.Fatal("a download of a megabyte was accepted for an archive of 13 bytes")
	}
	if exists(run.dir) {
		t.Error("the directory of the over-long download was kept")
	}
}

func TestFetchReleaseScript_aDownloadThatFailsEndsTheScriptAndRemovesItsDirectory(t *testing.T) {
	run := runFetchScript(t, testRef("archive bytes"), "", true, true)
	if run.err == nil || !strings.Contains(run.stderr, "404") || strings.Contains(run.stdout, markReleaseSum) {
		t.Fatalf("err %v, stdout %q, stderr %q", run.err, run.stdout, run.stderr)
	}
	if exists(run.dir) {
		t.Error("the directory of a failed download was kept")
	}
}

func TestFetchReleaseScript_aMachineWithoutCurlIsToldWhatToInstall(t *testing.T) {
	run := runFetchScript(t, testRef("archive bytes"), "archive bytes", false, false)
	if run.err == nil || !strings.Contains(run.stderr, "this machine has no curl; install it (apt-get install curl)") {
		t.Fatalf("err %v, stderr %q", run.err, run.stderr)
	}
	if exists(run.dir) {
		t.Error("a directory was made on a machine that could not download")
	}
}

func TestFetchReleaseScript_aFilesystemWithoutRoomFailsBeforeTheDownload(t *testing.T) {
	ref := testRef("archive bytes")
	ref.Length = 1 << 50
	run := runFetchScript(t, ref, "archive bytes", false, true)
	if run.err == nil || !strings.Contains(run.stderr, "MiB free") || !strings.Contains(run.stderr, "needs") {
		t.Fatalf("err %v, stderr %q", run.err, run.stderr)
	}
	if run.args != "" {
		t.Error("curl ran on a filesystem without room")
	}
	if exists(run.dir) {
		t.Error("the directory was kept")
	}
}

func TestFetchReleaseScript_aDirectoryThatExistsIsRefusedAndLeftAlone(t *testing.T) {
	sh, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no bash to run the script with")
	}
	dir, err := psetup.NewArchiveDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	marker := filepath.Join(dir, "not-ours")
	if err := os.WriteFile(marker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	run := runFetchScriptIn(t, sh, dir, testRef("archive bytes"), "archive bytes", false, true)
	if run.err == nil {
		t.Fatal("a download went into a directory that was already there")
	}
	if !exists(marker) {
		t.Error("the script removed a directory it did not make")
	}
}

// ---- the sshMachine

func fetchedAnswers(sum, manifest string) map[string]string {
	return map[string]string{
		"mkdir -m 700": markReleaseSum + "\n" + sum + "\n",
		"tar -xzOf":    manifest,
	}
}

// fixedDirMachine is a machine whose downloads go to testFetchedDir.
func fixedDirMachine(sh *recShell) *sshMachine {
	m, _ := testMachine(sh)
	m.newDir = func() (string, error) { return testFetchedDir, nil }
	return m
}

func TestSSHMachine_fetchReleaseReportsWhatTheMachineHolds(t *testing.T) {
	ref := testRef("archive bytes")
	sh := &recShell{answers: fetchedAnswers(ref.SHA256, `{"version":"0.3.1"}`)}
	m := fixedDirMachine(sh)
	f, err := m.FetchRelease(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	if f.Dir != testFetchedDir || f.SHA256 != ref.SHA256 || string(f.Manifest) != `{"version":"0.3.1"}` {
		t.Errorf("%+v", f)
	}
	if !strings.HasPrefix(sh.calls[0], "bash -c ") || !strings.Contains(sh.calls[0], ref.URL) || !strings.Contains(sh.calls[0], "dir="+testFetchedDir) {
		t.Errorf("the machine downloads under bash, from the signed URL, into the directory this computer named: %s", sh.calls[0])
	}
	if read := sh.first("tar -xzOf"); !strings.Contains(read, testFetchedDir+"/release.tar.gz manifest.json") {
		t.Errorf("the manifest is read from the download: %s", read)
	}
	if len(sh.calls) != 2 {
		t.Errorf("a download that succeeded was removed: %v", sh.calls)
	}
}

func TestSSHMachine_fetchReleaseNamesItsDirectoryAtRandom(t *testing.T) {
	ref := testRef("archive bytes")
	sh := &recShell{answers: fetchedAnswers(ref.SHA256, "{}")}
	m, _ := testMachine(sh)
	f, err := m.FetchRelease(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	if !psetup.ValidArchiveDir(f.Dir) || !strings.HasPrefix(f.Dir, "/var/tmp/orama-archive.") {
		t.Errorf("directory %q", f.Dir)
	}
}

func TestSSHMachine_fetchReleaseRemovesItsDirectoryOnAnyError(t *testing.T) {
	ref := testRef("archive bytes")
	for name, sh := range map[string]*recShell{
		"a script that fails":         {fail: map[string]error{"mkdir -m 700": errors.New("curl: (6) Could not resolve host: releases.example")}},
		"a session that drops":        {fail: map[string]error{"mkdir -m 700": errors.New("run on 203.0.113.11: signal: killed")}},
		"an answer that is no digest": {answers: map[string]string{"mkdir -m 700": markReleaseSum + "\nnot-a-digest\n"}},
		"no answer":                   {answers: map[string]string{"mkdir -m 700": ""}},
		"a manifest that cannot be read": {answers: fetchedAnswers(ref.SHA256, ""),
			fail: map[string]error{"tar -xzOf": errors.New("tar: manifest.json: Not found in archive")}},
	} {
		m := fixedDirMachine(sh)
		if _, err := m.FetchRelease(context.Background(), ref); err == nil {
			t.Errorf("%s: no error", name)
		}
		if last := sh.calls[len(sh.calls)-1]; !strings.Contains(last, "rm -rf "+testFetchedDir) || strings.Contains(last, "curl") {
			t.Errorf("%s: the directory stays on the machine: %v", name, sh.calls)
		}
	}
}

func TestSSHMachine_fetchReleaseErrorIsTheMachinesOwn(t *testing.T) {
	sh := &recShell{fail: map[string]error{"mkdir -m 700": errors.New("curl: (6) Could not resolve host: releases.example")}}
	m := fixedDirMachine(sh)
	_, err := m.FetchRelease(context.Background(), testRef("archive bytes"))
	if err == nil || !strings.Contains(err.Error(), "Could not resolve host") {
		t.Fatalf("got %v", err)
	}
}

func TestSSHMachine_fetchReleaseRefusesADirectoryItWasNotMeantToUse(t *testing.T) {
	sh := &recShell{}
	m, _ := testMachine(sh)
	m.newDir = func() (string, error) { return "/etc", nil }
	if _, err := m.FetchRelease(context.Background(), testRef("archive bytes")); err == nil || len(sh.calls) != 0 {
		t.Fatalf("err %v, commands %v", err, sh.calls)
	}
}

func TestSSHMachine_stageFetchedPutsTheSignedManifestInThenAssemblesThenStages(t *testing.T) {
	sh := &recShell{}
	m, _ := testMachine(sh)
	e := &Endorsement{Manifest: []byte(`{"signed":true}`), Signature: "0xsignature", CLISHA256: testCLISum2}
	if err := m.StageFetched(context.Background(), &FetchedRelease{Dir: testFetchedDir}, e); err != nil {
		t.Fatal(err)
	}
	if len(sh.calls) != 4 {
		t.Fatalf("want the manifest, the signature, the assembly and the stage, got:\n%s", strings.Join(sh.calls, "\n"))
	}
	if !strings.Contains(sh.calls[0], "cat > "+testFetchedDir+"/endorsed-manifest.json") || sh.stdins[sh.calls[0]] != `{"signed":true}` {
		t.Errorf("the signed manifest: %s <- %q", sh.calls[0], sh.stdins[sh.calls[0]])
	}
	if !strings.Contains(sh.calls[1], "cat > "+testFetchedDir+"/endorsed-manifest.sig") || sh.stdins[sh.calls[1]] != "0xsignature" {
		t.Errorf("the signature: %s <- %q", sh.calls[1], sh.stdins[sh.calls[1]])
	}
	if !strings.Contains(sh.calls[2], "cd "+testFetchedDir) {
		t.Errorf("the assembly:\n%s", sh.calls[2])
	}
	stage := sh.calls[3]
	for _, want := range []string{testFetchedDir + "/archive.tar.gz", testCLISum2 + "  ", "--trust-signers " + testEVM, "node stage-archive"} {
		if !strings.Contains(stage, want) {
			t.Errorf("the stage lacks %q:\n%s", want, stage)
		}
	}
}

// Until the stage command is issued the directory is the caller's to remove; once
// it is, only the stage command removes it, because removing it from outside while
// `node stage-archive` runs would take the archive from under it.
func TestSSHMachine_stageFetchedRemovesTheDirectoryOnlyBeforeTheStageBegins(t *testing.T) {
	for step, fragment := range map[string]string{"the manifest upload": "endorsed-manifest.json", "the assembly": "mkdir -m 700 tree"} {
		sh := &recShell{fail: map[string]error{fragment: errors.New("boom")}}
		m, _ := testMachine(sh)
		err := m.StageFetched(context.Background(), &FetchedRelease{Dir: testFetchedDir}, &Endorsement{Manifest: []byte("m"), Signature: "s", CLISHA256: testCLISum2})
		if err == nil || !strings.Contains(err.Error(), "boom") {
			t.Errorf("%s: got %v", step, err)
		}
		if sh.first("node stage-archive") != "" {
			t.Errorf("%s: an archive that could not be assembled was staged", step)
		}
		if last := sh.calls[len(sh.calls)-1]; last != "bash -c 'rm -rf "+testFetchedDir+"'" {
			t.Errorf("%s: the directory is not removed: %v", step, sh.calls)
		}
	}
	sh := &recShell{fail: map[string]error{"node stage-archive": errors.New("boom")}}
	m, _ := testMachine(sh)
	err := m.StageFetched(context.Background(), &FetchedRelease{Dir: testFetchedDir}, &Endorsement{Manifest: []byte("m"), Signature: "s", CLISHA256: testCLISum2})
	if err == nil || len(sh.calls) != 4 || !strings.Contains(sh.calls[3], "node stage-archive") {
		t.Errorf("a failed stage: err %v, commands %d: nothing is run after the stage command, which removes the directory itself", err, len(sh.calls))
	}
}

func TestSSHMachine_stageFetchedRefusesWhatCouldNotGoIntoARootShellCommand(t *testing.T) {
	for name, f := range map[string]struct {
		dir, cli string
	}{
		"a directory outside /var/tmp":  {"/etc", testCLISum2},
		"a CLI checksum with a command": {testFetchedDir, testCLISum2[:60] + "; id"},
	} {
		sh := &recShell{}
		m, _ := testMachine(sh)
		err := m.StageFetched(context.Background(), &FetchedRelease{Dir: f.dir}, &Endorsement{Manifest: []byte("m"), Signature: "s", CLISHA256: f.cli})
		if err == nil || len(sh.calls) != 0 {
			t.Errorf("%s: err %v, commands %v", name, err, sh.calls)
		}
	}
}

func TestAssembleEndorsedScript_parsesChecksTheMembersFirstAndKeepsEveryNameWithoutALeadingDot(t *testing.T) {
	script := assembleEndorsedScript(testFetchedDir)
	assertBashSyntax(t, script)
	ordered := []string{"tar -tvzf release.tar.gz", "tar -tzf release.tar.gz", "mkdir -m 700 tree",
		"--no-same-owner --no-same-permissions --no-overwrite-dir -xzf release.tar.gz -C tree", "install -m 644 endorsed-manifest.json tree/manifest.json",
		"find . -mindepth 1 -printf '%P\\0'", "--null -C tree -T -"}
	last := -1
	for _, want := range ordered {
		i := strings.Index(script, want)
		if i < 0 || i < last {
			t.Fatalf("missing or out of order %q in:\n%s", want, script)
		}
		last = i
	}
	// The stage extracts bin/orama by that name, which a "./bin/orama" entry would not
	// match, and a glob would leave out the names that begin with a dot.
	for _, forbidden := range []string{"-- *", "-C tree .", "-cf - ."} {
		if strings.Contains(script, forbidden) {
			t.Errorf("the repack would drop or rename members (%q):\n%s", forbidden, script)
		}
	}
}

func TestSSHMachine_discardFetchedRemovesOnlyAnArchiveDirectory(t *testing.T) {
	sh := &recShell{}
	m, _ := testMachine(sh)
	for _, dir := range []string{testFetchedDir, "/tmp/orama-archive.AbC12345"} {
		if err := m.DiscardFetched(context.Background(), &FetchedRelease{Dir: dir}); err != nil {
			t.Fatal(err)
		}
		if got := sh.calls[len(sh.calls)-1]; !strings.Contains(got, "rm -rf "+dir) {
			t.Errorf("got %q", got)
		}
	}
	before := len(sh.calls)
	for _, bad := range []string{"", "/", "/opt/orama", "/var/tmp", "/var/tmp/orama-archive.x; reboot", testFetchedDir + "/../.."} {
		if err := m.DiscardFetched(context.Background(), &FetchedRelease{Dir: bad}); err == nil {
			t.Errorf("%q was removed", bad)
		}
	}
	if len(sh.calls) != before {
		t.Errorf("a command was run for a directory that is not an archive directory: %v", sh.calls[before:])
	}
}

// The directory reaches root shell text in prepareEndorsed, so a directory setup
// did not make is refused there before anything runs on the machine.
func TestPrepareEndorsed_aDirectorySetupDidNotMakeIsRefusedBeforeAnythingRuns(t *testing.T) {
	sh := &recShell{}
	m, _ := testMachine(sh)
	for _, dir := range []string{"/tmp/x; rm -rf /", "/etc", "", "/var/tmp/orama-archive.ab$(id)cd"} {
		err := m.prepareEndorsed(context.Background(), &FetchedRelease{Dir: dir}, &Endorsement{Manifest: []byte("{}"), Signature: "0xsig"})
		if err == nil || !strings.Contains(err.Error(), "not one setup makes") {
			t.Errorf("dir %q: got %v", dir, err)
		}
	}
	if len(sh.calls) != 0 {
		t.Errorf("commands ran on the machine for a refused directory: %q", sh.calls)
	}
}
