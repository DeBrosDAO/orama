package install

import (
	"io"
	"testing"

	oramainstall "github.com/DeBrosOfficial/network/pkg/install"
)

func TestUseClusterACMECA_adoptsTheJoinResponseWhenUnset(t *testing.T) {
	staging := "https://acme-staging-v02.api.letsencrypt.org/directory"
	o := &Orchestrator{
		flags: &Flags{},
		setup: oramainstall.NewProductionSetup(t.TempDir(), io.Discard, false, true),
	}
	if err := o.useClusterACMECA(staging); err != nil {
		t.Fatal(err)
	}
	if o.flags.ACMECA != staging {
		t.Fatalf("flag = %q, want the cluster's CA", o.flags.ACMECA)
	}
	got, err := o.setup.ACMECA()
	if err != nil || got != staging {
		t.Fatalf("setup ACME = %q, %v — the joiner would still issue from production", got, err)
	}

	o.flags.ACMECA = "https://other.example/dir"
	if err := o.useClusterACMECA(staging); err != nil {
		t.Fatal(err)
	}
	if o.flags.ACMECA != "https://other.example/dir" {
		t.Fatalf("explicit --acme-ca was replaced with %q", o.flags.ACMECA)
	}

	o.flags.ACMECA = ""
	if err := o.useClusterACMECA("http://ca.example/directory"); err == nil {
		t.Fatal("accepted a non-https ACME directory from the join response")
	}
}
