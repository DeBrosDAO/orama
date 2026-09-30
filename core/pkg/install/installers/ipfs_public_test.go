package installers

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/rootfs"
)

func TestPublicKuboConfig_hasNoSwarmKeyAndFiltersPrivateRanges(t *testing.T) {
	existing := []byte(`{"Identity":{"PeerID":"12D3KooWexample"}}`)
	body, err := PublicKuboConfig(existing, "abc123token", 5_000_000_000, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "swarm.key") || strings.Contains(string(body), "LIBP2P_FORCE_PNET") {
		t.Fatalf("public config mentions a private swarm:\n%s", body)
	}
	var config map[string]interface{}
	if err := json.Unmarshal(body, &config); err != nil {
		t.Fatal(err)
	}
	identity := config["Identity"].(map[string]interface{})
	if identity["PeerID"] != "12D3KooWexample" {
		t.Fatalf("identity was dropped: %v", identity)
	}
	addresses := config["Addresses"].(map[string]interface{})
	api := addresses["API"].([]interface{})
	if api[0] != "/ip4/127.0.0.1/tcp/31011" || constants.GlobalIPFSAPIPort != 31011 {
		t.Fatalf("API = %v", api)
	}
	gateway := addresses["Gateway"].([]interface{})
	if gateway[0] != "/ip4/127.0.0.1/tcp/31012" {
		t.Fatalf("Gateway = %v", gateway)
	}
	swarm := strings.Join(asStrings(t, addresses["Swarm"]), " ")
	if !strings.Contains(swarm, "/ip4/0.0.0.0/tcp/31010") || !strings.Contains(swarm, "/ip4/0.0.0.0/udp/31010/quic-v1") {
		t.Fatalf("Swarm = %s", swarm)
	}
	noAnnounce := strings.Join(asStrings(t, addresses["NoAnnounce"]), " ")
	if !strings.Contains(noAnnounce, "/ip4/10.0.0.0/ipcidr/8") {
		t.Fatalf("NoAnnounce does not hide the mesh: %s", noAnnounce)
	}
	filters := config["Swarm"].(map[string]interface{})["AddrFilters"]
	if !strings.Contains(strings.Join(asStrings(t, filters), " "), "/ip4/10.0.0.0/ipcidr/8") {
		t.Fatalf("AddrFilters = %v", filters)
	}
	apiCfg := config["API"].(map[string]interface{})
	auth := apiCfg["Authorizations"].(map[string]interface{})
	user := auth["orama"].(map[string]interface{})
	if user["AuthSecret"] != "bearer:abc123token" {
		t.Fatalf("auth = %v", user)
	}
	if config["Provide"].(map[string]interface{})["Strategy"] != "pinned" {
		t.Fatalf("Provide = %v", config["Provide"])
	}
	if config["Routing"].(map[string]interface{})["Type"] != "dht" {
		t.Fatalf("Routing = %v", config["Routing"])
	}
	if config["Datastore"].(map[string]interface{})["StorageMax"] != "5GB" {
		t.Fatalf("StorageMax = %v", config["Datastore"])
	}
}

func TestPublicKuboConfig_refusesAnEmptyToken(t *testing.T) {
	if _, err := PublicKuboConfig(nil, "  ", 0, "127.0.0.1"); err == nil {
		t.Fatal("empty token was accepted")
	}
}

func TestPublicStorageMax_addsHeadroom(t *testing.T) {
	if got := PublicStorageMax(0); got != "1GB" {
		t.Fatalf("empty declaration = %s", got)
	}
	// 10GB + 10% = 11GB
	if got := PublicStorageMax(10_000_000_000); got != "11GB" {
		t.Fatalf("10GB declaration = %s", got)
	}
}

