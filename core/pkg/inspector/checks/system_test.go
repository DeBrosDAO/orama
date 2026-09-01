package checks

import (
	"testing"

	"github.com/DeBrosOfficial/network/pkg/inspector"
)

func TestCheckSystem_HealthyNode(t *testing.T) {
	nd := makeNodeData("1.1.1.1", "node")
	nd.System = &inspector.SystemData{
		Services: map[string]string{
			"orama-node":         "active",
			"orama-olric":        "active",
			"orama-ipfs":         "active",
			"orama-ipfs-cluster": "active",
			"caddy":              "active",
			"wg-quick@wg0":       "active",
		},
		FailedUnits:    nil,
		MemTotalMB:     8192,
		MemUsedMB:      4096,
		DiskUsePct:     50,
		DiskUsedGB:     "25G",
		DiskTotalGB:    "50G",
		LoadAvg:        "1.0, 0.8, 0.5",
		CPUCount:       4,
		OOMKills:       0,
		SwapTotalMB:    2048,
		SwapUsedMB:     100,
		UptimeRaw:      "2024-01-01 00:00:00",
		InodePct:       10,
		ListeningPorts: []int{5001, 3322, 6001, 4501},
		UFWActive:      true,
		ProcessUser:    "orama",
		PanicCount:     0,
	}

	data := makeCluster(map[string]*inspector.NodeData{"1.1.1.1": nd})
	results := CheckSystem(data)

	expectStatus(t, results, "system.svc_orama_node", inspector.StatusPass)
	expectStatus(t, results, "system.svc_orama_olric", inspector.StatusPass)
	expectStatus(t, results, "system.svc_orama_ipfs", inspector.StatusPass)
	expectStatus(t, results, "system.svc_orama_ipfs_cluster", inspector.StatusPass)
	expectStatus(t, results, "system.svc_caddy", inspector.StatusPass)
	expectStatus(t, results, "system.svc_wg", inspector.StatusPass)
	expectStatus(t, results, "system.no_failed_units", inspector.StatusPass)
	expectStatus(t, results, "system.memory", inspector.StatusPass)
	expectStatus(t, results, "system.disk", inspector.StatusPass)
	expectStatus(t, results, "system.load", inspector.StatusPass)
	expectStatus(t, results, "system.oom", inspector.StatusPass)
	expectStatus(t, results, "system.swap", inspector.StatusPass)
	expectStatus(t, results, "system.inodes", inspector.StatusPass)
	expectStatus(t, results, "system.ufw", inspector.StatusPass)
	expectStatus(t, results, "system.process_user", inspector.StatusPass)
	expectStatus(t, results, "system.panics", inspector.StatusPass)
	expectStatus(t, results, "system.port_5001", inspector.StatusPass)
	expectStatus(t, results, "system.port_3322", inspector.StatusPass)
	expectStatus(t, results, "system.port_6001", inspector.StatusPass)
	expectStatus(t, results, "system.port_4501", inspector.StatusPass)
}

func TestCheckSystem_ServiceInactive(t *testing.T) {
	nd := makeNodeData("1.1.1.1", "node")
	nd.System = &inspector.SystemData{
		Services: map[string]string{
			"orama-node":         "active",
			"orama-olric":        "inactive",
			"orama-ipfs":         "active",
			"orama-ipfs-cluster": "failed",
			"caddy":              "active",
		},
	}
	data := makeCluster(map[string]*inspector.NodeData{"1.1.1.1": nd})
	results := CheckSystem(data)

	expectStatus(t, results, "system.svc_orama_node", inspector.StatusPass)
	expectStatus(t, results, "system.svc_orama_olric", inspector.StatusFail)
	expectStatus(t, results, "system.svc_orama_ipfs_cluster", inspector.StatusFail)
}

func TestCheckSystem_NameserverServices(t *testing.T) {
	nd := makeNodeData("5.5.5.5", "nameserver-ns1")
	nd.System = &inspector.SystemData{
		Services: map[string]string{
			"orama-node":         "active",
			"orama-olric":        "active",
			"orama-ipfs":         "active",
			"orama-ipfs-cluster": "active",
			"coredns":            "active",
			"caddy":              "active",
		},
	}
	data := makeCluster(map[string]*inspector.NodeData{"5.5.5.5": nd})
	results := CheckSystem(data)
	expectStatus(t, results, "system.svc_coredns", inspector.StatusPass)
	expectStatus(t, results, "system.svc_caddy", inspector.StatusPass)
}

