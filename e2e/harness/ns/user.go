package ns

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
	"github.com/DeBrosOfficial/network/e2e/harness/oramacli"
)

// Routes and values this package relies on (docs/API_SURFACE.md).
const (
	PathCreate    = "/v1/namespaces"
	PathStatus    = "/v1/namespace/status"
	PathDelete    = "/v1/namespace/delete"
	PathQuery     = "/v1/rqlite/query"
	PathHealth    = "/health"
	StatusReady   = "ready"
	StatusFailed  = "failed"
	modeOpen      = "open"
	modeAllowlist = "allowlist"
	modeOperators = "operators"
)

// ClusterStatus is GET /v1/namespace/status?id=<cluster id>.
type ClusterStatus struct {
	ClusterID    string `json:"cluster_id"`
	Namespace    string `json:"namespace"`
	Status       string `json:"status"`
	RQLiteReady  bool   `json:"rqlite_ready"`
	OlricReady   bool   `json:"olric_ready"`
	GatewayReady bool   `json:"gateway_ready"`
	DNSReady     bool   `json:"dns_ready"`
	Error        string `json:"error"`
}

func newViaUser(t testing.TB, f *fleet.Fleet, name string, opts Options) *Namespace {
	t.Helper()
	owner := gw.NewUser(t, f, gw.LobbyNamespace)
	allowCreator(t, oramacli.ForState(f.State, f.Recorder()).For(t), owner.Wallet.Address())

	var created struct {
		ClusterID string `json:"cluster_id"`
		Status    string `json:"status"`
	}
	resp, err := owner.Client.JSON(t.Context(), http.MethodPost, PathCreate, owner.Token(), map[string]string{"name": name}, &created)
	if err != nil {
		t.Fatalf("failed to create namespace %s as %s: %v", name, owner.Wallet.Address(), err)
	}
	if resp.Status != http.StatusAccepted || created.ClusterID == "" {
		t.Fatalf("creating %s answered %d with no cluster being provisioned: %s", name, resp.Status, resp.Body)
	}
	n := &Namespace{Name: name, ClusterID: created.ClusterID, URL: gw.NamespaceURL(f.State, name)}
	n.Client = owner.Client.WithBase(n.URL)
	n.Owner = &gw.User{Wallet: owner.Wallet, Namespace: name, Client: owner.Client}
	if opts.DeviceAlg != "" {
		n.Owner.Device = gw.NewDevice(t, opts.DeviceAlg)
	}
	// Registered before waiting, so a namespace that never becomes ready is
	// still deleted.
	t.Cleanup(func() { n.deleteViaUser(t) })
	eventually.Require(t, PollInterval, ReadyBudget, "namespace "+name+" to serve", n.readyProbe(t.Context()))
	return n
}

// allowCreator makes sure wallet may create a namespace under the cluster's
// current namespace-creation mode, undoing any change afterwards.
func allowCreator(t testing.TB, cli *oramacli.Runner, walletAddr string) {
	t.Helper()
	mode, err := creationMode(cli.MustOK(t, "cluster", "settings", "show").Stdout)
	if err != nil {
		t.Fatal(err)
	}
	switch mode {
	case modeOpen:
	case modeAllowlist:
		cli.MustOK(t, "cluster", "creators", "add", walletAddr)
		t.Cleanup(func() {
			res, err := cli.Run(context.Background(), "cluster", "creators", "remove", walletAddr)
			if err != nil || res.Exit != 0 {
				t.Errorf("cleanup: failed to remove creator %s (exit %d): %v %s", walletAddr, res.Exit, err, res.Stderr)
			}
		})
	case modeOperators:
		t.Fatalf("namespace creation is %q on this cluster, so a user cannot create one; "+
			"use ns.ViaOperator or have the bootstrap stage set allowlist/open", mode)
	default:
		t.Fatalf("unknown namespace-creation mode %q", mode)
	}
}

