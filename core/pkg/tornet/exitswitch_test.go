package tornet

import (
	"strings"
	"testing"
)

func exitNetwork(allow bool) Network {
	n := testNetwork()
	n.AllowExit = allow
	return n
}

func plainRelayTorrc(t *testing.T) string {
	t.Helper()
	got, err := RelayTorrc(relayConfig())
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestSetExit_matchesWhatAFreshInstallRenders(t *testing.T) {
	cfg := relayConfig()
	cfg.Network = exitNetwork(true)
	cfg.Exit, cfg.ExitReject = true, []string{"203.0.113.0/24"}
	want, err := RelayTorrc(cfg)
	if err != nil {
		t.Fatal(err)
	}

	got, err := SetExit(plainRelayTorrc(t), true, []string{"203.0.113.0/24"}, exitNetwork(true))
	if err != nil {
		t.Fatalf("SetExit: %v", err)
	}

	if got != want {
		t.Errorf("switching a plain relay to an exit differs from installing an exit:\n--- got\n%s\n--- want\n%s", got, want)
	}
}

func TestSetExit_backToARelayIsTheOriginalTorrc(t *testing.T) {
	plain := plainRelayTorrc(t)
	exit, err := SetExit(plain, true, nil, exitNetwork(true))
	if err != nil {
		t.Fatal(err)
	}

	back, err := SetExit(exit, false, nil, exitNetwork(true))
	if err != nil {
		t.Fatalf("SetExit: %v", err)
	}

	if back != plain {
		t.Errorf("exit then relay again changed the torrc:\n%s", back)
	}
}

func TestSetExit_refusesAnExitOnANetworkThatForbidsIt(t *testing.T) {
	_, err := SetExit(plainRelayTorrc(t), true, nil, exitNetwork(false))
	if err == nil || !strings.Contains(err.Error(), "allow_exit false") {
		t.Fatalf("err = %v, want the network file's refusal", err)
	}
}

func TestSetExit_refusesARejectListOnANonExit(t *testing.T) {
	_, err := SetExit(plainRelayTorrc(t), false, []string{"203.0.113.0/24"}, exitNetwork(true))
	if err == nil || !strings.Contains(err.Error(), "needs the exit role") {
		t.Fatalf("err = %v", err)
	}
}

func TestSetExit_refusesABadRejectRule(t *testing.T) {
	_, err := SetExit(plainRelayTorrc(t), true, []string{"not an address"}, exitNetwork(true))
	if err == nil || !strings.Contains(err.Error(), "exit reject rule") {
		t.Fatalf("err = %v", err)
	}
}

func TestSetExit_refusesADirectoryAuthority(t *testing.T) {
	_, err := SetExit("AuthoritativeDirectory 1\nExitRelay 0\n", true, nil, exitNetwork(true))
	if err == nil || !strings.Contains(err.Error(), "directory authority") {
		t.Fatalf("err = %v", err)
	}
}

func TestSetExit_aTorrcWithoutAnExitSectionIsRefused(t *testing.T) {
	_, err := SetExit("DataDirectory /x\n", true, nil, exitNetwork(true))
	if err == nil || !strings.Contains(err.Error(), "no ExitRelay line") {
		t.Fatalf("err = %v", err)
	}
}
