package report

import (
	"slices"
	"testing"
)

func TestParseFailedUnits_bulletAsItsOwnField(t *testing.T) {
	out := "● cloud-init.service                         loaded failed failed Cloud-init: Network Stage\n" +
		"● orama-namespace-ipfs-cluster@index.service loaded failed failed Orama Namespace IPFS Cluster (index)\n"
	want := []string{"cloud-init.service", "orama-namespace-ipfs-cluster@index.service"}
	if got := parseFailedUnits(out); !slices.Equal(got, want) {
		t.Fatalf("parseFailedUnits = %v, want %v", got, want)
	}
}

func TestParseFailedUnits_plainOutput(t *testing.T) {
	out := "e2e-fail-1.service loaded failed failed /bin/false\n"
	if got := parseFailedUnits(out); !slices.Equal(got, []string{"e2e-fail-1.service"}) {
		t.Fatalf("parseFailedUnits = %v", got)
	}
}

func TestParseFailedUnits_bulletJoinedToTheName(t *testing.T) {
	if got := parseFailedUnits("●x.service loaded failed failed x\n"); !slices.Equal(got, []string{"x.service"}) {
		t.Fatalf("parseFailedUnits = %v", got)
	}
}

func TestParseFailedUnits_otherStatusGlyphs(t *testing.T) {
	out := "× a.service loaded failed failed a\n○ b.service loaded failed failed b\n"
	if got := parseFailedUnits(out); !slices.Equal(got, []string{"a.service", "b.service"}) {
		t.Fatalf("parseFailedUnits = %v", got)
	}
}

func TestParseFailedUnits_noneFailed(t *testing.T) {
	if got := parseFailedUnits("\n  \n"); len(got) != 0 {
		t.Fatalf("parseFailedUnits of empty output = %v", got)
	}
}

func TestParseFailedUnits_rootMountKeepsItsName(t *testing.T) {
	if got := parseFailedUnits("● -.mount loaded failed failed Root Mount\n"); !slices.Equal(got, []string{"-.mount"}) {
		t.Fatalf("parseFailedUnits = %v, want [-.mount]", got)
	}
}
