package command

import "testing"

func TestAllowedLogService(t *testing.T) {
	if !allowedLogService("gateway") {
		t.Error("gateway must be allowed")
	}
	if allowedLogService("../secrets") {
		t.Error("path traversal must be denied")
	}
	if allowedLogService("rqlite/../../etc/passwd") {
		t.Error("slash in service must be denied")
	}
	if allowedLogService("not-a-service") {
		t.Error("unknown service must be denied")
	}
	if allowedLogService("") {
		t.Error("empty service must be denied")
	}
	if p := logPathForService("gateway"); p != logsDir+"/gateway.log" {
		t.Errorf("logPathForService(gateway) = %q", p)
	}
}
