package cli

import "testing"

func TestWireGuardIPFromNodeConfig(t *testing.T) {
	got, err := wireGuardIPFromNodeConfig([]byte(`
database:
  http_adv_address: "10.0.0.1:10100"
  raft_adv_address: "10.0.0.1:10101"
`))
	if err != nil || got != "10.0.0.1" {
		t.Fatalf("got %q err %v", got, err)
	}
}

func TestWireGuardIPFromNodeConfig_ignoresLoopback(t *testing.T) {
	_, err := wireGuardIPFromNodeConfig([]byte("http_adv_address: \"127.0.0.1:10100\"\n"))
	if err == nil {
		t.Fatal("a loopback advertise address was accepted; localhost is where public traffic arrives")
	}
}
