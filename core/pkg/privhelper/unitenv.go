package privhelper

import (
	"fmt"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/unitenv"
)

// ToolUnitEnv stores namespace units' env files in the root-owned tree
// pkg/unitenv describes.
const ToolUnitEnv = "unitenv"

const (
	unitEnvSet   = "set"   // set <namespace> <service>, the file on input
	unitEnvClear = "clear" // clear <namespace>
)

// noUnitEnv are the services whose units run as a user other than orama (tor
// as debian-tor, ntfy as ntfy) or as root (wireguard). They read no env file;
// writing one would let the orama user set LD_PRELOAD and the like for a
// process it does not own.
var noUnitEnv = map[string]bool{"tor": true, "ntfy": true, "wireguard": true}

func validateUnitEnv(args []string) error {
	switch {
	case len(args) == 3 && args[0] == unitEnvSet:
		if !unitenv.Valid(args[1], args[2]) {
			return fmt.Errorf("unit env %q/%q is not valid", args[1], args[2])
		}
		if noUnitEnv[args[2]] {
			return fmt.Errorf("unit env for %q is not allowed: its unit does not run as orama", args[2])
		}
		return nil
	case len(args) == 2 && args[0] == unitEnvClear:
		if !unitenv.Valid(args[1], "x") {
			return fmt.Errorf("namespace %q is not valid", args[1])
		}
		return nil
	default:
		return fmt.Errorf("unitenv %q is not allowed", args)
	}
}

// SetUnitEnv stores the env file of service in namespace through the helper.
func SetUnitEnv(namespace, service, contents string) error {
	cmd := Command(ToolUnitEnv, unitEnvSet, namespace, service)
	cmd.Stdin = strings.NewReader(contents)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("store env of %s/%s: %w: %s", namespace, service, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// ClearUnitEnv removes every env file of namespace through the helper.
func ClearUnitEnv(namespace string) error {
	if out, err := Command(ToolUnitEnv, unitEnvClear, namespace).CombinedOutput(); err != nil {
		return fmt.Errorf("clear env files of %s: %w: %s", namespace, err, strings.TrimSpace(string(out)))
	}
	return nil
}
