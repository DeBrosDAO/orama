package install

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/constants"
)

func TestGlobalUnits_usersPortsAndNoClusterSecret(t *testing.T) {
	chain := RenderGlobalChainUnit("")
	ipfs := RenderGlobalIPFSUnit()
	relay := RenderGlobalRelayUnit()

	users := []string{}
	for _, unit := range []string{chain, ipfs, relay} {
		user := mustDirective(t, unit, "User")
		group := mustDirective(t, unit, "Group")
		// The public Kubo's group is the RPC group, so the provider can read its token.
		if user != group && !(user == globalIPFSUser && group == globalIPFSRPCGroup) {
			t.Errorf("User=%s Group=%s", user, group)
		}
		if user == "orama" || user == "" {
			t.Errorf("global unit runs as %q", user)
		}
		users = append(users, user)
		if strings.Contains(unit, "cluster-secret") || strings.Contains(unit, "/opt/orama/.orama/secrets") {
			t.Errorf("unit contains a cluster secret path:\n%s", unit)
		}
		if strings.Contains(unit, "PartOf=") {
			t.Errorf("global unit is PartOf another unit:\n%s", unit)
		}
		for _, port := range []string{":10100", ":10102", ":10103", ":10104", ":10107"} {
			if strings.Contains(unit, port) {
				t.Errorf("unit contains cluster port %s", port)
			}
		}
	}
	if users[0] == users[1] || users[1] == users[2] || users[0] == users[2] {
		t.Fatalf("global units share users %v", users)
	}
	if users[0] != globalChainUser || users[1] != globalIPFSUser || users[2] != globalRelayUser {
		t.Fatalf("users = %v, want %s %s %s", users, globalChainUser, globalIPFSUser, globalRelayUser)
	}

	for _, p := range []int{
		constants.ChainP2PPort, constants.ChainRPCPort, constants.ChainGRPCPort,
		constants.ChainAPIPort, constants.ChainPrometheusPort,
	} {
		if !strings.Contains(chain, strconv.Itoa(p)) {
			t.Errorf("chain unit missing port %d\n%s", p, chain)
		}
	}
	for _, want := range []string{
		"tcp://0.0.0.0:31000",
		"tcp://127.0.0.1:31001",
		"127.0.0.1:31002",
		"tcp://127.0.0.1:31003",
		"127.0.0.1:31004",
	} {
		if !strings.Contains(chain, want) {
			t.Errorf("chain unit missing %s", want)
		}
	}
	// The unit only runs the daemon on the repo; the installer's config carries the loopback API.
	if exec := mustDirective(t, ipfs, "ExecStart"); exec != "/usr/lib/orama-global/bin/ipfs daemon --repo-dir="+constants.GlobalIPFSHome {
		t.Errorf("ipfs ExecStart = %q", exec)
	}
	if constants.GlobalIPFSAPIPort != 31011 {
		t.Fatalf("GlobalIPFSAPIPort = %d", constants.GlobalIPFSAPIPort)
	}
	relayAddr := "127.0.0.1:" + strconv.Itoa(constants.GlobalRelayMetricsPort)
	if !strings.Contains(relay, relayAddr) {
		t.Errorf("relay unit missing %s\n%s", relayAddr, relay)
	}
	if constants.GlobalRelayMetricsPort != 31014 {
		t.Fatalf("GlobalRelayMetricsPort = %d", constants.GlobalRelayMetricsPort)
	}
}

func TestGlobalChainUnit_userMatchesStagenetDeploy(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("no caller")
	}
	script := filepath.Join(filepath.Dir(file), "..", "..", "..", "chain", "scripts", "stagenet", "deploy.sh")
	data, err := os.ReadFile(script)
	if err != nil {
		t.Fatalf("read %s: %v", script, err)
	}
	m := regexp.MustCompile(`(?m)^SVC_USER="([^"]+)"$`).FindSubmatch(data)
	if m == nil {
		t.Fatalf("%s does not set SVC_USER", script)
	}
	if string(m[1]) != globalChainUser {
		t.Fatalf("deploy.sh user %q, template user %q", m[1], globalChainUser)
	}
	unit := RenderGlobalChainUnit("")
	if !strings.Contains(unit, "User="+globalChainUser) {
		t.Fatalf("chain unit user is not %s", globalChainUser)
	}
}

