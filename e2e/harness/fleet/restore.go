package fleet

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/e2e/harness/config"
)

// restoreTest attributes the runner's node restoration in the evidence.
const restoreTest = "(runner: restore after destructive package)"

// restoreScript removes every iptables and ip6tables rule the run tagged
// (comment e2e-<run>-...), proves none is left, and turns NTP back on with
// the clock set to the runner's time (%[2]d) when a test left it off. The
// run id is checked against runIDShape before it is put here.
const restoreScript = `set -u
tag='--comment "?e2e-%[1]s-'
for t in iptables ip6tables; do
  command -v "$t-save" >/dev/null 2>&1 || continue
  "$t-save" | grep -E -- "$tag" | sed 's/^-A /-D /' | xargs -r -L1 "$t" -w || exit 1
  if "$t-save" | grep -qE -- "$tag"; then echo "$t still holds rules tagged e2e-%[1]s-" >&2; exit 1; fi
done
if [ "$(timedatectl show -p NTP --value)" != yes ]; then
  date -u -s @%[2]d >/dev/null && timedatectl set-ntp true || exit 1
fi`

// RestoreNodes undoes, on every member of the fleet, what a destructive
// package may have left when its own cleanups could not run (a killed test
// binary, a cleanup that failed): the partitions IPTablesBlock inserted and
// a clock ClockSkew moved with NTP off. It runs outside any test; every
// member is attempted and the failures are joined.
func (f *Fleet) RestoreNodes(ctx context.Context) error {
	if !runIDShape(f.State.RunID) {
		return fmt.Errorf("refusing to restore nodes: run id %q is not a run id", f.State.RunID)
	}
	var errs []error
	for _, n := range f.AllNodes() {
		cmd := fmt.Sprintf(restoreScript, f.State.RunID, time.Now().Unix())
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
