package tornet

import (
	"errors"
	"strings"
	"testing"
)

// The exit that stagenet's first bring-up ran (policy lines as they were
// rendered then): refused 224.0.0.0/4 and 240.0.0.0/4 beside the other
// reserved ranges. The authorities summarised it "reject 1-65535" and a client
// of the network built no exit circuit.
func legacyExitPolicy() []string {
	policy := ExitPolicyLines(nil)
	at := len(policy) - 1 - len(exitRejectPorts)
	legacy := append([]string{}, policy[:at]...)
	legacy = append(legacy, "ExitPolicy reject 224.0.0.0/4:*", "ExitPolicy reject 240.0.0.0/4:*")
	return append(legacy, policy[at:]...)
}

func TestCheckExitSummary_policyOfTheFirstBringUpAcceptsNoPort(t *testing.T) {
	if err := checkExitSummary(legacyExitPolicy()); !errors.Is(err, errNoSummaryPort) {
		t.Fatalf("the policy with multicast and class E refused = %v, want errNoSummaryPort", err)
	}
}

func TestCheckExitSummary_renderedPolicyAcceptsPorts(t *testing.T) {
	policy := ExitPolicyLines(nil)
	if err := checkExitSummary(policy); err != nil {
		t.Fatalf("the exit policy ExitPolicyLines renders: %v", err)
	}
	for _, line := range policy {
		for _, r := range exitPolicyUnroutable {
			if strings.Contains(line, r) {
				t.Errorf("%q: %s is in the policy, which makes Tor's summary refuse every port", line, r)
			}
		}
	}
}

func TestCheckExitSummary_cutoffIsTwoSlash8Blocks(t *testing.T) {
	accept := "ExitPolicy accept *:*"
	cases := []struct {
		name   string
		policy []string
		ok     bool
	}{
		{"nothing refused", []string{accept}, true},
		{"two /8 blocks on every port", []string{"ExitPolicy reject 4.0.0.0/7:*", accept}, true},
		{"three /8 blocks on every port", []string{"ExitPolicy reject 4.0.0.0/7:*", "ExitPolicy reject 8.0.0.0/8:*", accept}, false},
		{"everything", []string{"ExitPolicy reject *:*", accept}, false},
		{"one big block on one port, the rest open", []string{"ExitPolicy reject 0.0.0.0/1:80", accept}, true},
		{"private ranges are not counted", []string{"ExitPolicy reject 10.0.0.0/8:*", "ExitPolicy reject 172.16.0.0/12:*", "ExitPolicy reject 192.168.0.0/16:*", "ExitPolicy reject 127.0.0.0/8:*", "ExitPolicy reject 0.0.0.0/8:*", "ExitPolicy reject 169.254.0.0/16:*", "ExitPolicy reject 4.0.0.0/7:*", accept}, true},
		{"a range next to a private one is counted", []string{"ExitPolicy reject 10.0.0.0/7:*", "ExitPolicy reject 12.0.0.0/8:*", accept}, false},
		{"no accept rule", []string{"ExitPolicy reject 1.2.3.4:*"}, false},
		{"empty", nil, false},
	}
	for _, c := range cases {
		if err := checkExitSummary(c.policy); (err == nil) != c.ok {
			t.Errorf("%s: %v, want ok=%v", c.name, err, c.ok)
		}
	}
}

func TestCheckExitSummary_portsRefusedForEveryAddressStayRefused(t *testing.T) {
	// Every port but one is refused for all addresses: the summary still accepts that one.
	policy := []string{"ExitPolicy reject *:1-442", "ExitPolicy reject *:444-65535", "ExitPolicy accept *:*"}
	if err := checkExitSummary(policy); err != nil {
		t.Fatalf("port 443 is the only open port: %v", err)
	}
	policy = []string{"ExitPolicy reject *:1-65535", "ExitPolicy accept *:*"}
	if err := checkExitSummary(policy); !errors.Is(err, errNoSummaryPort) {
		t.Fatalf("every port refused = %v", err)
	}
}

func TestRelayTorrc_exitWhoseRejectListLeavesNoPortIsRefused(t *testing.T) {
	network := testNetwork()
	network.AllowExit = true
	c := RelayConfig{
		Network: network, Home: "/var/lib/orama-global/tor-relay", Nickname: "OramaExit", Contact: "ops@example.org",
		Address: "192.5.5.241", ORPort: 31020, Exit: true,
		ExitReject: []string{"0.0.0.0/1:*"},
	}
	if _, err := RelayTorrc(c); !errors.Is(err, errNoSummaryPort) {
		t.Fatalf("an exit refusing half the address space = %v, want errNoSummaryPort", err)
	}
	c.ExitReject = []string{"203.0.113.9:*", "198.51.100.0/24:443"}
	if _, err := RelayTorrc(c); err != nil {
		t.Fatalf("an ordinary reject list: %v", err)
	}
	c.Exit, c.ExitReject = false, nil
	if _, err := RelayTorrc(c); err != nil {
		t.Fatalf("a relay that is not an exit has no exit policy to summarise: %v", err)
	}
}
