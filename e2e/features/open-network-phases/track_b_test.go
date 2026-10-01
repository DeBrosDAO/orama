//go:build e2e_fleet

package opennetworkphases

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/features/internal/tenancy"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

const (
	trackB       = "plans/open-network/track-b-global-node.md"
	corefile     = "/etc/coredns/Corefile" // core/pkg/install/installers/coredns.go CorefilePath
	corednsUser  = "orama-coredns"
	chainUser    = "orama-chain" // core/pkg/constants/chain.go ChainUser
	chainHome    = "/var/lib/orama-global/chain"
	validatorKey = chainHome + "/config/priv_validator_key.json"
)

// TestPhaseB1_coreDNSRunsAsItsOwnAccount: on every nameserver CoreDNS runs
// as orama-coredns, its Corefile (which holds the index rqlite password) is
// root:orama-coredns 0640, and the orama account every other daemon runs as
// cannot read it (B1; docs/SECURITY.md "Per-service accounts").
func TestPhaseB1_coreDNSRunsAsItsOwnAccount(t *testing.T) {
	phase(t, "B1", "docs/SECURITY.md", "runs as `orama-coredns`", trackB+" B1")
	f := harness.Fleet(t)
	for _, n := range tenancy.Nameservers(f) {
		user := strings.TrimSpace(f.MustExec(t, n, "ps -o user= -p \"$(systemctl show -p MainPID --value "+infra.CoreDNSUnit+")\"").Stdout)
		if user != corednsUser {
			t.Errorf("%s: CoreDNS runs as %q, want %s", n.Name, user, corednsUser)
		}
		if mode := strings.TrimSpace(f.MustExec(t, n, "stat -c '%U:%G %a' "+corefile).Stdout); mode != "root:"+corednsUser+" 640" {
			t.Errorf("%s: %s is %q, want root:%s 640", n.Name, corefile, mode, corednsUser)
		}
		if f.Exec(t, n, "runuser -u orama -- cat "+corefile+" >/dev/null 2>&1").Exit == 0 {
			t.Errorf("%s: the orama account can read %s", n.Name, corefile)
		}
	}
}

// chainNodes are the core nodes running the co-hosted chain.
func chainNodes(t *testing.T, f *fleet.Fleet) []fleet.Node {
	t.Helper()
	harness.RequireChain(t)
	var out []fleet.Node
	for _, n := range f.State.Nodes {
		if f.Unit(t, n, infra.ChainUnit) == "active" {
			out = append(out, n)
		}
	}
	if len(out) == 0 {
		t.Fatalf("the run has chain %s but no node runs %s", f.State.ChainID, infra.ChainUnit)
	}
	return out
}

// TestPhaseB2_globalServicesOutsideTheClusterGraph: the chain is not a boot
// component of the node supervisor: its unit is not PartOf, BoundTo or
// Required by orama-node.service, so restarting the supervisor never
// restarts the chain (B2; docs/ARCHITECTURE.md "Chain, public IPFS and the
// relay are not boot components").
func TestPhaseB2_globalServicesOutsideTheClusterGraph(t *testing.T) {
	phase(t, "B2", "docs/ARCHITECTURE.md", "are not boot components", trackB+" B2")
	f := harness.Fleet(t)
	for _, n := range chainNodes(t, f) {
		deps := f.MustExec(t, n, "systemctl show -p PartOf -p BindsTo -p Requires -p Requisite "+infra.ChainUnit).Stdout
		if strings.Contains(deps, infra.NodeUnit) {
			t.Errorf("%s: %s is tied to %s:\n%s", n.Name, infra.ChainUnit, infra.NodeUnit, deps)
		}
	}
}

// TestPhaseB3_chainUnitAccountAndPorts: the chain runs as orama-chain, its
// RPC listens on loopback only and its p2p port on the WireGuard address
// only (B3; core/pkg/constants/chain.go, docs/MONITORING.md "chain").
func TestPhaseB3_chainUnitAccountAndPorts(t *testing.T) {
	phase(t, "B3", "docs/MONITORING.md", "orama-global-chain.service", trackB+" B3")
	f := harness.Fleet(t)
	for _, n := range chainNodes(t, f) {
		if user := strings.TrimSpace(f.MustExec(t, n, "systemctl show -p User --value "+infra.ChainUnit).Stdout); user != chainUser {
			t.Errorf("%s: %s runs as %q, want %s", n.Name, infra.ChainUnit, user, chainUser)
		}
		for _, l := range f.Listeners(t, n) {
			switch {
			case l.Port == infra.ChainRPCPort && l.Addr != "127.0.0.1":
				t.Errorf("%s: the chain RPC listens on %s", n.Name, l.Addr)
			case l.Port == infra.ChainP2PPort && l.Addr != n.WGIP:
				t.Errorf("%s: the chain p2p port listens on %s, want the WireGuard address %s", n.Name, l.Addr, n.WGIP)
			}
		}
	}
	if f.State.IsStagenet() {
		harness.SkipNotApplicable(t, "the stagenet chain runs in the orama-global netns: its RPC and p2p ports are not host listeners, so their bind addresses cannot be asserted from the host")
	}
}

// TestPhaseB4_identityKeyBindingFromTheLiveKey: `orama global bind` signs a
// binding with a node's real validator key without printing it; the public
// key it binds is the one the running chain reports for that node (B4).
func TestPhaseB4_identityKeyBindingFromTheLiveKey(t *testing.T) {
	phase(t, "B4", "docs/CLI_REFERENCE.md", "### orama global bind", trackB+" B4")
	f := harness.Fleet(t)
	n := chainNodes(t, f)[0]
	res := onNode(t, f, n, "global", "bind", "--chain-id", f.State.ChainID, "--operator", vectorOperator, "--service", "validator", "--key-file", validatorKey)
	if res.Exit != exitOK {
		t.Fatalf("%s: global bind of the validator key exited %d: %s", n.Name, res.Exit, f.Redact(res.Stderr))
	}
	var b struct {
		Pubkey    string `json:"pubkey"`
		Signature string `json:"signature"`
	}
	if err := json.Unmarshal([]byte(res.Stdout), &b); err != nil || b.Signature == "" {
		t.Fatalf("bind printed no binding: %v %s", err, res.Stdout)
	}
	if f.State.IsStagenet() {
		harness.SkipNotApplicable(t, "the stagenet chain RPC answers inside the orama-global netns, not on the node's 127.0.0.1:31001, so the key it reports cannot be read from the host loopback")
	}
	status := f.MustExec(t, n, fmt.Sprintf("curl -fsS http://127.0.0.1:%d/status", infra.ChainRPCPort)).Stdout
	var st struct {
		Result struct {
			ValidatorInfo struct {
				PubKey struct {
					Value string `json:"value"`
				} `json:"pub_key"`
			} `json:"validator_info"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(status), &st); err != nil {
		t.Fatalf("%s: chain /status: %v", n.Name, err)
	}
	raw, err := base64.StdEncoding.DecodeString(st.Result.ValidatorInfo.PubKey.Value)
	if err != nil || hex.EncodeToString(raw) != b.Pubkey {
		t.Errorf("%s: the binding's key %s is not the validator key the chain reports (%x, %v)", n.Name, b.Pubkey, raw, err)
	}
	if strings.Contains(res.Stdout+res.Stderr, "priv_key") {
		t.Errorf("%s: bind printed the private key", n.Name)
	}
}
