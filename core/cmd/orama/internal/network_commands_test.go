package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/printer"
	"github.com/DeBrosOfficial/network/pkg/netregistry"
	"github.com/DeBrosOfficial/network/pkg/tornet/tornettest"
)

var (
	networkTestRoot    = []byte(`{"signed":{"_type":"root"}}`)
	networkTestGenesis = []byte(`{"chain_id":"orama-teststage-1"}`)
)

func networkManifest(name, chainID string) netregistry.Manifest {
	return netregistry.Manifest{
		Name: name, ChainID: chainID, GenesisSHA256: netregistry.Digest(networkTestGenesis),
		Seeds: []string{"seed1." + name + ".example.org"}, Channel: "nightly", MinVersion: "0.3.0",
		ReleaseRepo: "https://releases.example.org/", ReleaseRootSHA256: netregistry.Digest(networkTestRoot),
	}
}

func manifestJSON(t *testing.T, m netregistry.Manifest) []byte {
	t.Helper()
	data, err := m.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// useNetworkFixtures points the network commands at a temp store and a built-in
// registry holding the named networks, and the environment config at cfg.
func useNetworkFixtures(t *testing.T, cfg *EnvironmentConfig, builtin ...string) string {
	t.Helper()
	cleanup := writeTestConfig(t, cfg)
	t.Cleanup(cleanup)

	fsys := fstest.MapFS{"embedded/README.md": {Data: []byte("x")}}
	for _, name := range builtin {
		fsys["embedded/"+name+"/manifest.json"] = &fstest.MapFile{Data: manifestJSON(t, networkManifest(name, "orama-"+name+"-1"))}
		fsys["embedded/"+name+"/release-root.json"] = &fstest.MapFile{Data: networkTestRoot}
	}
	oldEmbedded, oldDir := embeddedNetworksFn, networkStoreDirFn
	t.Cleanup(func() { embeddedNetworksFn, networkStoreDirFn = oldEmbedded, oldDir })
	embeddedNetworksFn = func() (*netregistry.Registry, error) { return netregistry.LoadFS(fsys, "embedded") }
	dir := filepath.Join(t.TempDir(), "networks")
	networkStoreDirFn = func() (string, error) { return dir, nil }
	return dir
}

func capture(t *testing.T) (*printer.Printer, *bytes.Buffer) {
	t.Helper()
	var out bytes.Buffer
	return printer.New(&out, &out), &out
}

func TestNetworkList_unionOfRegistryAndGateways(t *testing.T) {
	cfg := &EnvironmentConfig{
		ActiveEnvironment: "lab",
		Environments: []Environment{
			{Name: "lab", GatewayURL: "https://lab.example.org", Description: "my lab", Network: "stagenet"},
			{Name: "stagenet", GatewayURL: "https://stagenet.example.org"},
		},
	}
	useNetworkFixtures(t, cfg, "stagenet", "testnet")
	p, out := capture(t)

	if err := NetworkList(p); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{
		"lab", "orama-stagenet-1", "https://lab.example.org", "my lab", "configured",
		"built in, configured", "https://stagenet.example.org",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("list is missing %q:\n%s", want, got)
		}
	}
	lines := strings.Split(strings.TrimSpace(got), "\n")
	if len(lines) != 4 || !strings.HasPrefix(lines[1], "lab") || !strings.HasPrefix(lines[2], "stagenet") || !strings.HasPrefix(lines[3], "testnet") {
		t.Errorf("rows are not the three names sorted:\n%s", got)
	}
	if !strings.Contains(lines[1], "*") || strings.Contains(lines[2], "*") {
		t.Errorf("the active network is not the one marked:\n%s", got)
	}
}

func TestNetworkList_jsonKeysTheColumns(t *testing.T) {
	useNetworkFixtures(t, &EnvironmentConfig{Environments: []Environment{{Name: "lab", GatewayURL: "https://lab.example.org"}}}, "stagenet")
	p, out := capture(t)
	if err := NetworkList(p.WithJSON(true)); err != nil {
		t.Fatal(err)
	}
	var rows []map[string]string
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil || len(rows) != 2 {
		t.Fatalf("not two JSON rows: %v\n%s", err, out)
	}
	if rows[1]["name"] != "stagenet" || rows[1]["chain"] != "orama-stagenet-1" || rows[0]["gateway"] != "https://lab.example.org" {
		t.Errorf("rows = %v", rows)
	}
}

