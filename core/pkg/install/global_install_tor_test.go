package install

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/tornet"
)

const (
	torTestAddress = "57.129.166.16"
	torTestContact = "ops@example.org 0xabc"
)

// torFixture is a global fixture with a Tor network staged and the Tor package
// "installed" by a function that counts its calls.
type torFixture struct {
	*globalFixture
	network      tornet.Network
	identityPEM  []byte
	torInstalled int
}

func newRSAIdentity(t *testing.T) (pemKey []byte, fingerprint string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatal(err)
	}
	pemKey = pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	fp, err := tornet.RelayFingerprint(pemKey)
	if err != nil {
		t.Fatal(err)
	}
	return pemKey, fp
}

func newTorFixture(t *testing.T) *torFixture {
	t.Helper()
	return addTorNetwork(t, newGlobalFixture(t))
}

// addTorNetwork stages a Tor network beside gf's binaries.
func addTorNetwork(t *testing.T, gf *globalFixture) *torFixture {
	t.Helper()
	tf := &torFixture{globalFixture: gf}
	pemKey, fp := newRSAIdentity(t)
	tf.identityPEM = pemKey
	tf.network = tornet.Network{Name: "orama-teststage", VotingIntervalMinutes: 30, VoteDelaySeconds: 300, DistDelaySeconds: 300, AllowExit: true}
	for i, addr := range []string{torTestAddress, "57.129.166.17", "161.97.184.199"} {
		a := tornet.Authority{
			Nickname: fmt.Sprintf("OramaAuth%d", i+1), Address: addr, ORPort: constants.GlobalTorORPort, DirPort: constants.GlobalTorDirPort,
			V3Ident: fmt.Sprintf("%040X", 0xA0+i), Fingerprint: fmt.Sprintf("%040X", 0xB0+i),
			Ed25519ID: base64.RawStdEncoding.EncodeToString(append(make([]byte, 31), byte(i+1))),
		}
		if i == 0 {
			a.Fingerprint = fp
		}
		tf.network.Authorities = append(tf.network.Authorities, a)
	}
	tf.writeNetwork(t, tf.network)
	tf.host.InstallTor = func() error { tf.torInstalled++; return nil }
	return tf
}

func (tf *torFixture) writeNetwork(t *testing.T, n tornet.Network) {
	t.Helper()
	body, err := n.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tf.staged, constants.TorNetworkFile), body, 0o600); err != nil {
		t.Fatal(err)
	}
}

