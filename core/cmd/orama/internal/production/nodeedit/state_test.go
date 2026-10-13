package nodeedit

import (
	"os/exec"
	"strings"
	"testing"
)

func TestParseState(t *testing.T) {
	tests := []struct {
		name    string
		out     string
		want    NodeState
		wantErr string
	}{
		{"full node", "global=yes ipfs=yes relay=yes exit=no storagemax=55GB\n", NodeState{Global: true, IPFS: true, Relay: true, StorageMax: "55GB"}, ""},
		{"exit relay", "global=yes ipfs=no relay=yes exit=yes storagemax=none\n", NodeState{Global: true, Relay: true, Exit: true, StorageMax: "none"}, ""},
		{"cluster only", "global=no ipfs=no relay=no exit=no storagemax=none\n", NodeState{StorageMax: "none"}, ""},
		{"empty", "", NodeState{}, "has 0 fields"},
		{"missing a field", "global=yes ipfs=yes relay=yes exit=no\n", NodeState{}, "has 4 fields"},
		{"not a flag", "global=maybe ipfs=no relay=no exit=no storagemax=none\n", NodeState{}, `global="maybe"`},
		{"a word without a value", "global=yes ipfs relay=no exit=no storagemax=none\n", NodeState{}, "unexpected word"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseState(tt.out)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want it to contain %q", err, tt.wantErr)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("parseState = %+v, %v; want %+v", got, err, tt.want)
			}
		})
	}
}

// The script is what a node runs as root; it has to be valid, and on a machine
// with none of the global layer it must say so rather than fail.
func TestStateScript_onAMachineWithoutTheGlobalLayer(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not available")
	}
	out, err := exec.Command(bash, "-c", stateScript).Output()
	if err != nil {
		t.Fatalf("the state script failed: %v", err)
	}
	st, err := parseState(string(out))
	if err != nil {
		t.Fatalf("parseState(%q): %v", out, err)
	}
	if st.Global || st.IPFS || st.Relay || st.Exit || st.StorageMax != "none" {
		t.Errorf("state = %+v, want the empty state of a machine without the global layer", st)
	}
}
