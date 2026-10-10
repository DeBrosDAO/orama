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
)

const (
	testCLISum2    = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	testFetchedDir = "/tmp/orama-archive.AbC12345"
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
	script, err := fetchReleaseScript(ref)
	if err != nil {
		t.Fatal(err)
	}
	order := []string{
		"mktemp -d /tmp/orama-archive.XXXXXXXX",
		"curl --fail --silent --show-error --location --proto '=https' --proto-redir '=https' --max-redirs 5",
		"--max-filesize 13",
		"'" + ref.URL + "'",
		`[ "$size" = 13 ]`,
		`[ "$sum" = ` + ref.SHA256 + ` ]`,
		"ok=1",
		markReleaseDir,
	}
	last := -1
	for _, want := range order {
		i := strings.Index(script, want)
		if i < 0 || i < last {
			t.Fatalf("missing or out of order %q in:\n%s", want, script)
		}
		last = i
	}
	for _, forbidden := range []string{"tar ", "eval", "chmod", "| sh", "| bash"} {
		if strings.Contains(script, forbidden) {
			t.Errorf("the download script must not %q before the archive is checked:\n%s", forbidden, script)
		}
	}
	assertBashSyntax(t, script)
}

func TestFetchReleaseScript_aPlainHTTPRepositoryIsFetchedOverHTTPAndMayOnlyRedirectToHTTPS(t *testing.T) {
	ref := testRef("x")
	ref.URL = "http://192.0.2.7:8080/targets/nightly/orama-0.3.1-linux-amd64.tar.gz"
	script, err := fetchReleaseScript(ref)
	if err != nil || !strings.Contains(script, "--proto '=http' --proto-redir '=https'") {
		t.Fatalf("%v:\n%s", err, script)
	}
}

func TestFetchReleaseScript_refusesWhatCouldNotGoIntoARootShellCommand(t *testing.T) {
	cases := map[string]func(r *ReleaseRef){
		"a URL that is not http(s)":    func(r *ReleaseRef) { r.URL = "ftp://releases.example/x" },
		"a URL with no host":           func(r *ReleaseRef) { r.URL = "https:///x" },
		"text that is no URL":          func(r *ReleaseRef) { r.URL = "; reboot" },
		"a digest that is not hex":     func(r *ReleaseRef) { r.SHA256 = "abc; reboot" },
		"an upper case digest":         func(r *ReleaseRef) { r.SHA256 = strings.ToUpper(r.SHA256) },
		"a digest of the wrong length": func(r *ReleaseRef) { r.SHA256 = r.SHA256[:63] },
		"a length of zero":             func(r *ReleaseRef) { r.Length = 0 },
		"a negative length":            func(r *ReleaseRef) { r.Length = -5 },
	}
	for name, change := range cases {
		ref := testRef("archive")
		change(ref)
		if script, err := fetchReleaseScript(ref); err == nil {
			t.Errorf("%s was accepted:\n%s", name, script)
		}
	}
}

