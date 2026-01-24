package installers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"
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

// Configure creates CoreDNS configuration files and seeds static DNS records into RQLite
func (ci *CoreDNSInstaller) Configure(domain string, rqliteDSN string, ns1IP, ns2IP, ns3IP string) error {
	configDir := "/etc/coredns"
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	// Create Corefile (uses only RQLite plugin)
	corefile := ci.generateCorefile(domain, rqliteDSN)
	if err := os.WriteFile(filepath.Join(configDir, "Corefile"), []byte(corefile), 0644); err != nil {
		return fmt.Errorf("failed to write Corefile: %w", err)
	}

	// Seed static DNS records into RQLite
	fmt.Fprintf(ci.logWriter, "  Seeding static DNS records into RQLite...\n")
	if err := ci.seedStaticRecords(domain, rqliteDSN, ns1IP, ns2IP, ns3IP); err != nil {
		// Don't fail on seed errors - RQLite might not be up yet
		fmt.Fprintf(ci.logWriter, "  ⚠️  Could not seed DNS records (RQLite may not be ready): %v\n", err)
	} else {
		fmt.Fprintf(ci.logWriter, "  ✓ Static DNS records seeded\n")
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

// generateCorefile creates the CoreDNS configuration (RQLite only)
func (ci *CoreDNSInstaller) generateCorefile(domain, rqliteDSN string) string {
	return fmt.Sprintf(`# CoreDNS configuration for %s
# Uses RQLite for ALL DNS records (static + dynamic)
# Static records (SOA, NS, A) are seeded into RQLite during installation

%s {
    # RQLite handles all records: SOA, NS, A, TXT (ACME), etc.
    rqlite {
        dsn %s
        refresh 5s
        ttl 60
        cache_size 10000
    }

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
`, domain, domain, rqliteDSN)
}

// seedStaticRecords inserts static zone records into RQLite
func (ci *CoreDNSInstaller) seedStaticRecords(domain, rqliteDSN, ns1IP, ns2IP, ns3IP string) error {
	// Generate serial based on current date
	serial := fmt.Sprintf("%d", time.Now().Unix())

	// SOA record format: "mname rname serial refresh retry expire minimum"
	soaValue := fmt.Sprintf("ns1.%s. admin.%s. %s 3600 1800 604800 300", domain, domain, serial)

	// Define all static records
	records := []struct {
		fqdn       string
		recordType string
		value      string
		ttl        int
	}{
		// SOA record
		{domain + ".", "SOA", soaValue, 300},

		// NS records
		{domain + ".", "NS", "ns1." + domain + ".", 300},
		{domain + ".", "NS", "ns2." + domain + ".", 300},
		{domain + ".", "NS", "ns3." + domain + ".", 300},

		// Nameserver A records (glue)
		{"ns1." + domain + ".", "A", ns1IP, 300},
		{"ns2." + domain + ".", "A", ns2IP, 300},
		{"ns3." + domain + ".", "A", ns3IP, 300},

		// Root domain A records (round-robin)
		{domain + ".", "A", ns1IP, 300},
		{domain + ".", "A", ns2IP, 300},
		{domain + ".", "A", ns3IP, 300},

		// Wildcard A records (round-robin)
		{"*." + domain + ".", "A", ns1IP, 300},
		{"*." + domain + ".", "A", ns2IP, 300},
		{"*." + domain + ".", "A", ns3IP, 300},
	}

	// Build SQL statements
	var statements []string
	for _, r := range records {
		// Use INSERT OR REPLACE to handle updates
		stmt := fmt.Sprintf(
			`INSERT OR REPLACE INTO dns_records (fqdn, record_type, value, ttl, namespace, created_by) VALUES ('%s', '%s', '%s', %d, 'system', 'system')`,
			r.fqdn, r.recordType, r.value, r.ttl,
		)
		statements = append(statements, stmt)
	}

	// Execute via RQLite HTTP API
	return ci.executeRQLiteStatements(rqliteDSN, statements)
}

// executeRQLiteStatements executes SQL statements via RQLite HTTP API
func (ci *CoreDNSInstaller) executeRQLiteStatements(rqliteDSN string, statements []string) error {
	// RQLite execute endpoint
	executeURL := rqliteDSN + "/db/execute?pretty&timings"

	// Build request body
	body, err := json.Marshal(statements)
	if err != nil {
		return fmt.Errorf("failed to marshal statements: %w", err)
	}

	// Create request
	req, err := http.NewRequest("POST", executeURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	// Execute with timeout
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("RQLite returned status %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
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
