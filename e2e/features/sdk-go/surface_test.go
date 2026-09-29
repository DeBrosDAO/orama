//go:build e2e_fleet

package sdkgo

import (
	"reflect"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/client"
)

// TestGoClientSurface_documentedAbsences: the Go client has no Cache()
// accessor ("Cache Client: Not yet available") and no Serverless() accessor
// ("Serverless Client: Not yet available in the SDK"), and does not expose
// the anonymity proxy ("Neither is exposed by the SDK yet")
// (docs/GO_CLIENT_SDK.md). A method appearing under one of these names
// means the doc is stale.
func TestGoClientSurface_documentedAbsences(t *testing.T) {
	t.Parallel()
	c, err := client.NewClient(client.DefaultClientConfig(appName))
	if err != nil {
		t.Fatal(err)
	}
	typ := reflect.TypeOf(c)
	for i := range typ.NumMethod() {
		name := typ.Method(i).Name
		for _, absent := range []string{"Cache", "Serverless", "Function", "Proxy", "Anon", "Tunnel"} {
			if strings.Contains(name, absent) {
				t.Errorf("the client has %s(), which docs/GO_CLIENT_SDK.md says does not exist", name)
			}
		}
	}
	for _, want := range []string{"Database", "PubSub", "Storage", "Network", "Connect", "Disconnect", "Health", "Config"} {
		if _, ok := typ.MethodByName(want); !ok {
			t.Errorf("the client lacks the documented %s()", want)
		}
	}
}

// TestGoClientConfig_documentedValidation: NewClient refuses a nil config and
// an empty app name, DefaultClientConfig derives "<AppName>_db", and Connect
// refuses a listen address that is every interface
// (docs/GO_CLIENT_SDK.md "Creating a Client", "ClientConfig").
func TestGoClientConfig_documentedValidation(t *testing.T) {
	t.Parallel()
	if _, err := client.NewClient(nil); err == nil {
		t.Error("NewClient(nil) succeeded")
	}
	if _, err := client.NewClient(client.DefaultClientConfig("")); err == nil {
		t.Error("NewClient with an empty app name succeeded")
	}
	cfg := client.DefaultClientConfig(appName)
	if cfg.DatabaseName != appName+"_db" {
		t.Errorf("DatabaseName %q, want %q", cfg.DatabaseName, appName+"_db")
	}
	for _, addr := range []string{"/ip4/0.0.0.0/tcp/0", "/ip6/::/tcp/0", "not-a-multiaddr"} {
		cfg := client.DefaultClientConfig(appName)
		cfg.QuietMode, cfg.BootstrapPeers, cfg.ListenAddrs = true, nil, []string{addr}
		c, err := client.NewClient(cfg)
		if err != nil {
			t.Fatal(err)
		}
		if err := c.Connect(); err == nil {
			t.Errorf("Connect listening on %q succeeded", addr)
			if derr := c.Disconnect(); derr != nil {
				t.Errorf("Disconnect: %v", derr)
			}
		}
	}
}
