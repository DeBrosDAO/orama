package setup

import (
	"strings"
	"testing"
)

func validOptions() Options {
	return Options{IPs: []string{"203.0.113.10"}, Name: "alice"}
}

func TestNormalize_defaults(t *testing.T) {
	o := validOptions()
	if err := o.Normalize(); err != nil {
		t.Fatal(err)
	}
	if o.User != "root" || o.StorageGB != DefaultStorageGB {
		t.Errorf("user %q, storage %d: want root and the default storage", o.User, o.StorageGB)
	}
}

func TestNormalize_refusals(t *testing.T) {
	for name, tc := range map[string]struct {
		mutate func(*Options)
		want   string
	}{
		"no ip":                    {func(o *Options) { o.IPs = nil }, "--ip"},
		"a private ip":             {func(o *Options) { o.IPs = []string{"10.0.0.5"} }, "public"},
		"an ipv6 address":          {func(o *Options) { o.IPs = []string{"2001:db8::1"} }, "IPv4"},
		"a hostname":               {func(o *Options) { o.IPs = []string{"vps.example.org"} }, "not an IP"},
		"a duplicate ip":           {func(o *Options) { o.IPs = []string{"203.0.113.10", "203.0.113.10"} }, "twice"},
		"too many machines":        {func(o *Options) { o.IPs = manyIPs(MaxNodes + 1) }, "most is"},
		"password and key":         {func(o *Options) { o.UsePassword, o.BootstrapKey = true, "/k" }, "alternatives"},
		"no name for a full node":  {func(o *Options) { o.Name = "" }, "--name"},
		"a name with a dot":        {func(o *Options) { o.Name = "a.b" }, "--name"},
		"a one-letter name":        {func(o *Options) { o.Name = "a" }, "--name"},
		"a trailing hyphen":        {func(o *Options) { o.Name = "alice-" }, "--name"},
		"a two-letter name":        {func(o *Options) { o.Name = "ab" }, "3 to 32"},
		"a reserved name":          {func(o *Options) { o.Name = "gateway" }, "reserved"},
		"a seed label":             {func(o *Options) { o.Name = "seed1" }, "--name"},
		"exit without consent":     {func(o *Options) { o.Exit, o.TorNetwork = true, "t.json" }, "exit relay"},
		"no relay with a tor file": {func(o *Options) { o.NoRelay, o.TorNetwork = true, "t.json" }, "alternatives"},
		"no relay with an exit":    {func(o *Options) { o.NoRelay, o.Exit, o.Yes, o.TorNetwork = true, true, true, "" }, "--no-relay"},
		"no relay on cluster-only": {func(o *Options) { o.ClusterOnly, o.NoRelay = true, true }, "--cluster-only"},
		"exit on cluster-only":     {func(o *Options) { o.ClusterOnly, o.Exit, o.Yes = true, true, true }, "--cluster-only"},
		"storage on cluster-only":  {func(o *Options) { o.ClusterOnly, o.StorageGB = true, 10 }, "--cluster-only"},
		"a bare host key for two": {func(o *Options) {
			o.IPs = []string{"203.0.113.10", "203.0.113.11"}
			o.HostKeys = map[string]string{"": "SHA256:abc"}
		}, "names one machine"},
		"a host key for a stranger": {func(o *Options) { o.HostKeys = map[string]string{"203.0.113.99": "SHA256:abc"} }, "not one of the machines"},
		"a domain that is an ip":    {func(o *Options) { o.Domain = "203.0.113.1" }, "--domain"},
		"a one-label domain":        {func(o *Options) { o.Domain = "cluster" }, "--domain"},
	} {
		o := validOptions()
		tc.mutate(&o)
		err := o.Normalize()
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %v, want an error containing %q", name, err, tc.want)
		}
	}
}

func manyIPs(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = "203.0.113." + itoa(i+1)
	}
	return out
}

func itoa(n int) string {
	if n < 10 {
		return string(rune('0' + n))
	}
	return itoa(n/10) + string(rune('0'+n%10))
}

func TestNormalize_exitAcceptedWithConsentAndTheTorFile(t *testing.T) {
	o := validOptions()
	o.Exit, o.ExitConfirmed, o.TorNetwork = true, true, "tor-network.json"
	if err := o.Normalize(); err != nil {
		t.Fatalf("an exit with consent and the Tor network file: %v", err)
	}
}

func TestNormalize_clusterOnlyNeedsNoName(t *testing.T) {
	o := validOptions()
	o.ClusterOnly, o.Name = true, ""
	if err := o.Normalize(); err != nil {
		t.Fatal(err)
	}
	if o.StorageGB != 0 {
		t.Errorf("a cluster-only run offers no storage, got %d", o.StorageGB)
	}
}

func TestNormalize_ipsAreCanonical(t *testing.T) {
	o := validOptions()
	o.IPs = []string{" 203.0.113.10 "}
	if err := o.Normalize(); err != nil || o.IPs[0] != "203.0.113.10" {
		t.Fatalf("got %v, %v", o.IPs, err)
	}
}

func TestNodeNames(t *testing.T) {
	got := strings.Join(NodeNames("alice", 3), ",")
	if got != "alice,alice-2,alice-3" {
		t.Fatalf("got %s", got)
	}
	if len(NodeNames("alice", 0)) != 0 {
		t.Fatal("no nodes, no names")
	}
}

func TestValidateNodeName_boundary(t *testing.T) {
	if err := ValidateNodeName(strings.Repeat("a", nodeNameMax)); err != nil {
		t.Errorf("32 characters is allowed: %v", err)
	}
	if err := ValidateNodeName(strings.Repeat("a", nodeNameMax+1)); err == nil {
		t.Error("33 characters must be refused")
	}
}

