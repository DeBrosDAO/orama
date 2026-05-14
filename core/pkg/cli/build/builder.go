package build

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/constants"
)

// oramaBinary defines a binary to cross-compile from the project source.
type oramaBinary struct {
	Name    string // output binary name
	Package string // Go package path relative to project root
	// Extra ldflags beyond the standard ones
	ExtraLDFlags string
}

// Builder orchestrates the entire build process.
type Builder struct {
	flags      *Flags
	projectDir string
	tmpDir     string
	binDir     string
	version    string
	commit     string
	date       string
}

// NewBuilder creates a new Builder.
func NewBuilder(flags *Flags) *Builder {
	return &Builder{flags: flags}
}

// Build runs the full build pipeline.
func (b *Builder) Build() error {
	start := time.Now()

	// Find project root
	projectDir, err := findProjectRoot()
	if err != nil {
		return err
	}
	b.projectDir = projectDir

	// Read version from Makefile or use "dev"
	b.version = b.readVersion()
	b.commit = b.readCommit()
	b.date = time.Now().UTC().Format("2006-01-02T15:04:05Z")

	// Create temp build directory
	b.tmpDir, err = os.MkdirTemp("", "orama-build-*")
	if err != nil {
		return fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer os.RemoveAll(b.tmpDir)

	b.binDir = filepath.Join(b.tmpDir, "bin")
	if err := os.MkdirAll(b.binDir, 0755); err != nil {
		return fmt.Errorf("failed to create bin dir: %w", err)
	}

	fmt.Printf("Building orama %s for linux/%s\n", b.version, b.flags.Arch)
	fmt.Printf("Project: %s\n\n", b.projectDir)

	// Step 1: Cross-compile Orama binaries
	if err := b.buildOramaBinaries(); err != nil {
		return fmt.Errorf("failed to build orama binaries: %w", err)
	}

	// Step 2: Cross-compile Vault Guardian (Zig)
	if err := b.buildVaultGuardian(); err != nil {
		return fmt.Errorf("failed to build vault-guardian: %w", err)
	}

	// Step 3: Cross-compile Olric
	if err := b.buildOlric(); err != nil {
		return fmt.Errorf("failed to build olric: %w", err)
	}

	// Step 4: Cross-compile IPFS Cluster
	if err := b.buildIPFSCluster(); err != nil {
		return fmt.Errorf("failed to build ipfs-cluster: %w", err)
	}

	// Step 5: Build CoreDNS with RQLite plugin
	if err := b.buildCoreDNS(); err != nil {
		return fmt.Errorf("failed to build coredns: %w", err)
	}

	// Step 6: Build Caddy with Orama DNS module
	if err := b.buildCaddy(); err != nil {
		return fmt.Errorf("failed to build caddy: %w", err)
	}

	// Step 7: Download pre-built IPFS Kubo
	if err := b.downloadIPFS(); err != nil {
		return fmt.Errorf("failed to download ipfs: %w", err)
	}

	// Step 8: Download pre-built RQLite
	if err := b.downloadRQLite(); err != nil {
		return fmt.Errorf("failed to download rqlite: %w", err)
	}

	// Step 9: Copy systemd templates
	if err := b.copySystemdTemplates(); err != nil {
		return fmt.Errorf("failed to copy systemd templates: %w", err)
	}

	// Step 10: Generate manifest
	manifest, err := b.generateManifest()
	if err != nil {
		return fmt.Errorf("failed to generate manifest: %w", err)
	}

	// Step 11: Sign manifest (optional)
	if b.flags.Sign {
		if err := b.signManifest(manifest); err != nil {
			return fmt.Errorf("failed to sign manifest: %w", err)
		}
	}

	// Step 12: Create archive
	outputPath := b.flags.Output
	if outputPath == "" {
		outputPath = fmt.Sprintf("/tmp/orama-%s-linux-%s.tar.gz", b.version, b.flags.Arch)
	}

	if err := b.createArchive(outputPath, manifest); err != nil {
		return fmt.Errorf("failed to create archive: %w", err)
	}

	elapsed := time.Since(start).Round(time.Second)
	fmt.Printf("\nBuild complete in %s\n", elapsed)
	fmt.Printf("Archive: %s\n", outputPath)

	return nil
}

func (b *Builder) buildOramaBinaries() error {
	fmt.Println("[1/8] Cross-compiling Orama binaries...")

	ldflags := fmt.Sprintf("-s -w -X 'main.version=%s' -X 'main.commit=%s' -X 'main.date=%s'",
		b.version, b.commit, b.date)

	gatewayLDFlags := fmt.Sprintf("%s -X 'github.com/DeBrosOfficial/network/pkg/gateway.BuildVersion=%s' -X 'github.com/DeBrosOfficial/network/pkg/gateway.BuildCommit=%s' -X 'github.com/DeBrosOfficial/network/pkg/gateway.BuildTime=%s'",
		ldflags, b.version, b.commit, b.date)

	binaries := []oramaBinary{
		{Name: "orama", Package: "./cmd/cli/"},
		{Name: "orama-node", Package: "./cmd/node/"},
		{Name: "gateway", Package: "./cmd/gateway/", ExtraLDFlags: gatewayLDFlags},
		{Name: "identity", Package: "./cmd/identity/"},
		{Name: "sfu", Package: "./cmd/sfu/"},
		{Name: "turn", Package: "./cmd/turn/"},
		{Name: "orama-sni-router", Package: "./cmd/sni-router/"},
	}

	for _, bin := range binaries {
		flags := ldflags
		if bin.ExtraLDFlags != "" {
			flags = bin.ExtraLDFlags
		}

		output := filepath.Join(b.binDir, bin.Name)
		cmd := exec.Command("go", "build",
			"-ldflags", flags,
			"-trimpath",
			"-o", output,
			bin.Package)
		cmd.Dir = b.projectDir
		cmd.Env = b.crossEnv()
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr

		if b.flags.Verbose {
			fmt.Printf("  go build -o %s %s\n", bin.Name, bin.Package)
		}

		if err := cmd.Run(); err != nil {
			return fmt.Errorf("failed to build %s: %w", bin.Name, err)
		}
		fmt.Printf("  ✓ %s\n", bin.Name)
	}

	return nil
}

func (b *Builder) buildVaultGuardian() error {
	fmt.Println("[2/8] Cross-compiling Vault Guardian (Zig)...")

	// Ensure zig is available
	if _, err := exec.LookPath("zig"); err != nil {
		return fmt.Errorf("zig not found in PATH — install from https://ziglang.org/download/")
	}

	// Vault source is sibling to core/ within the orama monorepo
	vaultDir := filepath.Join(b.projectDir, "..", "vault")
	if _, err := os.Stat(filepath.Join(vaultDir, "build.zig")); err != nil {
		return fmt.Errorf("vault source not found at %s — expected orama-vault as sibling directory: %w", vaultDir, err)
	}

	// Map Go arch to Zig target triple
	var zigTarget string
	switch b.flags.Arch {
	case "amd64":
		zigTarget = "x86_64-linux-musl"
	case "arm64":
		zigTarget = "aarch64-linux-musl"
	default:
		return fmt.Errorf("unsupported architecture for vault: %s", b.flags.Arch)
	}

	if b.flags.Verbose {
		fmt.Printf("  zig build -Dtarget=%s -Doptimize=ReleaseSafe\n", zigTarget)
	}

	cmd := exec.Command("zig", "build",
		fmt.Sprintf("-Dtarget=%s", zigTarget),
		"-Doptimize=ReleaseSafe")
	cmd.Dir = vaultDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("zig build failed: %w", err)
	}

	// Copy output binary to build bin dir
	src := filepath.Join(vaultDir, "zig-out", "bin", "vault-guardian")
	dst := filepath.Join(b.binDir, "vault-guardian")
	if err := copyFile(src, dst); err != nil {
		return fmt.Errorf("failed to copy vault-guardian binary: %w", err)
	}

	fmt.Println("  ✓ vault-guardian")
	return nil
}