func TestNetworkList_nothingConfiguredAndNothingBuiltIn(t *testing.T) {
	useNetworkFixtures(t, &EnvironmentConfig{})
	p, out := capture(t)
	if err := NetworkList(p); err != nil {
		t.Fatal(err)
	}
	if strings.Count(strings.TrimSpace(out.String()), "\n") != 0 {
		t.Errorf("an empty list prints only its header:\n%s", out)
	}
}

// A computer that ran `orama env` writes environments.json in the shape it had
// before networks. The commands that replace env read that file as it is, keep
// its active environment, and keep every field they do not know when they
// write it back.
func TestNetworkCommands_readAndKeepAnEnvironmentsFileOfTheOldShape(t *testing.T) {
	legacy := `{
  "environments": [
    {"name": "devnet", "gateway_url": "https://devnet.example.org", "description": "dev",
     "is_active": false, "ca_file": "/tmp/ca.pem",
     "nodes": [{"host": "203.0.113.5", "user": "root", "role": "nameserver"}],
     "delegations": [{"domain": "devnet.example.org", "delegated": true, "checked_at": "2026-10-01T00:00:00Z"}]},
    {"name": "lab", "gateway_url": "https://lab.example.org", "description": ""}
  ],
  "active_environment": "devnet"
}`
	path := filepath.Join(t.TempDir(), "environments.json")
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	old := getEnvironmentConfigPathFn
	t.Cleanup(func() { getEnvironmentConfigPathFn = old })
	getEnvironmentConfigPathFn = func() (string, error) { return path, nil }
	useNetworkFixtures(t, &EnvironmentConfig{})
	getEnvironmentConfigPathFn = func() (string, error) { return path, nil }

	p, out := capture(t)
	if err := NetworkCurrent(p); err != nil || !strings.Contains(out.String(), "Current network: devnet") {
		t.Fatalf("current = %v\n%s", err, out)
	}
	if err := NetworkUse(p, "lab"); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadEnvironmentConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ActiveEnvironment != "lab" {
		t.Errorf("active = %q", cfg.ActiveEnvironment)
	}
	devnet := cfg.Environments[0]
	if devnet.CAFile != "/tmp/ca.pem" || len(devnet.Nodes) != 1 || devnet.Nodes[0].Role != "nameserver" ||
		len(devnet.Delegations) != 1 || !devnet.Delegations[0].Delegated || devnet.Network != "" {
		t.Errorf("a field of the old file was lost: %+v", devnet)
	}
}

func TestNetworkUse_byGatewayNameByRegistryNetworkAndNeither(t *testing.T) {
	cfg := &EnvironmentConfig{Environments: []Environment{
		{Name: "lab", GatewayURL: "https://lab.example.org", Network: "stagenet"},
		{Name: "one", GatewayURL: "https://one.example.org", Network: "testnet"},
		{Name: "two", GatewayURL: "https://two.example.org", Network: "testnet"},
	}}
	useNetworkFixtures(t, cfg, "stagenet", "testnet")
	p, _ := capture(t)

	if err := NetworkUse(p, "lab"); err != nil {
		t.Fatalf("by gateway name: %v", err)
	}
	if env, _ := GetActiveEnvironment(); env.Name != "lab" {
		t.Errorf("active = %v", env)
	}
	if err := NetworkUse(p, "stagenet"); err != nil {
		t.Fatalf("by registry network with one gateway: %v", err)
	}
	if err := NetworkUse(p, "testnet"); clierr.CodeOf(err) != clierr.CodeUsage || !strings.Contains(err.Error(), "one, two") {
		t.Errorf("two gateways on one network: %v", err)
	}
	if err := NetworkUse(p, "nowhere"); clierr.CodeOf(err) != clierr.CodeNotFound || !strings.Contains(err.Error(), "orama network add") {
		t.Errorf("an unknown network: %v", err)
	}
}