func TestNormalize_namesAreLowercased(t *testing.T) {
	o := validOptions()
	o.Name = " Alice "
	if err := o.Normalize(); err != nil || o.Name != "alice" {
		t.Fatalf("got %q, %v", o.Name, err)
	}
}

func TestParseIPList(t *testing.T) {
	got, err := ParseIPList("203.0.113.10, 203.0.113.11;203.0.113.12\n203.0.113.13\t")
	if err != nil || len(got) != 4 || got[3] != "203.0.113.13" {
		t.Fatalf("%v, %v", got, err)
	}
	if _, err := ParseIPList("   "); err == nil {
		t.Error("an empty paste names no machine")
	}
	if _, err := ParseIPList("203.0.113.10 nonsense"); err == nil {
		t.Error("a word that is not an address is refused")
	}
}

func TestNormalize_moreRefusals(t *testing.T) {
	for name, tc := range map[string]struct {
		mutate func(*Options)
		want   string
	}{
		"a user that is an ssh option":  {func(o *Options) { o.User = "-oProxyCommand=x" }, "--user"},
		"a user with a space":           {func(o *Options) { o.User = "me you" }, "--user"},
		"an environment with a slash":   {func(o *Options) { o.Env = "a/b" }, "--env"},
		"a domain with a path":          {func(o *Options) { o.Domain = "cluster.example.org/x" }, "--domain"},
		"a domain with a leading dash":  {func(o *Options) { o.Domain = "-x.example.org" }, "--domain"},
		"a storage size that overflows": {func(o *Options) { o.StorageGB = 18_000_000_000 }, "--storage-gb"},
	} {
		o := validOptions()
		tc.mutate(&o)
		err := o.Normalize()
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %v, want an error containing %q", name, err, tc.want)
		}
	}
}

func TestNormalize_acceptsWhatItShould(t *testing.T) {
	o := validOptions()
	o.User, o.Env, o.Domain, o.StorageGB = "ubuntu", "stagenet-alice", "Cluster.Example.org.", MaxStorageGB
	if err := o.Normalize(); err != nil {
		t.Fatal(err)
	}
	if o.Domain != "cluster.example.org" {
		t.Errorf("the domain is lowercased and loses its root dot: %q", o.Domain)
	}
}

func TestCommandLine_isTheCommandThatRunsTheSameSetup(t *testing.T) {
	o := Options{
		Network: "stagenet", Env: "stagenet-alice", Name: "alice", IPs: []string{"203.0.113.10", "203.0.113.11"},
		HostKeys: map[string]string{"203.0.113.11": "SHA256:bbb", "203.0.113.10": "SHA256:aaa"},
		User:     "ubuntu", BootstrapKey: "/home/me/my key", StorageGB: 80, NoValidator: true, ASN: 0, ASNSet: true,
	}
	got := o.CommandLine()
	want := "orama setup --network stagenet --env stagenet-alice --name alice --ip 203.0.113.10 --ip 203.0.113.11 " +
		"--host-key 203.0.113.10=SHA256:aaa --host-key 203.0.113.11=SHA256:bbb --user ubuntu --bootstrap-key '/home/me/my key' " +
		"--storage-gb 80 --asn 0 --no-validator --yes"
	if got != want {
		t.Errorf("got\n  %s\nwant\n  %s", got, want)
	}
}

func TestCommandLine_clusterOnlyAndSecrets(t *testing.T) {
	o := Options{IPs: []string{"203.0.113.10"}, ClusterOnly: true, Domain: "cluster.example.org", UsePassword: true, Password: "typed-secret", HostKeys: map[string]string{"": "SHA256:aaa"}, User: DefaultSSHUser}
	got := o.CommandLine()
	if strings.Contains(got, "typed-secret") || strings.Contains(got, "--password") {
		t.Errorf("a password typed for the run is never printed: %s", got)
	}
	for _, want := range []string{"--cluster-only", "--domain cluster.example.org", "--host-key SHA256:aaa", "--yes"} {
		if !strings.Contains(got, want) {
			t.Errorf("%s lacks %q", got, want)
		}
	}
	vault := Options{IPs: []string{"203.0.113.10"}, UsePassword: true, StorageGB: DefaultStorageGB}
	if got := vault.CommandLine(); !strings.Contains(got, "--password") || strings.Contains(got, "--storage-gb") {
		t.Errorf("the vault login is a switch and the default storage is not repeated: %s", got)
	}
}

func TestCommandLine_keepsTheTorChoicesOfTheRun(t *testing.T) {
	override := Options{IPs: []string{"203.0.113.10"}, Name: "alice", TorNetwork: "/etc/mine/tor-network.json", User: DefaultSSHUser, StorageGB: DefaultStorageGB}
	if got := override.CommandLine(); !strings.Contains(got, "--tor-network /etc/mine/tor-network.json") {
		t.Errorf("the override is lost: %s", got)
	}
	none := Options{IPs: []string{"203.0.113.10"}, Name: "alice", NoRelay: true, User: DefaultSSHUser, StorageGB: DefaultStorageGB}
	if got := none.CommandLine(); !strings.Contains(got, "--no-relay") {
		t.Errorf("a declined relay is lost, so the run again would install one: %s", got)
	}
}

func TestShellArg(t *testing.T) {
	for in, want := range map[string]string{"plain-1.2/x:y": "plain-1.2/x:y", "has space": "'has space'", "it's": `'it'"'"'s'`, "$(x)": "'$(x)'"} {
		if got := shellArg(in); got != want {
			t.Errorf("shellArg(%q) = %s, want %s", in, got, want)
		}
	}
}