// copyFile copies a file from src to dst, preserving executable permissions.
func copyFile(src, dst string) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	dstFile, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		return err
	}
	defer dstFile.Close()

	if _, err := srcFile.WriteTo(dstFile); err != nil {
		return err
	}
	return nil
}

func (b *Builder) buildOlric() error {
	fmt.Printf("[3/8] Cross-compiling Olric %s...\n", constants.OlricVersion)

	// go install doesn't support cross-compilation with GOBIN set,
	// so we create a temporary module and use go build -o instead.
	tmpDir, err := os.MkdirTemp("", "olric-build-*")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	modInit := exec.Command("go", "mod", "init", "olric-build")
	modInit.Dir = tmpDir
	modInit.Stderr = os.Stderr
	if err := modInit.Run(); err != nil {
		return fmt.Errorf("go mod init: %w", err)
	}

	modGet := exec.Command("go", "get",
		fmt.Sprintf("github.com/olric-data/olric/cmd/olric-server@%s", constants.OlricVersion))
	modGet.Dir = tmpDir
	modGet.Env = append(os.Environ(),
		"GOPROXY=https://proxy.golang.org|direct",
		"GONOSUMDB=*")
	modGet.Stderr = os.Stderr
	if err := modGet.Run(); err != nil {
		return fmt.Errorf("go get olric: %w", err)
	}

	cmd := exec.Command("go", "build",
		"-ldflags", "-s -w",
		"-trimpath",
		"-o", filepath.Join(b.binDir, "olric-server"),
		fmt.Sprintf("github.com/olric-data/olric/cmd/olric-server"))
	cmd.Dir = tmpDir
	cmd.Env = append(b.crossEnv(),
		"GOPROXY=https://proxy.golang.org|direct",
		"GONOSUMDB=*")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return err
	}
	fmt.Println("  ✓ olric-server")
	return nil
}

