package main

import (
	"strings"
	"testing"
)

func TestMissingConfigHint_namesNoCommandThatDoesNotExist(t *testing.T) {
	if strings.Contains(missingConfigHint, "config init") {
		t.Fatalf("hint names `orama config init`, which the binary does not have: %q", missingConfigHint)
	}
	if !strings.Contains(missingConfigHint, "orama maint node install") {
		t.Fatalf("hint does not name the command that writes the index gateway YAML: %q", missingConfigHint)
	}
}
