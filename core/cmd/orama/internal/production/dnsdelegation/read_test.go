package dnsdelegation

import (
	"errors"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/inspector"
)

// fakeCluster replaces the calls Read makes: one node, keys "prepared" by
// setting SSHKey on the slice given, and a query that answers body.
func fakeCluster(t *testing.T, body string, queried *inspector.Node) {
	t.Helper()
	prevResolve, prevPrepare, prevQuery := resolveNodes, prepareNodeKeys, querySQL
	t.Cleanup(func() { resolveNodes, prepareNodeKeys, querySQL = prevResolve, prevPrepare, prevQuery })
	resolveNodes = func(string) ([]inspector.Node, error) {
		return []inspector.Node{{User: "ubuntu", Host: "203.0.113.7"}}, nil
	}
	prepareNodeKeys = func(nodes []inspector.Node) (func(), error) {
		for i := range nodes {
			nodes[i].SSHKey = "/tmp/key-" + nodes[i].Host
		}
		return func() {}, nil
	}
	querySQL = func(n inspector.Node, _ string) ([]byte, error) {
		*queried = n
		if n.SSHKey == "" {
			return nil, errors.New("no SSH key for " + n.User + "@" + n.Host)
		}
		return []byte(body), nil
	}
}

// The node queried was a copy made before its key was prepared, so every run
// failed "no SSH key for …" (stagenet e2e, 2026-09-30).
func TestRead_queriesTheNodeItPreparedAKeyFor(t *testing.T) {
	var queried inspector.Node
	fakeCluster(t, `{"results":[{"columns":["domain","hostname","ip"],"values":[["example.test","ns1","203.0.113.7"]]}]}`, &queried)

	got, err := Read("stagenet")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if queried.SSHKey == "" {
		t.Fatal("the node queried carried no SSH key")
	}
	if len(got) != 1 || got[0].Domain != "example.test" || len(got[0].Nameservers) != 1 {
		t.Fatalf("delegations = %+v", got)
	}
}

func TestRead_noNodesIsAnError(t *testing.T) {
	var queried inspector.Node
	fakeCluster(t, "", &queried)
	resolveNodes = func(string) ([]inspector.Node, error) { return nil, nil }
	if _, err := Read("stagenet"); err == nil {
		t.Fatal("an environment with no nodes was read")
	}
}
