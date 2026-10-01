package fleet

import (
	"regexp"
	"strconv"
	"strings"
)

// The tenant deployment port range (core/pkg/privhelper DeployPortMin and
// DeployPortMax; deployment_range_test.go pins these to them).
const (
	DeployPortMin = 10200
	DeployPortMax = 19999
)

// InDeploymentRange reports whether port is one a tenant deployment is given.
func InDeploymentRange(port int) bool { return port >= DeployPortMin && port <= DeployPortMax }

var deployUnitInCgroup = regexp.MustCompile(`(?:^|/)(orama-deploy-(?:node|npm|go)@[A-Za-z0-9_.-]+\.service)(?:/|$)`)

// DeployUnitFromCgroup returns the runtime unit
// (orama-deploy-<runtime>@<instance>.service) a /proc/<pid>/cgroup names. The
// build and clean units (orama-deploy-build@, orama-deploy-clean@) are not
// runtimes and do not match.
func DeployUnitFromCgroup(cgroup string) (string, bool) {
	for _, line := range strings.Split(cgroup, "\n") {
		if m := deployUnitInCgroup.FindStringSubmatch(line); m != nil {
			return m[1], true
		}
	}
	return "", false
}

var ufwPortSpec = regexp.MustCompile(`^(\d+)(?::(\d+))?(?:/(tcp|udp))?$`)

// UFWAllowsPort reports whether `ufw status` has an ALLOW rule that opens
// proto/port to every interface: a rule whose destination is a port, a
// port/proto or a start:end range. A rule scoped to an interface ("Anywhere
// on wg0") names no port and is not one that exposes it to the internet.
func UFWAllowsPort(ufwStatus, proto string, port int) bool {
	for _, line := range strings.Split(ufwStatus, "\n") {
		body, _, _ := strings.Cut(line, "#")
		if !strings.Contains(body, "ALLOW") {
			continue
		}
		cols := ufwColumns.Split(strings.TrimSpace(body), -1)
		to := strings.TrimSpace(strings.ReplaceAll(cols[0], "(v6)", ""))
		fields := strings.Fields(to)
		m := ufwPortSpec.FindStringSubmatch(fields[len(fields)-1])
		if m == nil {
			continue
		}
		low, _ := strconv.Atoi(m[1])
		high := low
		if m[2] != "" {
			high, _ = strconv.Atoi(m[2])
		}
		if port >= low && port <= high && (m[3] == "" || strings.HasPrefix(proto, m[3])) {
			return true
		}
	}
	return false
}
