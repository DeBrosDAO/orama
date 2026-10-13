package client

import (
	"strings"
	"testing"
)

func connectForTest(t *testing.T, listen []string) *Client {
	t.Helper()
	cfg := DefaultClientConfig("listen-test")
	cfg.BootstrapPeers = nil
	cfg.DatabaseEndpoints = nil
	cfg.QuietMode = true
	cfg.ListenAddrs = listen
	nc, err := NewClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	c := nc.(*Client)
	t.Cleanup(func() { _ = c.Disconnect() })
	if err := c.Connect(); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	return c
}

// Nothing connects to a client, so by default its host has no listener. It
// used to listen on 0.0.0.0 — every interface of the gateway's machine, the
// public one included.
func TestConnect_listensNowhereByDefault(t *testing.T) {
	c := connectForTest(t, nil)
	if addrs := c.Host().Addrs(); len(addrs) != 0 {
		t.Errorf("host listens on %v, want nothing", addrs)
	}
}

// A caller that wants inbound connections names the interface, and the host
// listens there only.
func TestConnect_listensWhereConfigured(t *testing.T) {
	c := connectForTest(t, []string{"/ip4/127.0.0.1/tcp/0"})
	addrs := c.Host().Addrs()
	if len(addrs) == 0 {
		t.Fatal("host has no listen address")
	}
	for _, a := range addrs {
		if !strings.HasPrefix(a.String(), "/ip4/127.0.0.1/tcp/") {
			t.Errorf("host listens on %s, want only 127.0.0.1", a)
		}
	}
}

func TestListenOption_refusesEveryInterface(t *testing.T) {
	for _, bad := range []string{"/ip4/0.0.0.0/tcp/0", "/ip6/::/tcp/4001", "not-a-multiaddr"} {
		if _, err := listenOption([]string{bad}); err == nil {
			t.Errorf("listenOption(%q) accepted", bad)
		}
	}
	for _, good := range []string{"/ip4/10.0.0.3/tcp/0", "/ip6/::1/tcp/0"} {
		if _, err := listenOption([]string{good}); err != nil {
			t.Errorf("listenOption(%q): %v", good, err)
		}
	}
}

// An unspecified address makes Connect fail, before a host exists.
func TestConnect_refusesAnUnspecifiedListenAddress(t *testing.T) {
	cfg := DefaultClientConfig("listen-test")
	cfg.BootstrapPeers = nil
	cfg.ListenAddrs = []string{"/ip4/0.0.0.0/tcp/0"}
	nc, err := NewClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	c := nc.(*Client)
	if err := c.Connect(); err == nil {
		_ = c.Disconnect()
		t.Fatal("Connect accepted a listener on every interface")
	}
	if c.Host() != nil {
		t.Error("a host was created for a refused configuration")
	}
}

// Config() hands out a copy the caller cannot use to change the client.
func TestConfig_copiesListenAddrs(t *testing.T) {
	cfg := DefaultClientConfig("listen-test")
	cfg.ListenAddrs = []string{"/ip4/10.0.0.3/tcp/0"}
	nc, err := NewClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	c := nc.(*Client)
	snap := c.Config()
	snap.ListenAddrs[0] = "/ip4/0.0.0.0/tcp/0"
	if c.Config().ListenAddrs[0] != "/ip4/10.0.0.3/tcp/0" {
		t.Error("Config() aliases ListenAddrs")
	}
}
