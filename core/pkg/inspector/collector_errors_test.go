package inspector

import (
	"errors"
	"strings"
	"testing"
)

func TestSSHOutputError_empty_output_is_an_error(t *testing.T) {
	cases := map[string]SSHResult{
		"reset":     {Err: errors.New("exit status 255"), Stderr: "kex_exchange_identification: read: Connection reset", ExitCode: 255, Retries: 3},
		"exit code": {ExitCode: 1},
		"silent":    {},
		"blank":     {Stdout: " \n"},
	}
	for name, res := range cases {
		if err := sshOutputError(res); err == nil {
			t.Errorf("%s: expected an error for an empty session", name)
		}
	}
	err := sshOutputError(cases["reset"])
	if !strings.Contains(err.Error(), "Connection reset") {
		t.Errorf("error should quote stderr, got %q", err)
	}
}

func TestSSHOutputError_output_with_failed_exit_is_usable(t *testing.T) {
	if err := sshOutputError(SSHResult{Stdout: "===INSPECTOR_SEP===\ninactive\n", ExitCode: 1}); err != nil {
		t.Fatalf("a script that printed output must be parsed, got %v", err)
	}
}

func TestSplitSections(t *testing.T) {
	parts, err := splitSections(SSHResult{Stdout: sectionSep + "a" + sectionSep + "b"}, 3)
	if err != nil || len(parts) != 3 {
		t.Fatalf("got %v, %v", parts, err)
	}
	if _, err := splitSections(SSHResult{Stdout: "partial"}, 3); err == nil {
		t.Fatal("truncated output must be an error")
	}
	if _, err := splitSections(SSHResult{}, 1); err == nil {
		t.Fatal("empty output must be an error")
	}
}

func TestRecordFailure(t *testing.T) {
	nd := &NodeData{Node: Node{Host: "10.0.0.1"}}
	nd.recordFailure(SubsystemDNS, nil)
	if len(nd.Failed) != 0 || len(nd.Errors) != 0 {
		t.Fatal("nil error must record nothing")
	}
	nd.recordFailure(SubsystemDNS, errors.New("boom"))
	if nd.Failed[SubsystemDNS] != "boom" || len(nd.Errors) != 1 {
		t.Fatalf("failure not recorded: %+v", nd)
	}
}

func selectAll(string) bool { return true }

func TestCollectionResults_unreachable_node(t *testing.T) {
	nd := &NodeData{Node: Node{User: "u", Host: "10.0.0.1"}}
	nd.markUnreachable(errors.New("connection reset"))
	res := collectionResults(&ClusterData{Nodes: map[string]*NodeData{"10.0.0.1": nd}}, selectAll)
	if len(res) != 1 || res[0].ID != CheckNodeReachable || res[0].Status != StatusFail || res[0].Severity != Critical {
		t.Fatalf("want one critical node.reachable, got %+v", res)
	}
	if !strings.Contains(res[0].Message, "connection reset") {
		t.Errorf("message should carry the error: %q", res[0].Message)
	}
}

func TestCollectionResults_partial_failure_respects_selection(t *testing.T) {
	nd := &NodeData{Node: Node{User: "u", Host: "10.0.0.1"}}
	nd.recordFailure(SubsystemNamespace, errors.New("probe died"))
	data := &ClusterData{Nodes: map[string]*NodeData{"10.0.0.1": nd}}

	res := collectionResults(data, selectAll)
	if len(res) != 2 || res[0].ID != "namespace.collected" || res[1].ID != "webrtc.collected" {
		t.Fatalf("namespace failure should be reported for namespace and webrtc: %+v", res)
	}
	only := func(s string) bool { return s == "namespace" }
	if res := collectionResults(data, only); len(res) != 1 {
		t.Fatalf("unselected subsystem must not be reported: %+v", res)
	}
}

func TestCollectionResults_healthy_node_adds_nothing(t *testing.T) {
	nd := &NodeData{Node: Node{Host: "10.0.0.1"}}
	if res := collectionResults(&ClusterData{Nodes: map[string]*NodeData{"10.0.0.1": nd}}, selectAll); len(res) != 0 {
		t.Fatalf("got %+v", res)
	}
}
