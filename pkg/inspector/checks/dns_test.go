package checks

import (
	"testing"

	"github.com/DeBrosOfficial/network/pkg/inspector"
)

func TestCheckDNS_CoreDNSInactive(t *testing.T) {
	nd := makeNodeData("5.5.5.5", "nameserver-ns1")
	nd.DNS = &inspector.DNSData{CoreDNSActive: false}

	data := makeCluster(map[string]*inspector.NodeData{"5.5.5.5": nd})
	results := CheckDNS(data)

	expectStatus(t, results, "dns.coredns_active", inspector.StatusFail)
	// Early return — no port checks
	if findCheck(results, "dns.port_53") != nil {
		t.Error("should not check ports when CoreDNS inactive")
	}
}

func TestCheckDNS_HealthyNode(t *testing.T) {
	nd := makeNodeData("5.5.5.5", "nameserver-ns1")
	nd.DNS = &inspector.DNSData{
		CoreDNSActive:    true,
		CaddyActive:      true,
		Port53Bound:      true,
		Port80Bound:      true,
		Port443Bound:     true,
		CoreDNSMemMB:     50,
		CoreDNSRestarts:  0,
		LogErrors:        0,
		CorefileExists:   true,
		SOAResolves:      true,
		NSResolves:       true,
		NSRecordCount:    3,
		WildcardResolves: true,
		BaseAResolves:    true,
		BaseTLSDaysLeft:  60,
		WildTLSDaysLeft:  60,
	}

	data := makeCluster(map[string]*inspector.NodeData{"5.5.5.5": nd})
	results := CheckDNS(data)

	expectStatus(t, results, "dns.coredns_active", inspector.StatusPass)
	expectStatus(t, results, "dns.caddy_active", inspector.StatusPass)
	expectStatus(t, results, "dns.port_53", inspector.StatusPass)
	expectStatus(t, results, "dns.port_80", inspector.StatusPass)
	expectStatus(t, results, "dns.port_443", inspector.StatusPass)
	expectStatus(t, results, "dns.coredns_memory", inspector.StatusPass)
	expectStatus(t, results, "dns.coredns_restarts", inspector.StatusPass)
	expectStatus(t, results, "dns.coredns_log_errors", inspector.StatusPass)
	expectStatus(t, results, "dns.corefile_exists", inspector.StatusPass)
	expectStatus(t, results, "dns.soa_resolves", inspector.StatusPass)
	expectStatus(t, results, "dns.ns_resolves", inspector.StatusPass)
	expectStatus(t, results, "dns.wildcard_resolves", inspector.StatusPass)
	expectStatus(t, results, "dns.base_a_resolves", inspector.StatusPass)
	expectStatus(t, results, "dns.tls_base", inspector.StatusPass)
	expectStatus(t, results, "dns.tls_wildcard", inspector.StatusPass)
}

func TestCheckDNS_PortsFailing(t *testing.T) {
	nd := makeNodeData("5.5.5.5", "nameserver-ns1")
	nd.DNS = &inspector.DNSData{
		CoreDNSActive: true,
		Port53Bound:   false,
		Port80Bound:   false,
		Port443Bound:  false,
	}
	data := makeCluster(map[string]*inspector.NodeData{"5.5.5.5": nd})
	results := CheckDNS(data)
	expectStatus(t, results, "dns.port_53", inspector.StatusFail)
	expectStatus(t, results, "dns.port_80", inspector.StatusWarn)
	expectStatus(t, results, "dns.port_443", inspector.StatusFail)
}

func TestCheckDNS_Memory(t *testing.T) {
	tests := []struct {
		name   string
		memMB  int
		status inspector.Status
	}{
		{"healthy", 50, inspector.StatusPass},
		{"elevated", 150, inspector.StatusWarn},
		{"high", 250, inspector.StatusFail},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nd := makeNodeData("5.5.5.5", "nameserver-ns1")
			nd.DNS = &inspector.DNSData{CoreDNSActive: true, CoreDNSMemMB: tt.memMB}
			data := makeCluster(map[string]*inspector.NodeData{"5.5.5.5": nd})
			results := CheckDNS(data)
			expectStatus(t, results, "dns.coredns_memory", tt.status)
		})
	}
}

func TestCheckDNS_Restarts(t *testing.T) {
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
			nd := makeNodeData("5.5.5.5", "nameserver-ns1")
			nd.DNS = &inspector.DNSData{CoreDNSActive: true, CoreDNSRestarts: tt.restarts}
			data := makeCluster(map[string]*inspector.NodeData{"5.5.5.5": nd})
			results := CheckDNS(data)
			expectStatus(t, results, "dns.coredns_restarts", tt.status)
		})
	}
}