func TestFetchReleaseScript_aURLWithAQuoteStaysInsideItsQuotes(t *testing.T) {
	ref := testRef("archive")
	ref.URL = "https://releases.example/a'b$(reboot)"
	script, err := fetchReleaseScript(ref)
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
// set, and records its arguments in $CURL_ARGS.
func stubbedTools(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	tools := map[string]string{
		"curl": `#!/bin/sh
echo "$*" > "$CURL_ARGS"
out=""
while [ $# -gt 0 ]; do
  if [ "$1" = "--output" ]; then out="$2"; fi
  shift
done
if [ -n "$CURL_FAIL" ]; then echo "curl: (22) The requested URL returned error: 404" >&2; exit 22; fi
printf %s "$CURL_BODY" > "$out"
`,
		"stat": "#!/bin/sh\nwc -c < \"$3\" | tr -d ' '\n",
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

// archiveDirs lists the directories the download script made.
func archiveDirs(t *testing.T) map[string]bool {
	t.Helper()
	found, err := filepath.Glob("/tmp/orama-archive.????????")
	if err != nil {
		t.Fatal(err)
	}
	dirs := map[string]bool{}
	for _, d := range found {
		dirs[d] = true
	}
	return dirs
}

// runFetchScript runs the download script for ref with the stand-in tools; body
// is what the repository serves.
func runFetchScript(t *testing.T, ref *ReleaseRef, body string, fail bool) (stdout, stderr, curlArgs string, err error) {
	t.Helper()
	sh, lookErr := exec.LookPath("bash")
	if lookErr != nil {
		t.Skip("no bash to run the script with")
	}
	script, err := fetchReleaseScript(ref)
	if err != nil {
		t.Fatal(err)
	}
	argsFile := filepath.Join(t.TempDir(), "curl-args")
	cmd := exec.Command(sh, "-c", script)
	cmd.Env = append(os.Environ(), "PATH="+stubbedTools(t)+string(os.PathListSeparator)+os.Getenv("PATH"), "CURL_BODY="+body, "CURL_ARGS="+argsFile)
	if fail {
		cmd.Env = append(cmd.Env, "CURL_FAIL=1")
	}
	var out, errOut strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err = cmd.Run()
	args, _ := os.ReadFile(argsFile)
	return out.String(), errOut.String(), string(args), err
}

func TestFetchReleaseScript_runsAndKeepsTheDirectoryOfTheSignedFile(t *testing.T) {
	before := archiveDirs(t)
	ref := testRef("archive bytes")
	stdout, stderr, args, err := runFetchScript(t, ref, "archive bytes", false)
	if err != nil {
		t.Fatalf("%v\n%s", err, stderr)
	}
	sec := splitMarked(stdout, markReleaseDir, markReleaseSum)
	dir, sum := strings.TrimSpace(sec[markReleaseDir]), strings.TrimSpace(sec[markReleaseSum])
	t.Cleanup(func() { os.RemoveAll(dir) })
	if sum != ref.SHA256 || before[dir] || !strings.HasPrefix(dir, "/tmp/orama-archive.") {
		t.Fatalf("dir %q (new: %v), digest %q, want %q", dir, !before[dir], sum, ref.SHA256)
	}
	got, err := os.ReadFile(filepath.Join(dir, downloadedName))
	if err != nil || string(got) != "archive bytes" {
		t.Fatalf("the download is %q (%v)", got, err)
	}
	if !strings.Contains(args, ref.URL) || !strings.Contains(args, "--max-filesize 13") {
		t.Errorf("curl was run with %q", args)
	}
}

func TestFetchReleaseScript_aFileThatIsNotTheSignedOneIsRefusedAndRemoved(t *testing.T) {
	for name, c := range map[string]struct{ body, want string }{
		"another file of the same length": {"archive BYTES", "the download has sha256"},
		"a longer file":                   {"archive bytes and more", "the download is 22 bytes"},
		"a shorter file":                  {"archive", "the download is 7 bytes"},
	} {
		before := archiveDirs(t)
		_, stderr, _, err := runFetchScript(t, testRef("archive bytes"), c.body, false)
		if err == nil || !strings.Contains(stderr, c.want) {
			t.Errorf("%s: err %v, stderr %q, want %q", name, err, stderr, c.want)
		}
		for dir := range archiveDirs(t) {
			if !before[dir] {
				os.RemoveAll(dir)
				t.Errorf("%s: the directory %s of a refused download was kept", name, dir)
			}
		}
	}
}

func TestFetchReleaseScript_aDownloadThatFailsEndsTheScriptAndRemovesItsDirectory(t *testing.T) {
	before := archiveDirs(t)
	stdout, stderr, _, err := runFetchScript(t, testRef("archive bytes"), "", true)
	if err == nil || !strings.Contains(stderr, "404") || strings.Contains(stdout, markReleaseDir) {
		t.Fatalf("err %v, stdout %q, stderr %q", err, stdout, stderr)
	}
	for dir := range archiveDirs(t) {
		if !before[dir] {
			os.RemoveAll(dir)
			t.Errorf("the directory %s of a failed download was kept", dir)
		}
	}
}

// ---- the sshMachine

func fetchedAnswers(dir, sum, manifest string) map[string]string {
	return map[string]string{
		"mktemp -d": markReleaseDir + "\n" + dir + "\n" + markReleaseSum + "\n" + sum + "\n",
		"tar -xzOf": manifest,
	}
}

func TestSSHMachine_fetchReleaseReportsWhatTheMachineHolds(t *testing.T) {
	ref := testRef("archive bytes")
	sh := &recShell{answers: fetchedAnswers(testFetchedDir, ref.SHA256, `{"version":"0.3.1"}`)}
	m, _ := testMachine(sh)
	f, err := m.FetchRelease(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	if f.Dir != testFetchedDir || f.SHA256 != ref.SHA256 || string(f.Manifest) != `{"version":"0.3.1"}` {
		t.Errorf("%+v", f)
	}
	if !strings.HasPrefix(sh.calls[0], "bash -c ") || !strings.Contains(sh.calls[0], ref.URL) {
		t.Errorf("the machine downloads under bash, from the signed URL: %s", sh.calls[0])
	}
	if read := sh.first("tar -xzOf"); !strings.Contains(read, testFetchedDir+"/release.tar.gz manifest.json") {
		t.Errorf("the manifest is read from the download: %s", read)
	}
}

func TestSSHMachine_fetchReleaseNamesNothingAMachineMadeUp(t *testing.T) {
	ref := testRef("archive bytes")
	for name, out := range map[string]string{
		"a directory outside /tmp":   markReleaseDir + "\n/etc\n" + markReleaseSum + "\n" + ref.SHA256 + "\n",
		"a directory with a quote":   markReleaseDir + "\n/tmp/orama-archive.a'b12345\n" + markReleaseSum + "\n" + ref.SHA256 + "\n",
		"a digest that is no digest": markReleaseDir + "\n" + testFetchedDir + "\n" + markReleaseSum + "\nnot-a-digest\n",
		"no answer":                  "",
	} {
		sh := &recShell{answers: map[string]string{"mktemp -d": out}}
		m, _ := testMachine(sh)
		if _, err := m.FetchRelease(context.Background(), ref); err == nil {
			t.Errorf("%s was believed", name)
		}
		if len(sh.calls) != 1 {
			t.Errorf("%s: a command was run with what the machine answered: %v", name, sh.calls[1:])
		}
	}
}

func TestSSHMachine_fetchReleaseFailureIsTheMachinesOwnError(t *testing.T) {
	sh := &recShell{fail: map[string]error{"mktemp -d": errors.New("curl: (6) Could not resolve host: releases.example")}}
	m, _ := testMachine(sh)
	_, err := m.FetchRelease(context.Background(), testRef("archive bytes"))
	if err == nil || !strings.Contains(err.Error(), "Could not resolve host") {
		t.Fatalf("got %v", err)
	}
}

func TestSSHMachine_fetchReleaseThatCannotReadTheManifestRemovesTheDownload(t *testing.T) {
	ref := testRef("archive bytes")
	sh := &recShell{answers: fetchedAnswers(testFetchedDir, ref.SHA256, ""), fail: map[string]error{"tar -xzOf": errors.New("tar: manifest.json: Not found in archive")}}
	m, _ := testMachine(sh)
	_, err := m.FetchRelease(context.Background(), ref)
	if err == nil || !strings.Contains(err.Error(), "Not found in archive") {
		t.Fatalf("got %v", err)
	}
	if last := sh.calls[len(sh.calls)-1]; !strings.Contains(last, "rm -rf "+testFetchedDir) {
		t.Errorf("the download stays on the machine: %v", sh.calls)
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
	for _, want := range []string{"cd " + testFetchedDir, "tar --no-same-owner --no-same-permissions -xzf release.tar.gz -C tree",
		"install -m 644 endorsed-manifest.json tree/manifest.json", "install -m 644 endorsed-manifest.sig tree/manifest.sig",
		"gzip -1 > archive.tar.gz"} {
		if !strings.Contains(sh.calls[2], want) {
			t.Errorf("the assembly lacks %q:\n%s", want, sh.calls[2])
		}
	}
	stage := sh.calls[3]
	for _, want := range []string{testFetchedDir + "/archive.tar.gz", testCLISum2 + "  ", "--trust-signers " + testEVM, "node stage-archive"} {
		if !strings.Contains(stage, want) {
			t.Errorf("the stage lacks %q:\n%s", want, stage)
		}
	}
}

func TestSSHMachine_stageFetchedStopsAtTheFirstStepThatFails(t *testing.T) {
	for step, fragment := range map[string]string{"the manifest upload": "endorsed-manifest.json", "the assembly": "mkdir -m 700 tree", "the stage": "node stage-archive"} {
		sh := &recShell{fail: map[string]error{fragment: errors.New("boom")}}
		m, _ := testMachine(sh)
		err := m.StageFetched(context.Background(), &FetchedRelease{Dir: testFetchedDir}, &Endorsement{Manifest: []byte("m"), Signature: "s", CLISHA256: testCLISum2})
		if err == nil || !strings.Contains(err.Error(), "boom") {
			t.Errorf("%s: got %v", step, err)
		}
		if step == "the manifest upload" && len(sh.calls) != 1 {
			t.Errorf("the run went on after the upload failed: %v", sh.calls)
		}
		if step == "the assembly" && sh.first("node stage-archive") != "" {
			t.Error("an archive that could not be assembled was staged")
		}
	}
}

func TestSSHMachine_stageFetchedRefusesWhatCouldNotGoIntoARootShellCommand(t *testing.T) {
	for name, f := range map[string]struct {
		dir, cli string
	}{
		"a directory outside /tmp":      {"/etc", testCLISum2},
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

func TestAssembleEndorsedScript_parsesAndNamesNothingWithALeadingDot(t *testing.T) {
	script := assembleEndorsedScript(testFetchedDir)
	assertBashSyntax(t, script)
	// The stage extracts bin/orama by that name, which a "./bin/orama" entry would not match.
	if strings.Contains(script, "-C tree .") || strings.Contains(script, "tar -cf - .") {
		t.Errorf("the archive would name its entries ./...:\n%s", script)
	}
	if strings.Index(script, "xzf") > strings.Index(script, "install -m 644 endorsed-manifest.json") {
		t.Errorf("the signed manifest must be put in after the release is unpacked, or the release's own replaces it:\n%s", script)
	}
}

func TestSSHMachine_discardFetchedRemovesOnlyAnArchiveDirectory(t *testing.T) {
	sh := &recShell{}
	m, _ := testMachine(sh)
	if err := m.DiscardFetched(context.Background(), &FetchedRelease{Dir: testFetchedDir}); err != nil {
		t.Fatal(err)
	}
	if got := sh.calls[0]; !strings.Contains(got, "rm -rf "+testFetchedDir) {
		t.Errorf("got %q", got)
	}
	for _, bad := range []string{"", "/", "/opt/orama", "/tmp/orama-archive.x; reboot", testFetchedDir + "/../.."} {
		if err := m.DiscardFetched(context.Background(), &FetchedRelease{Dir: bad}); err == nil {
			t.Errorf("%q was removed", bad)
		}
	}
	if len(sh.calls) != 1 {
		t.Errorf("a command was run for a directory that is not an archive directory: %v", sh.calls)
	}
}
