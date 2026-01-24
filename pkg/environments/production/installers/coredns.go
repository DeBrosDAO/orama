package installers

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
)

const (
	coreDNSVersion = "1.12.0"
	coreDNSRepo    = "https://github.com/coredns/coredns.git"
)

// CoreDNSInstaller handles CoreDNS installation with RQLite plugin
type CoreDNSInstaller struct {
	*BaseInstaller
	version      string
	oramaHome    string
	rqlitePlugin string // Path to the RQLite plugin source
}

// NewCoreDNSInstaller creates a new CoreDNS installer
func NewCoreDNSInstaller(arch string, logWriter io.Writer, oramaHome string) *CoreDNSInstaller {
	return &CoreDNSInstaller{
		BaseInstaller: NewBaseInstaller(arch, logWriter),
		version:       coreDNSVersion,
		oramaHome:     oramaHome,
		rqlitePlugin:  filepath.Join(oramaHome, "src", "pkg", "coredns", "rqlite"),
	}
}

// IsInstalled checks if CoreDNS with RQLite plugin is already installed
func (ci *CoreDNSInstaller) IsInstalled() bool {
	// Check if coredns binary exists
	corednsPath := "/usr/local/bin/coredns"
	if _, err := os.Stat(corednsPath); os.IsNotExist(err) {
		return false
	}

	// Verify it has the rqlite plugin
	cmd := exec.Command(corednsPath, "-plugins")
	output, err := cmd.Output()
	if err != nil {
		return false
	}

	return containsLine(string(output), "rqlite")
}

// Install builds and installs CoreDNS with the custom RQLite plugin
func (ci *CoreDNSInstaller) Install() error {
	if ci.IsInstalled() {
		fmt.Fprintf(ci.logWriter, "  ✓ CoreDNS with RQLite plugin already installed\n")
		return nil
	}

	fmt.Fprintf(ci.logWriter, "  Building CoreDNS with RQLite plugin...\n")

	// Check if Go is available
	if _, err := exec.LookPath("go"); err != nil {
		return fmt.Errorf("go not found - required to build CoreDNS. Please install Go first")
	}

	// Check if RQLite plugin source exists
	if _, err := os.Stat(ci.rqlitePlugin); os.IsNotExist(err) {
		return fmt.Errorf("RQLite plugin source not found at %s - ensure the repository is cloned", ci.rqlitePlugin)
	}

	buildDir := "/tmp/coredns-build"

	// Clean up any previous build
	os.RemoveAll(buildDir)
	if err := os.MkdirAll(buildDir, 0755); err != nil {
		return fmt.Errorf("failed to create build directory: %w", err)
	}
	defer os.RemoveAll(buildDir)

	// Clone CoreDNS
	fmt.Fprintf(ci.logWriter, "    Cloning CoreDNS v%s...\n", ci.version)
	cmd := exec.Command("git", "clone", "--depth", "1", "--branch", "v"+ci.version, coreDNSRepo, buildDir)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to clone CoreDNS: %w\n%s", err, string(output))
	}

	// Copy custom RQLite plugin
	fmt.Fprintf(ci.logWriter, "    Copying RQLite plugin...\n")
	pluginDir := filepath.Join(buildDir, "plugin", "rqlite")
	if err := os.MkdirAll(pluginDir, 0755); err != nil {
		return fmt.Errorf("failed to create plugin directory: %w", err)
	}

	// Copy all .go files from the RQLite plugin
	files, err := os.ReadDir(ci.rqlitePlugin)
	if err != nil {
		return fmt.Errorf("failed to read plugin source: %w", err)
	}

	for _, file := range files {
		if file.IsDir() || filepath.Ext(file.Name()) != ".go" {
			continue
		}
		srcPath := filepath.Join(ci.rqlitePlugin, file.Name())
		dstPath := filepath.Join(pluginDir, file.Name())

		data, err := os.ReadFile(srcPath)
		if err != nil {
			return fmt.Errorf("failed to read %s: %w", file.Name(), err)
		}
		if err := os.WriteFile(dstPath, data, 0644); err != nil {
			return fmt.Errorf("failed to write %s: %w", file.Name(), err)
		}
	}

	// Create plugin.cfg with our custom RQLite plugin
	fmt.Fprintf(ci.logWriter, "    Configuring plugins...\n")
	pluginCfg := ci.generatePluginConfig()
	pluginCfgPath := filepath.Join(buildDir, "plugin.cfg")
	if err := os.WriteFile(pluginCfgPath, []byte(pluginCfg), 0644); err != nil {
		return fmt.Errorf("failed to write plugin.cfg: %w", err)
	}

	// Add dependencies
	fmt.Fprintf(ci.logWriter, "    Adding dependencies...\n")
	goPath := os.Getenv("PATH") + ":/usr/local/go/bin"

	getCmd := exec.Command("go", "get", "github.com/miekg/dns@latest")
	getCmd.Dir = buildDir
	getCmd.Env = append(os.Environ(), "PATH="+goPath)
	if output, err := getCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to get miekg/dns: %w\n%s", err, string(output))
	}

	getCmd = exec.Command("go", "get", "go.uber.org/zap@latest")
	getCmd.Dir = buildDir
	getCmd.Env = append(os.Environ(), "PATH="+goPath)
	if output, err := getCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to get zap: %w\n%s", err, string(output))
	}

	tidyCmd := exec.Command("go", "mod", "tidy")
	tidyCmd.Dir = buildDir
	tidyCmd.Env = append(os.Environ(), "PATH="+goPath)
	if output, err := tidyCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to run go mod tidy: %w\n%s", err, string(output))
	}

	// Generate plugin code
	fmt.Fprintf(ci.logWriter, "    Generating plugin code...\n")
	genCmd := exec.Command("go", "generate")
	genCmd.Dir = buildDir
	genCmd.Env = append(os.Environ(), "PATH="+goPath)
	if output, err := genCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to generate: %w\n%s", err, string(output))
	}

	// Build CoreDNS
	fmt.Fprintf(ci.logWriter, "    Building CoreDNS binary...\n")
	buildCmd := exec.Command("go", "build", "-o", "coredns")
	buildCmd.Dir = buildDir
	buildCmd.Env = append(os.Environ(), "PATH="+goPath, "CGO_ENABLED=0")
	if output, err := buildCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to build CoreDNS: %w\n%s", err, string(output))
	}

	// Verify the binary has rqlite plugin
	verifyCmd := exec.Command(filepath.Join(buildDir, "coredns"), "-plugins")
	output, err := verifyCmd.Output()
	if err != nil {
		return fmt.Errorf("failed to verify CoreDNS binary: %w", err)
	}
	if !containsLine(string(output), "rqlite") {
		return fmt.Errorf("CoreDNS binary does not contain rqlite plugin")
	}

	// Install the binary
	fmt.Fprintf(ci.logWriter, "    Installing CoreDNS binary...\n")
	srcBinary := filepath.Join(buildDir, "coredns")
	dstBinary := "/usr/local/bin/coredns"

	data, err := os.ReadFile(srcBinary)
	if err != nil {
		return fmt.Errorf("failed to read built binary: %w", err)
	}
	if err := os.WriteFile(dstBinary, data, 0755); err != nil {
		return fmt.Errorf("failed to install binary: %w", err)
	}

	fmt.Fprintf(ci.logWriter, "  ✓ CoreDNS with RQLite plugin installed\n")
	return nil
}

