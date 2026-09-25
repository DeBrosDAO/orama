package build

import (
	"strings"
	"testing"
)

func TestCheckZigVersion_SameMinorAtOrAboveMinimumPasses(t *testing.T) {
	for _, have := range []string{"0.15.2", "0.15.3", "0.15.2-dev.12+abc"} {
		if err := checkZigVersion(have, "0.15.2"); err != nil {
			t.Errorf("%s against minimum 0.15.2: %v", have, err)
		}
	}
}

// Homebrew's zig moved to 0.16 while the vault still needs 0.15; before this
// check the build ran two minutes and died inside the vault source.
func TestCheckZigVersion_OtherMinorOrOlderPatchIsRefused(t *testing.T) {
	for _, have := range []string{"0.16.0", "0.14.1", "0.15.1", "1.15.2"} {
		err := checkZigVersion(have, "0.15.2")
		if err == nil {
			t.Errorf("%s must be refused for minimum 0.15.2", have)
			continue
		}
		if want := "the vault needs zig 0.15"; !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should say %q", err, want)
		}
	}
}

func TestCheckZigVersion_GarbageIsAnError(t *testing.T) {
	if err := checkZigVersion("", "0.15.2"); err == nil {
		t.Error("empty version must be an error")
	}
	if err := checkZigVersion("0.15.2", "fifteen"); err == nil {
		t.Error("unparseable minimum must be an error")
	}
}

func TestMinimumZigVersion_ReadsTheZon(t *testing.T) {
	zon := ".{\n    .name = .vault,\n    .version = \"0.1.0\",\n    .minimum_zig_version = \"0.15.2\",\n}\n"
	got, err := minimumZigVersion(zon)
	if err != nil || got != "0.15.2" {
		t.Fatalf("got %q, %v", got, err)
	}
	if _, err := minimumZigVersion(".{ .version = \"0.1.0\" }"); err == nil {
		t.Error("a zon without minimum_zig_version must be an error")
	}
}