// readyProbe checks, in order: the provisioning status, a sign-in to the
// namespace answering 200 (not 202), and a real query through the
// namespace's own gateway. Each call resumes where the last one stopped.
func (n *Namespace) readyProbe(ctx context.Context) func() (bool, error) {
	return func() (bool, error) {
		st, err := n.status(ctx)
		if err != nil {
			return false, err
		}
		if st.Status == StatusFailed {
			return false, eventually.Stop(fmt.Errorf("provisioning failed: %s", st.Error))
		}
		if st.Status != StatusReady {
			return false, fmt.Errorf("status=%s rqlite=%v olric=%v gateway=%v dns=%v",
				st.Status, st.RQLiteReady, st.OlricReady, st.GatewayReady, st.DNSReady)
		}
		if n.Owner.Session == nil {
			s, err := n.Owner.Client.SignIn(ctx, n.Owner.Wallet, n.Name, n.Owner.Device)
			if err != nil {
				return false, fmt.Errorf("status says ready but sign-in does not: %w", err)
			}
			n.Owner.Session = s
		}
		_, err = n.Client.JSON(ctx, http.MethodPost, PathQuery, n.Owner.Token(),
			map[string]any{"sql": "SELECT 1", "args": []any{}}, nil)
		if err != nil {
			return false, fmt.Errorf("status says ready but the namespace gateway does not serve: %w", err)
		}
		return true, nil
	}
}

func (n *Namespace) status(ctx context.Context) (*ClusterStatus, error) {
	var st ClusterStatus
	_, err := n.Owner.Client.JSON(ctx, http.MethodGet, PathStatus+"?id="+url.QueryEscape(n.ClusterID), "", nil, &st)
	if err != nil {
		return nil, err
	}
	return &st, nil
}

// deleteViaUser deletes the namespace as its owner and waits until the status
// route no longer knows the cluster and the namespace gateway stops serving.
func (n *Namespace) deleteViaUser(t testing.TB) {
	t.Helper()
	if n.removed {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), TeardownBudget)
	defer cancel()
	if n.Owner.Session == nil {
		s, err := n.Owner.Client.SignIn(ctx, n.Owner.Wallet, n.Name, n.Owner.Device)
		if err != nil {
			t.Errorf("cleanup: cannot sign in to delete namespace %s, it may leak: %v", n.Name, err)
			return
		}
		n.Owner.Session = s
	}
	if _, err := n.Owner.Client.JSON(ctx, http.MethodDelete, PathDelete, n.Owner.Token(), nil, nil); err != nil {
		t.Errorf("cleanup: failed to delete namespace %s: %v", n.Name, err)
		return
	}
	if err := n.pollGone(ctx); err != nil {
		t.Errorf("cleanup: teardown of %s not verified: %v", n.Name, err)
	}
}

// MarkRemoved records that something other than the owner (an operator's
// removal) deleted the namespace, and waits until its cluster status is gone
// and its gateway has stopped serving; it fails t when that does not happen.
// The owner's teardown then has nothing left to do.
func (n *Namespace) MarkRemoved(t testing.TB) {
	t.Helper()
	n.removed = true
	ctx, cancel := context.WithTimeout(t.Context(), TeardownBudget)
	defer cancel()
	if err := n.pollGone(ctx); err != nil {
		t.Fatalf("namespace %s was not torn down: %v", n.Name, err)
	}
}

// pollGone waits until the namespace's cluster status is 404 and its gateway
// no longer serves /health.
func (n *Namespace) pollGone(ctx context.Context) error {
	return eventually.Poll(ctx, PollInterval, TeardownBudget, "namespace "+n.Name+" to be gone", func() (bool, error) {
		_, err := n.status(ctx)
		var se *gw.StatusError
		if !errors.As(err, &se) || se.Status != http.StatusNotFound {
			return false, fmt.Errorf("status route still answers for cluster %s: %v", n.ClusterID, err)
		}
		return n.gatewayGone(ctx)
	})
}

// gatewayGone reports whether the namespace gateway stopped serving health.
func (n *Namespace) gatewayGone(ctx context.Context) (bool, error) {
	resp, err := n.Client.Send(ctx, gw.Req{Path: PathHealth})
	if err == nil && resp.Status == http.StatusOK {
		return false, errors.New("the namespace gateway still answers /health with 200")
	}
	return true, nil
}