func (b *Builder) buildIPFSCluster() error {
	fmt.Printf("[4/8] Cross-compiling IPFS Cluster %s...\n", constants.IPFSClusterVersion)

	tmpDir, err := os.MkdirTemp("", "ipfs-cluster-build-*")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	modInit := exec.Command("go", "mod", "init", "ipfs-cluster-build")
	modInit.Dir = tmpDir
	modInit.Stderr = os.Stderr
	if err := modInit.Run(); err != nil {
		return fmt.Errorf("go mod init: %w", err)
	}

	modGet := exec.Command("go", "get",
		fmt.Sprintf("github.com/ipfs-cluster/ipfs-cluster/cmd/ipfs-cluster-service@%s", constants.IPFSClusterVersion))
	modGet.Dir = tmpDir
	modGet.Env = append(os.Environ(),
		"GOPROXY=https://proxy.golang.org|direct",
		"GONOSUMDB=*")
	modGet.Stderr = os.Stderr
	if err := modGet.Run(); err != nil {
		return fmt.Errorf("go get ipfs-cluster: %w", err)
	}

	cmd := exec.Command("go", "build",
		"-ldflags", "-s -w",
		"-trimpath",
		"-o", filepath.Join(b.binDir, "ipfs-cluster-service"),
		"github.com/ipfs-cluster/ipfs-cluster/cmd/ipfs-cluster-service")
	cmd.Dir = tmpDir
	cmd.Env = append(b.crossEnv(),
		"GOPROXY=https://proxy.golang.org|direct",
		"GONOSUMDB=*")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return err
	}
	fmt.Println("  ✓ ipfs-cluster-service")
	return nil
}

