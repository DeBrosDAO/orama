package inspector

import (
	"fmt"
	"sort"
	"strings"
)

// subsystemAll and subsystemWGAlias are the two names a subsystem list may use
// that no checker is registered under: every checker, and wireguard's short name.
const (
	subsystemAll     = "all"
	subsystemWGAlias = "wg"
)

// ValidateSubsystems refuses a subsystem list naming something no checker is
// registered for. Without it a typo selects nothing, and the run collects from
// every node, checks nothing, and reports success.
//
// An empty list means every subsystem, as RunChecks reads it.
func ValidateSubsystems(subsystems []string) error {
	for _, s := range subsystems {
		if s == subsystemAll || s == subsystemWGAlias {
			continue
		}
		if _, ok := SubsystemCheckers[s]; !ok {
			names := make([]string, 0, len(SubsystemCheckers)+2)
			for name := range SubsystemCheckers {
				names = append(names, name)
			}
			names = append(names, subsystemAll, subsystemWGAlias)
			sort.Strings(names)
			return fmt.Errorf("unknown subsystem %q (one of: %s)", s, strings.Join(names, ", "))
		}
	}
	return nil
}
