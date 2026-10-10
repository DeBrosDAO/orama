//go:build e2e_fleet

package sdkgo

import (
	"crypto/tls"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
	"github.com/DeBrosOfficial/network/e2e/harness/ns"
	"github.com/DeBrosOfficial/network/pkg/client"
)

// Budgets for one SDK call against the fleet.
const (
	callBudget = time.Minute
	pollEvery  = 2 * time.Second
	// pinBudget: a fresh upload is pinned cluster-wide within this.
	pinBudget = 2 * time.Minute
	// failFastBudget bounds a call that must fail because its transport
	// does not exist here, rather than hang.
	failFastBudget = 30 * time.Second
	// appName names the SDK client in its logs and default database.
	appName = "e2e-sdk-go"
)

var trustOnce sync.Once

// trustRunCA makes the process's default HTTP transport trust the run's CA
// roots and nothing else. The SDK builds its HTTP clients on
// http.DefaultTransport and offers no TLS option (core/pkg/client
// storage_client.go), and the fleet's certificates come from Let's Encrypt
// staging, which no system store trusts. Every request of this package goes
// to the fleet, so replacing the roots is the pin the rest of the suite uses.
func trustRunCA(t testing.TB) {
	t.Helper()
	var err error
	trustOnce.Do(func() {
		pool, loadErr := gw.LoadCAPool(harness.Fleet(t).State.CAFile)
		if loadErr != nil {
			err = loadErr
			return
		}
		http.DefaultTransport.(*http.Transport).TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	})
	if err != nil {
		t.Fatalf("failed to load the run's CA roots: %v", err)
	}
}

// credential is how an SDK client authenticates.
type credential struct {
	jwt, apiKey string
}

// newClient is an SDK client for n's gateway with cred, configured the way
// website/src/docs/developer/go-sdk.mdx "Quick Start" does, connected, and disconnected at
// cleanup. No bootstrap peers are dialled: none is reachable from outside.
func newClient(t testing.TB, n *ns.Namespace, cred credential) client.NetworkClient {
	t.Helper()
	trustRunCA(t)
	cfg := client.DefaultClientConfig(appName)
	cfg.GatewayURL = n.URL
	cfg.JWT, cfg.APIKey = cred.jwt, cred.apiKey
	cfg.BootstrapPeers = nil
	cfg.QuietMode = true
	c, err := client.NewClient(cfg)
	if err != nil {
		t.Fatalf("client.NewClient: %v", err)
	}
	if err := c.Connect(); err != nil {
		t.Fatalf("client.Connect: %v", err)
	}
	t.Cleanup(func() {
		if err := c.Disconnect(); err != nil {
			t.Errorf("cleanup: client.Disconnect: %v", err)
		}
	})
	return c
}

func owner(n *ns.Namespace) credential { return credential{jwt: n.Owner.Token()} }