func TestGlobalUnits_hideTheClusterTreeAndDenyPrivateNets(t *testing.T) {
	units := map[string]string{
		"chain":    RenderGlobalChainUnit(""),
		"ipfs":     RenderGlobalIPFSUnit(),
		"gc":       RenderGlobalIPFSGCUnit(),
		"provider": RenderGlobalProviderUnit(),
		"relay":    RenderGlobalRelayUnit(),
		"tor":      RenderGlobalTorRelayUnit(),
		"dirauth":  RenderGlobalTorDirauthUnit(),
		"onion":    RenderGlobalTorOnionUnit(),
		"sbws":     RenderGlobalSBWSUnit(),
		"reporter": RenderGlobalReporterUnit(),
		"archiver": RenderGlobalArchiverUnit(),
		"repair":   RenderGlobalRepairUnit(),
		"indexer":  RenderGlobalIndexerUnit(),
	}
	deny := "IPAddressDeny=10.0.0.0/8 172.16.0.0/12 192.168.0.0/16 169.254.0.0/16 100.64.0.0/10 fc00::/7 fe80::/10"
	for name, unit := range units {
		if strings.Contains(unit, "PartOf=") {
			t.Errorf("%s is PartOf another unit", name)
		}
		if !strings.Contains(unit, "TemporaryFileSystem=/opt/orama:ro") {
			t.Errorf("%s does not hide /opt/orama", name)
		}
		if !strings.Contains(unit, "InaccessiblePaths=/var/lib/orama-unit-env /etc/wireguard /etc/orama") {
			t.Errorf("%s can still see cluster paths", name)
		}
		if !strings.Contains(unit, deny) {
			t.Errorf("%s does not deny private ranges", name)
		}
		if !strings.Contains(unit, "PrivatePIDs=yes") {
			t.Errorf("%s does not set PrivatePIDs", name)
		}
		if !strings.Contains(unit, "CapabilityBoundingSet=") {
			t.Errorf("%s keeps capabilities", name)
		}
		exec := mustDirective(t, unit, "ExecStart")
		if !strings.HasPrefix(exec, "/usr/lib/orama-global/bin/") && !strings.HasPrefix(exec, "/usr/bin/") {
			t.Errorf("%s ExecStart %q is not a global or distro binary", name, exec)
		}
		if strings.Contains(exec, "/opt/orama") {
			t.Errorf("%s runs a binary under /opt/orama, which the tmpfs hides", name)
		}
	}
	if !strings.Contains(RenderGlobalTorRelayUnit(), "MemoryDenyWriteExecute=yes") {
		t.Error("tor relay does not set MemoryDenyWriteExecute")
	}
	if strings.Contains(RenderGlobalChainUnit(""), "MemoryDenyWriteExecute=yes") {
		t.Error("the Go chain unit sets MemoryDenyWriteExecute; the runtime cannot start under it")
	}
	provider := RenderGlobalProviderUnit()
	if !strings.Contains(provider, "SupplementaryGroups=orama-ipfs-pub-rpc") {
		t.Error("provider cannot read the Kubo RPC token group")
	}
	if strings.Contains(RenderGlobalArchiverUnit(), "SupplementaryGroups=") {
		t.Error("archiver reads blocks over RPC and needs no group on the chain home")
	}
	timer := RenderGlobalIPFSGCTimer()
	if strings.Contains(timer, "PartOf=") || !strings.Contains(timer, "orama-global-ipfs-gc.service") {
		t.Fatalf("gc timer:\n%s", timer)
	}
}

func mustDirective(t *testing.T, unit, key string) string {
	t.Helper()
	var found string
	n := 0
	for _, line := range strings.Split(unit, "\n") {
		if strings.HasPrefix(line, key+"=") {
			found = strings.TrimPrefix(line, key+"=")
			n++
		}
	}
	if n != 1 {
		t.Fatalf("%s appears %d times in\n%s", key, n, unit)
	}
	return found
}

