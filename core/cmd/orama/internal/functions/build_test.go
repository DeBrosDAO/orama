package functions

import (
	"strings"
	"testing"
)

// TestTinygoBuildArgs_PersistentGetsCSharedBuildmode is the regression
// guard for bug #240/#249 follow-up #6: TinyGo command-mode `_start`
// doesn't set the reactor-mode runtime guard, so any export call from
// the host (e.g. orama_alloc → ws_open payload) traps with
// "wasm error: unreachable" inside the runtime hashmap path.
//
// Fix: persistent functions get `-buildmode=c-shared` which flips
// TinyGo to reactor mode (exports `_initialize`, no `_start`). The
// gateway's persistent-instance bootstrap already calls `_initialize`
// first if exported (pkg/serverless/engine.go::InstantiatePersistent),
// so reactor-built wasms cleanly initialize the TinyGo runtime and
// every subsequent host-driven export call works.
//
// Empirically confirmed against TinyGo 0.40.1: the same source
// compiled with vs. without `-buildmode=c-shared` produces wasms with
// `_start` only vs. `_initialize` only respectively.
//
// If a future refactor drops the flag (or adds it for stateless), this
// test fails loud — the AnChat WS chain went down for ~1 day chasing
// this exact behavior.
func TestTinygoBuildArgs_PersistentGetsCSharedBuildmode(t *testing.T) {
	tests := []struct {
		name         string
		wsPersistent bool
		wantContains string // substring that must appear in the joined args
		wantAbsent   string // substring that must NOT appear
	}{
		{
			name:         "stateless function stays in command mode (default)",
			wsPersistent: false,
			wantContains: "-target wasi",
			wantAbsent:   "-buildmode=c-shared",
		},
		{
			name:         "persistent function gets reactor mode (c-shared)",
			wsPersistent: true,
			wantContains: "-buildmode=c-shared",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tinygoBuildArgs("/tmp/out.wasm", tt.wsPersistent)
			joined := strings.Join(got, " ")

			if !strings.Contains(joined, tt.wantContains) {
				t.Errorf("missing %q in args: %q", tt.wantContains, joined)
			}
			if tt.wantAbsent != "" && strings.Contains(joined, tt.wantAbsent) {
				t.Errorf("unexpected %q in args (only persistent should get this): %q",
					tt.wantAbsent, joined)
			}

			// Invariants for both: build action, output path, source dir.
			for _, want := range []string{"build", "-o", "/tmp/out.wasm", "-target", "wasi", "."} {
				found := false
				for _, a := range got {
					if a == want {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("missing required arg %q in: %v", want, got)
				}
			}

			// Invariant: the source directory `.` must be the LAST arg
			// (TinyGo's positional). If we accidentally reorder the
			// builder so the flag goes after `.`, TinyGo will treat the
			// flag as a build target and fail with a confusing error.
			if got[len(got)-1] != "." {
				t.Errorf("last arg should be `.`, got %q (full args: %v)", got[len(got)-1], got)
			}
		})
	}
}
