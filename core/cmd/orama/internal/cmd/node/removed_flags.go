package node

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// removedAnyoneFlagPrefix matches cobra's error for any of the --anyone-*
// options install and upgrade used to take (--anyone-client, --anyone-relay,
// --anyone-migrate and the relay's nickname/wallet/contact/family settings).
const removedAnyoneFlagPrefix = "unknown flag: --anyone-"

// removedAnyoneReason says what replaced the Anyone flags.
const removedAnyoneReason = "the Anyone network was removed; the Tor client is installed on every node automatically, so drop the flag"

// explainRemovedFlags keeps an old script that still passes an --anyone-*
// flag failing, and tells its operator why: the Anyone network was replaced by
// a Tor client that every node installs, so there is nothing left to choose.
func explainRemovedFlags(_ *cobra.Command, err error) error {
	if strings.HasPrefix(err.Error(), removedAnyoneFlagPrefix) {
		return fmt.Errorf("%w: %s", err, removedAnyoneReason)
	}
	return err
}

// upgradeReexecAnyoneClientFlag is the one --anyone-* flag upgrade still
// registers, hidden. The upgrade that introduces Tor is started by the OLD
// binary, which accepts --anyone-client, swaps in the new binary and re-execs
// it with its own argv. Rejecting the flag there would abort the upgrade with
// the node's services already stopped, so the re-exec may carry it; an
// operator may not.
const upgradeReexecAnyoneClientFlag = "anyone-client"

// checkUpgradeAnyoneClient refuses --anyone-client unless it arrived through
// the post-swap re-exec.
func checkUpgradeAnyoneClient(passed, reexeced bool) error {
	if passed && !reexeced {
		return fmt.Errorf("--%s: %s", upgradeReexecAnyoneClientFlag, removedAnyoneReason)
	}
	return nil
}
