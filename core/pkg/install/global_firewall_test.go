package install

import (
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/constants"
)

func TestGlobalFirewall_isTaggedApartFromClusterRules(t *testing.T) {
	cluster := NewFirewallProvisioner(FirewallConfig{})
	for _, rule := range cluster.GenerateRules() {
		if strings.Contains(rule, "31000") || strings.Contains(rule, "31010") || strings.Contains(rule, "31020") {
			t.Errorf("cluster firewall opens a global port: %s", rule)
		}
	}
	if NewFirewallProvisioner(FirewallConfig{}).GlobalAllowArgs() != nil {
		t.Fatal("an empty global section produced rules")
	}

	fp := NewFirewallProvisioner(FirewallConfig{Global: GlobalFirewall{
		ChainP2P: true, PublicStorage: true, Provider: true, TorRelay: true, Dirauth: true,
	}})
	args := fp.GlobalAllowArgs()
	want := map[string]bool{
		"31000/tcp": true, "31000/udp": true,
		"31010/tcp": true, "31010/udp": true,
		"31013/tcp": true,
		"31020/tcp": true,
		"31021/tcp": true,
	}
	if len(args) != len(want) {
		t.Fatalf("%d rules, want %d: %v", len(args), len(want), args)
	}
	for _, argv := range args {
		if len(argv) != 4 || argv[0] != "allow" || argv[2] != "comment" || argv[3] != GlobalRuleComment {
			t.Fatalf("rule %v is not tagged %s", argv, GlobalRuleComment)
		}
		if !want[argv[1]] {
			t.Errorf("unexpected spec %s", argv[1])
		}
		delete(want, argv[1])
		if argv[1] == "31011/tcp" || argv[1] == "31001/tcp" {
			t.Errorf("loopback port %s was published", argv[1])
		}
	}
	if len(want) != 0 {
		t.Errorf("missing specs %v", want)
	}

	// Cluster reconcile only removes comment "orama". A global rule must not
	// match that, or the next cluster reconcile deletes the chain's p2p port.
	status := "31000/tcp                     ALLOW       Anywhere                   # orama-global\n" +
		"22/tcp                        ALLOW       Anywhere                   # orama\n"
	owned := parseOwnedAllowRules(status)
	for _, rule := range owned {
		if strings.Contains(rule, "31000") {
			t.Fatalf("cluster reconcile would remove %q", rule)
		}
	}
	if constants.ChainP2PPort != 31000 || constants.GlobalProviderPort != 31013 {
		t.Fatalf("ports drifted")
	}
}

func TestGlobalAllowArgs_providerAloneOpensOnlyItsPort(t *testing.T) {
	fp := NewFirewallProvisioner(FirewallConfig{Global: GlobalFirewall{Provider: true}})
	args := fp.GlobalAllowArgs()
	if len(args) != 1 || args[0][1] != "31013/tcp" {
		t.Fatalf("provider rules = %v, want only 31013/tcp", args)
	}
	fp = NewFirewallProvisioner(FirewallConfig{Global: GlobalFirewall{PublicStorage: true}})
	for _, argv := range fp.GlobalAllowArgs() {
		if argv[1] == "31013/tcp" {
			t.Fatal("the public Kubo swarm opened the provider port")
		}
	}
}
