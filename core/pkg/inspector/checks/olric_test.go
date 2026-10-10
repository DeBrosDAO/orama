package checks

import (
	"strconv"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/inspector"
)

func TestCheckOlric_ServiceInactive(t *testing.T) {
	nd := makeNodeData("1.1.1.1", "node")
	nd.Olric = &inspector.OlricData{ServiceActive: false}

	data := makeCluster(map[string]*inspector.NodeData{"1.1.1.1": nd})
	results := CheckOlric(data)

	expectStatus(t, results, "olric.service_active", inspector.StatusFail)
	// Should return early — no further per-node checks
	if findCheck(results, "olric.memberlist_port") != nil {
		t.Error("should not check memberlist when service inactive")
	}
}

func TestCheckOlric_HealthyNode(t *testing.T) {
	nd := makeNodeData("1.1.1.1", "node")
	nd.Olric = &inspector.OlricData{
		ServiceActive: true,
		MemberlistUp:  true,
		RestartCount:  0,
		ProcessMemMB:  100,
		LogSuspects:   0,
		LogFlapping:   0,
		LogErrors:     0,
	}

	data := makeCluster(map[string]*inspector.NodeData{"1.1.1.1": nd})
	results := CheckOlric(data)

	expectStatus(t, results, "olric.service_active", inspector.StatusPass)
	expectStatus(t, results, "olric.memberlist_port", inspector.StatusPass)
	expectStatus(t, results, "olric.restarts", inspector.StatusPass)
	expectStatus(t, results, "olric.log_failed_members", inspector.StatusPass)
	expectStatus(t, results, "olric.log_suspicions", inspector.StatusPass)
	expectStatus(t, results, "olric.log_flapping", inspector.StatusPass)
	expectStatus(t, results, "olric.log_errors", inspector.StatusPass)
}

func TestCheckOlric_RestartCounts(t *testing.T) {
	tests := []struct {
		name     string
		restarts int
		status   inspector.Status
	}{
		{"zero", 0, inspector.StatusPass},
		{"few", 2, inspector.StatusWarn},
		{"many", 5, inspector.StatusFail},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nd := makeNodeData("1.1.1.1", "node")
			nd.Olric = &inspector.OlricData{ServiceActive: true, RestartCount: tt.restarts}
			data := makeCluster(map[string]*inspector.NodeData{"1.1.1.1": nd})
			results := CheckOlric(data)
			expectStatus(t, results, "olric.restarts", tt.status)
		})
	}
}

func TestCheckOlric_Memory(t *testing.T) {
	tests := []struct {
		name   string
		memMB  int
		status inspector.Status
	}{
		{"healthy", 100, inspector.StatusPass},
		{"elevated", 300, inspector.StatusWarn},
		{"high", 600, inspector.StatusFail},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nd := makeNodeData("1.1.1.1", "node")
			nd.Olric = &inspector.OlricData{ServiceActive: true, ProcessMemMB: tt.memMB}
			data := makeCluster(map[string]*inspector.NodeData{"1.1.1.1": nd})
			results := CheckOlric(data)
			expectStatus(t, results, "olric.memory", tt.status)
		})
	}
}

// A member memberlist marked failed is a critical failure; suspicions that
// were refuted (a member answering late, e.g. on a starved node, or one leaving
// with its namespace) are a warning: they used to fail the whole inspection.
func TestCheckOlric_LogSuspects(t *testing.T) {
	tests := []struct {
		name       string
		deadMarks  int
		suspects   int
		wantFailed inspector.Status
		wantSuspic inspector.Status
	}{
		{"quiet", 0, 0, inspector.StatusPass, inspector.StatusPass},
		{"suspicions only", 0, 10, inspector.StatusPass, inspector.StatusWarn},
		{"member marked failed", 1, 0, inspector.StatusFail, inspector.StatusPass},
		{"both", 2, 5, inspector.StatusFail, inspector.StatusWarn},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nd := makeNodeData("1.1.1.1", "node")
			nd.Olric = &inspector.OlricData{ServiceActive: true, LogDeadMarks: tt.deadMarks, LogSuspects: tt.suspects}
			results := CheckOlric(makeCluster(map[string]*inspector.NodeData{"1.1.1.1": nd}))
			expectStatus(t, results, "olric.log_failed_members", tt.wantFailed)
			expectStatus(t, results, "olric.log_suspicions", tt.wantSuspic)
		})
	}
}

func TestCheckOlric_LogErrors(t *testing.T) {
	tests := []struct {
		name   string
		errors int
		status inspector.Status
	}{
		{"none", 0, inspector.StatusPass},
		{"few", 10, inspector.StatusWarn},
		{"many", 30, inspector.StatusFail},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nd := makeNodeData("1.1.1.1", "node")
			nd.Olric = &inspector.OlricData{ServiceActive: true, LogErrors: tt.errors}
			data := makeCluster(map[string]*inspector.NodeData{"1.1.1.1": nd})
			results := CheckOlric(data)
			expectStatus(t, results, "olric.log_errors", tt.status)
		})
	}
}

func TestCheckOlric_CrossNode_AllActive(t *testing.T) {
	nodes := map[string]*inspector.NodeData{}
	for _, host := range []string{"1.1.1.1", "2.2.2.2", "3.3.3.3"} {
		nd := makeNodeData(host, "node")
		nd.Olric = &inspector.OlricData{ServiceActive: true, MemberlistUp: true}
		nodes[host] = nd
	}
	data := makeCluster(nodes)
	results := CheckOlric(data)
	expectStatus(t, results, "olric.all_active", inspector.StatusPass)
	expectStatus(t, results, "olric.all_memberlist", inspector.StatusPass)
}

func TestCheckOlric_CrossNode_PartialActive(t *testing.T) {
	nodes := map[string]*inspector.NodeData{}
	for i, host := range []string{"1.1.1.1", "2.2.2.2", "3.3.3.3"} {
		nd := makeNodeData(host, "node")
		nd.Olric = &inspector.OlricData{ServiceActive: i < 2, MemberlistUp: i < 2}
		nodes[host] = nd
	}
	data := makeCluster(nodes)
	results := CheckOlric(data)
	expectStatus(t, results, "olric.all_active", inspector.StatusFail)
}

func TestCheckOlric_NilData(t *testing.T) {
	nd := makeNodeData("1.1.1.1", "node")
	data := makeCluster(map[string]*inspector.NodeData{"1.1.1.1": nd})
	results := CheckOlric(data)
	if len(results) != 0 {
		t.Errorf("expected 0 results for nil Olric data, got %d", len(results))
	}
}

// The collector probes the memberlist on constants.OlricMemberlistPort; the
// check used to tell the operator to look at 3322, a port nothing listens on.
func TestCheckOlric_memberlistNamesTheRealPort(t *testing.T) {
	for _, up := range []bool{true, false} {
		nd := makeNodeData("1.1.1.1", "node")
		nd.Olric = &inspector.OlricData{ServiceActive: true, MemberlistUp: up}
		results := CheckOlric(makeCluster(map[string]*inspector.NodeData{"1.1.1.1": nd}))
		c := findCheck(results, "olric.memberlist_port")
		if c == nil {
			t.Fatal("no memberlist check")
		}
		port := strconv.Itoa(constants.OlricMemberlistPort)
		if !strings.Contains(c.Name, port) || !strings.Contains(c.Message, port) || strings.Contains(c.Message, "3322") {
			t.Errorf("up=%v: name %q, message %q; want port %s", up, c.Name, c.Message, port)
		}
	}
}