// Configure creates CoreDNS configuration files
func (ci *CoreDNSInstaller) Configure(domain string, rqliteDSN string, ns1IP, ns2IP, ns3IP string) error {
	configDir := "/etc/coredns"
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	// Create Corefile
	corefile := ci.generateCorefile(domain, rqliteDSN, configDir)
	if err := os.WriteFile(filepath.Join(configDir, "Corefile"), []byte(corefile), 0644); err != nil {
		return fmt.Errorf("failed to write Corefile: %w", err)
	}

	// Create zone file
	zonefile := ci.generateZoneFile(domain, ns1IP, ns2IP, ns3IP)
	if err := os.WriteFile(filepath.Join(configDir, "db."+domain), []byte(zonefile), 0644); err != nil {
		return fmt.Errorf("failed to write zone file: %w", err)
	}

	return nil
}

// generatePluginConfig creates the plugin.cfg for CoreDNS
func (ci *CoreDNSInstaller) generatePluginConfig() string {
	return `# CoreDNS plugins with RQLite support for dynamic DNS records
metadata:metadata
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
}

// generateCorefile creates the CoreDNS configuration
func (ci *CoreDNSInstaller) generateCorefile(domain, rqliteDSN, configDir string) string {
	return fmt.Sprintf(`# CoreDNS configuration for %s
# Uses RQLite for dynamic DNS records (deployments, ACME challenges)
# Falls back to static zone file for base records (SOA, NS)

%s {
    # First try RQLite for dynamic records (TXT for ACME, A for deployments)
    rqlite {
        dsn %s
        refresh 5s
        ttl 60
        cache_size 10000
    }

    # Fall back to static zone file for SOA/NS records
    file %s/db.%s

    # Enable logging and error reporting
    log
    errors
    cache 60
}

# Forward all other queries to upstream DNS
. {
    forward . 8.8.8.8 8.8.4.4 1.1.1.1
    cache 300
    errors
}
`, domain, domain, rqliteDSN, configDir, domain)
}

// generateZoneFile creates the static DNS zone file
func (ci *CoreDNSInstaller) generateZoneFile(domain, ns1IP, ns2IP, ns3IP string) string {
	return fmt.Sprintf(`$ORIGIN %s.
$TTL 300

@       IN      SOA     ns1.%s. admin.%s. (
                        2024012401 ; Serial
                        3600       ; Refresh
                        1800       ; Retry
                        604800     ; Expire
                        300 )      ; Negative TTL

; Nameservers
@       IN      NS      ns1.%s.
@       IN      NS      ns2.%s.
@       IN      NS      ns3.%s.

; Nameserver A records
ns1     IN      A       %s
ns2     IN      A       %s
ns3     IN      A       %s

; Root domain points to all nodes (round-robin)
@       IN      A       %s
@       IN      A       %s
@       IN      A       %s

; Wildcard fallback (RQLite records take precedence for specific subdomains)
*       IN      A       %s
*       IN      A       %s
*       IN      A       %s
`, domain, domain, domain, domain, domain, domain,
		ns1IP, ns2IP, ns3IP,
		ns1IP, ns2IP, ns3IP,
		ns1IP, ns2IP, ns3IP)
}

// containsLine checks if a string contains a specific line
func containsLine(text, line string) bool {
	for _, l := range splitLines(text) {
		if l == line || l == "dns."+line {
			return true
		}
	}
	return false
}

// splitLines splits a string into lines
func splitLines(text string) []string {
	var lines []string
	var current string
	for _, c := range text {
		if c == '\n' {
			lines = append(lines, current)
			current = ""
		} else {
			current += string(c)
		}
	}
	if current != "" {
		lines = append(lines, current)
	}
	return lines
}
