//go:build e2e_fleet

package opennetworkphases

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/tenancy"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
	"github.com/DeBrosOfficial/network/e2e/harness/ns"
	"github.com/DeBrosOfficial/network/e2e/harness/oramacli"
)

const deleteBudget = ns.TeardownBudget

// listed reports whether `orama namespace list --json` shows name, and its
// cluster status.
func listed(ctx context.Context, cli *oramacli.Runner, name string) (string, bool, error) {
	res, err := cli.Run(ctx, "namespace", "list", "--json")
	if err != nil {
		return "", false, err
	}
	var rows []struct {
		Name    string `json:"name"`
		Cluster string `json:"cluster"`
	}
	if err := oramacli.DecodeJSON(res, &rows); err != nil {
		return "", false, err
	}
	for _, r := range rows {
		if r.Name == name {
			return r.Cluster, true, nil
		}
	}
	return "", false, nil
}

// waitListed waits until name is listed ready.
func waitListed(t *testing.T, cli *oramacli.Runner, name string) {
	t.Helper()
	eventually.Require(t, pollEvery, ns.ReadyBudget, "namespace "+name+" ready", func() (bool, error) {
		status, ok, err := listed(t.Context(), cli, name)
		if err != nil {
			return false, err
		}
		if status == ns.StatusFailed {
			return false, eventually.Stop(errors.New("provisioning failed"))
		}
		if ok && status == ns.StatusReady {
			return true, nil
		}
		return false, fmt.Errorf("listed=%t status=%q", ok, status)
	})
}

// deleteCurrent deletes the CLI's current namespace (the one it last signed
// in to) and waits until it is no longer listed.
func deleteCurrent(t *testing.T, cli *oramacli.Runner, name string) {
	ctx, cancel := context.WithTimeout(context.Background(), deleteBudget+time.Minute)
	defer cancel()
	if res, err := cli.Run(ctx, "namespace", "delete", "--force"); err != nil || res.Exit != exitOK {
		t.Errorf("cleanup: orama namespace delete %s: %v %s", name, err, res.Stderr)
		return
	}
	err := eventually.Poll(ctx, pollEvery, deleteBudget, "namespace "+name+" gone", func() (bool, error) {
		_, ok, err := listed(ctx, cli, name)
		return err == nil && !ok, err
	})
	if err != nil {
		t.Errorf("cleanup: %v", err)
	}
}

// db is a namespace's database reached as an admin member.
type db struct {
	c     *gw.Client
	token string
}

func newDB(t *testing.T, f *fleet.Fleet, n *ns.Namespace) *db {
	t.Helper()
	admin := tenancy.OperatorMember(t, f, n, tenancy.RoleAdmin)
	d := &db{c: harness.GW(t).WithBase(gw.NamespaceURL(f.State, n.Name)), token: admin.Token()}
	if _, err := d.c.JSON(t.Context(), http.MethodPost, "/v1/rqlite/create-table", d.token, map[string]string{"schema": "CREATE TABLE a9 (v TEXT PRIMARY KEY)"}, nil); err != nil {
		t.Fatal(err)
	}
	return d
}

func (d *db) exec(t *testing.T, sql string, args ...any) {
	t.Helper()
	if args == nil {
		args = []any{}
	}
	if _, err := d.c.JSON(t.Context(), http.MethodPost, "/v1/rqlite/exec", d.token, map[string]any{"sql": sql, "args": args}, nil); err != nil {
		t.Fatal(err)
	}
}

func (d *db) rows(t *testing.T, values ...string) {
	t.Helper()
	for _, v := range values {
		d.exec(t, "INSERT INTO a9 (v) VALUES (?)", v)
	}
}

func (d *db) count(t *testing.T) int {
	t.Helper()
	var out struct {
		Items []map[string]any `json:"items"`
	}
	if _, err := d.c.JSON(t.Context(), http.MethodPost, "/v1/rqlite/query", d.token, map[string]any{"sql": "SELECT v FROM a9", "args": []any{}}, &out); err != nil {
		t.Fatal(err)
	}
	return len(out.Items)
}