func TestCheckSystem_IndexUnitsAlias(t *testing.T) {
	nd := makeNodeData("1.1.1.1", "node")
	nd.System = &inspector.SystemData{
		Services: map[string]string{
			"orama-node":                         "active",
			"orama-namespace-olric@index":        "active",
			"orama-namespace-ipfs@index":         "active",
			"orama-namespace-ipfs-cluster@index": "active",
			"orama-namespace-caddy@index":        "active",
			"orama-namespace-wireguard@index":    "active",
		},
	}
	data := makeCluster(map[string]*inspector.NodeData{"1.1.1.1": nd})
	results := CheckSystem(data)
	expectStatus(t, results, "system.svc_orama_olric", inspector.StatusPass)
	expectStatus(t, results, "system.svc_orama_ipfs", inspector.StatusPass)
	expectStatus(t, results, "system.svc_orama_ipfs_cluster", inspector.StatusPass)
	expectStatus(t, results, "system.svc_caddy", inspector.StatusPass)
	expectStatus(t, results, "system.svc_wg", inspector.StatusPass)
}

func TestCheckSystem_NameserverCoreDNSAlias(t *testing.T) {
	nd := makeNodeData("5.5.5.5", "nameserver-ns1")
	nd.System = &inspector.SystemData{
		Services: map[string]string{
			"orama-node":                         "active",
			"orama-olric":                        "active",
			"orama-ipfs":                         "active",
			"orama-ipfs-cluster":                 "active",
			"caddy":                              "active",
			"orama-namespace-coredns@nameserver": "active",
		},
	}
	data := makeCluster(map[string]*inspector.NodeData{"5.5.5.5": nd})
	results := CheckSystem(data)
	expectStatus(t, results, "system.svc_coredns", inspector.StatusPass)
}

func TestCheckSystem_NameserverServicesNotCheckedOnRegularNode(t *testing.T) {
	nd := makeNodeData("1.1.1.1", "node")
	nd.System = &inspector.SystemData{
		Services: map[string]string{
			"orama-node":         "active",
			"orama-olric":        "active",
			"orama-ipfs":         "active",
			"orama-ipfs-cluster": "active",
			"caddy":              "active",
		},
	}
	data := makeCluster(map[string]*inspector.NodeData{"1.1.1.1": nd})
	results := CheckSystem(data)
	if findCheck(results, "system.svc_coredns") != nil {
		t.Error("should not check coredns on regular node")
	}
}

func TestCheckSystem_FailedUnits_Debros(t *testing.T) {
	nd := makeNodeData("1.1.1.1", "node")
	nd.System = &inspector.SystemData{
		Services:    map[string]string{},
		FailedUnits: []string{"orama-node.service"},
	}
	data := makeCluster(map[string]*inspector.NodeData{"1.1.1.1": nd})
	results := CheckSystem(data)
	expectStatus(t, results, "system.no_failed_units", inspector.StatusFail)
}

func TestCheckSystem_FailedUnits_External(t *testing.T) {
	nd := makeNodeData("1.1.1.1", "node")
	nd.System = &inspector.SystemData{
		Services:    map[string]string{},
		FailedUnits: []string{"cloud-init.service"},
	}
	data := makeCluster(map[string]*inspector.NodeData{"1.1.1.1": nd})
	results := CheckSystem(data)
	expectStatus(t, results, "system.no_failed_units", inspector.StatusPass)
	expectStatus(t, results, "system.external_failed_units", inspector.StatusWarn)
}

func TestCheckSystem_Memory(t *testing.T) {
	tests := []struct {
		name   string
		used   int
		total  int
		status inspector.Status
	}{
		{"healthy", 4000, 8000, inspector.StatusPass},  // 50%
		{"elevated", 7000, 8000, inspector.StatusWarn}, // 87.5%
		{"critical", 7500, 8000, inspector.StatusFail}, // 93.75%
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nd := makeNodeData("1.1.1.1", "node")
			nd.System = &inspector.SystemData{
				Services:   map[string]string{},
				MemTotalMB: tt.total,
				MemUsedMB:  tt.used,
			}
			data := makeCluster(map[string]*inspector.NodeData{"1.1.1.1": nd})
			results := CheckSystem(data)
			expectStatus(t, results, "system.memory", tt.status)
		})
	}
}

