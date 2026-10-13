package fleet

import (
	"testing"

	"github.com/DeBrosOfficial/network/pkg/privhelper"
)

func TestDeploymentRange_matchesCore(t *testing.T) {
	if DeployPortMin != privhelper.DeployPortMin || DeployPortMax != privhelper.DeployPortMax {
		t.Errorf("e2e range %d-%d, core %d-%d", DeployPortMin, DeployPortMax, privhelper.DeployPortMin, privhelper.DeployPortMax)
	}
}

func TestInDeploymentRange_boundaries(t *testing.T) {
	for port, want := range map[int]bool{10199: false, 10200: true, 15000: true, 19999: true, 20000: false, 0: false, 443: false, 31000: false} {
		if got := InDeploymentRange(port); got != want {
			t.Errorf("InDeploymentRange(%d) = %v, want %v", port, got, want)
		}
	}
}

func TestDeployUnitFromCgroup(t *testing.T) {
	cases := []struct {
		name, cg, unit string
		ok             bool
	}{
		{"go runtime", "0::/system.slice/system-orama\\x2ddeploy\\x2dgo.slice/orama-deploy-go@stagenetproof-proofgo.service\n", "orama-deploy-go@stagenetproof-proofgo.service", true},
		{"node runtime", "0::/system.slice/orama-deploy-node@ns-app.service\n", "orama-deploy-node@ns-app.service", true},
		{"npm runtime", "0::/system.slice/orama-deploy-npm@ns-app.service", "orama-deploy-npm@ns-app.service", true},
		{"build unit is not a runtime", "0::/system.slice/orama-deploy-build@ns-app.service\n", "", false},
		{"clean unit is not a runtime", "0::/system.slice/orama-deploy-clean@ns-app.service\n", "", false},
		{"orama namespace service", "0::/system.slice/orama-namespace-gateway@index.service\n", "", false},
		{"a unit that only contains the text", "0::/system.slice/evil-orama-deploy-go@x.service\n", "", false},
		{"user session", "0::/user.slice/user-1000.slice/session-3.scope\n", "", false},
		{"empty", "", "", false},
	}
	for _, c := range cases {
		unit, ok := DeployUnitFromCgroup(c.cg)
		if unit != c.unit || ok != c.ok {
			t.Errorf("%s: got (%q, %v), want (%q, %v)", c.name, unit, ok, c.unit, c.ok)
		}
	}
}

const tenantUFW = `Status: active

     To                         Action      From
     --                         ------      ----
22/tcp                     ALLOW IN    Anywhere                   # orama
Anywhere on wg0            ALLOW IN    10.0.0.0/24                # orama
49152:65535/udp            ALLOW IN    Anywhere                   # orama
10300/tcp                  ALLOW IN    Anywhere
10400:10410/tcp            ALLOW IN    Anywhere
10500                      ALLOW IN    Anywhere
10600/tcp                  DENY IN     Anywhere
10700/tcp (v6)             ALLOW IN    Anywhere (v6)
`

func TestUFWAllowsPort(t *testing.T) {
	cases := []struct {
		proto string
		port  int
		want  bool
	}{
		{"tcp", 22, true},
		{"tcp", 10200, false},
		{"tcp", 10300, true},
		{"udp", 10300, false},
		{"tcp", 10405, true},
		{"tcp", 10411, false},
		{"tcp", 10500, true},
		{"tcp", 10600, false},
		{"tcp", 10700, true},
		{"udp", 50000, true},
		{"tcp", 50000, false},
	}
	for _, c := range cases {
		if got := UFWAllowsPort(tenantUFW, c.proto, c.port); got != c.want {
			t.Errorf("UFWAllowsPort(%s/%d) = %v, want %v", c.proto, c.port, got, c.want)
		}
	}
	if UFWAllowsPort("", "tcp", 10200) {
		t.Error("an empty status allows nothing")
	}
}

func TestUFWAllowsPort_anywhereAndLimit(t *testing.T) {
	cases := []struct {
		name, ufw, proto string
		port             int
		want             bool
	}{
		{"bare Anywhere opens every port", "Anywhere                   ALLOW IN    203.0.113.7\n", "tcp", 10200, true},
		{"bare Anywhere v6", "Anywhere (v6)              ALLOW IN    Anywhere (v6)\n", "tcp", 10200, true},
		{"interface-scoped Anywhere does not", "Anywhere on wg0            ALLOW IN    10.0.0.0/24\n", "tcp", 10200, false},
		{"LIMIT opens the port like ALLOW", "10200/tcp                  LIMIT IN    Anywhere\n", "tcp", 10200, true},
		{"LIMIT on another port", "22/tcp                     LIMIT IN    Anywhere\n", "tcp", 10200, false},
		{"a forward rule opens nothing on the host", "Anywhere                   ALLOW FWD   198.18.0.2 on ogl-host     # orama-global\n", "tcp", 10200, false},
		{"DENY of Anywhere is not an allow", "Anywhere                   DENY IN     203.0.113.7\n", "tcp", 10200, false},
	}
	for _, c := range cases {
		if got := UFWAllowsPort("Status: active\n\n"+c.ufw, c.proto, c.port); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

func TestUFWDefaultDenyActive(t *testing.T) {
	cases := []struct {
		name, out string
		want      bool
	}{
		{"active and deny", "Status: active\nLogging: on (low)\nDefault: deny (incoming), allow (outgoing), disabled (routed)\n", true},
		{"inactive", "Status: inactive\n", false},
		{"active but default allow", "Status: active\nDefault: allow (incoming), allow (outgoing), disabled (routed)\n", false},
		{"non-verbose output has no default line", "Status: active\n\nTo Action From\n", false},
		{"empty", "", false},
	}
	for _, c := range cases {
		if got := UFWDefaultDenyActive(c.out); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}
