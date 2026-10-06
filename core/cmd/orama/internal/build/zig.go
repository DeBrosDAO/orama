package build

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// zigEnvVar names a specific zig binary for the build, for machines whose
// default zig is a different release than the vault needs.
const zigEnvVar = "ORAMA_ZIG"

// Zig breaks its language and standard library between minor releases until
// 1.0, so "at least the minimum" is not enough: the vault builds with the
// minor release its build.zig.zon names and no other. `zig` on PATH moved to
// 0.16 on Homebrew while the vault needs 0.15, and the failure surfaced two
// minutes into a build as a compile error inside the vault source.
var zonMinimumPattern = regexp.MustCompile(`\.minimum_zig_version\s*=\s*"([^"]+)"`)

// resolveZig returns the zig binary to build with, checked against the
// vault's minimum_zig_version before anything is compiled.
func resolveZig(vaultDir string) (string, error) {
	zon, err := os.ReadFile(filepath.Join(vaultDir, "build.zig.zon"))
	if err != nil {
		return "", fmt.Errorf("read vault build.zig.zon: %w", err)
	}
	minimum, err := minimumZigVersion(string(zon))
	if err != nil {
		return "", err
	}

	zig := os.Getenv(zigEnvVar)
	if zig == "" {
		if zig, err = exec.LookPath("zig"); err != nil {
			return "", fmt.Errorf("zig not found in PATH — the build needs zig %s: install it from https://ziglang.org/download/ or set %s", minorOf(minimum), zigEnvVar)
		}
	}
	// cgo splits CC on spaces, so a path with one cannot be the C compiler.
	if strings.ContainsAny(zig, " \t") {
		return "", fmt.Errorf("zig path %q contains whitespace, which CC=\"<zig> cc\" cannot carry; link it from a path without spaces", zig)
	}
	out, err := exec.Command(zig, "version").Output()
	if err != nil {
		return "", fmt.Errorf("run %s version: %w", zig, err)
	}
	if err := checkZigVersion(strings.TrimSpace(string(out)), minimum); err != nil {
		return "", fmt.Errorf("%s: %w; set %s to a zig %s binary (Homebrew: %s=$(brew --prefix zig@%s)/bin/zig)",
			zig, err, zigEnvVar, minorOf(minimum), zigEnvVar, minorOf(minimum))
	}
	return zig, nil
}

// minimumZigVersion extracts minimum_zig_version from build.zig.zon content.
func minimumZigVersion(zon string) (string, error) {
	m := zonMinimumPattern.FindStringSubmatch(zon)
	if m == nil {
		return "", fmt.Errorf("vault build.zig.zon has no minimum_zig_version")
	}
	return m[1], nil
}

// checkZigVersion accepts have when it is the same major.minor release as
// minimum and not an older patch.
func checkZigVersion(have, minimum string) error {
	h, err := parseZigVersion(have)
	if err != nil {
		return fmt.Errorf("unreadable zig version %q: %w", have, err)
	}
	m, err := parseZigVersion(minimum)
	if err != nil {
		return fmt.Errorf("unreadable minimum_zig_version %q: %w", minimum, err)
	}
	if h[0] != m[0] || h[1] != m[1] || h[2] < m[2] {
		return fmt.Errorf("is zig %s, the vault needs zig %s (>= %s)", have, minorOf(minimum), minimum)
	}
	return nil
}

// parseZigVersion reads "0.15.2" (or "0.16.0-dev.123+abc") as [0 15 2].
func parseZigVersion(v string) ([3]int, error) {
	var out [3]int
	core, _, _ := strings.Cut(v, "-")
	core, _, _ = strings.Cut(core, "+")
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return out, fmt.Errorf("want major.minor.patch")
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return out, err
		}
		out[i] = n
	}
	return out, nil
}

// minorOf turns "0.15.2" into "0.15".
func minorOf(v string) string {
	parts := strings.SplitN(v, ".", 3)
	if len(parts) < 2 {
		return v
	}
	return parts[0] + "." + parts[1]
}
