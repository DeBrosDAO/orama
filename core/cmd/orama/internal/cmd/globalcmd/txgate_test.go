package globalcmd

import (
	"bytes"
	"net/http"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/tornet"
	"github.com/spf13/cobra"
)

// A caller that drips a request body holds a connection of the gate, and no
// limiter has counted it yet: every phase of a connection has a deadline.
func TestNewGateServer_boundsEveryPhaseOfAConnection(t *testing.T) {
	srv := newGateServer(http.NotFoundHandler())
	if srv.ReadHeaderTimeout <= 0 || srv.ReadTimeout <= 0 || srv.WriteTimeout <= 0 || srv.IdleTimeout <= 0 {
		t.Fatalf("a phase of a connection has no deadline: %+v", srv)
	}
	if srv.WriteTimeout <= srv.ReadTimeout {
		t.Errorf("the write deadline %v does not cover the chain call after a read of %v", srv.WriteTimeout, srv.ReadTimeout)
	}
}

// One role whose DataDirectory cannot be read must not hide the others.
func TestPrintTorInfo_aRoleThatCouldNotBeReadIsShownBesideTheOthers(t *testing.T) {
	infos := []tornet.NodeInfo{
		{Home: "/var/lib/orama-global/tor-relay", Error: "read fingerprint: permission denied"},
		{Home: "/var/lib/orama-global/tor-onion", Onion: strings.Repeat("a", 56) + ".onion"},
	}
	var out bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&out)
	if err := printTorInfo(cmd, infos, false); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"error        read fingerprint: permission denied", ".onion"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
}
