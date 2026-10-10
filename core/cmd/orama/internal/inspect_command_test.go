package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
)

func validInspectOptions() InspectOptions {
	return InspectOptions{Env: "devnet", Subsystem: "system", Format: "table", Timeout: 30 * time.Second}
}

// A mistake on the command line is found before any node is asked anything:
// stagenet e2e, 2026-09-30, an unknown subsystem ran the whole inspection and
// exited 0, an unknown format and a negative timeout exited 1.
func TestInspectOptions_validateRefusesBadArgumentsAsUsage(t *testing.T) {
	for name, mutate := range map[string]func(*InspectOptions){
		"no env":             func(o *InspectOptions) { o.Env = "" },
		"unknown subsystem":  func(o *InspectOptions) { o.Subsystem = "e2e-nope" },
		"empty subsystem":    func(o *InspectOptions) { o.Subsystem = "system," },
		"unknown format":     func(o *InspectOptions) { o.Format = "yaml" },
		"negative timeout":   func(o *InspectOptions) { o.Timeout = -time.Second },
		"zero timeout":       func(o *InspectOptions) { o.Timeout = 0 },
		"one bad of several": func(o *InspectOptions) { o.Subsystem = "system,e2e-nope" },
	} {
		t.Run(name, func(t *testing.T) {
			opts := validInspectOptions()
			mutate(&opts)
			err := opts.Validate()
			if got := clierr.CodeOf(err); got != clierr.CodeUsage {
				t.Fatalf("exit code %d (%v), want %d", got, err, clierr.CodeUsage)
			}
		})
	}
}

func TestInspectOptions_validateAcceptsGoodArguments(t *testing.T) {
	for _, format := range []string{"table", "json"} {
		for _, subsystem := range []string{"all", "system", "system,wg", "rqlite,olric"} {
			opts := validInspectOptions()
			opts.Format, opts.Subsystem = format, subsystem
			if err := opts.Validate(); err != nil {
				t.Errorf("format %s subsystem %s: %v", format, subsystem, err)
			}
		}
	}
}

// --format json is for programs: stdout is one JSON document and the progress
// line is on stderr (stagenet e2e, 2026-09-30: a banner came first).
func TestInspectNodes_jsonStdoutIsOneDocument(t *testing.T) {
	opts := validInspectOptions()
	opts.Format = "json"
	var stdout, stderr bytes.Buffer

	if err := inspectNodes(nil, opts, &stdout, &stderr); err != nil {
		t.Fatalf("inspectNodes: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &doc); err != nil {
		t.Fatalf("stdout is not one JSON document (%v): %q", err, stdout.String())
	}
	if !strings.Contains(stderr.String(), "Inspecting") {
		t.Errorf("the progress line is not on stderr: %q", stderr.String())
	}
	if strings.Contains(stdout.String(), "Inspecting") {
		t.Errorf("the progress line is on stdout: %q", stdout.String())
	}
}
