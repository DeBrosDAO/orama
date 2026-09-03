package constants_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// legacyPorts are the pre-migration index-service ports. Commit 5987131 moved
// the index internals into the 10100-10199 block, but every operational tool
// that addressed a service through an integer literal was left behind: the
// failure detector, the quorum guard, recover-raft, invite, the upgrade health
// gate, doctor, cluster status and the report collectors all silently stopped
// working, and one of them failed unsafe.
//
// The fix was to route every index-service address through this package. This
// test is what stops the next port move from re-creating the same class of bug:
// a literal is a compile-time-invisible dependency, so it has to be caught here.
var legacyPorts = map[string]string{
	"5001": "index RQLite HTTP — use constants.RQLiteHTTPPort / LocalRQLiteURL",
	"7001": "index RQLite Raft — use constants.RQLiteRaftPort / RQLiteRaftAddrFor",
	"6001": "index gateway — use constants.GatewayAPIPort / LocalGatewayURL / GatewayURLFor",
	"3320": "index Olric — use constants.OlricHTTPPort / LocalOlricURL / OlricAddrFor",
	"4501": "Kubo HTTP API — use constants.IPFSAPIPort / LocalIPFSAPIURL",
}

// portLiteral matches a legacy port used as an address or a bare numeric
// literal: ":5001", "5001)", "= 5001", "\"5001\"". It deliberately does not
// match longer numbers such as 15001 or 50011.
var portLiteral = regexp.MustCompile(`(^|[^0-9])(5001|7001|6001|3320|4501)([^0-9]|$)`)

// TestNoLegacyPortLiterals walks the Go sources under core/ and fails on any
// non-test file that still hardcodes a pre-migration index port.
//
// Comments are checked too. A stale comment naming a dead port is how an
// operator ends up curling the wrong endpoint during an incident, which is
// exactly what docs/DEV_DEPLOY.md did until this sweep.
func TestNoLegacyPortLiterals(t *testing.T) {
	root := repoGoRoot(t)

	var offences []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			// Vendored and generated trees are not ours to police.
			switch info.Name() {
			case "vendor", "node_modules", ".git", "bin", "bin-linux", "testdata":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		rel, _ := filepath.Rel(root, path)
		for i, line := range strings.Split(string(data), "\n") {
			m := portLiteral.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			offences = append(offences, rel+":"+itoa(i+1)+": "+strings.TrimSpace(line)+
				"\n    → "+legacyPorts[m[2]])
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}

	if len(offences) > 0 {
		t.Fatalf("legacy index-service port literals found in %d place(s).\n"+
			"Route the address through pkg/constants instead:\n\n%s",
			len(offences), strings.Join(offences, "\n"))
	}
}

// repoGoRoot returns the directory holding go.mod (core/), walking up from the
// test's own working directory so the test is location-independent.
func repoGoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found above the test working directory")
		}
		dir = parent
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