// bundle writes an authority key bundle (deploy/<nickname>/keys) for authority 0.
func (tf *torFixture) bundle(t *testing.T, identity []byte, v3 string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "OramaAuth1")
	keys := filepath.Join(dir, "keys")
	if err := os.MkdirAll(keys, 0o700); err != nil {
		t.Fatal(err)
	}
	cert := fmt.Sprintf("dir-key-certificate-version 3\nfingerprint %s\ndir-key-expires 2099-10-08 00:00:00\n", v3)
	files := map[string][]byte{
		tornet.KeyAuthoritySigning: []byte("signing key"), tornet.KeyAuthorityCert: []byte(cert),
		tornet.KeyRelayIdentity: identity, tornet.KeyEd25519Master: []byte("master secret"), tornet.KeyEd25519MasterPub: ed25519PublicFile(1),
		"ed25519_signing_cert": []byte("signing cert made by the ceremony"),
	}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(keys, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func (tf *torFixture) relayOptions(extra func(*TorOptions)) GlobalInstallOptions {
	o := tf.options(GlobalServiceRelay)
	o.Tor = TorOptions{Address: "57.129.166.18", Contact: torTestContact, NodeID: "node-1"}
	if extra != nil {
		extra(&o.Tor)
	}
	return o
}

func (tf *torFixture) state(parts ...string) string {
	return filepath.Join(append([]string{tf.host.StateDir}, parts...)...)
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestInstallGlobal_relayWritesItsTorrcAndNeedsNoChain(t *testing.T) {
	tf := newTorFixture(t)
	if err := InstallGlobal(tf.relayOptions(nil), tf.host); err != nil {
		t.Fatal(err)
	}
	if tf.torInstalled != 1 {
		t.Errorf("the Tor package was installed %d times", tf.torInstalled)
	}
	torrc := readFile(t, tf.state("tor-relay.torrc"))
	for _, want := range []string{
		"Nickname " + tornet.NicknameFor("node-1"), "ContactInfo " + torTestContact, "Address 57.129.166.18",
		"ORPort 31020 IPv4Only", "ExitRelay 0", "ExitPolicy reject *:*", "DataDirectory " + constants.GlobalTorRelayHome,
	} {
		if !strings.Contains(torrc, want+"\n") {
			t.Errorf("torrc lacks %q:\n%s", want, torrc)
		}
	}
	if readFile(t, tf.state(constants.GlobalTorAuthoritiesFile)) != readFile(t, filepath.Join(tf.staged, constants.TorNetworkFile)) {
		t.Error("the network file was not kept in the state directory")
	}
	info, err := os.Stat(tf.state("tor-relay"))
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Errorf("DataDirectory = %v %v, want 0700", info, err)
	}
	// The torrc is root's, beside the DataDirectory and not in it: the Tor
	// account owns its DataDirectory and could rewrite a file there.
	info, _ = os.Stat(tf.state("tor-relay.torrc"))
	if info.Mode().Perm() != 0o644 {
		t.Errorf("torrc mode = %v", info.Mode())
	}
	if _, err := os.Stat(tf.state("tor-relay", "torrc")); !os.IsNotExist(err) {
		t.Error("a torrc was written inside the Tor account's DataDirectory")
	}
	if !slices.Contains(tf.chowns, chownCall{tf.state("tor-relay"), 990, 991}) {
		t.Error("the DataDirectory was not given to the Tor account")
	}
	if slices.ContainsFunc(tf.chowns, func(c chownCall) bool { return c.path == tf.state("tor-relay.torrc") }) {
		t.Error("the torrc was given to the Tor account")
	}
	unit := readFile(t, filepath.Join(tf.host.UnitDir, constants.GlobalTorRelayUnit))
	if want := "ExecStart=/usr/bin/tor -f " + constants.GlobalTorRelayHome + ".torrc\n"; !strings.Contains(unit, want) {
		t.Errorf("the unit does not run the root-owned torrc:\n%s", unit)
	}
	if !tf.node.users["orama-tor-relay"] || tf.node.users["debian-tor"] {
		t.Errorf("accounts = %v: a relay runs as an account of its own, not the Tor package's", tf.node.users)
	}
	if got := tf.node.named("systemctl"); !slices.Equal(got, []string{"daemon-reload", "enable " + constants.GlobalTorRelayUnit}) {
		t.Errorf("systemctl = %v", got)
	}
	if got := tf.node.named("ufw"); !slices.Equal(got, []string{"status", "allow 31020/tcp comment orama-global"}) {
		t.Errorf("ufw = %v, want only the ORPort", got)
	}
	if _, err := os.Stat(filepath.Join(tf.host.BinDir, "oramad")); !os.IsNotExist(err) {
		t.Error("a relay-only host got the chain binary")
	}
}

func TestInstallGlobal_relayIsIdempotent(t *testing.T) {
	tf := newTorFixture(t)
	opts := tf.relayOptions(nil)
	if err := InstallGlobal(opts, tf.host); err != nil {
		t.Fatal(err)
	}
	first := readFile(t, tf.state("tor-relay.torrc"))
	if err := InstallGlobal(opts, tf.host); err != nil {
		t.Fatal(err)
	}
	if readFile(t, tf.state("tor-relay.torrc")) != first {
		t.Error("a second install changed the torrc")
	}
}

func TestInstallGlobal_exitOptInWithRejectListAndFamily(t *testing.T) {
	tf := newTorFixture(t)
	if err := os.MkdirAll(tf.host.StateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tf.state(constants.GlobalTorExitRejectFile), []byte("# abuse desk\n203.0.113.9\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	family := fmt.Sprintf("%040X", 9)
	opts := tf.relayOptions(func(o *TorOptions) { o.Exit, o.Family, o.BandwidthMbit = true, []string{family}, 200 })
	if err := InstallGlobal(opts, tf.host); err != nil {
		t.Fatal(err)
	}
	torrc := readFile(t, tf.state("tor-relay.torrc"))
	for _, want := range []string{"ExitRelay 1", "IPv6Exit 0", "ExitPolicy reject 203.0.113.9:*", "ExitPolicy reject *:25", "ExitPolicy accept *:*", "MyFamily $" + family, "RelayBandwidthRate 200 Mbits"} {
		if !strings.Contains(torrc, want+"\n") {
			t.Errorf("torrc lacks %q", want)
		}
	}
	// Re-installed as a plain relay, the same host stops exiting.
	if err := InstallGlobal(tf.relayOptions(nil), tf.host); err != nil {
		t.Fatal(err)
	}
	plain := readFile(t, tf.state("tor-relay.torrc"))
	if strings.Contains(plain, "ExitRelay 1") || strings.Contains(plain, "ExitPolicy accept") || strings.Contains(plain, "203.0.113.9") {
		t.Errorf("a relay re-installed without exit still exits:\n%s", plain)
	}
}

func TestInstallGlobal_exitRefusedByANetworkThatDoesNotAllowIt(t *testing.T) {
	tf := newTorFixture(t)
	closed := tf.network
	closed.AllowExit = false
	tf.writeNetwork(t, closed)
	err := InstallGlobal(tf.relayOptions(func(o *TorOptions) { o.Exit = true }), tf.host)
	if err == nil || !strings.Contains(err.Error(), "allow_exit") {
		t.Fatalf("err = %v", err)
	}
	assertNothingChanged(t, tf)
}

// assertNothingChanged: a refusal reached before the host was touched.
func assertNothingChanged(t *testing.T, tf *torFixture) {
	t.Helper()
	if tf.torInstalled != 0 {
		t.Error("the Tor package was installed before the install was refused")
	}
	if len(tf.node.calls) > 1 {
		t.Errorf("host commands ran before the refusal: %v", tf.node.calls)
	}
	if _, err := os.Stat(tf.host.UnitDir + "/" + constants.GlobalTorRelayUnit); !os.IsNotExist(err) {
		t.Error("a unit was written")
	}
	if _, err := os.Stat(tf.state("tor-relay")); !os.IsNotExist(err) {
		t.Error("a DataDirectory was made")
	}
}

func TestInstallGlobal_badRejectListRefusedBeforeAnyChange(t *testing.T) {
	tf := newTorFixture(t)
	if err := os.MkdirAll(tf.host.StateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tf.state(constants.GlobalTorExitRejectFile), []byte("ExitRelay 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := InstallGlobal(tf.relayOptions(func(o *TorOptions) { o.Exit = true }), tf.host)
	if err == nil || !strings.Contains(err.Error(), constants.GlobalTorExitRejectFile) {
		t.Fatalf("err = %v", err)
	}
	assertNothingChanged(t, tf)
}

func TestInstallGlobal_missingOrBadNetworkFileNamedBeforeAnyChange(t *testing.T) {
	tf := newTorFixture(t)
	if err := os.Remove(filepath.Join(tf.staged, constants.TorNetworkFile)); err != nil {
		t.Fatal(err)
	}
	err := InstallGlobal(tf.relayOptions(nil), tf.host)
	if err == nil || !strings.Contains(err.Error(), constants.TorNetworkFile) {
		t.Fatalf("err = %v", err)
	}
	assertNothingChanged(t, tf)
	if err := os.WriteFile(filepath.Join(tf.staged, constants.TorNetworkFile), []byte(`{"name":"x"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := InstallGlobal(tf.relayOptions(nil), tf.host); err == nil {
		t.Fatal("a network with no authorities was installed")
	}
	assertNothingChanged(t, tf)
}

func TestInstallGlobal_directoryAuthorityInstallsItsKeysAndPorts(t *testing.T) {
	tf := newTorFixture(t)
	opts := tf.options(GlobalServiceDirauth)
	opts.Tor = TorOptions{Address: torTestAddress, Contact: torTestContact, DirauthKeysDir: tf.bundle(t, tf.identityPEM, tf.network.Authorities[0].V3Ident)}
	if err := InstallGlobal(opts, tf.host); err != nil {
		t.Fatal(err)
	}
	torrc := readFile(t, tf.state("tor-dirauth.torrc"))
	for _, want := range []string{"Nickname OramaAuth1", "AuthoritativeDirectory 1", "V3AuthoritativeDirectory 1", "DirPort 31021", "ExitPolicy reject *:*", "V3AuthVotingInterval 30 minutes"} {
		if !strings.Contains(torrc, want+"\n") {
			t.Errorf("torrc lacks %q:\n%s", want, torrc)
		}
	}
	for _, k := range []string{tornet.KeyAuthoritySigning, tornet.KeyAuthorityCert, tornet.KeyRelayIdentity, tornet.KeyEd25519Master} {
		info, err := os.Stat(tf.state("tor-dirauth", "keys", k))
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Errorf("key %s: %v %v", k, info, err)
		}
		if !slices.Contains(tf.chowns, chownCall{tf.state("tor-dirauth", "keys", k), 990, 991}) {
			t.Errorf("key %s was not given to the Tor account", k)
		}
	}
	if _, err := os.Stat(tf.state("tor-dirauth", "keys", tornet.KeyAuthorityIdentity)); !os.IsNotExist(err) {
		t.Error("the offline identity key reached the host")
	}
	if got := tf.node.named("ufw"); !slices.Equal(got, []string{"status", "allow 31020/tcp comment orama-global", "allow 31021/tcp comment orama-global"}) {
		t.Errorf("ufw = %v", got)
	}
	wantUnits := []string{"daemon-reload", "enable " + constants.GlobalTorDirauthUnit, "enable " + constants.GlobalTorArchiveTimer}
	if got := tf.node.named("systemctl"); !slices.Equal(got, wantUnits) {
		t.Errorf("systemctl = %v, want %v", got, wantUnits)
	}
	if _, err := os.Stat(filepath.Join(tf.host.UnitDir, constants.GlobalTorArchiveUnit)); err != nil {
		t.Errorf("the archive oneshot was not written: %v", err)
	}
	if _, err := os.Stat(filepath.Join(tf.host.BinDir, "orama")); err != nil {
		t.Errorf("the archive needs the orama CLI: %v", err)
	}
}

func TestInstallGlobal_anotherAuthoritysBundleIsRefused(t *testing.T) {
	tf := newTorFixture(t)
	otherKey, _ := newRSAIdentity(t)
	cases := map[string]struct {
		identity []byte
		v3       string
		address  string
		want     string
	}{
		"relay identity of another authority": {otherKey, tf.network.Authorities[0].V3Ident, torTestAddress, "another authority's bundle"},
		"certificate of another authority":    {tf.identityPEM, fmt.Sprintf("%040X", 0xEE), torTestAddress, "published as"},
		"address that is not an authority":    {tf.identityPEM, tf.network.Authorities[0].V3Ident, "57.129.166.99", "not a directory authority"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			opts := tf.options(GlobalServiceDirauth)
			opts.Tor = TorOptions{Address: c.address, Contact: torTestContact, DirauthKeysDir: tf.bundle(t, c.identity, c.v3)}
			err := InstallGlobal(opts, tf.host)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want it to say %q", err, c.want)
			}
			if tf.torInstalled != 0 {
				t.Error("the host was changed before the bundle was checked")
			}
		})
	}
}

func TestInstallGlobal_missingKeyInTheBundleIsNamed(t *testing.T) {
	tf := newTorFixture(t)
	dir := tf.bundle(t, tf.identityPEM, tf.network.Authorities[0].V3Ident)
	if err := os.Remove(filepath.Join(dir, "keys", tornet.KeyAuthoritySigning)); err != nil {
		t.Fatal(err)
	}
	opts := tf.options(GlobalServiceDirauth)
	opts.Tor = TorOptions{Address: torTestAddress, Contact: torTestContact, DirauthKeysDir: dir}
	err := InstallGlobal(opts, tf.host)
	if err == nil || !strings.Contains(err.Error(), tornet.KeyAuthoritySigning) {
		t.Fatalf("err = %v", err)
	}
}

// An authority's relay identity is its name in every other node's DirAuthority
// line; a second install with a different one would orphan it.
func TestInstallGlobal_installedIdentityKeyIsNeverReplaced(t *testing.T) {
	tf := newTorFixture(t)
	opts := tf.options(GlobalServiceDirauth)
	opts.Tor = TorOptions{Address: torTestAddress, Contact: torTestContact, DirauthKeysDir: tf.bundle(t, tf.identityPEM, tf.network.Authorities[0].V3Ident)}
	if err := InstallGlobal(opts, tf.host); err != nil {
		t.Fatal(err)
	}
	if err := InstallGlobal(opts, tf.host); err != nil {
		t.Fatalf("the same bundle again: %v", err)
	}
	// A rotated signing certificate replaces the old one.
	rotated := tf.bundle(t, tf.identityPEM, tf.network.Authorities[0].V3Ident)
	if err := os.WriteFile(filepath.Join(rotated, "keys", tornet.KeyAuthoritySigning), []byte("new signing key"), 0o600); err != nil {
		t.Fatal(err)
	}
	opts.Tor.DirauthKeysDir = rotated
	if err := InstallGlobal(opts, tf.host); err != nil {
		t.Fatalf("a rotated signing key: %v", err)
	}
	if readFile(t, tf.state("tor-dirauth", "keys", tornet.KeyAuthoritySigning)) != "new signing key" {
		t.Error("the signing key was not rotated")
	}
	// Another relay identity under the same fingerprint check cannot exist, so
	// swap the master secret, which does not change the fingerprint.
	swapped := tf.bundle(t, tf.identityPEM, tf.network.Authorities[0].V3Ident)
	if err := os.WriteFile(filepath.Join(swapped, "keys", tornet.KeyEd25519Master), []byte("another master"), 0o600); err != nil {
		t.Fatal(err)
	}
	opts.Tor.DirauthKeysDir = swapped
	err := InstallGlobal(opts, tf.host)
	if err == nil || !strings.Contains(err.Error(), "new identity") {
		t.Fatalf("err = %v", err)
	}
	if readFile(t, tf.state("tor-dirauth", "keys", tornet.KeyEd25519Master)) != "master secret" {
		t.Error("the installed identity key was replaced")
	}
}

func TestInstallGlobal_onionServiceNeedsTheChainInstalledHereOrInTheInstall(t *testing.T) {
	tf := newTorFixture(t)
	err := InstallGlobal(tf.options(GlobalServiceOnion), tf.host)
	if err == nil || !strings.Contains(err.Error(), "add chain to --services") {
		t.Fatalf("an onion service on a machine with no chain: %v", err)
	}
	if tf.torInstalled != 0 {
		t.Error("the Tor package was installed before the install was refused")
	}
	// With the chain installed already, the onion service joins it without
	// naming the chain again (which would rewrite the chain unit).
	if err := InstallGlobal(tf.options(GlobalServiceChain), tf.host); err != nil {
		t.Fatal(err)
	}
	if err := InstallGlobal(tf.options(GlobalServiceOnion), tf.host); err != nil {
		t.Fatalf("an onion service beside an installed chain: %v", err)
	}
	if _, err := os.Stat(tf.state("tor-onion.torrc")); err != nil {
		t.Errorf("the onion service was not configured: %v", err)
	}
}

func TestInstallGlobal_onionServiceWritesTheGateAndOpensNoPort(t *testing.T) {
	tf := newTorFixture(t)
	if err := InstallGlobal(tf.options(GlobalServiceChain, GlobalServiceOnion), tf.host); err != nil {
		t.Fatal(err)
	}
	torrc := readFile(t, tf.state("tor-onion.torrc"))
	for _, want := range []string{"HiddenServicePort 80 127.0.0.1:31022", "ORPort 0", "SocksPort 0"} {
		if !strings.Contains(torrc, want+"\n") {
			t.Errorf("torrc lacks %q:\n%s", want, torrc)
		}
	}
	if !tf.node.users["orama-txgate"] || !tf.node.users["orama-tor-onion"] || tf.node.users["debian-tor"] {
		t.Errorf("accounts = %v", tf.node.users)
	}
	gate := readFile(t, filepath.Join(tf.host.UnitDir, constants.GlobalTxGateUnit))
	if gate != RenderGlobalTxGateUnit() {
		t.Error("the gate unit is not the global one")
	}
	enabled := tf.node.named("systemctl")
	for _, u := range []string{constants.GlobalTorOnionUnit, constants.GlobalTxGateUnit} {
		if !slices.Contains(enabled, "enable "+u) {
			t.Errorf("%s was not enabled: %v", u, enabled)
		}
	}
	if got := tf.node.named("ufw"); slices.ContainsFunc(got, func(s string) bool { return strings.Contains(s, "3102") }) {
		t.Errorf("the onion service opened a port: %v", got)
	}
}

func TestInstallGlobal_colocatedGateReachesTheChainOnTheNamespaceAddress(t *testing.T) {
	opts := GlobalInstallOptions{Services: []GlobalService{GlobalServiceChain, GlobalServiceOnion}, Colocated: true}
	files, err := opts.unitFilesFor(GlobalServiceOnion)
	if err != nil {
		t.Fatal(err)
	}
	var gate, onion string
	for _, f := range files {
		switch f.name {
		case constants.GlobalTxGateUnit:
			gate = f.body
		case constants.GlobalTorOnionUnit:
			onion = f.body
		}
	}
	if !strings.Contains(gate, "--upstream "+constants.ColocatedChainAPIURL()) || strings.Contains(gate, constants.LocalChainAPIURL()) {
		t.Errorf("the gate does not use the namespace address:\n%s", gate)
	}
	for name, body := range map[string]string{"gate": gate, "onion": onion} {
		if !strings.Contains(body, "NetworkNamespacePath") && !strings.Contains(body, "netns") {
			t.Errorf("the %s unit does not join the namespace:\n%s", name, body)
		}
	}
}

func TestTorOptionsValidate(t *testing.T) {
	good := TorOptions{Address: torTestAddress, Contact: "c", NodeID: "n"}
	cases := map[string]struct {
		services []GlobalService
		opts     TorOptions
		ok       bool
	}{
		"relay":                  {[]GlobalService{GlobalServiceRelay}, good, true},
		"relay without node id":  {[]GlobalService{GlobalServiceRelay}, TorOptions{Address: torTestAddress, Contact: "c"}, false},
		"relay without address":  {[]GlobalService{GlobalServiceRelay}, TorOptions{Contact: "c", NodeID: "n"}, false},
		"relay without contact":  {[]GlobalService{GlobalServiceRelay}, TorOptions{Address: torTestAddress, NodeID: "n"}, false},
		"dirauth without keys":   {[]GlobalService{GlobalServiceDirauth}, TorOptions{Address: torTestAddress, Contact: "c"}, false},
		"dirauth with keys":      {[]GlobalService{GlobalServiceDirauth}, TorOptions{Address: torTestAddress, Contact: "c", DirauthKeysDir: "/k"}, true},
		"keys on a relay":        {[]GlobalService{GlobalServiceRelay}, TorOptions{Address: torTestAddress, Contact: "c", NodeID: "n", DirauthKeysDir: "/k"}, false},
		"exit without relay":     {[]GlobalService{GlobalServiceDirauth}, TorOptions{Exit: true, Address: torTestAddress, Contact: "c", DirauthKeysDir: "/k"}, false},
		"tor options on a chain": {[]GlobalService{GlobalServiceChain}, TorOptions{Address: torTestAddress}, false},
		"tor options on onion":   {[]GlobalService{GlobalServiceChain, GlobalServiceOnion}, TorOptions{Address: torTestAddress}, false},
		"onion alone":            {[]GlobalService{GlobalServiceChain, GlobalServiceOnion}, TorOptions{}, true},
		"chain alone":            {[]GlobalService{GlobalServiceChain}, TorOptions{}, true},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			err := c.opts.validate(c.services)
			if (err == nil) != c.ok {
				t.Fatalf("validate = %v, want ok=%v", err, c.ok)
			}
		})
	}
}

func TestRenderGlobalTorUnits(t *testing.T) {
	for name, unit := range map[string]string{"relay": RenderGlobalTorRelayUnit(), "dirauth": RenderGlobalTorDirauthUnit(), "onion": RenderGlobalTorOnionUnit()} {
		want := map[string]string{"relay": "orama-tor-relay", "dirauth": "orama-tor-dirauth", "onion": "orama-tor-onion"}[name]
		if got := mustDirective(t, unit, "User"); got != want {
			t.Errorf("%s runs as %s, want %s", name, got, want)
		}
		if !strings.Contains(unit, "IPAddressDeny=198.18.0.0/15\nIPAddressAllow=localhost\n") {
			t.Errorf("%s may reach the co-located namespace's address range:\n%s", name, unit)
		}
		if mustDirective(t, unit, "SystemCallErrorNumber") != "EPERM" {
			t.Errorf("%s: a refused syscall kills tor", name)
		}
		if !strings.Contains(unit, "AF_NETLINK") {
			t.Errorf("%s: tor cannot list its interfaces", name)
		}
	}
	for name, home := range map[string]string{
		"orama-global/tor-relay": constants.GlobalTorRelayHome, "orama-global/tor-dirauth": constants.GlobalTorDirauthHome, "orama-global/tor-onion": constants.GlobalTorOnionHome,
	} {
		if !strings.HasSuffix(home, strings.TrimPrefix(name, "orama-global")) {
			t.Errorf("%s does not match %s", name, home)
		}
	}
	onion := RenderGlobalTorOnionUnit()
	if got := mustDirective(t, onion, "After"); !strings.Contains(got, constants.GlobalTxGateUnit) {
		t.Errorf("the onion unit does not start after its gate: %s", got)
	}
	gate := RenderGlobalTxGateUnit()
	if got := mustDirective(t, gate, "ExecStart"); !strings.HasSuffix(got, "global txgate --listen 127.0.0.1:31022 --upstream http://127.0.0.1:31003") {
		t.Errorf("gate ExecStart = %s", got)
	}
	if mustDirective(t, gate, "User") != globalTxGateUser {
		t.Error("the gate does not run as its own account")
	}
	if !strings.Contains(mustDirective(t, gate, "After"), constants.ChainServiceUnit) {
		t.Error("the gate does not start after the chain")
	}
	archive := mustDirective(t, RenderGlobalTorArchiveUnit(), "ExecStart")
	if !strings.Contains(archive, "global tor archive --data-dir "+constants.GlobalTorDirauthHome+" --archive-dir "+constants.GlobalTorDirauthHome+"/archive") {
		t.Errorf("archive ExecStart = %s", archive)
	}
	if !strings.Contains(RenderGlobalTorArchiveTimer(), "Unit="+constants.GlobalTorArchiveUnit) {
		t.Error("the timer fires another unit")
	}
	if !strings.Contains(RenderGlobalTorArchiveTimer(), "OnUnitActiveSec=1min") {
		t.Error("the archive looks less often than the shortest voting interval allows a period to pass")
	}
	if got := mustDirective(t, RenderGlobalTorArchiveUnit(), "User"); got != globalTorDirauthUser {
		t.Errorf("the archive runs as %s, not as the authority's account", got)
	}
}

// The namespace rulesets describe every service of the machine: adding a relay
// to a machine that already runs the chain must not unpublish the chain's ports.
func TestInstallGlobal_colocatedRelayKeepsThePortsOfWhatIsAlreadyInstalled(t *testing.T) {
	cf := newColocatedFixture(t)
	tf := addTorNetwork(t, cf.globalFixture)
	if err := InstallGlobal(cf.options(GlobalServiceChain, GlobalServiceProvider), cf.host); err != nil {
		t.Fatal(err)
	}
	opts := tf.relayOptions(nil)
	opts.Colocated = true
	if err := InstallGlobal(opts, cf.host); err != nil {
		t.Fatal(err)
	}
	rules, err := os.ReadFile(filepath.Join(cf.host.Netns.ConfigDir, "netns-host.nft"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(rules), "tcp dport { 31000, 31013, 31020 } dnat to 198.18.0.2") {
		t.Errorf("the host rules lost the chain or provider port, or lack the ORPort:\n%s", rules)
	}
	relay, err := os.ReadFile(filepath.Join(cf.host.UnitDir, constants.GlobalTorRelayUnit))
	if err != nil || !strings.Contains(string(relay), "NetworkNamespacePath=/run/netns/orama-global") {
		t.Errorf("the relay does not run in the namespace: %v\n%s", err, relay)
	}
}

// ed25519PublicFile is a tor ed25519 public key file: the 32-byte header and a
// 32-byte key whose last byte is last (the key the test network publishes for
// authority number last).
func ed25519PublicFile(last byte) []byte {
	header := append([]byte("== ed25519v1-public: type0 =="), 0, 0, 0)
	return append(header, append(make([]byte, 31), last)...)
}

// A bundle made for another authority is refused on its ed25519 identity too:
// the other authorities would refuse the node, not the install.
func TestInstallGlobal_aBundleWithAnotherEd25519IdentityIsRefused(t *testing.T) {
	tf := newTorFixture(t)
	dir := tf.bundle(t, tf.identityPEM, tf.network.Authorities[0].V3Ident)
	if err := os.WriteFile(filepath.Join(dir, "keys", tornet.KeyEd25519MasterPub), ed25519PublicFile(9), 0o600); err != nil {
		t.Fatal(err)
	}
	opts := tf.options(GlobalServiceDirauth)
	opts.Tor = TorOptions{Address: torTestAddress, Contact: torTestContact, DirauthKeysDir: dir}
	err := InstallGlobal(opts, tf.host)
	if err == nil || !strings.Contains(err.Error(), "ed25519 identity") {
		t.Fatalf("err = %v", err)
	}
	if tf.torInstalled != 0 {
		t.Error("the host was changed before the bundle was checked")
	}
	if err := os.WriteFile(filepath.Join(dir, "keys", tornet.KeyEd25519MasterPub), []byte("not a key file"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := InstallGlobal(opts, tf.host); err == nil {
		t.Fatal("a key file that is not a tor ed25519 public key was accepted")
	}
}

// tor rotates its signing key, its signing certificate and its onion keys
// itself. A re-install must not put the ceremony-time copies back over them.
func TestInstallGlobal_keysTorRotatesAreNotPutBack(t *testing.T) {
	tf := newTorFixture(t)
	opts := tf.options(GlobalServiceDirauth)
	opts.Tor = TorOptions{Address: torTestAddress, Contact: torTestContact, DirauthKeysDir: tf.bundle(t, tf.identityPEM, tf.network.Authorities[0].V3Ident)}
	if err := InstallGlobal(opts, tf.host); err != nil {
		t.Fatal(err)
	}
	rotated := tf.state("tor-dirauth", "keys", "ed25519_signing_cert")
	if readFile(t, rotated) != "signing cert made by the ceremony" {
		t.Fatalf("the optional key was not installed the first time: %q", readFile(t, rotated))
	}
	if err := os.WriteFile(rotated, []byte("renewed by tor"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := InstallGlobal(opts, tf.host); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, rotated); got != "renewed by tor" {
		t.Errorf("a re-install put the ceremony's signing certificate back: %q", got)
	}
	// The authority's signing key and certificate, by contrast, are the
	// operator's to replace (a renewal ships a new bundle).
	cert := tf.state("tor-dirauth", "keys", tornet.KeyAuthorityCert)
	if err := os.WriteFile(cert, []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := InstallGlobal(opts, tf.host); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, cert); got == "stale" {
		t.Error("the authority certificate was not replaced from the bundle")
	}
}

// In the co-located namespace a relay or authority resolves through a public
// resolver, so it keeps no use for loopback, where the chain's gRPC and metrics
// listen; the onion service needs it for the gate.
func TestInstallGlobal_colocatedRelayAndAuthorityDenyLoopback(t *testing.T) {
	for _, s := range []GlobalService{GlobalServiceRelay, GlobalServiceDirauth} {
		opts := GlobalInstallOptions{Services: []GlobalService{s}, Colocated: true}
		files, err := opts.unitFilesFor(s)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(files[0].body, "IPAddressDeny=127.0.0.0/8\n") || strings.Contains(files[0].body, "IPAddressAllow=localhost") {
			t.Errorf("%s in the namespace still reaches loopback:\n%s", s, files[0].body)
		}
		plain := GlobalInstallOptions{Services: []GlobalService{s}}.unitFiles(s)
		if !strings.Contains(plain[0].body, "IPAddressAllow=localhost") {
			t.Errorf("%s on a plain host lost loopback, where its resolver stub is", s)
		}
	}
	opts := GlobalInstallOptions{Services: []GlobalService{GlobalServiceChain, GlobalServiceOnion}, Colocated: true}
	files, err := opts.unitFilesFor(GlobalServiceOnion)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(files[0].body, "IPAddressAllow=localhost") {
		t.Error("the onion service cannot reach its gate on loopback")
	}
}

// A directory authority is a relay, and both publish the ORPort: whichever role
// a host took first, the other is refused, by a later install as well.
func TestInstallGlobal_aHostIsAnAuthorityOrARelayNotBoth(t *testing.T) {
	tf := newTorFixture(t)
	dir := tf.bundle(t, tf.identityPEM, tf.network.Authorities[0].V3Ident)
	auth := tf.options(GlobalServiceDirauth)
	auth.Tor = TorOptions{Address: torTestAddress, Contact: torTestContact, DirauthKeysDir: dir}
	if err := InstallGlobal(tf.relayOptions(nil), tf.host); err != nil {
		t.Fatal(err)
	}
	if err := InstallGlobal(auth, tf.host); err == nil || !strings.Contains(err.Error(), "already runs the relay role") {
		t.Fatalf("an authority beside an installed relay: %v", err)
	}
	other := newTorFixture(t)
	dir = other.bundle(t, other.identityPEM, other.network.Authorities[0].V3Ident)
	auth = other.options(GlobalServiceDirauth)
	auth.Tor = TorOptions{Address: torTestAddress, Contact: torTestContact, DirauthKeysDir: dir}
	if err := InstallGlobal(auth, other.host); err != nil {
		t.Fatal(err)
	}
	if err := InstallGlobal(other.relayOptions(nil), other.host); err == nil || !strings.Contains(err.Error(), "already runs the dirauth role") {
		t.Fatalf("a relay beside an installed authority: %v", err)
	}
}

// A refusal on the second key must not leave the first replaced: the identity
// checks run before any write.
func TestInstallGlobal_aRefusedBundleLeavesTheInstalledKeysAsTheyWere(t *testing.T) {
	tf := newTorFixture(t)
	opts := tf.options(GlobalServiceDirauth)
	opts.Tor = TorOptions{Address: torTestAddress, Contact: torTestContact, DirauthKeysDir: tf.bundle(t, tf.identityPEM, tf.network.Authorities[0].V3Ident)}
	if err := InstallGlobal(opts, tf.host); err != nil {
		t.Fatal(err)
	}
	next := tf.bundle(t, tf.identityPEM, tf.network.Authorities[0].V3Ident)
	for name, data := range map[string]string{tornet.KeyAuthoritySigning: "new signing key", tornet.KeyEd25519Master: "another master"} {
		if err := os.WriteFile(filepath.Join(next, "keys", name), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	opts.Tor.DirauthKeysDir = next
	if err := InstallGlobal(opts, tf.host); err == nil {
		t.Fatal("a bundle with another master key was installed")
	}
	if got := readFile(t, tf.state("tor-dirauth", "keys", tornet.KeyAuthoritySigning)); got != "signing key" {
		t.Errorf("the signing key was replaced before the identity check refused: %q", got)
	}
}

func TestInstallGlobal_anExpiredAuthorityCertificateIsRefused(t *testing.T) {
	tf := newTorFixture(t)
	dir := tf.bundle(t, tf.identityPEM, tf.network.Authorities[0].V3Ident)
	expired := fmt.Sprintf("dir-key-certificate-version 3\nfingerprint %s\ndir-key-expires 2020-01-01 00:00:00\n", tf.network.Authorities[0].V3Ident)
	if err := os.WriteFile(filepath.Join(dir, "keys", tornet.KeyAuthorityCert), []byte(expired), 0o600); err != nil {
		t.Fatal(err)
	}
	opts := tf.options(GlobalServiceDirauth)
	opts.Tor = TorOptions{Address: torTestAddress, Contact: torTestContact, DirauthKeysDir: dir}
	err := InstallGlobal(opts, tf.host)
	if err == nil || !strings.Contains(err.Error(), "expired on 2020-01-01") {
		t.Fatalf("err = %v", err)
	}
	if tf.torInstalled != 0 {
		t.Error("the host was changed before the certificate was checked")
	}
}

// The reject list matters to an exit alone: a malformed one must not stop a relay.
func TestInstallGlobal_theRejectListIsReadOnlyForAnExit(t *testing.T) {
	tf := newTorFixture(t)
	if err := os.MkdirAll(tf.host.StateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tf.state(constants.GlobalTorExitRejectFile), []byte("ExitRelay 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := InstallGlobal(tf.relayOptions(nil), tf.host); err != nil {
		t.Fatalf("a malformed reject list stopped a relay that is not an exit: %v", err)
	}
}

func TestTorOptionsValidate_nodeIDIsForRelays(t *testing.T) {
	err := TorOptions{Address: torTestAddress, Contact: "c", NodeID: "n", DirauthKeysDir: "/k"}.validate([]GlobalService{GlobalServiceDirauth})
	if err == nil || !strings.Contains(err.Error(), "--tor-node-id") {
		t.Fatalf("a node id beside an authority: %v", err)
	}
}