func TestCheckDNS_LogErrors(t *testing.T) {
	tests := []struct {
		name   string
		errors int
		status inspector.Status
	}{
		{"none", 0, inspector.StatusPass},
		{"few", 3, inspector.StatusWarn},
		{"many", 10, inspector.StatusFail},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nd := makeNodeData("5.5.5.5", "nameserver-ns1")
			nd.DNS = &inspector.DNSData{CoreDNSActive: true, LogErrors: tt.errors}
			data := makeCluster(map[string]*inspector.NodeData{"5.5.5.5": nd})
			results := CheckDNS(data)
			expectStatus(t, results, "dns.coredns_log_errors", tt.status)
		})
	}
}

func TestCheckDNS_TLSExpiry(t *testing.T) {
	tests := []struct {
		name   string
		days   int
		status inspector.Status
	}{
		{"healthy", 60, inspector.StatusPass},
		{"expiring soon", 20, inspector.StatusWarn},
		{"critical", 3, inspector.StatusFail},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nd := makeNodeData("5.5.5.5", "nameserver-ns1")
			nd.DNS = &inspector.DNSData{
				CoreDNSActive:   true,
				BaseTLSDaysLeft: tt.days,
				WildTLSDaysLeft: tt.days,
			}
			data := makeCluster(map[string]*inspector.NodeData{"5.5.5.5": nd})
			results := CheckDNS(data)
			expectStatus(t, results, "dns.tls_base", tt.status)
			expectStatus(t, results, "dns.tls_wildcard", tt.status)
		})
	}
}

func TestCheckDNS_TLSNotChecked(t *testing.T) {
	nd := makeNodeData("5.5.5.5", "nameserver-ns1")
	nd.DNS = &inspector.DNSData{
		CoreDNSActive:   true,
		BaseTLSDaysLeft: -1,
		WildTLSDaysLeft: -1,
	}
	data := makeCluster(map[string]*inspector.NodeData{"5.5.5.5": nd})
	results := CheckDNS(data)
	// TLS checks should not be emitted when days == -1
	if findCheck(results, "dns.tls_base") != nil {
		t.Error("should not emit tls_base when days == -1")
	}
}

func TestCheckDNS_ResolutionFailures(t *testing.T) {
	nd := makeNodeData("5.5.5.5", "nameserver-ns1")
	nd.DNS = &inspector.DNSData{
		CoreDNSActive:    true,
		SOAResolves:      false,
		NSResolves:       false,
		WildcardResolves: false,
		BaseAResolves:    false,
	}
	data := makeCluster(map[string]*inspector.NodeData{"5.5.5.5": nd})
	results := CheckDNS(data)
	expectStatus(t, results, "dns.soa_resolves", inspector.StatusFail)
	expectStatus(t, results, "dns.ns_resolves", inspector.StatusFail)
	expectStatus(t, results, "dns.wildcard_resolves", inspector.StatusFail)
	expectStatus(t, results, "dns.base_a_resolves", inspector.StatusWarn)
}

func TestCheckDNS_CrossNode_AllActive(t *testing.T) {
	nodes := map[string]*inspector.NodeData{}
	for _, host := range []string{"5.5.5.5", "6.6.6.6", "7.7.7.7"} {
		nd := makeNodeData(host, "nameserver-ns1")
		nd.DNS = &inspector.DNSData{CoreDNSActive: true}
		nodes[host] = nd
	}
	data := makeCluster(nodes)
	results := CheckDNS(data)
	expectStatus(t, results, "dns.all_ns_active", inspector.StatusPass)
}

func TestCheckDNS_CrossNode_PartialActive(t *testing.T) {
	nodes := map[string]*inspector.NodeData{}
	active := []bool{true, true, false}
	for i, host := range []string{"5.5.5.5", "6.6.6.6", "7.7.7.7"} {
		nd := makeNodeData(host, "nameserver-ns1")
		nd.DNS = &inspector.DNSData{CoreDNSActive: active[i]}
		nodes[host] = nd
	}
	data := makeCluster(nodes)
	results := CheckDNS(data)
	expectStatus(t, results, "dns.all_ns_active", inspector.StatusFail)
}

func TestCheckDNS_NilData(t *testing.T) {
	nd := makeNodeData("5.5.5.5", "nameserver-ns1")
	data := makeCluster(map[string]*inspector.NodeData{"5.5.5.5": nd})
	results := CheckDNS(data)
	if len(results) != 0 {
		t.Errorf("expected 0 results for nil DNS data, got %d", len(results))
	}
}
