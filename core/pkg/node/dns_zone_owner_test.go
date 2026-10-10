package node

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// zoneApexRecordSQL matches an NS or SOA record type written as an SQL
// literal: how a statement against dns_records names the record it touches.
var zoneApexRecordSQL = regexp.MustCompile(`'(NS|SOA)'`)

// zoneApexOwner is the one file that may touch a zone's NS and SOA records.
const zoneApexOwner = "pkg/node/dns_nameservers.go"

// TestZoneApexRecords_haveOneWriter keeps the zone's NS set and SOA with the
// nameserver component, which derives them from the claimed, glued slots.
//
// Install and upgrade used to seed ns1..ns3 NS records and an ns1 SOA on every
// run, whatever slots the cluster had: a one-nameserver cluster published two
// NS names that resolved nowhere, and the seed's delete-and-insert of the SOA
// fought the component's in-place update for the serial.
func TestZoneApexRecords_haveOneWriter(t *testing.T) {
	root := goModuleRoot(t)
	var offences []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			switch info.Name() {
			case "vendor", "node_modules", ".git", "bin", "testdata":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		if filepath.ToSlash(rel) == zoneApexOwner {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(data), "\n") {
			if zoneApexRecordSQL.MatchString(line) {
				offences = append(offences, rel+":"+strconv.Itoa(i+1)+": "+strings.TrimSpace(line))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	if len(offences) > 0 {
		t.Fatalf("NS/SOA records written outside %s:\n%s", zoneApexOwner, strings.Join(offences, "\n"))
	}
}

func goModuleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found above the test directory")
		}
		dir = parent
	}
}
