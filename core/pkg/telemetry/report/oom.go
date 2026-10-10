package report

import (
	"sort"
	"strconv"
	"strings"
)

// OOMKillWindowArg is the journalctl --since offset (without the leading "-")
// that bounds the OOM kill count; OOMKillWindowLabel is how alerts name it.
const (
	OOMKillWindowArg   = "1h"
	OOMKillWindowLabel = "the last hour"
)

const (
	oomKillMarker     = "Killed process"
	oomEventStart     = "invoked oom-killer"
	oomSummaryMarker  = "oom-kill:"
	oomConstraintKey  = "constraint="
	oomTaskMemcgKey   = "task_memcg="
	oomConstraintMemc = "CONSTRAINT_MEMCG"
	// tenantUnitPrefix starts every tenant deployment unit (orama-deploy-
	// <type>@<instance>, including the build and clean units). Platform
	// units are orama-* without it.
	tenantUnitPrefix = "orama-deploy-"
	unitInstanceSep  = "@"
	unitSuffix       = ".service"
)

// OOMCounts is the kernel OOM kills of one journal window, split by whose
// fault they are.
type OOMCounts struct {
	// System counts global OOMs and kills inside non-tenant cgroups: the
	// node ran out of memory or a platform unit hit its limit.
	System int
	// Tenant counts kills inside tenant deployment cgroups hitting their own
	// MemoryMax; TenantUnits attributes them per deployment unit instance.
	Tenant      int
	TenantUnits map[string]int
}

// ClassifyOOMKills counts the OOM kills in kernel journal output. Each kill
// is a "Killed process N (name)" line, preceded in the same event by
// "oom-kill:constraint=...,task_memcg=<victim cgroup>,...". Only a memcg
// OOM whose victim lives in a tenant deployment cgroup is a tenant kill; a
// global OOM is a node fault whoever the victim was, and a kill without a
// summary line (older kernels) cannot be attributed so counts as a node's.
func ClassifyOOMKills(journal string) OOMCounts {
	var c OOMCounts
	summary := ""
	for _, line := range strings.Split(journal, "\n") {
		switch {
		case strings.Contains(line, oomEventStart):
			summary = ""
		case strings.Contains(line, oomSummaryMarker):
			summary = line
		case strings.Contains(line, oomKillMarker):
			if unit, ok := tenantVictim(summary); ok {
				c.Tenant++
				if c.TenantUnits == nil {
					c.TenantUnits = map[string]int{}
				}
				c.TenantUnits[unit]++
			} else {
				c.System++
			}
			summary = ""
		}
	}
	return c
}

// tenantVictim reports the deployment unit instance of a memcg OOM summary
// line whose victim runs in a tenant deployment cgroup.
func tenantVictim(summary string) (string, bool) {
	if summary == "" || oomField(summary, oomConstraintKey) != oomConstraintMemc {
		return "", false
	}
	memcg := oomField(summary, oomTaskMemcgKey)
	unit := memcg[strings.LastIndex(memcg, "/")+1:]
	if !strings.HasPrefix(unit, tenantUnitPrefix) {
		return "", false
	}
	return strings.TrimSuffix(unit, unitSuffix), true
}

// oomField returns the value of a comma-separated key=value field of the
// kernel's oom-kill summary line.
func oomField(summary, key string) string {
	i := strings.Index(summary, key)
	if i < 0 {
		return ""
	}
	v := summary[i+len(key):]
	if j := strings.IndexByte(v, ','); j >= 0 {
		v = v[:j]
	}
	return strings.TrimSpace(v)
}

// TenantOOMSummary renders a per-unit kill map as "unit xN, ..." sorted by unit.
func TenantOOMSummary(units map[string]int) string {
	names := make([]string, 0, len(units))
	for u := range units {
		names = append(names, u)
	}
	sort.Strings(names)
	parts := make([]string, 0, len(names))
	for _, u := range names {
		parts = append(parts, u+" x"+strconv.Itoa(units[u]))
	}
	return strings.Join(parts, ", ")
}

func (r *SystemReport) applyOOMCounts(c OOMCounts) {
	r.OOMKills = c.System
	r.TenantOOMKills = c.Tenant
	r.TenantOOMKillsByUnit = c.TenantUnits
}