func (b *Builder) buildCoreDNS() error {
	fmt.Printf("[5/8] Building CoreDNS %s with RQLite plugin...\n", constants.CoreDNSVersion)

	buildDir := filepath.Join(b.tmpDir, "coredns-build")

	// Clone CoreDNS
	fmt.Println("  Cloning CoreDNS...")
	cmd := exec.Command("git", "clone", "--depth", "1",
		"--branch", "v"+constants.CoreDNSVersion,
		"https://github.com/coredns/coredns.git", buildDir)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to clone coredns: %w", err)
	}

	// Copy RQLite plugin from local source
	pluginSrc := filepath.Join(b.projectDir, "pkg", "coredns", "rqlite")
	pluginDst := filepath.Join(buildDir, "plugin", "rqlite")
	if err := os.MkdirAll(pluginDst, 0755); err != nil {
		return err
	}

	entries, err := os.ReadDir(pluginSrc)
	if err != nil {
		return fmt.Errorf("failed to read rqlite plugin source at %s: %w", pluginSrc, err)
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".go" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(pluginSrc, entry.Name()))
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(pluginDst, entry.Name()), data, 0644); err != nil {
			return err
		}
	}

	// Write plugin.cfg (same as build-linux-coredns.sh)
	pluginCfg := `metadata:metadata
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
	if err := os.WriteFile(filepath.Join(buildDir, "plugin.cfg"), []byte(pluginCfg), 0644); err != nil {
		return err
	}

	// Add dependencies
	fmt.Println("  Adding dependencies...")
	goPath := os.Getenv("PATH")
	baseEnv := append(os.Environ(),
		"PATH="+goPath,
		"GOPROXY=https://proxy.golang.org|direct",
		"GONOSUMDB=*")

	for _, dep := range []string{"github.com/miekg/dns@latest", "go.uber.org/zap@latest"} {
		cmd := exec.Command("go", "get", dep)
		cmd.Dir = buildDir
		cmd.Env = baseEnv
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("failed to get %s: %w", dep, err)
		}
	}

	cmd = exec.Command("go", "mod", "tidy")
	cmd.Dir = buildDir
	cmd.Env = baseEnv
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("go mod tidy failed: %w", err)
	}

	// Generate plugin code
	fmt.Println("  Generating plugin code...")
	cmd = exec.Command("go", "generate")
	cmd.Dir = buildDir
	cmd.Env = baseEnv
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("go generate failed: %w", err)
	}

	// Cross-compile
	fmt.Println("  Building binary...")
	cmd = exec.Command("go", "build",
		"-ldflags", "-s -w",
		"-trimpath",
		"-o", filepath.Join(b.binDir, "coredns"))
	cmd.Dir = buildDir
	cmd.Env = append(baseEnv,
		"GOOS=linux",
		fmt.Sprintf("GOARCH=%s", b.flags.Arch),
		"CGO_ENABLED=0")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("build failed: %w", err)
	}

	fmt.Println("  ✓ coredns")
	return nil
}

func (b *Builder) buildCaddy() error {
	fmt.Printf("[6/8] Building Caddy %s with Orama DNS module...\n", constants.CaddyVersion)

	// Ensure xcaddy is available
	if _, err := exec.LookPath("xcaddy"); err != nil {
		return fmt.Errorf("xcaddy not found in PATH — install with: go install github.com/caddyserver/xcaddy/cmd/xcaddy@latest")
	}

	moduleDir := filepath.Join(b.tmpDir, "caddy-dns-orama")
	if err := os.MkdirAll(moduleDir, 0755); err != nil {
		return err
	}

	// Write go.mod
	goMod := fmt.Sprintf(`module github.com/DeBrosOfficial/caddy-dns-orama

go 1.22

