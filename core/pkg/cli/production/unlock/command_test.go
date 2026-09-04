package unlock

import (
	"strings"
	"testing"
)

// The command's first step was GET /v1/agent/genesis-key on the node. The
// OramaOS agent serves /v1/agent/unlock, /v1/agent/command, /status, /health
// and /logs, and has never served that path — so every run failed the fetch,
// waited out a ten-second timeout, and only then said to pass --key-file.

func TestValidate_requiresTheKeyFile(t *testing.T) {
	err := (&Flags{NodeIP: "10.0.0.1"}).validate()
	if err == nil {
		t.Fatal("--key-file must be required: nothing serves the key")
	}
	if !strings.Contains(err.Error(), "key-file") {
		t.Errorf("the error must name the flag: %v", err)
	}
}

func TestValidate_requiresTheNodeIP(t *testing.T) {
	if err := (&Flags{KeyFile: "/tmp/key"}).validate(); err == nil {
		t.Fatal("--node-ip must be required")
	}
}

func TestValidate_acceptsBoth(t *testing.T) {
	if err := (&Flags{NodeIP: "10.0.0.1", KeyFile: "/tmp/key"}).validate(); err != nil {
		t.Errorf("both flags given: %v", err)
	}
}

// A run that reaches the network must not still be trying the endpoint that
// does not exist.
func TestRun_doesNotFetchFromTheNode(t *testing.T) {
	// A missing key file stops the command before anything is sent, which is
	// the point: the failure is local and immediate rather than a timeout.
	err := Run(&Flags{NodeIP: "10.0.0.1", Genesis: true, KeyFile: "/nonexistent/genesis.key"})
	if err == nil {
		t.Fatal("a missing key file must be an error")
	}
	if !strings.Contains(err.Error(), "key file") {
		t.Errorf("the error must be about the key file, not a network timeout: %v", err)
	}
}

// --genesis is the confirmation that this is the genesis node, not a routine
// unlock, so it stays required.
func TestRun_requiresGenesis(t *testing.T) {
	err := Run(&Flags{NodeIP: "10.0.0.1", KeyFile: "/tmp/key"})
	if err == nil || !strings.Contains(err.Error(), "genesis") {
		t.Errorf("--genesis must be required: %v", err)
	}
}