func TestNetworkAddCluster_recordsTheRegistryNetwork(t *testing.T) {
	useNetworkFixtures(t, &EnvironmentConfig{}, "stagenet")
	p, out := capture(t)

	err := NetworkAddCluster(p, []string{"mine", "https://mine.example.org", "my cluster"}, ClusterOptions{Network: "stagenet"})
	if err != nil {
		t.Fatal(err)
	}
	env, err := GetEnvironmentByName("mine")
	if err != nil || env.Network != "stagenet" || env.Description != "my cluster" {
		t.Fatalf("stored %+v, %v", env, err)
	}
	if !strings.Contains(out.String(), "Added network: mine") || !strings.Contains(out.String(), "Registry network: stagenet") {
		t.Errorf("output:\n%s", out)
	}
	// Adding it again without --network keeps the association.
	if err := NetworkAddCluster(p, []string{"mine", "https://mine.example.org"}, ClusterOptions{}); err != nil {
		t.Fatal(err)
	}
	if env, _ := GetEnvironmentByName("mine"); env.Network != "stagenet" {
		t.Errorf("re-adding dropped the network: %+v", env)
	}
}

func TestNetworkAddCluster_refusals(t *testing.T) {
	useNetworkFixtures(t, &EnvironmentConfig{}, "stagenet")
	p, _ := capture(t)
	for name, tc := range map[string]struct {
		args []string
		opts ClusterOptions
		code int
	}{
		"blank name":      {[]string{" ", "https://x.example.org"}, ClusterOptions{}, clierr.CodeUsage},
		"plain http":      {[]string{"a", "http://x.example.org"}, ClusterOptions{}, clierr.CodeUsage},
		"no host":         {[]string{"a", "https://"}, ClusterOptions{}, clierr.CodeUsage},
		"unknown network": {[]string{"a", "https://x.example.org"}, ClusterOptions{Network: "nope"}, clierr.CodeNotFound},
		"missing CA file": {[]string{"a", "https://x.example.org"}, ClusterOptions{CAFile: filepath.Join(t.TempDir(), "absent.pem")}, clierr.CodeFailure},
	} {
		t.Run(name, func(t *testing.T) {
			if err := NetworkAddCluster(p, tc.args, tc.opts); clierr.CodeOf(err) != tc.code {
				t.Fatalf("error = %v (code %d), want code %d", err, clierr.CodeOf(err), tc.code)
			}
		})
	}
	if err := NetworkAddCluster(p, []string{"loop", "http://127.0.0.1:6001"}, ClusterOptions{}); err != nil {
		t.Errorf("a loopback http gateway was refused: %v", err)
	}
}

// manifestServer serves a network under /nets/custom/ and returns its manifest URL.
func manifestServer(t *testing.T, edit func(files map[string][]byte)) (string, *http.Client) {
	t.Helper()
	files := map[string][]byte{
		"manifest.json":     manifestJSON(t, networkManifest("custom", "orama-custom-1")),
		"release-root.json": networkTestRoot,
	}
	if edit != nil {
		edit(files)
	}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := files[strings.TrimPrefix(r.URL.Path, "/nets/custom/")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	client := netregistry.NewHTTPClient()
	client.Transport = srv.Client().Transport
	return srv.URL + "/nets/custom/manifest.json", client
}

func TestNetworkAddManifest_showsTheDigestAndStoresOnlyAfterYes(t *testing.T) {
	dir := useNetworkFixtures(t, &EnvironmentConfig{}, "stagenet")
	url, client := manifestServer(t, nil)
	p, out := capture(t)

	err := NetworkAddManifest(context.Background(), p, client, strings.NewReader("yes\n"), url, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"orama-custom-1", netregistry.Digest(networkTestRoot), "Type yes", "Added network: custom"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output is missing %q:\n%s", want, out)
		}
	}
	registry, err := LoadNetworks()
	if err != nil {
		t.Fatal(err)
	}
	if n, err := registry.Get("custom"); err != nil || n.Builtin || n.Source != url {
		t.Errorf("stored network = %+v, %v", n, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "custom", "manifest.json")); err != nil {
		t.Error(err)
	}
}

func TestNetworkAddManifest_aPinnedTorNetworkIsShownAndStored(t *testing.T) {
	dir := useNetworkFixtures(t, &EnvironmentConfig{}, "stagenet")
	torFile := tornettest.NetworkFile(t)
	url, client := manifestServer(t, func(files map[string][]byte) {
		m := networkManifest("custom", "orama-custom-1")
		m.TorNetworkSHA256 = netregistry.Digest(torFile)
		files["manifest.json"] = manifestJSON(t, m)
		files["tor-network.json"] = torFile
	})
	p, out := capture(t)

	if err := NetworkAddManifest(context.Background(), p, client, strings.NewReader("yes\n"), url, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Tor network:   sha256 "+netregistry.Digest(torFile)) {
		t.Errorf("the digest of the Tor network file is not shown for the person to confirm:\n%s", out)
	}
	if got, err := os.ReadFile(filepath.Join(dir, "custom", "tor-network.json")); err != nil || string(got) != string(torFile) {
		t.Errorf("stored Tor network = %q, %v", got, err)
	}
}

