package build

import (
	"fmt"
	"os"
)

// The gateway links github.com/mattn/go-sqlite3 for namespace SQLite
// databases (pkg/gateway/handlers/sqlite). That driver is cgo: built with
// CGO_ENABLED=0 it compiles to a stub whose every Open fails, and since
// CHG-202 its tenant connect hook (SetLimit on ATTACH) does not compile at
// all. So binaries that need it are cross-compiled with cgo through zig cc
// against static musl — the toolchain the vault build already requires — and
// stay single static executables like every other binary in the archive.
const (
	// netgo and osusergo keep DNS and user lookups in pure Go, so the static
	// binary never reaches for libc's resolver or NSS at runtime.
	cgoBuildTags = "netgo,osusergo,sqlite_omit_load_extension"
	// cgoLinkFlags links the C parts statically.
	cgoLinkFlags = "-linkmode external -extldflags -static"
)

// zigTargetFor maps a Go architecture to the zig musl target triple.
func zigTargetFor(arch string) (string, error) {
	switch arch {
	case "amd64":
		return "x86_64-linux-musl", nil
	case "arm64":
		return "aarch64-linux-musl", nil
	default:
		return "", fmt.Errorf("unsupported architecture for a zig cross-compile: %s (supported: amd64, arm64)", arch)
	}
}

// cgoEnv returns the environment for a cgo cross-compile to linux/arch with
// the given zig binary as the C toolchain.
func cgoEnv(base []string, arch, zig string) ([]string, error) {
	target, err := zigTargetFor(arch)
	if err != nil {
		return nil, err
	}
	return append(base,
		"GOOS=linux",
		"GOARCH="+arch,
		"CGO_ENABLED=1",
		"CC="+zig+" cc -target "+target,
		"CXX="+zig+" c++ -target "+target,
	), nil
}

// buildEnvFor returns the go build environment for bin.
func (b *Builder) buildEnvFor(bin oramaBinary) ([]string, error) {
	if !bin.CGO {
		return b.crossEnv(), nil
	}
	return cgoEnv(os.Environ(), b.flags.Arch, b.zig)
}

// goBuildArgs returns the `go` arguments that build bin into output.
func goBuildArgs(bin oramaBinary, ldflags, output string) []string {
	args := []string{"build"}
	if bin.CGO {
		args = append(args, "-tags", cgoBuildTags)
		ldflags += " " + cgoLinkFlags
	}
	return append(args, "-ldflags", ldflags, "-trimpath", "-o", output, bin.Package)
}
