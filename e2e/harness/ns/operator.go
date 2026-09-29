package ns

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
	"github.com/DeBrosOfficial/network/e2e/harness/oramacli"
)

// listEntry is one row of `orama namespace list --json`.
type listEntry struct {
	Name    string `json:"name"`
	Cluster string `json:"cluster"`
}

func newViaOperator(t testing.TB, f *fleet.Fleet, name string) *Namespace {
	t.Helper()
	cli := oramacli.ForState(f.State, f.Recorder()).For(t)
	url := gw.NamespaceURL(f.State, name)
	return createViaOperator(t, cli, gw.ForFleet(t, f).WithBase(url), name, url)
}

// createViaOperator prepares everything that can fail before creating the
// namespace, then registers its deletion right after the create succeeds: a
// failure between the two would leak a namespace nobody deletes.
func createViaOperator(t testing.TB, cli *oramacli.Runner, client *gw.Client, name, url string) *Namespace {
	t.Helper()
	// A HOME of its own: signing in to the namespace and deleting "the current
	// namespace" must not switch the shared CLI's state under parallel tests.
	n := &Namespace{Name: name, URL: url, Client: client, CLI: cli.Isolated(t)}
	cli.MustOK(t, "namespace", "create", name)
	t.Cleanup(func() { n.deleteViaOperator(t, cli) })

	eventually.Require(t, PollInterval, ReadyBudget, "namespace "+name+" to serve", func() (bool, error) {
		status, err := listedStatus(t.Context(), cli, name)
		if err != nil {
			return false, err
		}
		if status == StatusFailed {
			return false, eventually.Stop(fmt.Errorf("namespace %s: provisioning failed", name))
		}
		if status != StatusReady {
			return false, fmt.Errorf("orama namespace list says %s", status)
		}
		resp, err := n.Client.Send(t.Context(), gw.Req{Path: PathHealth})
		if err != nil || resp.Status != http.StatusOK {
			return false, fmt.Errorf("listed ready but https://ns-%s does not serve /health: %v", name, statusOf(resp, err))
		}
		return true, nil
	})
	n.CLI.MustOK(t, "auth", "login", "--namespace", name)
	return n
}

// listedStatus is the namespace's cluster status as `orama namespace list`
// shows it; an error when it is not listed.
func listedStatus(ctx context.Context, cli *oramacli.Runner, name string) (string, error) {
	res, err := cli.Run(ctx, "namespace", "list", "--json")
	if err != nil {
		return "", err
	}
	if res.Exit != 0 {
		return "", fmt.Errorf("orama namespace list exited %d: %s", res.Exit, res.Stderr)
	}
	var rows []listEntry
	if err := oramacli.DecodeJSON(res, &rows); err != nil {
		return "", err
	}
	for _, r := range rows {
		if r.Name == name {
			return r.Cluster, nil
		}
	}
	return "", errNotListed
}

var errNotListed = errors.New("namespace not listed")

func statusOf(resp *gw.Response, err error) string {
	if err != nil {
		return err.Error()
	}
	return fmt.Sprintf("HTTP %d", resp.Status)
}

// deleteViaOperator deletes from the isolated HOME and waits until the
// namespace is no longer listed and its gateway stops serving.
func (n *Namespace) deleteViaOperator(t testing.TB, shared *oramacli.Runner) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), TeardownBudget)
	defer cancel()
	for _, args := range [][]string{{"auth", "login", "--namespace", n.Name}, {"namespace", "delete", "--force"}} {
		res, err := n.CLI.Run(ctx, args...)
		if err != nil || res.Exit != 0 {
			t.Errorf("cleanup: orama %v failed (exit %d), namespace %s may leak: %v %s", args, res.Exit, n.Name, err, res.Stderr)
			return
		}
	}
	err := eventually.Poll(ctx, PollInterval, TeardownBudget, "namespace "+n.Name+" to be gone", func() (bool, error) {
		_, err := listedStatus(ctx, shared, n.Name)
		if !errors.Is(err, errNotListed) {
			return false, fmt.Errorf("still listed (or list failed): %v", err)
		}
		return n.gatewayGone(ctx)
	})
	if err != nil {
		t.Errorf("cleanup: teardown of %s not verified: %v", n.Name, err)
	}
}