func TestNetworkAddManifest_declinedStoresNothing(t *testing.T) {
	dir := useNetworkFixtures(t, &EnvironmentConfig{})
	url, client := manifestServer(t, nil)
	p, _ := capture(t)

	for _, answer := range []string{"no\n", "y\n", "", "YES\n"} {
		err := NetworkAddManifest(context.Background(), p, client, strings.NewReader(answer), url, false)
		if clierr.CodeOf(err) != clierr.CodeAborted {
			t.Errorf("answer %q: error = %v, want an aborted confirmation", answer, err)
		}
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Error("a declined network left files behind")
	}
}

func TestNetworkAddManifest_yesSkipsThePrompt(t *testing.T) {
	useNetworkFixtures(t, &EnvironmentConfig{})
	url, client := manifestServer(t, nil)
	p, out := capture(t)
	if err := NetworkAddManifest(context.Background(), p, client, strings.NewReader(""), url, true); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "Type yes") {
		t.Error("--yes still prompted")
	}
}

func TestNetworkAddManifest_refusals(t *testing.T) {
	for name, tc := range map[string]struct {
		edit    func(map[string][]byte)
		builtin []string
		url     func(string) string
	}{
		"release root is not the pinned one": {edit: func(f map[string][]byte) { f["release-root.json"] = []byte("x") }},
		"manifest is invalid":                {edit: func(f map[string][]byte) { f["manifest.json"] = []byte(`{"name":"custom"}`) }},
		"manifest is missing":                {edit: func(f map[string][]byte) { delete(f, "manifest.json") }},
		"name of a built-in network":         {builtin: []string{"custom"}},
		"plain http":                         {url: func(u string) string { return strings.Replace(u, "https://", "http://", 1) }},
	} {
		t.Run(name, func(t *testing.T) {
			dir := useNetworkFixtures(t, &EnvironmentConfig{}, tc.builtin...)
			url, client := manifestServer(t, tc.edit)
			if tc.url != nil {
				url = tc.url(url)
			}
			p, _ := capture(t)
			if err := NetworkAddManifest(context.Background(), p, client, strings.NewReader("yes\n"), url, true); err == nil {
				t.Fatal("NetworkAddManifest accepted it")
			}
			if _, err := os.Stat(dir); !os.IsNotExist(err) {
				t.Error("a refused network left files behind")
			}
		})
	}
}

func TestNetworkAddManifest_replacingShowsWhatItReplaces(t *testing.T) {
	useNetworkFixtures(t, &EnvironmentConfig{})
	url, client := manifestServer(t, nil)
	p, out := capture(t)
	for i := 0; i < 2; i++ {
		out.Reset()
		if err := NetworkAddManifest(context.Background(), p, client, strings.NewReader(""), url, true); err != nil {
			t.Fatal(err)
		}
	}
	if !strings.Contains(out.String(), "Replaces:      chain orama-custom-1") {
		t.Errorf("a second add does not say what it replaces:\n%s", out)
	}
}

func TestNetworkRemove_addedNetworkAndGatewayAndBuiltIn(t *testing.T) {
	useNetworkFixtures(t, &EnvironmentConfig{Environments: []Environment{{Name: "custom", GatewayURL: "https://c.example.org"}, {Name: "stagenet", GatewayURL: "https://s.example.org"}}}, "stagenet")
	url, client := manifestServer(t, nil)
	p, out := capture(t)
	if err := NetworkAddManifest(context.Background(), p, client, strings.NewReader(""), url, true); err != nil {
		t.Fatal(err)
	}

	if err := NetworkRemove(p, "custom"); err != nil {
		t.Fatal(err)
	}
	registry, _ := LoadNetworks()
	if _, err := registry.Get("custom"); err == nil {
		t.Error("the added network is still there")
	}
	if _, err := GetEnvironmentByName("custom"); err == nil {
		t.Error("the gateway of that name is still there")
	}

	out.Reset()
	if err := NetworkRemove(p, "stagenet"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "built into this binary") {
		t.Errorf("removing a built-in network does not say it stays:\n%s", out)
	}
	if registry, _ := LoadNetworks(); len(registry.Names()) != 1 {
		t.Errorf("the built-in network was removed: %v", registry.Names())
	}
	if err := NetworkRemove(p, "never-added"); err != nil {
		t.Errorf("removing an absent network failed: %v", err)
	}
}

