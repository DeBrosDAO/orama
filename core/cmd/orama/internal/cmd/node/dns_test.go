package node

import (
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/printer"
	"github.com/spf13/cobra"
)

// The delegation is per environment; reading "the active one" would print one
// cluster's nameservers for an operator configuring another's zone.
func TestRunDNSDelegation_requiresEnv(t *testing.T) {
	err := runDNSDelegation(printer.For(&cobra.Command{}), "")
	if err == nil || !strings.Contains(err.Error(), "--env") {
		t.Fatalf("runDNSDelegation without --env: %v", err)
	}
}

func TestDNSDelegation_isMountedUnderNodeDNS(t *testing.T) {
	found, _, err := Cmd.Find([]string{"dns", "delegation"})
	if err != nil || found != dnsDelegationCmd {
		t.Fatalf("orama node dns delegation resolves to %v (%v)", found, err)
	}
	t.Cleanup(func() { dnsDelegationEnv = "" })
	if err := dnsDelegationCmd.ParseFlags([]string{"--env", "devnet"}); err != nil || dnsDelegationEnv != "devnet" {
		t.Errorf("parse --env: env=%q err=%v", dnsDelegationEnv, err)
	}
}

func TestParentZoneLabel(t *testing.T) {
	if got := parentZoneLabel("stagenet.dbrsteting.bid"); !strings.HasPrefix(got, "dbrsteting.bid:") {
		t.Errorf("subdomain label = %q", got)
	}
	if got := parentZoneLabel("dbrsteting.bid"); !strings.Contains(got, "registrar") {
		t.Errorf("registered-domain label = %q", got)
	}
}
