package main

import (
	"strings"
	"testing"
)

func TestMissingConfigHint_namesACommandThatExists(t *testing.T) {
	if strings.Contains(missingConfigHint, "config init") {
		t.Fatalf("hint names `orama config init`, which the binary does not have: %q", missingConfigHint)
	}
	if !strings.Contains(missingConfigHint, "orama node install") {
		t.Fatalf("hint does not name the command that writes node.yaml: %q", missingConfigHint)
	}
}
