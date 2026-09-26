// Package privhelper is the one way an unprivileged Orama process runs a
// command as root.
//
// orama-node and the gateway run as the "orama" user. The few root actions they
// need — managing Orama's own systemd units and opening TURN ports in UFW —
// used to be granted by sudoers rules with wildcard arguments
// (`systemctl start orama-namespace-*`, `ufw allow *`). Two things were wrong
// with that. sudo-rs, the default sudo on Ubuntu 26.04, refuses wildcards in
// command arguments outright, so the rules did not load there. And sudo's own
// documentation warns that `*` in an argument matches spaces and further
// arguments, so `ufw allow *` granted any ufw rule at all.
//
// Neither sudo nor sudoers is involved any more: sudo cannot gain root inside
// a unit that has no_new_privs, which systemd implies for orama-node's
// sandboxing. orama-privhelper is a socket-activated root service
// (orama-privhelper.socket, root:orama 0660); callers hand it a request
// through `orama-privhelper call`. Everything it will do is decided here, by
// exact parsing of each argument, and anything else is refused before a
// process is started.
package privhelper

import (
	"fmt"
	"regexp"
	"strconv"
)

// Path is where the installer puts the helper binary.
const Path = "/usr/local/bin/orama-privhelper"

// Tools the helper runs.
const (
	ToolSystemctl = "systemctl"
	ToolUFW       = "ufw"
)

// Invocation is a validated command: a tool and its argv, nothing else.
type Invocation struct {
	Tool string
	Args []string
}

var (
	// orama-namespace-<service>@<namespace>[.service|.timer]
	namespaceUnit = regexp.MustCompile(`^orama-namespace-[a-z][a-z0-9-]{0,31}@[A-Za-z0-9][A-Za-z0-9_-]{0,63}(\.service|\.timer)?$`)
	// orama-deploy-<runtime>@<namespace>-<name>.service
	deployUnit = regexp.MustCompile(`^orama-deploy-[a-z][a-z0-9]{0,15}@[A-Za-z0-9][A-Za-z0-9_-]{0,160}\.service$`)
	memoryMax  = regexp.MustCompile(`^MemoryMax=[1-9][0-9]{0,11}[KMGT]?$`)
	cpuQuota   = regexp.MustCompile(`^CPUQuota=[1-9][0-9]{0,5}%$`)
	portSpec   = regexp.MustCompile(`^([0-9]{1,5})(?::([0-9]{1,5}))?/(tcp|udp)$`)
)

// hostTURNUnit is the shared, host-level TURN server (not a namespace instance).
const hostTURNUnit = "orama-turn.service"

// wgQuickUnit is the pre-namespace WireGuard unit.
const wgQuickUnit = "wg-quick@wg0.service"

// legacyUnits are the pre-namespace host units that the index migration stops
// and disables once their @index replacement runs. They may only be stopped or
// disabled, never started.
var legacyUnits = map[string]bool{
	"orama-olric.service":        true,
	"orama-ipfs.service":         true,
	"orama-ipfs-cluster.service": true,
	"orama-ipfs-gc.timer":        true,
	"orama-vault.service":        true,
	"orama-sni-router.service":   true,
	"caddy.service":              true,
	"coredns.service":            true,
	"ntfy.service":               true,
	"wg-quick@wg0.service":       true,
}

var unitVerbs = map[string]bool{"start": true, "stop": true, "restart": true, "enable": true, "disable": true}

// Validate parses argv (tool first) and returns the invocation it allows.
func Validate(argv []string) (Invocation, error) {
	if len(argv) == 0 {
		return Invocation{}, fmt.Errorf("no command")
	}
	tool, args := argv[0], argv[1:]
	var err error
	switch tool {
	case ToolSystemctl:
		err = validateSystemctl(args)
	case ToolUFW:
		err = validateUFW(args)
	case ToolWireGuard:
		err = validateWireGuard(args)
	case ToolDeploy:
		err = validateDeploy(args)
	case ToolUnitEnv:
		err = validateUnitEnv(args)
	default:
		err = fmt.Errorf("tool %q is not allowed (allowed: %s, %s, %s, %s, %s)", tool, ToolSystemctl, ToolUFW, ToolWireGuard, ToolDeploy, ToolUnitEnv)
	}
	if err != nil {
		return Invocation{}, err
	}
	return Invocation{Tool: tool, Args: append([]string(nil), args...)}, nil
}

