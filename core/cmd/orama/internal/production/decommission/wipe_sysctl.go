package decommission

import (
	"strings"

	"github.com/DeBrosOfficial/network/pkg/hardening"
	"github.com/DeBrosOfficial/network/pkg/install"
)

// ipv6DefaultValue is what the wipe puts IPv6 back to when the install recorded nothing: the
// kernel default, IPv6 enabled.
const ipv6DefaultValue = "0"

// restoreSysctlsFunc defines restore_prior_sysctls <drop-in> <fallback> <key>...: it sets each key
// to the value the drop-in records as the one from before the install (a `# prior: key=value`
// line), or to <fallback> when it records none (an empty fallback leaves the key as it is). A key
// the kernel does not have is skipped; one that cannot be set is listed in sysctl_unrestored, which
// the leftover check reports, so a wipe never reports clean over a machine whose IPv6 is still off.
func restoreSysctlsFunc() string {
	return `sysctl_unrestored=""
restore_prior_sysctls() {
    dropin="$1"; fallback="$2"; shift 2
    for key in "$@"; do
        [ -e "/proc/sys/${key//.//}" ] || continue
        line="$(grep -F -m1 "` + install.PriorSysctlPrefix + `$key=" "$dropin" 2>/dev/null || true)"
        if [ -n "$line" ]; then value="${line#"` + install.PriorSysctlPrefix + `$key="}"; else value="$fallback"; fi
        [ -n "$value" ] || continue
        sysctl -q -w "$key=$value" || sysctl_unrestored="$sysctl_unrestored $key"
    done
}`
}

// ipv6RestoreBlock puts IPv6 back and removes the drop-in that turned it off at boot. The
// drop-in is read before it goes.
func ipv6RestoreBlock() string {
	return `# The installer turned IPv6 off at every boot; put the values it found back (the kernel
# default when it recorded none) and remove the drop-in.
restore_prior_sysctls ` + install.IPv6DisableSysctlPath + ` ` + ipv6DefaultValue + ` ` + strings.Join(sysctlKeys(install.IPv6DisableSysctls), " ") + `
rm -f ` + install.IPv6DisableSysctlPath
}

// ramHygieneRestoreBlock is the nuclear wipe's part: the hardening drop-in goes, and the keys it
// set go back to the values the install recorded. An install that recorded none leaves the
// running values to the next boot, which has no drop-in.
func ramHygieneRestoreBlock() string {
	return `    restore_prior_sysctls ` + install.RAMHygieneSysctlPath + ` "" ` + strings.Join(sysctlKeys(hardening.Sysctls), " ") + `
    rm -f ` + install.RAMHygieneSysctlPath
}

func sysctlKeys(sysctls []hardening.Sysctl) []string {
	keys := make([]string, len(sysctls))
	for i, s := range sysctls {
		keys[i] = s.Key
	}
	return keys
}
