package provision

import (
	"context"
	"fmt"
	"strings"

	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/e2e/harness/hetzner"
	"github.com/DeBrosOfficial/network/e2e/harness/sshx"
)

// Files BreakUpgrade and RestoreUpgrade use on the node.
const (
	// stagedSignature is the signature of the build staged in /opt/orama;
	// `orama node upgrade` verifies it before it stops anything.
	stagedSignature = "/opt/orama/manifest.sig"
	// breakBackupDir holds the real signature while it is broken.
	breakBackupDir = "/var/lib/orama-e2e/break-upgrade"
	// breakMarker replaces the signature: not hex, so it can never verify.
	breakMarker = "e2e-break-upgrade"
)

// breakScript backs up the staged signature and replaces it with the marker.
// Running it on a node already broken by it changes nothing.
var breakScript = fmt.Sprintf(`set -eu
sig=%s; dir=%s; mark=%s
if [ -f "$dir/manifest.sig" ] && [ "$(cat "$sig")" = "$mark" ]; then exit 0; fi
[ -f "$sig" ] && [ ! -L "$sig" ] || { echo "no staged $sig to break" >&2; exit 3; }
[ ! -e "$dir" ] || { echo "$dir exists but $sig is not broken: restore or remove it first" >&2; exit 4; }
install -d -m 0700 "$dir"
cp -p "$sig" "$dir/manifest.sig"
printf '%%s\n' "$mark" > "$sig"`, stagedSignature, breakBackupDir, breakMarker)

// restoreScript puts the backed-up signature back, only over the marker.
var restoreScript = fmt.Sprintf(`set -eu
sig=%s; dir=%s; mark=%s
[ -f "$dir/manifest.sig" ] || { echo "nothing to restore: $dir/manifest.sig is missing" >&2; exit 3; }
[ "$(cat "$sig")" = "$mark" ] || { echo "$sig changed since it was broken (a push re-staged it?); not restoring over it" >&2; exit 4; }
cp -p "$dir/manifest.sig" "$sig.e2e-restore"
mv -f "$sig.e2e-restore" "$sig"
rm -rf "$dir"`, stagedSignature, breakBackupDir, breakMarker)

// DestroyNode deletes the server at public IP host at once, with no clean
// shutdown: the node vanishes the way a failed machine does. It is dropped
// from st (nodes, extras or probes) and its host key unpinned; the caller
// saves st.
//
// In a feature process (E2E_BROKER_SOCK set) only an extra can be destroyed,
// through the runner's broker.
func DestroyNode(ctx context.Context, st *fleet.State, host string) error {
	b, err := runBroker()
	if err != nil {
		return err
	}
	if b != nil {
		return brokerDestroy(ctx, b, st, host)
	}
	d, err := depsFromEnv()
	if err != nil {
		return err
	}
	return destroyNode(ctx, st, host, d)
}

func destroyNode(ctx context.Context, st *fleet.State, host string, d deps) error {
	list, i, ok := findMember(st, func(n fleet.Node) bool { return n.PublicIP == host })
	if !ok {
		return fmt.Errorf("%s is not a server of run %s", host, st.RunID)
	}
	n := (*list)[i]
	if err := deleteOwnedServer(ctx, d, st.RunID, n.ServerID, ""); err != nil {
		return err
	}
	if err := waitServerGone(ctx, d, hetzner.Server{ID: n.ServerID, Name: n.Name}); err != nil {
		return err
	}
	*list = append((*list)[:i], (*list)[i+1:]...)
	return sshx.RemoveKnownHost(st.KnownHostsFile, host)
}

// findMember locates the first fleet member matching match, returning its
// list and index.
func findMember(st *fleet.State, match func(fleet.Node) bool) (*[]fleet.Node, int, bool) {
	for _, list := range []*[]fleet.Node{&st.Nodes, &st.Extras, &st.Probes} {
		for i, n := range *list {
			if match(n) {
				return list, i, true
			}
		}
	}
	return nil, 0, false
}

// BreakUpgrade makes the next `orama node upgrade` of the node at host fail,
// reversibly: the staged build's manifest.sig is copied to
// /var/lib/orama-e2e/break-upgrade/ on the node and replaced with a marker
// that cannot verify, so the upgrade refuses the archive before it stops any
// service. The undo information lives on the node itself; RestoreUpgrade
// puts the signature back. A push after BreakUpgrade re-stages a valid
// signature, which RestoreUpgrade then refuses to overwrite.
func BreakUpgrade(ctx context.Context, st *fleet.State, host string) error {
	return runOnMember(ctx, st, host, breakScript, sshxRemote{}, defaultTiming())
}

// RestoreUpgrade undoes BreakUpgrade on the node at host.
func RestoreUpgrade(ctx context.Context, st *fleet.State, host string) error {
	return runOnMember(ctx, st, host, restoreScript, sshxRemote{}, defaultTiming())
}

func runOnMember(ctx context.Context, st *fleet.State, host, script string, rem remote, tm timing) error {
	list, i, ok := findMember(st, func(n fleet.Node) bool { return n.PublicIP == host })
	if !ok {
		return fmt.Errorf("%s is not a server of run %s", host, st.RunID)
	}
	n := (*list)[i]
	if n.SSHUser != sshUser {
		return fmt.Errorf("%s logs in as %s; changing /opt/orama needs %s", host, n.SSHUser, sshUser)
	}
	rctx, cancel := context.WithTimeout(ctx, tm.remoteAction)
	defer cancel()
	_, errOut, exit, err := rem.Run(rctx, target(st, n), script)
	if err != nil {
		return err
	}
	if exit != 0 {
		return fmt.Errorf("on %s (exit %d): %s", host, exit, strings.TrimSpace(errOut))
	}
	return nil
}
