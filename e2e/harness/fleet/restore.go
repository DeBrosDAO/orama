package fleet

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/e2e/harness/config"
	"github.com/DeBrosOfficial/network/pkg/constants"
)

// restoreTest attributes the runner's node restoration in the evidence.
const restoreTest = "(runner: restore after destructive package)"

// restoreScript removes every iptables and ip6tables rule the run tagged
// (comment e2e-<run>-...), proves none is left, removes the files the chain
// packages leave when interrupted (the disk filler on the chain home's
// filesystem, the throwaway keyrings and transaction directories in /tmp,
// all named e2e-chain*-<run>-*), turns NTP back on with the clock set to the
// runner's time (%[2]d) when a test left it off, and fails loudly, with the
// steps to restore it, when a state-sync test left the chain's old data
// directory (%[3]s/data.e2e-<run>) aside. The run id is checked against
// runIDShape before it is put here.
const restoreScript = `set -u
tag='--comment "?e2e-%[1]s-'
for t in iptables ip6tables; do
  command -v "$t-save" >/dev/null 2>&1 || continue
  "$t-save" | grep -E -- "$tag" | sed 's/^-A /-D /' | xargs -r -L1 "$t" -w || exit 1
  if "$t-save" | grep -qE -- "$tag"; then echo "$t still holds rules tagged e2e-%[1]s-" >&2; exit 1; fi
done
mnt=$(df --output=target %[3]s 2>/dev/null | tail -n 1); mnt=${mnt:-/}
find "$mnt" /tmp -xdev -maxdepth 1 \( -name 'e2e-chaindisk-%[1]s-*' -o -name 'e2e-chainkeys-%[1]s-*' -o -name 'e2e-chaintx-%[1]s-*' \) -exec rm -rf -- {} + || exit 1
if [ "$(timedatectl show -p NTP --value)" != yes ]; then
  date -u -s @%[2]d >/dev/null && timedatectl set-ntp true || exit 1
fi
if [ -e %[3]s/data.e2e-%[1]s ]; then
  echo "UNRESTORED STATE-SYNC BACKUP: %[3]s/data.e2e-%[1]s holds this validator's old chain data. To put it back: systemctl stop orama-global-chain.service; copy %[3]s/data/priv_validator_state.json over the backup's (the newer sign state, so it cannot sign a height twice); rm -rf %[3]s/data; mv %[3]s/data.e2e-%[1]s %[3]s/data; systemctl start orama-global-chain.service" >&2
  exit 1
fi`

// RestoreNodes undoes, on every member of the fleet, what a destructive
// package may have left when its own cleanups could not run (a killed test
// binary, a cleanup that failed): the partitions IPTablesBlock inserted and
// a clock ClockSkew moved with NTP off, and the files an interrupted chain
// package leaves (see restoreScript). It runs outside any test; every
// member is attempted and the failures are joined.
func (f *Fleet) RestoreNodes(ctx context.Context) error {
	if !runIDShape(f.State.RunID) {
		return fmt.Errorf("refusing to restore nodes: run id %q is not a run id", f.State.RunID)
	}
	var errs []error
	for _, n := range f.AllNodes() {
		cmd := fmt.Sprintf(restoreScript, f.State.RunID, time.Now().Unix(), constants.ChainHome)
		out, err := f.shellFor(restoreTest, n).Run(ctx, cmd)
		switch {
		case err != nil:
			errs = append(errs, fmt.Errorf("failed to restore %s: %w", n.Name, err))
		case out.Exit != 0:
			errs = append(errs, fmt.Errorf("restoring %s exited %d: %s", n.Name, out.Exit, f.Redact(strings.TrimSpace(out.Stderr))))
		}
	}
	return errors.Join(errs...)
}

// runIDShape is the run id's shape (provision's runIDPattern): lowercase
// letters and digits, or a stagenet run id.
func runIDShape(id string) bool {
	if config.StagenetRunID(id) {
		return true
	}
	if len(id) < 4 || len(id) > 16 {
		return false
	}
	for _, c := range id {
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') {
			return false
		}
	}
	return true
}
