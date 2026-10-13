// Package nodeedit is `orama edit`: change a node that is already installed,
// without installing it again. Each change maps onto a step that exists: the
// declared storage capacity is a chain transaction (MsgDeclareCapacity) plus the
// size of the node's public Kubo, and the exit role is the exit section of the
// node's Tor relay. What has no safe edit (turning the global layer off or on)
// is refused, with the command that does it.
package nodeedit

import (
	"fmt"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/constants"
)

const (
	// unitDir is where the global layer's units are installed.
	unitDir = "/etc/systemd/system"
	// ipfsConfig and relayTorrc are the public Kubo's config and the relay's torrc.
	ipfsConfig  = constants.GlobalStateRoot + "/ipfs/config"
	relayTorrc  = constants.GlobalStateRoot + "/tor-relay.torrc"
	yes, no     = "yes", "no"
	stateFields = 5
)

// stateScript prints what a node has of the global layer, one key=value per
// word. It reads files and unit names only.
var stateScript = fmt.Sprintf(`g=no; ls %[1]s/orama-global-*.service >/dev/null 2>&1 && g=yes
i=no; [ -e %[1]s/%[2]s ] && i=yes
r=no; [ -e %[1]s/%[3]s ] && r=yes
e=no; grep -qx 'ExitRelay 1' %[4]s 2>/dev/null && e=yes
s=$(grep -o '"StorageMax": *"[^"]*"' %[5]s 2>/dev/null | head -1 | sed 's/.*: *"//; s/"$//')
echo "global=$g ipfs=$i relay=$r exit=$e storagemax=${s:-none}"
`, unitDir, constants.GlobalIPFSUnit, constants.GlobalTorRelayUnit, relayTorrc, ipfsConfig)

// NodeState is what `orama edit` can see of a node.
type NodeState struct {
	// Global is true when the node runs units of the global layer.
	Global bool
	// IPFS is true when it has the public Kubo, whose size is its storage.
	IPFS bool
	// Relay is true when it has a Tor relay; Exit when the relay is an exit.
	Relay, Exit bool
	// StorageMax is the public Kubo's StorageMax ("55GB"), "none" without one.
	StorageMax string
}

// parseState reads stateScript's output.
func parseState(out string) (NodeState, error) {
	kv := map[string]string{}
	for _, word := range strings.Fields(out) {
		k, v, ok := strings.Cut(word, "=")
		if !ok {
			return NodeState{}, fmt.Errorf("unexpected word %q in the node's state %q", word, strings.TrimSpace(out))
		}
		kv[k] = v
	}
	if len(kv) != stateFields {
		return NodeState{}, fmt.Errorf("the node's state %q has %d fields, want %d", strings.TrimSpace(out), len(kv), stateFields)
	}
	flags := map[string]bool{}
	for _, k := range []string{"global", "ipfs", "relay", "exit"} {
		switch kv[k] {
		case yes:
			flags[k] = true
		case no:
			flags[k] = false
		default:
			return NodeState{}, fmt.Errorf("the node's state has %s=%q, want yes or no", k, kv[k])
		}
	}
	return NodeState{Global: flags["global"], IPFS: flags["ipfs"], Relay: flags["relay"], Exit: flags["exit"], StorageMax: kv["storagemax"]}, nil
}