func TestLoadNetworks_anAddedNameThatBecameBuiltInIsExplained(t *testing.T) {
	dir := useNetworkFixtures(t, &EnvironmentConfig{}, "custom")
	m := networkManifest("custom", "orama-custom-9")
	if err := (netregistry.Store{Dir: dir}).Save(&netregistry.Network{Manifest: &m, Root: networkTestRoot, Source: "https://example.org/manifest.json"}); err != nil {
		t.Fatal(err)
	}
	_, err := LoadNetworks()
	if err == nil || !strings.Contains(err.Error(), "orama network remove") {
		t.Fatalf("error = %v, want the remedy", err)
	}
}

func TestExpectedChainID(t *testing.T) {
	cfg := func(active, network string) *EnvironmentConfig {
		return &EnvironmentConfig{
			ActiveEnvironment: active,
			Environments:      []Environment{{Name: "main", GatewayURL: "https://gw.example.org", Network: network}},
		}
	}
	for name, tc := range map[string]struct {
		cfg         *EnvironmentConfig
		wantID      string
		wantNetwork string
	}{
		"a registry network names its chain": {cfg("main", "stagenet"), "orama-stagenet-1", "stagenet"},
		"no network configured":              {&EnvironmentConfig{}, "", ""},
		"the active one is not configured":   {cfg("gone", "stagenet"), "", ""},
		"the gateway belongs to no network":  {cfg("main", ""), "", ""},
		"the network is not in the registry": {cfg("main", "ghost"), "", ""},
	} {
		useNetworkFixtures(t, tc.cfg, "stagenet")
		id, network, err := ExpectedChainID()
		if err != nil || id != tc.wantID || network != tc.wantNetwork {
			t.Errorf("%s: %q, %q, %v; want %q, %q", name, id, network, err, tc.wantID, tc.wantNetwork)
		}
	}
}

// Only "nothing is selected" and "the network is not in the registry" mean there is no pin. A
// configuration or a registry that cannot be read is an error carrying the real cause: swallowing
// it would turn a fault into "name your chain with --chain-id", or worse, into no check.
func TestExpectedChainID_aFaultIsAnErrorWithItsCause(t *testing.T) {
	useNetworkFixtures(t, &EnvironmentConfig{ActiveEnvironment: "main", Environments: []Environment{{Name: "main", Network: "stagenet"}}}, "stagenet")
	path, err := getEnvironmentConfigPathFn()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, err = ExpectedChainID()
	if err == nil || errors.Is(err, ErrNoActiveNetwork) || !strings.Contains(err.Error(), "active network") {
		t.Fatalf("an unreadable configuration: err = %v", err)
	}

	useNetworkFixtures(t, &EnvironmentConfig{ActiveEnvironment: "main", Environments: []Environment{{Name: "main", Network: "stagenet"}}}, "stagenet")
	boom := errors.New("store unreadable")
	embeddedNetworksFn = func() (*netregistry.Registry, error) { return nil, boom }
	if _, _, err = ExpectedChainID(); !errors.Is(err, boom) {
		t.Fatalf("an unreadable registry: err = %v, want it to wrap the cause", err)
	}
}

func TestExpectedChainIDOf_followsTheNamedEnvironmentNotTheActiveOne(t *testing.T) {
	cfg := &EnvironmentConfig{
		ActiveEnvironment: "other",
		Environments: []Environment{
			{Name: "main", GatewayURL: "https://gw.example.org", Network: "stagenet"},
			{Name: "other", GatewayURL: "https://other.example.org", Network: ""},
		},
	}
	useNetworkFixtures(t, cfg, "stagenet")
	if id, network, err := ExpectedChainIDOf("main"); err != nil || id != "orama-stagenet-1" || network != "stagenet" {
		t.Errorf("main: %q, %q, %v", id, network, err)
	}
	if id, _, err := ExpectedChainIDOf("other"); err != nil || id != "" {
		t.Errorf("an environment on no registry network has no pin: %q, %v", id, err)
	}
	if id, _, err := ExpectedChainIDOf("ghost"); err != nil || id != "" {
		t.Errorf("an environment that is not configured has no pin: %q, %v", id, err)
	}
}
