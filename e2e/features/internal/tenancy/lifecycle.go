//go:build e2e_fleet

package tenancy

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
	"github.com/DeBrosOfficial/network/e2e/harness/ns"
	"github.com/DeBrosOfficial/network/e2e/harness/oramacli"
)

// Namespace lifecycle routes (docs/API_SURFACE.md "Namespace management").
const (
	PathCreate = "/v1/namespaces"
	pathStatus = "/v1/namespace/status"
	PathDelete = "/v1/namespace/delete"
	PathList   = "/v1/namespace/list"
	pathQuery  = "/v1/rqlite/query"
	pathHealth = "/health"
	// Creation modes (docs/CLI_REFERENCE.md "orama maint cluster settings set").
	modeOpen      = "open"
	modeAllowlist = "allowlist"
)

// Created is POST /v1/namespaces's answer.
type Created struct {
	Name      string `json:"name"`
	Owner     string `json:"owner"`
	Status    string `json:"status"`
	ClusterID string `json:"cluster_id"`
	PollURL   string `json:"poll_url"`
	Cluster   string `json:"cluster"`
}

// Creator is a fresh wallet in the lobby that the cluster lets create a
// namespace: added to the creator allowlist (and removed at cleanup) when the
// cluster's mode is allowlist.
func Creator(t testing.TB, f *fleet.Fleet) *gw.User {
	t.Helper()
	u := gw.NewUser(t, f, gw.LobbyNamespace)
	cli := oramacli.ForState(f.State, f.Recorder()).For(t)
	out := cli.MustOK(t, "maint", "cluster", "settings", "show").Stdout
	switch mode := settingValue(out, "namespace-creation"); mode {
	case modeOpen:
	case modeAllowlist:
		addr := u.Wallet.Address()
		cli.MustOK(t, "maint", "cluster", "creators", "add", addr)
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), cleanupBudget)
			defer cancel()
			if res, err := cli.Run(ctx, "maint", "cluster", "creators", "remove", addr); err != nil || res.Exit != 0 {
				t.Errorf("cleanup: failed to remove creator %s: %v %s", addr, err, res.Stderr)
			}
		})
	default:
		t.Fatalf("namespace creation is %q: a user cannot create a namespace on this cluster", mode)
	}
	return u
}

func settingValue(out, key string) string {
	for _, line := range strings.Split(out, "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), key+":"); ok {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// Create posts a namespace creation as owner and returns the raw answer.
func Create(t testing.TB, owner *gw.User, name string) *gw.Response {
	t.Helper()
	return Post(t, owner.Client, PathCreate, Cred{Bearer: owner.Token()}, map[string]string{"name": name})
}

// Adopt takes a namespace the test created with Create: it registers its
// deletion, waits until it really serves, and returns it signed in as owner.
func Adopt(t testing.TB, f *fleet.Fleet, owner *gw.User, c Created) *ns.Namespace {
	t.Helper()
	n := &ns.Namespace{Name: c.Name, ClusterID: c.ClusterID, URL: gw.NamespaceURL(f.State, c.Name)}
	n.Client = owner.Client.WithBase(n.URL)
	n.Owner = &gw.User{Wallet: owner.Wallet, Namespace: c.Name, Client: owner.Client}
	t.Cleanup(func() {
		if err := DeleteIfPresent(n); err != nil {
			t.Errorf("cleanup: namespace %s: %v", n.Name, err)
		}
	})
	eventually.Require(t, ns.PollInterval, ns.ReadyBudget, "namespace "+c.Name+" to serve", func() (bool, error) {
		return ready(t.Context(), n)
	})
	return n
}

func ready(ctx context.Context, n *ns.Namespace) (bool, error) {
	st, err := Status(ctx, n.Owner.Client, n.ClusterID)
	if err != nil {
		return false, err
	}
	if st.Status == ns.StatusFailed {
		return false, eventually.Stop(fmt.Errorf("provisioning failed: %s", st.Error))
	}
	if st.Status != ns.StatusReady {
		return false, fmt.Errorf("status=%s", st.Status)
	}
	if n.Owner.Session == nil {
		s, err := n.Owner.Client.SignIn(ctx, n.Owner.Wallet, n.Name, nil)
		if err != nil {
			return false, fmt.Errorf("ready but sign-in fails: %w", err)
		}
		n.Owner.Session = s
	}
	if _, err := n.Client.JSON(ctx, http.MethodPost, pathQuery, n.Owner.Token(), map[string]any{"sql": "SELECT 1"}, nil); err != nil {
		return false, fmt.Errorf("ready but the namespace gateway does not serve: %w", err)
	}
	return true, nil
}

// Status reads the provisioning status of clusterID (open route).
func Status(ctx context.Context, c *gw.Client, clusterID string) (*ns.ClusterStatus, error) {
	var st ns.ClusterStatus
	_, err := c.JSON(ctx, http.MethodGet, pathStatus+"?id="+url.QueryEscape(clusterID), "", nil, &st)
	if err != nil {
		return nil, err
	}
	return &st, nil
}

// Gone reports whether the status route no longer knows clusterID and the
// namespace gateway no longer serves /health.
func Gone(ctx context.Context, n *ns.Namespace) (bool, error) {
	_, err := Status(ctx, n.Owner.Client, n.ClusterID)
	var se *gw.StatusError
	if !errors.As(err, &se) || se.Status != http.StatusNotFound {
		return false, fmt.Errorf("status route still answers for %s: %v", n.ClusterID, err)
	}
	if resp, err := n.Client.Send(ctx, gw.Req{Path: pathHealth}); err == nil && resp.Status == http.StatusOK {
		return false, errors.New("the namespace gateway still answers /health")
	}
	return true, nil
}

// DeleteIfPresent deletes n as its owner unless a test already did, and waits
// until it is gone.
func DeleteIfPresent(n *ns.Namespace) error {
	ctx, cancel := context.WithTimeout(context.Background(), ns.TeardownBudget)
	defer cancel()
	if gone, _ := Gone(ctx, n); gone {
		return nil
	}
	if n.Owner.Session == nil {
		s, err := n.Owner.Client.SignIn(ctx, n.Owner.Wallet, n.Name, nil)
		if err != nil {
			return fmt.Errorf("cannot sign in to delete it, it may leak: %w", err)
		}
		n.Owner.Session = s
	}
	if _, err := n.Client.JSON(ctx, http.MethodDelete, PathDelete, n.Owner.Token(), nil, nil); err != nil {
		return fmt.Errorf("delete failed: %w", err)
	}
	return eventually.Poll(ctx, ns.PollInterval, ns.TeardownBudget, "namespace "+n.Name+" to be gone",
		func() (bool, error) { return Gone(ctx, n) })
}