func TestGlobalIndexerUnit_ownUserLoopbackAPIAndOwnHome(t *testing.T) {
	unit := RenderGlobalIndexerUnit()
	if user := mustDirective(t, unit, "User"); user != "orama-indexer" || mustDirective(t, unit, "Group") != user {
		t.Fatalf("indexer runs as %q", user)
	}
	for _, other := range []string{globalChainUser, globalArchiverUser, globalProviderUser, globalRepairUser, globalRelayUser, globalIPFSUser} {
		if other == globalIndexerUser {
			t.Fatalf("indexer shares user %s", other)
		}
	}
	exec := mustDirective(t, unit, "ExecStart")
	want := "/usr/lib/orama-global/bin/orama-global indexer --rpc tcp://127.0.0.1:31001 --home /var/lib/orama-global/indexer --listen 127.0.0.1:31015"
	if exec != want {
		t.Fatalf("ExecStart = %q, want %q", exec, want)
	}
	if mustDirective(t, unit, "StateDirectory") != "orama-global/indexer" ||
		mustDirective(t, unit, "ReadWritePaths") != constants.GlobalIndexerHome {
		t.Fatalf("indexer state is not its own home:\n%s", unit)
	}
	if strings.Contains(unit, "SupplementaryGroups=") || strings.Contains(unit, constants.ChainHome) {
		t.Fatalf("indexer can reach the chain home or another group:\n%s", unit)
	}
}

func TestGlobalChainUnit_runsOramadUnderCosmovisorWithoutDownloads(t *testing.T) {
	unit := RenderGlobalChainUnit("")
	exec := mustDirective(t, unit, "ExecStart")
	if !strings.HasPrefix(exec, "/usr/lib/orama-global/bin/cosmovisor run start --home "+constants.ChainHome+" ") {
		t.Fatalf("ExecStart %q does not run oramad through cosmovisor", exec)
	}
	for _, want := range []string{
		"Environment=DAEMON_NAME=oramad\n",
		"Environment=DAEMON_HOME=" + constants.ChainHome + "\n",
		"Environment=DAEMON_ALLOW_DOWNLOAD_BINARIES=false\n",
		"Environment=DAEMON_RESTART_AFTER_UPGRADE=true\n",
		"ReadWritePaths=" + constants.ChainHome + "\n",
		"ReadOnlyPaths=" + constants.ChainHome + "/cosmovisor/genesis " + constants.ChainHome + "/cosmovisor/upgrades\n",
	} {
		if !strings.Contains(unit, want) {
			t.Errorf("chain unit is missing %q\n%s", want, unit)
		}
	}
	if strings.Contains(unit, "DAEMON_ALLOW_DOWNLOAD_BINARIES=true") {
		t.Error("cosmovisor may download binaries")
	}
	if strings.Contains(unit, "rpc.unsafe") {
		t.Errorf("the chain unit turns on the CometBFT RPC's unsafe routes:\n%s", unit)
	}
}

// Every global account has no home directory and ProtectHome hides /home, so a
// unit whose HOME stays at the account's /home path breaks any program that
// resolves files under it: Kubo refused to start on stagenet because its
// denylist directory was "permission denied" under /home/orama-ipfs-pub.
func TestGlobalUnits_homeIsTheUnitsOwnStateDirectory(t *testing.T) {
	units := map[string]string{
		"chain":    RenderGlobalChainUnit(""),
		"ipfs":     RenderGlobalIPFSUnit(),
		"gc":       RenderGlobalIPFSGCUnit(),
		"provider": RenderGlobalProviderUnit(),
		"relay":    RenderGlobalRelayUnit(),
		"tor":      RenderGlobalTorRelayUnit(),
		"sbws":     RenderGlobalSBWSUnit(),
		"reporter": RenderGlobalReporterUnit(),
		"archiver": RenderGlobalArchiverUnit(),
		"repair":   RenderGlobalRepairUnit(),
		"indexer":  RenderGlobalIndexerUnit(),
	}
	for name, unit := range units {
		wd := mustDirective(t, unit, "WorkingDirectory")
		if !strings.Contains(unit, "\nEnvironment=HOME="+wd+"\n") {
			t.Errorf("%s: HOME is not its WorkingDirectory %s:\n%s", name, wd, unit)
		}
		if strings.HasPrefix(wd, "/home/") {
			t.Errorf("%s: WorkingDirectory %s is under /home, which ProtectHome hides", name, wd)
		}
	}
}
