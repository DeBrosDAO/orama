package report

import "testing"

const (
	globalOOM = "sleep invoked oom-killer: gfp_mask=0x140cca, order=0\n" +
		"oom-kill:constraint=CONSTRAINT_NONE,nodemask=(null),cpuset=/,mems_allowed=0,global_oom,task_memcg=/system.slice/orama-deploy-node@app-1.service,task=node,pid=9,uid=1001\n" +
		"Out of memory: Killed process 9 (node) total-vm:1kB, anon-rss:2kB\n"
	platformOOM = "rqlited invoked oom-killer: gfp_mask=0xcc0, order=0\n" +
		"oom-kill:constraint=CONSTRAINT_MEMCG,nodemask=(null),cpuset=/,mems_allowed=0,oom_memcg=/system.slice/orama-rqlite.service,task_memcg=/system.slice/orama-rqlite.service,task=rqlited,pid=4242,uid=0\n" +
		"Memory cgroup out of memory: Killed process 4242 (rqlited) total-vm:1kB\n"
	tenantOOM = "node invoked oom-killer: gfp_mask=0xcc0, order=0\n" +
		"oom-kill:constraint=CONSTRAINT_MEMCG,nodemask=(null),cpuset=/,mems_allowed=0,oom_memcg=/system.slice/orama-deploy-node@app-1.service,task_memcg=/system.slice/orama-deploy-node@app-1.service,task=node,pid=77,uid=1001\n" +
		"Memory cgroup out of memory: Killed process 77 (node) total-vm:1kB\n"
	tenantBuildOOM = "oom-kill:constraint=CONSTRAINT_MEMCG,oom_memcg=/system.slice/orama-deploy-build@app-2.service,task_memcg=/system.slice/orama-deploy-build@app-2.service,task=npm,pid=5,uid=1001\n" +
		"Memory cgroup out of memory: Killed process 5 (npm) total-vm:1kB\n"
)

func TestClassifyOOMKills(t *testing.T) {
	tests := []struct {
		name    string
		journal string
		system  int
		tenant  int
		units   map[string]int
	}{
		{"empty", "", 0, 0, nil},
		{"no entries marker", "-- No entries --", 0, 0, nil},
		{"unrelated kernel lines", "oom_reaper: reaped process 12 (x), now anon-rss:0kB\nCPU: 1 PID: 4 Comm: x\n", 0, 0, nil},
		{"global OOM stays a node fault even when the victim was a tenant", globalOOM, 1, 0, nil},
		{"platform unit cgroup OOM is a node fault", platformOOM, 1, 0, nil},
		{"tenant cgroup OOM is not", tenantOOM, 0, 1, map[string]int{"orama-deploy-node@app-1": 1}},
		{"tenant build cgroup OOM is not", tenantBuildOOM, 0, 1, map[string]int{"orama-deploy-build@app-2": 1}},
		{"kill without a summary line (old kernel) is a node fault", "Out of memory: Killed process 2 (b) total-vm:1kB\n", 1, 0, nil},
		{"a stale summary does not leak into the next event", "oom-kill:constraint=CONSTRAINT_MEMCG,task_memcg=/system.slice/orama-deploy-node@x.service\nx invoked oom-killer: order=0\nOut of memory: Killed process 3 (c) total-vm:1kB\n", 1, 0, nil},
		{"mixed window", tenantOOM + platformOOM + tenantOOM + globalOOM, 2, 2, map[string]int{"orama-deploy-node@app-1": 2}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ClassifyOOMKills(tt.journal)
			if got.System != tt.system || got.Tenant != tt.tenant {
				t.Fatalf("system=%d tenant=%d, want system=%d tenant=%d", got.System, got.Tenant, tt.system, tt.tenant)
			}
			if len(got.TenantUnits) != len(tt.units) {
				t.Fatalf("units = %v, want %v", got.TenantUnits, tt.units)
			}
			for u, n := range tt.units {
				if got.TenantUnits[u] != n {
					t.Fatalf("units = %v, want %v", got.TenantUnits, tt.units)
				}
			}
		})
	}
}

func TestTenantOOMSummary_sorted(t *testing.T) {
	if got := TenantOOMSummary(map[string]int{"b@2": 1, "a@1": 3}); got != "a@1 x3, b@2 x1" {
		t.Fatalf("got %q", got)
	}
}

func TestSystemErrors_carriesOOMKillsError(t *testing.T) {
	if got := systemErrors(&SystemReport{OOMKillsError: "journalctl: boom"}); len(got) != 1 || got[0] != "journalctl: boom" {
		t.Fatalf("systemErrors = %v", got)
	}
	if got := systemErrors(&SystemReport{OOMKills: 2}); len(got) != 0 {
		t.Fatalf("known count must not be an error: %v", got)
	}
	if got := systemErrors(nil); len(got) != 0 {
		t.Fatalf("nil system: %v", got)
	}
}