func TestWritePublicKuboFiles_tokenModeAndNoSwarmKey(t *testing.T) {
	dir := t.TempDir()
	root := rootfs.At(dir)
	if err := WritePublicKuboFiles(root, dir, "abc123token", 1_000_000_000, nil, "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, PublicAPITokenFile))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0640 {
		t.Fatalf("token mode %o, want 0640", info.Mode().Perm())
	}
	if _, err := os.Stat(filepath.Join(dir, "swarm.key")); !os.IsNotExist(err) {
		t.Fatalf("swarm.key exists: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "swarm.key"), []byte("key"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := WritePublicKuboFiles(root, dir, "abc123token", 1_000_000_000, nil, "127.0.0.1"); err == nil {
		t.Fatal("a repo that already has a swarm.key was accepted")
	}
}

func TestParseDenylist(t *testing.T) {
	got, err := ParseDenylist("# comment\n\nQmYwAPJzv5CZsnA625s3Xf2nemtYgPpHdWEz79ojWnPbdG\n")
	if err != nil || len(got) != 1 {
		t.Fatalf("%v %v", got, err)
	}
	if _, err := ParseDenylist("not a cid"); err == nil {
		t.Fatal("a line with a space was accepted")
	}
}

func TestGlobalIPFSUnit_matchesThePublicConfig(t *testing.T) {
	// The unit and the config must name the same loopback RPC. Importing the
	// install package from here would cycle, so the ports are the constants.
	if constants.GlobalIPFSAPIPort != 31011 || constants.GlobalIPFSSwarmPort != 31010 {
		t.Fatalf("ports drifted")
	}
}

func asStrings(t *testing.T, v interface{}) []string {
	t.Helper()
	items, ok := v.([]interface{})
	if !ok {
		t.Fatalf("not a list: %T", v)
	}
	out := make([]string, len(items))
	for i, item := range items {
		s, ok := item.(string)
		if !ok {
			t.Fatalf("item %d is %T", i, item)
		}
		out[i] = s
	}
	return out
}

func TestPublicKuboConfig_tokenAllowsOnlyWhatTheProviderAndGCCall(t *testing.T) {
	body, err := PublicKuboConfig(nil, "tok", 1, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		API struct {
			Authorizations map[string]struct{ AllowedPaths []string }
		}
	}
	if err := json.Unmarshal(body, &config); err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, auth := range config.API.Authorizations {
		paths = auth.AllowedPaths
	}
	want := []string{"/api/v0/add", "/api/v0/cat", "/api/v0/pin/add", "/api/v0/pin/rm", "/api/v0/repo/gc"}
	if strings.Join(paths, ",") != strings.Join(want, ",") {
		t.Fatalf("allowed paths = %v, want %v", paths, want)
	}
	for _, p := range paths {
		if p == "/api/v0" || strings.HasPrefix(p, "/api/v0/config") || strings.HasPrefix(p, "/api/v0/swarm") || strings.HasPrefix(p, "/api/v0/shutdown") {
			t.Errorf("the bearer allows %s", p)
		}
	}
}

// ipfs init --profile=server writes Datastore.Spec, which Kubo requires, and
// connection-manager defaults. The public config used to replace whole
// sections, so Kubo refused to start: "required Datastore.Spec entry missing
// from config file" (stagenet, 2026-09-30).
func TestPublicKuboConfig_keepsWhatIPFSInitWrote(t *testing.T) {
	existing := []byte(`{
  "Identity": {"PeerID": "12D3KooWtest", "PrivKey": "CAESQ..."},
  "Datastore": {"StorageMax": "10GB", "GCPeriod": "1h",
    "Spec": {"type": "mount", "mounts": [{"mountpoint": "/blocks", "type": "measure", "prefix": "flatfs.datastore"}]}},
  "Swarm": {"ConnMgr": {"Type": "basic", "HighWater": 96}, "AddrFilters": null},
  "Addresses": {"Announce": [], "API": "/ip4/127.0.0.1/tcp/5001"},
  "API": {"HTTPHeaders": {}},
  "Routing": {"AcceleratedDHTClient": false}
}`)
	out, err := PublicKuboConfig(existing, "tok123", 10<<30, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]map[string]interface{}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if got["Datastore"]["Spec"] == nil || got["Datastore"]["GCPeriod"] != "1h" {
		t.Errorf("Datastore lost what ipfs init wrote: %v", got["Datastore"])
	}
	if got["Datastore"]["StorageMax"] != PublicStorageMax(10<<30) {
		t.Errorf("StorageMax = %v", got["Datastore"]["StorageMax"])
	}
	if got["Swarm"]["ConnMgr"] == nil || got["Swarm"]["AddrFilters"] == nil {
		t.Errorf("Swarm = %v, want ConnMgr kept and AddrFilters set", got["Swarm"])
	}
	if got["Identity"]["PeerID"] != "12D3KooWtest" {
		t.Errorf("Identity = %v", got["Identity"])
	}
	if got["Routing"]["Type"] != "dht" || got["Routing"]["AcceleratedDHTClient"] != false {
		t.Errorf("Routing = %v", got["Routing"])
	}
	if got["API"]["Authorizations"] == nil || got["API"]["HTTPHeaders"] == nil {
		t.Errorf("API = %v", got["API"])
	}
}

// An address announced by hand in an earlier config would keep being announced
// after a re-install, since the config is merged, not replaced.
func TestPublicKuboConfig_dropsHandAnnouncedAddresses(t *testing.T) {
	existing := []byte(`{"Addresses": {"Announce": ["/ip4/10.0.0.1/tcp/4101"], "AppendAnnounce": ["/ip4/192.168.1.2/tcp/4101"]}}`)
	out, err := PublicKuboConfig(existing, "tok123", 10<<30, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	var got struct{ Addresses map[string][]string }
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"Announce", "AppendAnnounce"} {
		if len(got.Addresses[key]) != 0 {
			t.Errorf("Addresses.%s = %v, want empty", key, got.Addresses[key])
		}
	}
}

// A section an operator edited into something other than an object cannot be
// merged into; replacing it would silently drop it, so the config is refused.
func TestPublicKuboConfig_refusesASectionThatIsNotAnObject(t *testing.T) {
	for _, existing := range []string{`{"Datastore": "10GB"}`, `{"Swarm": ["x"]}`, `{"API": 5}`} {
		_, err := PublicKuboConfig([]byte(existing), "tok123", 10<<30, "127.0.0.1")
		if err == nil || !strings.Contains(err.Error(), "not an object") {
			t.Errorf("%s: err = %v, want a not-an-object refusal", existing, err)
		}
	}
}

// A section written as null is absent: it is created.
func TestPublicKuboConfig_createsANullSection(t *testing.T) {
	out, err := PublicKuboConfig([]byte(`{"Routing": null}`), "tok123", 10<<30, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `"Type": "dht"`) {
		t.Errorf("Routing was not set:\n%s", out)
	}
}

func TestPublicKuboConfig_apiHostOverwritesAnExistingAPIAddress(t *testing.T) {
	existing := []byte(`{"Addresses": {"API": ["/ip4/127.0.0.1/tcp/31011"], "Gateway": "/ip4/127.0.0.1/tcp/8080"}}`)
	out, err := PublicKuboConfig(existing, "tok123", 10<<30, "198.18.0.2")
	if err != nil {
		t.Fatal(err)
	}
	var got struct{ Addresses map[string][]string }
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if api := got.Addresses["API"]; len(api) != 1 || api[0] != "/ip4/198.18.0.2/tcp/31011" {
		t.Errorf("API = %v", api)
	}
	if gw := got.Addresses["Gateway"]; len(gw) != 1 || gw[0] != "/ip4/127.0.0.1/tcp/31012" {
		t.Errorf("Gateway = %v, want it left on loopback", gw)
	}
	back, err := PublicKuboConfig(out, "tok123", 10<<30, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(back), "/ip4/127.0.0.1/tcp/31011") || strings.Contains(string(back), "198.18.0.2") {
		t.Errorf("a non-co-located re-install kept the namespace address:\n%s", back)
	}
}
