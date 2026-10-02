package inspector

import "testing"

const (
	tenantOOMLines   = "ok\nx invoked oom-killer: order=0\noom-kill:constraint=CONSTRAINT_MEMCG,task_memcg=/system.slice/orama-deploy-node@app-1.service,task=node\nMemory cgroup out of memory: Killed process 7 (node) total-vm:1kB\n"
	platformOOMLines = "ok\nx invoked oom-killer: order=0\noom-kill:constraint=CONSTRAINT_MEMCG,task_memcg=/system.slice/orama-rqlite.service,task=rqlited\nMemory cgroup out of memory: Killed process 8 (rqlited) total-vm:1kB\n"
)

func TestParseOOMKillsField(t *testing.T) {
	tests := []struct {
		name       string
		in         string
		wantSystem int
		wantTenant int
		wantErr    bool
	}{
		{"no kills", "ok\n", 0, 0, false},
		{"platform unit cgroup OOM", platformOOMLines, 1, 0, false},
		{"tenant cgroup OOM", tenantOOMLines, 0, 1, false},
		{"both", tenantOOMLines + platformOOMLines[len("ok\n"):], 1, 1, false},
		{"unknown", "unknown", 0, 0, true},
		{"empty", "", 0, 0, true},
		{"bare count from an old probe", "3", 0, 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, errMsg := parseOOMKillsField(tt.in)
			if c.System != tt.wantSystem || c.Tenant != tt.wantTenant || (errMsg != "") != tt.wantErr {
				t.Fatalf("parseOOMKillsField(%q) = (%+v, %q)", tt.in, c, errMsg)
			}
		})
	}
}
