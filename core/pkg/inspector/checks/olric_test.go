package checks

import (
	"testing"

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
	expectStatus(t, results, "olric.log_suspects", inspector.StatusPass)
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

func TestCheckOlric_LogSuspects(t *testing.T) {
	nd := makeNodeData("1.1.1.1", "node")
	nd.Olric = &inspector.OlricData{ServiceActive: true, LogSuspects: 5}
	data := makeCluster(map[string]*inspector.NodeData{"1.1.1.1": nd})
	results := CheckOlric(data)
	expectStatus(t, results, "olric.log_suspects", inspector.StatusFail)
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
