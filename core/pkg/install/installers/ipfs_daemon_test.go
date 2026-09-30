package installers

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/rootfs"
)

// kuboBinEnv names the ipfs binary the daemon tests run. They are skipped
// without it: they prove the configs this package writes are ones the pinned
// Kubo release accepts at startup (Kubo validates its config when the daemon
// starts, and tightens that validation between releases).
const kuboBinEnv = "ORAMA_TEST_KUBO_BIN"

const kuboStartTimeout = 90 * time.Second

func kuboBinary(t *testing.T) string {
	t.Helper()
	bin := os.Getenv(kuboBinEnv)
	if bin == "" {
		t.Skipf("%s is not set; point it at the pinned Kubo ipfs binary to run this test", kuboBinEnv)
	}
	return bin
}

func freeTCPPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

// startKuboDaemon runs `ipfs daemon` on repo and returns once the RPC answers
// with the bearer. The daemon stops when the test ends. A daemon that exits
// (an invalid config makes it) fails the test with its output.
func startKuboDaemon(t *testing.T, bin, repo string, apiPort int, token string, args ...string) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, bin, append([]string{"daemon"}, args...)...)
	cmd.Env = append(os.Environ(), "IPFS_PATH="+repo)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatalf("start ipfs daemon: %v", err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	t.Cleanup(func() {
		cancel()
		<-exited
	})

	deadline := time.Now().Add(kuboStartTimeout)
	for time.Now().Before(deadline) {
		select {
		case err := <-exited:
			t.Fatalf("ipfs daemon exited (%v) before its RPC answered:\n%s", err, out.String())
		default:
		}
		if kuboRPCAnswers(apiPort, token) {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("ipfs daemon's RPC did not answer within %s:\n%s", kuboStartTimeout, out.String())
}

func kuboRPCAnswers(apiPort int, token string) bool {
	req, err := http.NewRequest(http.MethodPost, "http://127.0.0.1:"+strconv.Itoa(apiPort)+"/api/v0/version", nil)
	if err != nil {
		return false
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode == http.StatusOK
}

// kuboID is the daemon's own addresses, from /api/v0/id.
func kuboID(t *testing.T, apiPort int, token string) []string {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, "http://127.0.0.1:"+strconv.Itoa(apiPort)+"/api/v0/id", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var id struct{ Addresses []string }
	if err := json.NewDecoder(resp.Body).Decode(&id); err != nil {
		t.Fatal(err)
	}
	return id.Addresses
}

// A repo the private installer configures (swarm key, AutoConf off, routing
// none, cleared bootstrap, WireGuard-style bind) is one Kubo starts on.
func TestInitializeRepo_kuboStartsOnThePrivateConfig(t *testing.T) {
	bin := kuboBinary(t)
	t.Setenv("PATH", filepath.Dir(bin)+string(os.PathListSeparator)+os.Getenv("PATH"))

	me, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	uid, _ := strconv.ParseUint(me.Uid, 10, 32)
	gid, _ := strconv.ParseUint(me.Gid, 10, 32)
	stubServiceAccount(t, serviceAccount{uid: uint32(uid), gid: uint32(gid), home: me.HomeDir}, nil)

	dir := t.TempDir()
	repo := filepath.Join(dir, "ipfs", "repo")
	swarmKey := filepath.Join(dir, "swarm.key")
	key := "/key/swarm/psk/1.0.0/\n/base16/\n" + strings.Repeat("ab", 32) + "\n"
	if err := os.WriteFile(swarmKey, []byte(key), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cluster-secret"), []byte(strings.Repeat("cd", 32)), 0o600); err != nil {
		t.Fatal(err)
	}

	apiPort, gatewayPort, swarmPort := freeTCPPort(t), freeTCPPort(t), freeTCPPort(t)
	peer := &IPFSPeerInfo{
		PeerID: "12D3KooWDpJ7As7BWAwRMfu1VU2WCqNjvq387JEYKDBj4kx6nXTN",
		Addrs:  []string{"/ip4/10.0.0.2/tcp/4101"},
	}
	ii := NewIPFSInstaller("amd64", io.Discard)
	if err := ii.InitializeRepo(rootfs.At(dir), repo, swarmKey, apiPort, gatewayPort, swarmPort, "127.0.0.1", peer); err != nil {
		t.Fatalf("InitializeRepo: %v", err)
	}

	token := readKuboToken(t, repo)
	startKuboDaemon(t, bin, repo, apiPort, token)
	addrs := kuboID(t, apiPort, token)
	t.Logf("private daemon addresses: %v", addrs)
	if len(addrs) == 0 {
		t.Fatal("the private daemon reports no addresses")
	}
}

// readKuboToken is the bearer InitializeRepo wrote into the repo config.
func readKuboToken(t *testing.T, repo string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(repo, "config"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		API struct {
			Authorizations map[string]struct{ AuthSecret string }
		}
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	for _, a := range cfg.API.Authorizations {
		return strings.TrimPrefix(a.AuthSecret, "bearer:")
	}
	t.Fatal("no API.Authorizations entry in the repo config")
	return ""
}

// The public config, merged into what `ipfs init --profile=server` wrote, is
// one Kubo starts on, and the daemon announces no loopback, link-local or
// private address (Kubo v0.40 announces every interface address of a
// 0.0.0.0 listener unless it is filtered).
func TestPublicKuboConfig_kuboStartsAndAnnouncesNoPrivateAddress(t *testing.T) {
	bin := kuboBinary(t)

	repo := t.TempDir()
	cmd := exec.Command(bin, "init", "--profile=server", "--repo-dir="+repo)
	cmd.Env = append(os.Environ(), "IPFS_PATH="+repo, "HOME="+repo)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ipfs init: %v\n%s", err, out)
	}
	existing, err := os.ReadFile(filepath.Join(repo, "config"))
	if err != nil {
		t.Fatal(err)
	}
	token := hex.EncodeToString(bytes.Repeat([]byte{0x5a}, 32))
	if err := WritePublicKuboFiles(rootfs.At(repo), repo, token, 5_000_000_000, existing); err != nil {
		t.Fatalf("WritePublicKuboFiles: %v", err)
	}

	// The public bearer may not call id or version, so the probe gets its own.
	const probeToken = "probe-token"
	addProbeAuthorization(t, repo, probeToken)
	apiPort := publicAPIPortOf(t, repo)
	startKuboDaemon(t, bin, repo, apiPort, probeToken)
	announced := kuboID(t, apiPort, probeToken)
	t.Logf("announced addresses: %v", announced)
	for _, a := range announced {
		if privateMultiaddr(a) {
			t.Errorf("the public daemon announces %s", a)
		}
	}
}

// addProbeAuthorization lets probeToken call the RPC paths the test reads.
func addProbeAuthorization(t *testing.T, repo, probeToken string) {
	t.Helper()
	path := filepath.Join(repo, "config")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	auths := cfg["API"].(map[string]any)["Authorizations"].(map[string]any)
	auths["probe"] = map[string]any{"AuthSecret": "bearer:" + probeToken, "AllowedPaths": []string{"/api/v0/id", "/api/v0/version"}}
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, out, 0o600); err != nil {
		t.Fatal(err)
	}
}

func publicAPIPortOf(t *testing.T, repo string) int {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(repo, "config"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct{ Addresses struct{ API []string } }
	if err := json.Unmarshal(raw, &cfg); err != nil || len(cfg.Addresses.API) != 1 {
		t.Fatalf("API address in the public config: %v %v", cfg.Addresses.API, err)
	}
	parts := strings.Split(cfg.Addresses.API[0], "/")
	port, err := strconv.Atoi(parts[len(parts)-1])
	if err != nil {
		t.Fatal(err)
	}
	return port
}

// privateMultiaddr reports whether a /ip4 or /ip6 multiaddr names a loopback,
// link-local, private or otherwise non-public address.
func privateMultiaddr(addr string) bool {
	parts := strings.Split(addr, "/")
	if len(parts) < 3 || (parts[1] != "ip4" && parts[1] != "ip6") {
		return false
	}
	ip := net.ParseIP(parts[2])
	if ip == nil {
		return false
	}
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() ||
		(ip.To4() != nil && ip.To4()[0] == 100 && ip.To4()[1]&0xc0 == 64)
}