require (
	github.com/caddyserver/caddy/v2 v2.%s
	github.com/libdns/libdns v1.1.0
)
`, constants.CaddyVersion[2:])
	if err := os.WriteFile(filepath.Join(moduleDir, "go.mod"), []byte(goMod), 0644); err != nil {
		return err
	}

	// Write provider.go — read from the caddy installer's generated code
	// We inline the same provider code used by the VPS-side caddy installer
	providerCode := generateCaddyProviderCode()
	if err := os.WriteFile(filepath.Join(moduleDir, "provider.go"), []byte(providerCode), 0644); err != nil {
		return err
	}

	// go mod tidy
	cmd := exec.Command("go", "mod", "tidy")
	cmd.Dir = moduleDir
	cmd.Env = append(os.Environ(),
		"GOPROXY=https://proxy.golang.org|direct",
		"GONOSUMDB=*")
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("go mod tidy failed: %w", err)
	}

	// Build with xcaddy
	fmt.Println("  Building binary...")
	cmd = exec.Command("xcaddy", "build",
		"v"+constants.CaddyVersion,
		"--with", "github.com/DeBrosOfficial/caddy-dns-orama="+moduleDir,
		"--output", filepath.Join(b.binDir, "caddy"))
	cmd.Env = append(os.Environ(),
		"GOOS=linux",
		fmt.Sprintf("GOARCH=%s", b.flags.Arch),
		"GOPROXY=https://proxy.golang.org|direct",
		"GONOSUMDB=*")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("xcaddy build failed: %w", err)
	}

	fmt.Println("  ✓ caddy")
	return nil
}

func (b *Builder) downloadIPFS() error {
	fmt.Printf("[7/8] Downloading IPFS Kubo %s...\n", constants.IPFSKuboVersion)

	arch := b.flags.Arch
	tarball := fmt.Sprintf("kubo_%s_linux-%s.tar.gz", constants.IPFSKuboVersion, arch)
	url := fmt.Sprintf("https://dist.ipfs.tech/kubo/%s/%s", constants.IPFSKuboVersion, tarball)
	tarPath := filepath.Join(b.tmpDir, tarball)

	if err := downloadFile(url, tarPath); err != nil {
		return err
	}

	// Extract ipfs binary from kubo/ipfs
	if err := extractFileFromTarball(tarPath, "kubo/ipfs", filepath.Join(b.binDir, "ipfs")); err != nil {
		return err
	}

	fmt.Println("  ✓ ipfs")
	return nil
}

func (b *Builder) downloadRQLite() error {
	fmt.Printf("[8/8] Downloading RQLite %s...\n", constants.RQLiteVersion)

	arch := b.flags.Arch
	tarball := fmt.Sprintf("rqlite-v%s-linux-%s.tar.gz", constants.RQLiteVersion, arch)
	url := fmt.Sprintf("https://github.com/rqlite/rqlite/releases/download/v%s/%s", constants.RQLiteVersion, tarball)
	tarPath := filepath.Join(b.tmpDir, tarball)

	if err := downloadFile(url, tarPath); err != nil {
		return err
	}

	// Extract rqlited binary
	extractDir := fmt.Sprintf("rqlite-v%s-linux-%s", constants.RQLiteVersion, arch)
	if err := extractFileFromTarball(tarPath, extractDir+"/rqlited", filepath.Join(b.binDir, "rqlited")); err != nil {
		return err
	}

	fmt.Println("  ✓ rqlited")
	return nil
}

func (b *Builder) copySystemdTemplates() error {
	systemdSrc := filepath.Join(b.projectDir, "systemd")
	systemdDst := filepath.Join(b.tmpDir, "systemd")
	if err := os.MkdirAll(systemdDst, 0755); err != nil {
		return err
	}

	entries, err := os.ReadDir(systemdSrc)
	if err != nil {
		return fmt.Errorf("failed to read systemd dir: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".service") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(systemdSrc, entry.Name()))
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(systemdDst, entry.Name()), data, 0644); err != nil {
			return err
		}
	}

	return nil
}

// crossEnv returns the environment for cross-compilation.
func (b *Builder) crossEnv() []string {
	return append(os.Environ(),
		"GOOS=linux",
		fmt.Sprintf("GOARCH=%s", b.flags.Arch),
		"CGO_ENABLED=0")
}

func (b *Builder) readVersion() string {
	// Primary: read the repo-root VERSION file (single source of truth).
	// The Makefile resolves $(shell cat ../VERSION) at make time, but this
	// CLI builder is a separate Go binary that doesn't go through make, so
	// we must read VERSION directly. Try ../VERSION first (when projectDir
	// is core/), then VERSION in projectDir.
	for _, p := range []string{
		filepath.Join(b.projectDir, "..", "VERSION"),
		filepath.Join(b.projectDir, "VERSION"),
	} {
		if data, err := os.ReadFile(p); err == nil {
			if v := strings.TrimSpace(string(data)); v != "" {
				return v
			}
		}
	}
	// Fallback: parse Makefile in case someone runs an older layout where
	// VERSION is still hard-coded inline.
	data, err := os.ReadFile(filepath.Join(b.projectDir, "Makefile"))
	if err != nil {
		return "dev"
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "VERSION") {
			parts := strings.SplitN(line, ":=", 2)
			if len(parts) == 2 {
				v := strings.TrimSpace(parts[1])
				// Ignore unevaluated make expressions like $(shell ...)
				if !strings.Contains(v, "$(") {
					return v
				}
			}
		}
	}
	return "dev"
}

func (b *Builder) readCommit() string {
	cmd := exec.Command("git", "rev-parse", "--short", "HEAD")
	cmd.Dir = b.projectDir
	out, err := cmd.Output()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(out))
}

// generateCaddyProviderCode returns the Caddy DNS provider Go source.
// This is the same code used by the VPS-side caddy installer.
func generateCaddyProviderCode() string {
	return `// Package orama implements a DNS provider for Caddy that uses the Orama Network
