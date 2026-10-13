package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var now = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

const acceptedList = `accepted:
  - module: chain
    id: GO-2026-0001
    reason: not reachable from a network peer
    review_by: 2026-12-01
`

const (
	vulnConfig  = `{"config":{"protocol_version":"v1.0.0"}}` + "\n"
	vulnReached = `{"finding":{"osv":"GO-2026-0001","trace":[{"function":"Read","module":"golang.org/x/crypto"}]}}` + "\n"
	vulnOther   = `{"finding":{"osv":"GO-2026-0002","trace":[{"function":"Parse","module":"example.com/x"}]}}` + "\n"
	// A finding with no function is an imported package or a required module, not a reached one.
	vulnImportedOnly = `{"finding":{"osv":"GO-2026-0003","trace":[{"module":"example.com/y"}]}}` + "\n"
)

func writeAccepted(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "accepted.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func gate(t *testing.T, stdin string, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = run(args, strings.NewReader(stdin), &out, &errOut, now)
	return code, out.String(), errOut.String()
}

func TestVuln_acceptedFindingPasses(t *testing.T) {
	path := writeAccepted(t, acceptedList)
	code, out, errOut := gate(t, vulnConfig+vulnReached, "vuln", "--module", "chain", "--accepted", path)
	if code != exitClean {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
	if !strings.Contains(out, "1 reachable, all accepted") {
		t.Errorf("stdout %q", out)
	}
}

func TestVuln_unlistedFindingFails(t *testing.T) {
	path := writeAccepted(t, acceptedList)
	code, _, errOut := gate(t, vulnConfig+vulnReached+vulnOther, "vuln", "--module", "chain", "--accepted", path)
	if code != exitFindings || !strings.Contains(errOut, "GO-2026-0002 is reachable and not accepted") {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
}

func TestVuln_staleAcceptanceFails(t *testing.T) {
	path := writeAccepted(t, acceptedList)
	code, _, errOut := gate(t, vulnConfig, "vuln", "--module", "chain", "--accepted", path)
	if code != exitFindings || !strings.Contains(errOut, "no longer reports") {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
}

func TestVuln_expiredAcceptanceFails(t *testing.T) {
	expired := strings.Replace(acceptedList, "2026-12-01", "2026-09-01", 1)
	path := writeAccepted(t, expired)
	code, _, errOut := gate(t, vulnConfig+vulnReached, "vuln", "--module", "chain", "--accepted", path)
	if code != exitFindings || !strings.Contains(errOut, "expired on 2026-09-01") {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
}

func TestVuln_importedOnlyFindingIsNotReached(t *testing.T) {
	path := writeAccepted(t, "accepted: []\n")
	code, _, errOut := gate(t, vulnConfig+vulnImportedOnly, "vuln", "--module", "chain", "--accepted", path)
	if code != exitClean {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
}

func TestVuln_otherModulesEntriesAreNotJudged(t *testing.T) {
	other := strings.Replace(acceptedList, "module: chain", "module: core", 1)
	path := writeAccepted(t, other)
	code, _, errOut := gate(t, vulnConfig, "vuln", "--module", "chain", "--accepted", path)
	if code != exitClean {
		t.Fatalf("an entry for core is not stale in chain: exit %d, stderr %q", code, errOut)
	}
}

func TestVuln_outputThatIsNotGovulncheckIsNotAPass(t *testing.T) {
	path := writeAccepted(t, "accepted: []\n")
	for _, in := range []string{"", "go: command not found\n", vulnReached} {
		code, _, errOut := gate(t, in, "vuln", "--module", "chain", "--accepted", path)
		if code != exitUsage {
			t.Errorf("input %q: exit %d, stderr %q", in, code, errOut)
		}
	}
}

func TestVuln_badArguments(t *testing.T) {
	good := writeAccepted(t, "accepted: []\n")
	bad := writeAccepted(t, "accepted:\n  - module: nowhere\n    id: GO-2026-0001\n    reason: r\n    review_by: 2026-12-01\n")
	cases := map[string][]string{
		"no module":          {"vuln", "--accepted", good},
		"no accepted":        {"vuln", "--module", "chain"},
		"missing file":       {"vuln", "--module", "chain", "--accepted", filepath.Join(t.TempDir(), "absent.yaml")},
		"module not scanned": {"vuln", "--module", "chain", "--accepted", bad},
		"unknown flag":       {"vuln", "--nope"},
	}
	for name, args := range cases {
		if code, _, _ := gate(t, vulnConfig, args...); code != exitUsage {
			t.Errorf("%s: exit %d, want %d", name, code, exitUsage)
		}
	}
}

func TestStatic_findingFails(t *testing.T) {
	finding := `{"code":"U1000","severity":"warning","location":{"file":"/m/x/keeper.go","line":9,"column":3},"message":"func f is unused"}` + "\n"
	code, _, errOut := gate(t, finding, "static")
	if code != exitFindings || !strings.Contains(errOut, "/m/x/keeper.go:9:3: func f is unused (U1000)") {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
}

func TestStatic_deprecationInGeneratedGatewayFilePasses(t *testing.T) {
	gen := filepath.Join(t.TempDir(), "query.pb.gw.go")
	if err := os.WriteFile(gen, []byte("// Code generated by protoc-gen-grpc-gateway. DO NOT EDIT.\npackage x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	finding := `{"code":"SA1019","severity":"warning","location":{"file":"` + gen + `","line":16,"column":2},"message":"grpc.Dial is deprecated"}` + "\n"
	code, out, errOut := gate(t, finding, "static")
	if code != exitClean {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
	if !strings.Contains(out, "1 findings") {
		t.Errorf("stdout %q", out)
	}
}

func TestStatic_deprecationInHandWrittenFileFails(t *testing.T) {
	hand := filepath.Join(t.TempDir(), "keeper.go")
	if err := os.WriteFile(hand, []byte("package x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	finding := `{"code":"SA1019","severity":"warning","location":{"file":"` + hand + `","line":3,"column":2},"message":"x is deprecated"}` + "\n"
	if code, _, _ := gate(t, finding, "static"); code != exitFindings {
		t.Fatalf("exit %d, want %d", code, exitFindings)
	}
}

func TestStatic_cleanAndMalformedOutput(t *testing.T) {
	if code, _, errOut := gate(t, "", "static"); code != exitClean {
		t.Errorf("empty output is no findings: exit %d, stderr %q", code, errOut)
	}
	if code, _, _ := gate(t, "-: no Go files\n", "static"); code != exitUsage {
		t.Errorf("text that is not a finding must not pass: exit %d", code)
	}
}

func TestRun_usage(t *testing.T) {
	if code, _, _ := gate(t, ""); code != exitUsage {
		t.Errorf("no command: exit %d", code)
	}
	if code, _, _ := gate(t, "", "lint"); code != exitUsage {
		t.Errorf("unknown command: exit %d", code)
	}
}
