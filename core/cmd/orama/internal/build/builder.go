package build

import (
	"errors"
	"fmt"
	"io/fs"
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
	// CGO builds the binary with cgo through zig cc (see cgo.go).
	CGO bool
}

// Builder orchestrates the entire build process.
type Builder struct {
	flags      *Flags
	projectDir string
	tmpDir     string
	binDir     string
	version    string
	commit     string
	outputPath string // the archive Build wrote
	date       string
	// zig is the zig binary for the vault and the cgo cross-compiles,
	// resolved and version-checked before anything is built.
	zig string
	// agent signs the manifest: the RootWallet agent.
	agent archiveSigner
}

// NewBuilder creates a new Builder.
func NewBuilder(flags *Flags) *Builder {
	return &Builder{flags: flags, agent: newAgentSigner()}
}

// OutputPath is the archive the last successful Build wrote. A caller that
// builds and then deploys uses exactly this file, never the newest one it can
// find: a shared /tmp holds archives from other checkouts too.
func (b *Builder) OutputPath() string { return b.outputPath }

// Build runs the full build pipeline.
func (b *Builder) Build() error {
	start := time.Now()

	// A bad --arch is a command-line mistake; say so before looking for a
	// project root, a zig toolchain or anything else the build needs.
	if err := validateArch(b.flags.Arch); err != nil {
		return err
	}

	// Find project root
	projectDir, err := findProjectRoot()
	if err != nil {
		return err
	}
	b.projectDir = projectDir

	signer, err := b.signingPlan()
	if err != nil {
		return err
	}

	b.zig, err = resolveZig(filepath.Join(projectDir, "..", "vault"))
	if err != nil {
		return err
	}

	if b.version, err = b.readVersion(); err != nil {
		return err
	}
	if b.commit, err = b.readCommit(); err != nil {
		return err
	}
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

	// Step 11: Sign the manifest (unless --unsigned)
	if signer != "" {
		fmt.Printf("\nSigning the manifest as %s (approve in RootWallet if asked)...\n", signer)
	}
	manifestJSON, signature, err := sealManifest(manifest, b.agent, signer)
	if err != nil {
		return err
	}
	switch {
	case signer == "":
		fmt.Printf("\n⚠️  Unsigned archive: no node will install it\n")
	case len(manifest.Signers) > 0:
		fmt.Printf("  Signed. Nodes that install this build will trust only: %s\n", strings.Join(manifest.Signers, ", "))
	default:
		fmt.Printf("  Signed.\n")
	}

	// Step 12: Create archive
	outputPath := b.flags.Output
	if outputPath == "" {
		outputPath = filepath.Join(ArchiveDir, ArchiveName(b.version, b.flags.Arch))
	}

	if err := b.createArchive(outputPath, manifest, manifestJSON, signature); err != nil {
		return fmt.Errorf("failed to create archive: %w", err)
	}
	b.outputPath = outputPath

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

	for _, bin := range oramaBinaries(gatewayLDFlags) {
		flags := ldflags
		if bin.ExtraLDFlags != "" {
			flags = bin.ExtraLDFlags
		}

		env, err := b.buildEnvFor(bin)
		if err != nil {
			return err
		}
		output := filepath.Join(b.binDir, bin.Name)
		cmd := exec.Command("go", goBuildArgs(bin, flags, output)...)
		cmd.Dir = b.projectDir
		cmd.Env = env
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

// oramaBinaries lists the Go binaries in the archive.
func oramaBinaries(gatewayLDFlags string) []oramaBinary {
	return []oramaBinary{
		{Name: "orama", Package: "./cmd/orama/"},
		{Name: "orama-node", Package: "./cmd/node/"},
		{Name: "orama-privhelper", Package: "./cmd/privhelper/"},
		{Name: "gateway", Package: "./cmd/gateway/", ExtraLDFlags: gatewayLDFlags, CGO: true},
		{Name: "identity", Package: "./cmd/identity/"},
		{Name: "sfu", Package: "./cmd/sfu/"},
		{Name: "turn", Package: "./cmd/turn/"},
		{Name: "orama-sni-router", Package: "./cmd/sni-router/"},
		{Name: "pubsub", Package: "./cmd/pubsub/"},
	}
}

func (b *Builder) buildVaultGuardian() error {
	fmt.Println("[2/8] Cross-compiling Vault Guardian (Zig)...")

	// Vault source is sibling to core/ within the orama monorepo
	vaultDir := filepath.Join(b.projectDir, "..", "vault")
	if _, err := os.Stat(filepath.Join(vaultDir, "build.zig")); err != nil {
		return fmt.Errorf("vault source not found at %s — expected orama-vault as sibling directory: %w", vaultDir, err)
	}

	zigTarget, err := zigTargetFor(b.flags.Arch)
	if err != nil {
		return fmt.Errorf("vault: %w", err)
	}

	// Emit directly into zig-out/bin so the copy step below is unchanged.
	outDir := filepath.Join(vaultDir, "zig-out", "bin")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("failed to create vault output dir: %w", err)
	}

	if b.flags.Verbose {
		fmt.Printf("  zig build-exe src/main.zig -target %s -O ReleaseSafe\n", zigTarget)
	}

	// Use `zig build-exe` rather than `zig build` (the build-system runner).
	// `zig build` first compiles a HOST build-runner binary and links it against
	// the host libc; on newer macOS SDKs (Darwin 25+/macOS 26) Zig 0.15.2 fails
	// to link that runner (undefined libSystem symbols) even though the
	// linux-musl cross-compile itself works fine. The vault build.zig is a
	// single executable built from src/main.zig, so build-exe is equivalent and
	// sidesteps the broken host runner. On platforms where `zig build` works,
	// this produces an identical binary.
	cmd := exec.Command(b.zig, "build-exe",
		"src/main.zig",
		"-target", zigTarget,
		"-O", "ReleaseSafe",
		"--name", "vault-guardian",
		"-femit-bin=zig-out/bin/vault-guardian")
	cmd.Dir = vaultDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("zig build-exe failed: %w", err)
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
		"github.com/olric-data/olric/cmd/olric-server")
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
	cmd := gitCommand("clone", "--depth", "1",
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

// caddyModulePath is the Go module of the Caddy modules the node's Caddy is
// built with (dns.providers.orama and caddy.storage.orama), kept in the
// repository's caddy/ directory beside core/.
const caddyModulePath = "github.com/DeBrosOfficial/caddy-orama"

func (b *Builder) buildCaddy() error {
	fmt.Printf("[6/8] Building Caddy %s with the Orama modules...\n", constants.CaddyVersion)

	// Ensure xcaddy is available
	if _, err := exec.LookPath("xcaddy"); err != nil {
		return fmt.Errorf("xcaddy not found in PATH — install with: go install github.com/caddyserver/xcaddy/cmd/xcaddy@latest")
	}

	moduleDir := filepath.Join(b.projectDir, "..", "caddy")
	if _, err := os.Stat(filepath.Join(moduleDir, "go.mod")); err != nil {
		return fmt.Errorf("the Caddy modules are missing at %s (expected the repository's caddy/ directory): %w", moduleDir, err)
	}

	fmt.Println("  Building binary...")
	cmd := exec.Command("xcaddy", "build",
		"v"+constants.CaddyVersion,
		"--with", caddyModulePath+"="+moduleDir,
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

	if err := fetchPinned(url, tarPath, tarball, arch, constants.IPFSKuboTarballSHA256); err != nil {
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

	if err := fetchPinned(url, tarPath, tarball, arch, constants.RQLiteTarballSHA256); err != nil {
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
		if entry.IsDir() {
			continue
		}
		if !strings.HasSuffix(entry.Name(), ".service") && !strings.HasSuffix(entry.Name(), ".timer") {
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

// readVersion reads the repository VERSION file, the single source of truth
// (pkg/version embeds the same value, and a test holds the two together). A
// build that cannot read it fails: the version is part of what the archive
// signature covers, so a guessed "dev" would be signed as if it were real.
func (b *Builder) readVersion() (string, error) {
	var tried []string
	for _, p := range []string{
		filepath.Join(b.projectDir, "..", "VERSION"),
		filepath.Join(b.projectDir, "VERSION"),
	} {
		data, err := os.ReadFile(p)
		if errors.Is(err, fs.ErrNotExist) {
			tried = append(tried, p)
			continue
		}
		if err != nil {
			return "", fmt.Errorf("read %s: %w", p, err)
		}
		v := strings.TrimSpace(string(data))
		if v == "" {
			return "", fmt.Errorf("%s is empty", p)
		}
		return v, nil
	}
	return "", fmt.Errorf("no VERSION file (looked for %s)", strings.Join(tried, ", "))
}

// readCommit is the checkout's commit. A build outside a git checkout fails
// rather than being signed as commit "unknown".
//
// The command does not inherit GIT_DIR. A push hook and plenty of scripts set
// it, and git then ignores cmd.Dir and reports that other repository's commit.
func (b *Builder) readCommit() (string, error) {
	cmd := gitCommand("rev-parse", "--short", "HEAD")
	cmd.Dir = b.projectDir
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("read the commit of %s with git: %w", b.projectDir, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// gitCommand runs git in the directory the caller sets, not in whatever
// repository GIT_DIR names. See readCommit.
func gitCommand(args ...string) *exec.Cmd {
	cmd := exec.Command("git", args...)
	cmd.Env = withoutGitDir(os.Environ())
	return cmd
}

// withoutGitDir drops the variables that point git at a repository other than
// the working directory it is started in.
func withoutGitDir(environ []string) []string {
	out := make([]string, 0, len(environ))
	for _, entry := range environ {
		if strings.HasPrefix(entry, "GIT_DIR=") || strings.HasPrefix(entry, "GIT_WORK_TREE=") || strings.HasPrefix(entry, "GIT_COMMON_DIR=") {
			continue
		}
		out = append(out, entry)
	}
	return out
}
