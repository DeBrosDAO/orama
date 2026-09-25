package inspector

import (
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/constants"
)

const sep = "===INSPECTOR_SEP==="

func torOutput(fields ...string) string {
	return sep + "\n" + strings.Join(fields, "\n"+sep+"\n") + "\n"
}

func TestParseTorOutput_healthy(t *testing.T) {
	d := parseTorOutput(torOutput("active", "yes", "100", "no"))
	if !d.ClientActive || !d.SocksListening || !d.Bootstrapped || d.BootstrapPct != 100 || d.LegacyAnyone {
		t.Errorf("parsed %+v, want a healthy client", d)
	}
}

func TestParseTorOutput_unknownBootstrap(t *testing.T) {
	d := parseTorOutput(torOutput("active", "yes", "-1", "yes"))
	if d.BootstrapPct != TorBootstrapUnknown || d.Bootstrapped {
		t.Errorf("parsed %+v, want bootstrap unknown", d)
	}
	if !d.LegacyAnyone {
		t.Error("legacy Anyone flag lost")
	}
}

func TestParseTorOutput_emptyOutput(t *testing.T) {
	d := parseTorOutput("")
	if d.ClientActive || d.SocksListening || d.BootstrapPct != TorBootstrapUnknown {
		t.Errorf("parsed %+v from nothing, want inactive with unknown bootstrap", d)
	}
}

func TestTorCollectScript_probesTheTorUnitAndSharedPort(t *testing.T) {
	script := torCollectScript()
	for _, want := range []string{TorUnit, ":9050 ", "_SYSTEMD_INVOCATION_ID"} {
		if !strings.Contains(script, want) {
			t.Errorf("collect script missing %q", want)
		}
	}
	if constants.TorSOCKSPort != 9050 {
		t.Fatalf("update this test: TorSOCKSPort is %d", constants.TorSOCKSPort)
	}
}
