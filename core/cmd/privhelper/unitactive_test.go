package main

import (
	"testing"

	"github.com/DeBrosOfficial/network/pkg/privhelper"
)

func TestSystemdUnitActive_stateMapping(t *testing.T) {
	orig := runTool
	defer func() { runTool = orig }()
	tests := []struct {
		name    string
		resp    privhelper.Response
		want    bool
		wantErr bool
	}{
		{"active", privhelper.Response{Output: "active\n"}, true, false},
		{"activating", privhelper.Response{Output: "activating\n", ExitCode: 3}, true, false},
		{"reloading", privhelper.Response{Output: "reloading\n", ExitCode: 3}, true, false},
		{"deactivating", privhelper.Response{Output: "deactivating\n", ExitCode: 3}, true, false},
		{"inactive", privhelper.Response{Output: "inactive\n", ExitCode: 3}, false, false},
		{"failed", privhelper.Response{Output: "failed\n", ExitCode: 3}, false, false},
		{"unknown", privhelper.Response{Output: "unknown\n", ExitCode: 4}, false, false},
		{"unrecognised state", privhelper.Response{Output: "maintenance\n", ExitCode: 3}, false, true},
		{"empty output", privhelper.Response{ExitCode: 1}, false, true},
		{"helper error", privhelper.Response{Output: "orama-privhelper: systemctl not found\n", ExitCode: 1}, false, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			runTool = func(tool string, args []string) privhelper.Response { return tc.resp }
			got, err := systemdUnitActive("orama-deploy-node@acme.service")
			if got != tc.want || (err != nil) != tc.wantErr {
				t.Errorf("got (%v, %v), want (%v, err=%v)", got, err, tc.want, tc.wantErr)
			}
		})
	}
}
