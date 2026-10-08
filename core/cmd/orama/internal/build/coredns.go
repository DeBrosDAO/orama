package build

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/constants"
)

// corednsPluginConfig is CoreDNS's plugin.cfg with the RQLite plugin added: the
// stock plugin list in order, then rqlite.
const corednsPluginConfig = `metadata:metadata
cancel:cancel
tls:tls
reload:reload
nsid:nsid
bufsize:bufsize
root:root
bind:bind
debug:debug
trace:trace
ready:ready
health:health
pprof:pprof
prometheus:metrics
errors:errors
log:log
dnstap:dnstap
local:local
dns64:dns64
acl:acl
any:any
chaos:chaos
loadbalance:loadbalance
cache:cache
rewrite:rewrite
header:header
dnssec:dnssec
autopath:autopath
minimal:minimal
template:template
transfer:transfer
hosts:hosts
file:file
auto:auto
secondary:secondary
loop:loop
forward:forward
grpc:grpc
erratic:erratic
whoami:whoami
on:github.com/coredns/caddy/onevent
sign:sign
view:view
rqlite:rqlite
`

// corednsProxy is the module proxy CoreDNS's dependencies come through. The
// toolchain checks every module it downloads against the checksum database
// and against the go.sum CoreDNS ships at the pinned commit.
const corednsProxy = "GOPROXY=https://proxy.golang.org|direct"

func (b *Builder) buildCoreDNS() error {
	fmt.Printf("[5/8] Building CoreDNS %s with RQLite plugin...\n", constants.CoreDNSVersion)
	buildDir := filepath.Join(b.tmpDir, "coredns-build")

	fmt.Println("  Cloning CoreDNS...")
	if err := cloneCoreDNS(buildDir); err != nil {
		return err
	}
	if err := installRQLitePlugin(filepath.Join(b.projectDir, "pkg", "coredns", "rqlite"), buildDir); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(buildDir, "plugin.cfg"), []byte(corednsPluginConfig), 0o644); err != nil {
		return err
	}

	// CoreDNS already requires the miekg/dns and zap versions the plugin
	// uses, so nothing is upgraded: tidy only completes the go.sum for the
	// plugin's imports, from the versions CoreDNS's go.mod names.
	env := append(hermeticGoEnv(os.Environ()), corednsProxy)
	fmt.Println("  Generating plugin code...")
	for _, step := range [][]string{{"mod", "tidy"}, {"generate"}} {
		if err := runGo(buildDir, env, step...); err != nil {
			return err
		}
	}
	fmt.Println("  Building binary...")
	buildEnv := append(env, "GOOS=linux", "GOARCH="+b.flags.Arch, "CGO_ENABLED=0")
	if err := runGo(buildDir, buildEnv, goBuildCommandArgs(goLDFlags, filepath.Join(b.binDir, "coredns"))...); err != nil {
		return err
	}
	fmt.Println("  ✓ coredns")
	return nil
}

// cloneCoreDNS checks out the release tag into dir and refuses it unless it is
// the commit pinned in constants.CoreDNSCommit: a tag can be moved, a commit
// cannot.
func cloneCoreDNS(dir string) error {
	cmd := gitCommand("clone", "--depth", "1", "--branch", "v"+constants.CoreDNSVersion,
		"https://github.com/coredns/coredns.git", dir)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to clone coredns: %w", err)
	}
	head := gitCommand("rev-parse", "HEAD")
	head.Dir = dir
	out, err := head.Output()
	if err != nil {
		return fmt.Errorf("read the commit of the coredns checkout: %w", err)
	}
	if got := strings.TrimSpace(string(out)); got != constants.CoreDNSCommit {
		return fmt.Errorf("coredns v%s is commit %s, not the pinned %s; the tag moved or the version pin is stale",
			constants.CoreDNSVersion, got, constants.CoreDNSCommit)
	}
	return nil
}

// installRQLitePlugin copies the plugin's Go files from src into the CoreDNS
// tree at dir.
func installRQLitePlugin(src, dir string) error {
	dst := filepath.Join(dir, "plugin", "rqlite")
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return fmt.Errorf("failed to read rqlite plugin source at %s: %w", src, err)
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".go" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(src, entry.Name()))
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dst, entry.Name()), data, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// runGo runs `go args...` in dir with env.
func runGo(dir string, env []string, args ...string) error {
	cmd := exec.Command("go", args...)
	cmd.Dir = dir
	cmd.Env = env
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("go %s in %s: %w", strings.Join(args, " "), dir, err)
	}
	return nil
}