func TestCheckSystem_Disk(t *testing.T) {
	tests := []struct {
		name   string
		pct    int
		status inspector.Status
	}{
		{"healthy", 60, inspector.StatusPass},
		{"elevated", 85, inspector.StatusWarn},
		{"critical", 92, inspector.StatusFail},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nd := makeNodeData("1.1.1.1", "node")
			nd.System = &inspector.SystemData{
				Services:    map[string]string{},
				DiskUsePct:  tt.pct,
				DiskUsedGB:  "25G",
				DiskTotalGB: "50G",
			}
			data := makeCluster(map[string]*inspector.NodeData{"1.1.1.1": nd})
			results := CheckSystem(data)
			expectStatus(t, results, "system.disk", tt.status)
		})
	}
}

func TestCheckSystem_Load(t *testing.T) {
	tests := []struct {
		name   string
		load   string
		cpus   int
		status inspector.Status
	}{
		{"healthy", "1.0, 0.8, 0.5", 4, inspector.StatusPass},
		{"elevated", "6.0, 5.0, 4.0", 4, inspector.StatusWarn},
		{"overloaded", "10.0, 9.0, 8.0", 4, inspector.StatusFail},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nd := makeNodeData("1.1.1.1", "node")
			nd.System = &inspector.SystemData{
				Services: map[string]string{},
				LoadAvg:  tt.load,
				CPUCount: tt.cpus,
			}
			data := makeCluster(map[string]*inspector.NodeData{"1.1.1.1": nd})
			results := CheckSystem(data)
			expectStatus(t, results, "system.load", tt.status)
		})
	}
}

func TestCheckSystem_OOMKills(t *testing.T) {
	nd := makeNodeData("1.1.1.1", "node")
	nd.System = &inspector.SystemData{Services: map[string]string{}, OOMKills: 3}
	data := makeCluster(map[string]*inspector.NodeData{"1.1.1.1": nd})
	results := CheckSystem(data)
	expectStatus(t, results, "system.oom", inspector.StatusFail)
}

func TestCheckSystem_Inodes(t *testing.T) {
	tests := []struct {
		name   string
		pct    int
		status inspector.Status
	}{
		{"healthy", 50, inspector.StatusPass},
		{"elevated", 82, inspector.StatusWarn},
		{"critical", 96, inspector.StatusFail},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nd := makeNodeData("1.1.1.1", "node")
			nd.System = &inspector.SystemData{Services: map[string]string{}, InodePct: tt.pct}
			data := makeCluster(map[string]*inspector.NodeData{"1.1.1.1": nd})
			results := CheckSystem(data)
			expectStatus(t, results, "system.inodes", tt.status)
		})
	}
}

func TestCheckSystem_ProcessUser(t *testing.T) {
	tests := []struct {
		name   string
		user   string
		status inspector.Status
	}{
		{"correct", "orama", inspector.StatusPass},
		{"root", "root", inspector.StatusWarn},
		{"other", "ubuntu", inspector.StatusWarn},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nd := makeNodeData("1.1.1.1", "node")
			nd.System = &inspector.SystemData{Services: map[string]string{}, ProcessUser: tt.user}
			data := makeCluster(map[string]*inspector.NodeData{"1.1.1.1": nd})
			results := CheckSystem(data)
			expectStatus(t, results, "system.process_user", tt.status)
		})
	}
}

func TestCheckSystem_Panics(t *testing.T) {
	nd := makeNodeData("1.1.1.1", "node")
	nd.System = &inspector.SystemData{Services: map[string]string{}, PanicCount: 5}
	data := makeCluster(map[string]*inspector.NodeData{"1.1.1.1": nd})
	results := CheckSystem(data)
	expectStatus(t, results, "system.panics", inspector.StatusFail)
}

func TestCheckSystem_ExpectedPorts(t *testing.T) {
	nd := makeNodeData("1.1.1.1", "node")
	nd.System = &inspector.SystemData{
		Services:       map[string]string{},
		ListeningPorts: []int{5001, 6001}, // Missing 3322, 4501
	}
	data := makeCluster(map[string]*inspector.NodeData{"1.1.1.1": nd})
	results := CheckSystem(data)

	expectStatus(t, results, "system.port_5001", inspector.StatusPass)
	expectStatus(t, results, "system.port_6001", inspector.StatusPass)
	expectStatus(t, results, "system.port_3322", inspector.StatusWarn)
	expectStatus(t, results, "system.port_4501", inspector.StatusWarn)
}

func TestCheckSystem_NilData(t *testing.T) {
	nd := makeNodeData("1.1.1.1", "node")
	data := makeCluster(map[string]*inspector.NodeData{"1.1.1.1": nd})
	results := CheckSystem(data)
	if len(results) != 0 {
		t.Errorf("expected 0 results for nil System data, got %d", len(results))
	}
}