// gateway's internal ACME API for DNS-01 challenge validation.
package orama

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/libdns/libdns"
)

func init() {
	caddy.RegisterModule(Provider{})
}

// Provider wraps the Orama DNS provider for Caddy.
type Provider struct {
	// Endpoint is the URL of the Orama gateway's ACME API
	// Default: http://localhost:6001/v1/internal/acme
	Endpoint string ` + "`json:\"endpoint,omitempty\"`" + `
}

// CaddyModule returns the Caddy module information.
func (Provider) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  "dns.providers.orama",
		New: func() caddy.Module { return new(Provider) },
	}
}

// Provision sets up the module.
func (p *Provider) Provision(ctx caddy.Context) error {
	if p.Endpoint == "" {
		p.Endpoint = "http://localhost:6001/v1/internal/acme"
	}
	return nil
}

// UnmarshalCaddyfile parses the Caddyfile configuration.
func (p *Provider) UnmarshalCaddyfile(d *caddyfile.Dispenser) error {
	for d.Next() {
		for d.NextBlock(0) {
			switch d.Val() {
			case "endpoint":
				if !d.NextArg() {
					return d.ArgErr()
				}
				p.Endpoint = d.Val()
			default:
				return d.Errf("unrecognized option: %s", d.Val())
			}
		}
	}
	return nil
}

// AppendRecords adds records to the zone.
func (p *Provider) AppendRecords(ctx context.Context, zone string, records []libdns.Record) ([]libdns.Record, error) {
	var added []libdns.Record
	for _, rec := range records {
		rr := rec.RR()
		if rr.Type != "TXT" {
			continue
		}
		fqdn := rr.Name + "." + zone
		payload := map[string]string{"fqdn": fqdn, "value": rr.Data}
		body, err := json.Marshal(payload)
		if err != nil {
			return added, fmt.Errorf("failed to marshal request: %w", err)
		}
		req, err := http.NewRequestWithContext(ctx, "POST", p.Endpoint+"/present", bytes.NewReader(body))
		if err != nil {
			return added, fmt.Errorf("failed to create request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		client := &http.Client{Timeout: 30 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			return added, fmt.Errorf("failed to present challenge: %w", err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return added, fmt.Errorf("present failed with status %d", resp.StatusCode)
		}
		added = append(added, rec)
	}
	return added, nil
}

// DeleteRecords removes records from the zone.
func (p *Provider) DeleteRecords(ctx context.Context, zone string, records []libdns.Record) ([]libdns.Record, error) {
	var deleted []libdns.Record
	for _, rec := range records {
		rr := rec.RR()
		if rr.Type != "TXT" {
			continue
		}
		fqdn := rr.Name + "." + zone
		payload := map[string]string{"fqdn": fqdn, "value": rr.Data}
		body, err := json.Marshal(payload)
		if err != nil {
			return deleted, fmt.Errorf("failed to marshal request: %w", err)
		}
		req, err := http.NewRequestWithContext(ctx, "POST", p.Endpoint+"/cleanup", bytes.NewReader(body))
		if err != nil {
			return deleted, fmt.Errorf("failed to create request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		client := &http.Client{Timeout: 30 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			return deleted, fmt.Errorf("failed to cleanup challenge: %w", err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return deleted, fmt.Errorf("cleanup failed with status %d", resp.StatusCode)
		}
		deleted = append(deleted, rec)
	}
	return deleted, nil
}

// GetRecords returns the records in the zone. Not used for ACME.
func (p *Provider) GetRecords(ctx context.Context, zone string) ([]libdns.Record, error) {
	return nil, nil
}

// SetRecords sets the records in the zone. Not used for ACME.
func (p *Provider) SetRecords(ctx context.Context, zone string, records []libdns.Record) ([]libdns.Record, error) {
	return nil, nil
}

// Interface guards
var (
	_ caddy.Module          = (*Provider)(nil)
	_ caddy.Provisioner     = (*Provider)(nil)
	_ caddyfile.Unmarshaler = (*Provider)(nil)
	_ libdns.RecordAppender = (*Provider)(nil)
	_ libdns.RecordDeleter  = (*Provider)(nil)
	_ libdns.RecordGetter   = (*Provider)(nil)
	_ libdns.RecordSetter   = (*Provider)(nil)
)
`
}
