package decommission

import (
	"errors"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/inspector"
)

var errStop = errors.New("stop before touching any machine")

// stubWipe replaces node and key resolution; the key stub records what it was
// asked for and stops the wipe there.
func stubWipe(t *testing.T, fleet []inspector.Node) *[]string {
	t.Helper()
	asked := &[]string{}
	origResolve, origKeys := resolveWipeNodes, prepareWipeKeys
	t.Cleanup(func() { resolveWipeNodes, prepareWipeKeys = origResolve, origKeys })
	resolveWipeNodes = func(string) ([]inspector.Node, error) { return fleet, nil }
	prepareWipeKeys = func(nodes []inspector.Node) (func(), error) {
		for _, n := range nodes {
			*asked = append(*asked, n.Host)
		}
		return func() {}, errStop
	}
	return asked
}

var stagenet = []inspector.Node{
	{Environment: "stagenet", User: "debian", Host: "37.59.116.212"},
	{Environment: "stagenet", User: "ubuntu", Host: "141.227.165.168"},
	{Environment: "stagenet", User: "ubuntu", Host: "57.128.226.141"},
}

// Wiping athena failed because poseidon — not set up yet, so no key in the
// vault — had its key resolved too.
func TestExecuteWipe_resolvesOnlyTheTargetsKey(t *testing.T) {
	asked := stubWipe(t, stagenet)
	err := executeWipe(&WipeFlags{Env: "stagenet", Node: "37.59.116.212", Force: true})
	if !errors.Is(err, errStop) {
		t.Fatalf("executeWipe = %v, want to reach key resolution", err)
	}
	if len(*asked) != 1 || (*asked)[0] != "37.59.116.212" {
		t.Errorf("keys resolved for %v, want only the target", *asked)
	}
}

func TestExecuteWipe_unknownNodeAsksForNoKeys(t *testing.T) {
	asked := stubWipe(t, stagenet)
	if err := executeWipe(&WipeFlags{Env: "stagenet", Node: "203.0.113.9", Force: true}); err == nil {
		t.Fatal("a node outside the environment was accepted")
	}
	if len(*asked) != 0 {
		t.Errorf("keys resolved for %v for a node that is not in the environment", *asked)
	}
}

func TestExecuteWipe_wholeEnvironmentResolvesEveryKey(t *testing.T) {
	asked := stubWipe(t, stagenet)
	_ = executeWipe(&WipeFlags{Env: "stagenet", Force: true})
	if len(*asked) != len(stagenet) {
		t.Errorf("keys resolved for %v, want all %d nodes", *asked, len(stagenet))
	}
}

// stubWipeRun lets the wipe reach the machines: keys resolve, the remote wipe
// reports wipeErr, and the vault delete reports forgetErr.
func stubWipeRun(t *testing.T, wipeErr, forgetErr error) (wiped, forgotten *[]string) {
	t.Helper()
	wiped, forgotten = &[]string{}, &[]string{}
	origResolve, origKeys, origWipe, origForget := resolveWipeNodes, prepareWipeKeys, wipeRemote, forgetNodeKey
	t.Cleanup(func() { resolveWipeNodes, prepareWipeKeys, wipeRemote, forgetNodeKey = origResolve, origKeys, origWipe, origForget })
	resolveWipeNodes = func(string) ([]inspector.Node, error) { return stagenet, nil }
	prepareWipeKeys = func([]inspector.Node) (func(), error) { return func() {}, nil }
	wipeRemote = func(n inspector.Node, _ bool) error { *wiped = append(*wiped, n.Host); return wipeErr }
	forgetNodeKey = func(n inspector.Node) error { *forgotten = append(*forgotten, n.Host); return forgetErr }
	return wiped, forgotten
}

// Every setup stores a key in the operator's vault; a wiped node's key used to
// stay there forever.
func TestExecuteWipe_forgetsTheKeyOfAWipedNode(t *testing.T) {
	_, forgotten := stubWipeRun(t, nil, nil)
	if err := executeWipe(&WipeFlags{Env: "stagenet", Node: "141.227.165.168", Force: true}); err != nil {
		t.Fatalf("executeWipe: %v", err)
	}
	if len(*forgotten) != 1 || (*forgotten)[0] != "141.227.165.168" {
		t.Errorf("keys forgotten: %v", *forgotten)
	}
}

// A node that was not wiped still needs its key.
func TestExecuteWipe_keepsTheKeyWhenTheWipeFails(t *testing.T) {
	_, forgotten := stubWipeRun(t, errors.New("ssh: connection refused"), nil)
	if err := executeWipe(&WipeFlags{Env: "stagenet", Node: "141.227.165.168", Force: true}); err == nil {
		t.Fatal("a failed wipe reported success")
	}
	if len(*forgotten) != 0 {
		t.Errorf("forgot the key of a node that was not wiped: %v", *forgotten)
	}
}

func TestExecuteWipe_vaultFailureIsReported(t *testing.T) {
	stubWipeRun(t, nil, errors.New("agent locked"))
	if err := executeWipe(&WipeFlags{Env: "stagenet", Node: "141.227.165.168", Force: true}); err == nil {
		t.Fatal("a key left in the vault was reported as a clean wipe")
	}
}