func validateSystemctl(args []string) error {
	if len(args) == 1 && args[0] == "daemon-reload" {
		return nil
	}
	if len(args) == 0 {
		return fmt.Errorf("systemctl: no verb")
	}
	verb := args[0]
	if verb == "set-property" {
		return validateSetProperty(args[1:])
	}
	if !unitVerbs[verb] {
		return fmt.Errorf("systemctl %q is not allowed", verb)
	}
	if len(args) != 2 {
		return fmt.Errorf("systemctl %s takes exactly one unit", verb)
	}
	unit := args[1]
	switch {
	case namespaceUnit.MatchString(unit), deployUnit.MatchString(unit), unit == hostTURNUnit:
		return nil
	case unit == wgQuickUnit && verb != "disable":
		// Stopping it runs wg-quick down and cuts the node off the mesh; the
		// index migration only ever disables it.
		return fmt.Errorf("%s may only be disabled", unit)
	case legacyUnits[unit] && (verb == "stop" || verb == "disable"):
		return nil
	case legacyUnits[unit]:
		return fmt.Errorf("legacy unit %s may only be stopped or disabled", unit)
	default:
		return fmt.Errorf("unit %q is not an Orama unit", unit)
	}
}

// validateSetProperty allows resource limits on a deployment unit only.
func validateSetProperty(args []string) error {
	if len(args) < 2 || len(args) > 3 {
		return fmt.Errorf("systemctl set-property takes a deployment unit and one or two properties")
	}
	if !deployUnit.MatchString(args[0]) {
		return fmt.Errorf("set-property is only allowed on deployment units, not %q", args[0])
	}
	seen := map[string]bool{}
	for _, p := range args[1:] {
		var key string
		switch {
		case memoryMax.MatchString(p):
			key = "MemoryMax"
		case cpuQuota.MatchString(p):
			key = "CPUQuota"
		default:
			return fmt.Errorf("property %q is not allowed (MemoryMax=<n>[KMGT] or CPUQuota=<n>%%)", p)
		}
		if seen[key] {
			return fmt.Errorf("property %s given twice", key)
		}
		seen[key] = true
	}
	return nil
}

// ufwOwnedComment is the tag install.Reconcile recognises (ownedRuleComment).
// The helper allows that word and no other, so a TURN rule can be told apart
// from an operator's rule without letting the caller append arbitrary ufw
// arguments.
const ufwOwnedComment = "orama"

func validateUFW(args []string) error {
	switch {
	case len(args) == 1 && (args[0] == "status" || args[0] == "reload"):
		return nil
	case len(args) == 2 && args[0] == "status" && args[1] == "verbose":
		return nil
	case len(args) == 2 && args[0] == "allow":
		return validatePortSpec(args[1])
	case len(args) == 4 && args[0] == "allow" && args[2] == "comment" && args[3] == ufwOwnedComment:
		return validatePortSpec(args[1])
	case len(args) == 3 && args[0] == "delete" && args[1] == "allow":
		return validatePortSpec(args[2])
	case len(args) == 5 && args[0] == "delete" && args[1] == "allow" && args[3] == "comment" && args[4] == ufwOwnedComment:
		return validatePortSpec(args[2])
	default:
		return fmt.Errorf("ufw %q is not allowed", args)
	}
}

// TURN is the only thing that changes the firewall at runtime, so these are
// the only rules the helper will add or delete: the TURN listeners, and UDP
// relay ranges inside the dynamic port range. Anything else — an internal port
// such as rqlite's — stays closed even to a compromised orama user.
var turnListenerSpecs = map[string]bool{"3478/udp": true, "3478/tcp": true, "5349/tcp": true}

const (
	relayRangeMin = 49152
	relayRangeMax = 65535
)

// validatePortSpec accepts a TURN listener ("3478/udp") or a UDP relay range
// inside 49152-65535 ("49152:65535/udp").
func validatePortSpec(spec string) error {
	if turnListenerSpecs[spec] {
		return nil
	}
	m := portSpec.FindStringSubmatch(spec)
	if m == nil || m[2] == "" || m[3] != "udp" {
		return fmt.Errorf("firewall rule %q is not allowed (TURN listeners 3478/udp, 3478/tcp, 5349/tcp, or a UDP relay range)", spec)
	}
	start, _ := strconv.Atoi(m[1])
	end, _ := strconv.Atoi(m[2])
	if start < relayRangeMin || end > relayRangeMax || start > end {
		return fmt.Errorf("relay range %q must lie within %d-%d", spec, relayRangeMin, relayRangeMax)
	}
	return nil
}
