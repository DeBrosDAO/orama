package sandbox

import (
	"bufio"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

// Setup runs the interactive sandbox setup wizard.
func Setup() error {
	fmt.Println("Orama Sandbox Setup")
	fmt.Println("====================")
	fmt.Println()

	reader := bufio.NewReader(os.Stdin)

	// Step 1: Hetzner API token
	fmt.Print("Hetzner Cloud API token: ")
	token, err := reader.ReadString('\n')
	if err != nil {
		return fmt.Errorf("read token: %w", err)
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return fmt.Errorf("API token is required")
	}

	fmt.Print("  Validating token... ")
	client := NewHetznerClient(token)
	if err := client.ValidateToken(); err != nil {
		fmt.Println("FAILED")
		return fmt.Errorf("invalid token: %w", err)
	}
	fmt.Println("OK")
	fmt.Println()

	// Step 2: Domain
	fmt.Print("Sandbox domain (e.g., sbx.dbrs.space): ")
	domain, err := reader.ReadString('\n')
	if err != nil {
		return fmt.Errorf("read domain: %w", err)
	}
	domain = strings.TrimSpace(domain)
	if domain == "" {
		return fmt.Errorf("domain is required")
	}

	cfg := &Config{
		HetznerAPIToken: token,
		Domain:          domain,
	}

	// Step 3: Location selection
	fmt.Println()
	location, err := selectLocation(client, reader)
	if err != nil {
		return err
	}
	cfg.Location = location

	// Step 4: Server type selection
	fmt.Println()
	serverType, err := selectServerType(client, reader, location)
	if err != nil {
		return err
	}
	cfg.ServerType = serverType

	// Step 5: Floating IPs
	fmt.Println()
	fmt.Println("Checking floating IPs...")
	floatingIPs, err := setupFloatingIPs(client, cfg.Location)
	if err != nil {
		return err
	}
	cfg.FloatingIPs = floatingIPs

	// Step 6: Firewall
	fmt.Println()
	fmt.Println("Checking firewall...")
	fwID, err := setupFirewall(client)
	if err != nil {
		return err
	}
	cfg.FirewallID = fwID

	// Step 7: SSH key
	fmt.Println()
	fmt.Println("Setting up SSH key...")
	sshKeyConfig, err := setupSSHKey(client)
	if err != nil {
		return err
	}
	cfg.SSHKey = sshKeyConfig

	// Step 8: Display DNS instructions
	fmt.Println()
	fmt.Println("DNS Configuration")
	fmt.Println("-----------------")
	fmt.Println("Configure the following at your domain registrar:")
	fmt.Println()
	fmt.Printf("  1. Add glue records (Personal DNS Servers):\n")
	fmt.Printf("     ns1.%s  ->  %s\n", domain, cfg.FloatingIPs[0].IP)
	fmt.Printf("     ns2.%s  ->  %s\n", domain, cfg.FloatingIPs[1].IP)
	fmt.Println()
	fmt.Printf("  2. Set custom nameservers for %s:\n", domain)
	fmt.Printf("     ns1.%s\n", domain)
	fmt.Printf("     ns2.%s\n", domain)
	fmt.Println()

	// Step 9: Verify DNS (optional)
	fmt.Print("Verify DNS now? [y/N]: ")
	verifyChoice, _ := reader.ReadString('\n')
	verifyChoice = strings.TrimSpace(strings.ToLower(verifyChoice))
	if verifyChoice == "y" || verifyChoice == "yes" {
		verifyDNS(domain, cfg.FloatingIPs, reader)
	}

	// Save config
	if err := SaveConfig(cfg); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	fmt.Println()
	fmt.Println("Setup complete! Config saved to ~/.orama/sandbox.yaml")
	fmt.Println()
	fmt.Println("Next: orama sandbox create")
	return nil
}

// selectLocation fetches available Hetzner locations and lets the user pick one.
func selectLocation(client *HetznerClient, reader *bufio.Reader) (string, error) {
	fmt.Println("Fetching available locations...")
	locations, err := client.ListLocations()
	if err != nil {
		return "", fmt.Errorf("list locations: %w", err)
	}

	sort.Slice(locations, func(i, j int) bool {
		return locations[i].Name < locations[j].Name
	})

	defaultLoc := "nbg1"
	fmt.Println("  Available datacenter locations:")
	for i, loc := range locations {
		def := ""
		if loc.Name == defaultLoc {
			def = " (default)"
		}
		fmt.Printf("    %d) %s — %s, %s%s\n", i+1, loc.Name, loc.City, loc.Country, def)
	}

	fmt.Printf("\n  Select location [%s]: ", defaultLoc)
	choice, _ := reader.ReadString('\n')
	choice = strings.TrimSpace(choice)

	if choice == "" {
		fmt.Printf("  Using %s\n", defaultLoc)
		return defaultLoc, nil
	}

	// Try as number first
	if num, err := strconv.Atoi(choice); err == nil && num >= 1 && num <= len(locations) {
		loc := locations[num-1].Name
		fmt.Printf("  Using %s\n", loc)
		return loc, nil
	}

	// Try as location name
	for _, loc := range locations {
		if strings.EqualFold(loc.Name, choice) {
			fmt.Printf("  Using %s\n", loc.Name)
			return loc.Name, nil
		}
	}

	return "", fmt.Errorf("unknown location %q", choice)
}

// selectServerType fetches available server types for a location and lets the user pick one.
func selectServerType(client *HetznerClient, reader *bufio.Reader, location string) (string, error) {
	fmt.Println("Fetching available server types...")
	serverTypes, err := client.ListServerTypes()
	if err != nil {
		return "", fmt.Errorf("list server types: %w", err)
	}

	// Filter to x86 shared-vCPU types available at the selected location, skip deprecated
	type option struct {
		name    string
		cores   int
		memory  float64
		disk    int
		hourly  string
		monthly string
	}

	var options []option
	for _, st := range serverTypes {
		if st.Architecture != "x86" {
			continue
		}
		if st.Deprecation != nil {
			continue
		}
		// Only show shared-vCPU types (cx/cpx prefixes) — skip dedicated (ccx/cx5x)
		if !strings.HasPrefix(st.Name, "cx") && !strings.HasPrefix(st.Name, "cpx") {
			continue
		}

		// Find pricing for the selected location
		hourly, monthly := "", ""
		for _, p := range st.Prices {
			if p.Location == location {
				hourly = p.Hourly.Gross
				monthly = p.Monthly.Gross
				break
			}
		}
		if hourly == "" {
			continue // Not available in this location
		}

		options = append(options, option{
			name:    st.Name,
			cores:   st.Cores,
			memory:  st.Memory,
			disk:    st.Disk,
			hourly:  hourly,
			monthly: monthly,
		})
	}

	if len(options) == 0 {
		return "", fmt.Errorf("no server types available in %s", location)
	}

	// Sort by hourly price (cheapest first)
	sort.Slice(options, func(i, j int) bool {
		pi, _ := strconv.ParseFloat(options[i].hourly, 64)
		pj, _ := strconv.ParseFloat(options[j].hourly, 64)
		return pi < pj
	})

	defaultType := options[0].name // cheapest
	fmt.Printf("  Available server types in %s:\n", location)
	for i, opt := range options {
		def := ""
		if opt.name == defaultType {
			def = " (default)"
		}
		fmt.Printf("    %d) %-8s %d vCPU / %4.0f GB RAM / %3d GB disk — €%s/hr (€%s/mo)%s\n",
			i+1, opt.name, opt.cores, opt.memory, opt.disk, formatPrice(opt.hourly), formatPrice(opt.monthly), def)
	}

	fmt.Printf("\n  Select server type [%s]: ", defaultType)
	choice, _ := reader.ReadString('\n')
	choice = strings.TrimSpace(choice)

	if choice == "" {
		fmt.Printf("  Using %s (×5 nodes ≈ €%s/hr)\n", defaultType, multiplyPrice(options[0].hourly, 5))
		return defaultType, nil
	}

	// Try as number
	if num, err := strconv.Atoi(choice); err == nil && num >= 1 && num <= len(options) {
		opt := options[num-1]
		fmt.Printf("  Using %s (×5 nodes ≈ €%s/hr)\n", opt.name, multiplyPrice(opt.hourly, 5))
		return opt.name, nil
	}

	// Try as name
	for _, opt := range options {
		if strings.EqualFold(opt.name, choice) {
			fmt.Printf("  Using %s (×5 nodes ≈ €%s/hr)\n", opt.name, multiplyPrice(opt.hourly, 5))
			return opt.name, nil
		}
	}

	return "", fmt.Errorf("unknown server type %q", choice)
}

// formatPrice trims trailing zeros from a price string like "0.0063000000000000" → "0.0063".
func formatPrice(price string) string {
	f, err := strconv.ParseFloat(price, 64)
	if err != nil {
		return price
	}
	// Use enough precision then trim trailing zeros
	s := fmt.Sprintf("%.4f", f)
	s = strings.TrimRight(s, "0")
	s = strings.TrimRight(s, ".")
	return s
}

// multiplyPrice multiplies a price string by n and returns formatted.
func multiplyPrice(price string, n int) string {
	f, err := strconv.ParseFloat(price, 64)
	if err != nil {
		return "?"
	}
	return formatPrice(fmt.Sprintf("%.10f", f*float64(n)))
}

// setupFloatingIPs checks for existing floating IPs or creates new ones.
func setupFloatingIPs(client *HetznerClient, location string) ([]FloatIP, error) {
	existing, err := client.ListFloatingIPsByLabel("orama-sandbox-dns=true")
	if err != nil {
		return nil, fmt.Errorf("list floating IPs: %w", err)
	}

	if len(existing) >= 2 {
		fmt.Printf("  Found %d existing floating IPs:\n", len(existing))
		result := make([]FloatIP, 2)
		for i := 0; i < 2; i++ {
			fmt.Printf("    ns%d: %s (ID: %d)\n", i+1, existing[i].IP, existing[i].ID)
			result[i] = FloatIP{ID: existing[i].ID, IP: existing[i].IP}
		}
		return result, nil
	}

	// Need to create missing floating IPs
	needed := 2 - len(existing)
	fmt.Printf("  Need to create %d floating IP(s)...\n", needed)

	reader := bufio.NewReader(os.Stdin)
	fmt.Printf("  Create %d floating IP(s) in %s? (~$0.005/hr each) [Y/n]: ", needed, location)
	choice, _ := reader.ReadString('\n')
	choice = strings.TrimSpace(strings.ToLower(choice))
	if choice == "n" || choice == "no" {
		return nil, fmt.Errorf("floating IPs required, aborting setup")
	}

	result := make([]FloatIP, 0, 2)
	for _, fip := range existing {
		result = append(result, FloatIP{ID: fip.ID, IP: fip.IP})
	}

	for i := len(existing); i < 2; i++ {
		desc := fmt.Sprintf("orama-sandbox-ns%d", i+1)
		labels := map[string]string{"orama-sandbox-dns": "true"}
		fip, err := client.CreateFloatingIP(location, desc, labels)
		if err != nil {
			return nil, fmt.Errorf("create floating IP %d: %w", i+1, err)
		}
		fmt.Printf("    Created ns%d: %s (ID: %d)\n", i+1, fip.IP, fip.ID)
		result = append(result, FloatIP{ID: fip.ID, IP: fip.IP})
	}

	return result, nil
}

// setupFirewall ensures a sandbox firewall exists.
func setupFirewall(client *HetznerClient) (int64, error) {
	existing, err := client.ListFirewallsByLabel("orama-sandbox=infra")
	if err != nil {
		return 0, fmt.Errorf("list firewalls: %w", err)
	}

	if len(existing) > 0 {
		fmt.Printf("  Found existing firewall: %s (ID: %d)\n", existing[0].Name, existing[0].ID)
		return existing[0].ID, nil
	}

	fmt.Print("  Creating sandbox firewall... ")
	fw, err := client.CreateFirewall(
		"orama-sandbox",
		SandboxFirewallRules(),
		map[string]string{"orama-sandbox": "infra"},
	)
	if err != nil {
		fmt.Println("FAILED")
		return 0, fmt.Errorf("create firewall: %w", err)
	}
	fmt.Printf("OK (ID: %d)\n", fw.ID)
	return fw.ID, nil
}

// setupSSHKey generates an SSH keypair and uploads it to Hetzner.
func setupSSHKey(client *HetznerClient) (SSHKeyConfig, error) {
	dir, err := configDir()
	if err != nil {
		return SSHKeyConfig{}, err
	}

	privPath := dir + "/sandbox_key"
	pubPath := privPath + ".pub"

	// Check for existing key
	if _, err := os.Stat(privPath); err == nil {
		fmt.Printf("  SSH key already exists: %s\n", privPath)

		// Read public key and check if it's on Hetzner
		pubData, err := os.ReadFile(pubPath)
		if err != nil {
			return SSHKeyConfig{}, fmt.Errorf("read public key: %w", err)
		}

		// Try to upload (will fail with uniqueness error if already exists)
		key, err := client.UploadSSHKey("orama-sandbox", strings.TrimSpace(string(pubData)))
		if err != nil {
			// Key already exists on Hetzner — find it by fingerprint
			sshPubKey, _, _, _, parseErr := ssh.ParseAuthorizedKey(pubData)
			if parseErr != nil {
				return SSHKeyConfig{}, fmt.Errorf("parse public key to find fingerprint: %w", parseErr)
			}
			fingerprint := ssh.FingerprintLegacyMD5(sshPubKey)

			existing, listErr := client.ListSSHKeysByFingerprint(fingerprint)
			if listErr == nil && len(existing) > 0 {
				fmt.Printf("  Found existing SSH key on Hetzner (ID: %d)\n", existing[0].ID)
				return SSHKeyConfig{
					HetznerID:      existing[0].ID,
					PrivateKeyPath: "~/.orama/sandbox_key",
					PublicKeyPath:  "~/.orama/sandbox_key.pub",
				}, nil
			}

			return SSHKeyConfig{}, fmt.Errorf("SSH key exists locally but could not find it on Hetzner (fingerprint: %s): %w", fingerprint, err)
		}

		fmt.Printf("  Uploaded to Hetzner (ID: %d)\n", key.ID)
		return SSHKeyConfig{
			HetznerID:      key.ID,
			PrivateKeyPath: "~/.orama/sandbox_key",
			PublicKeyPath:  "~/.orama/sandbox_key.pub",
		}, nil
	}

	// Generate new ed25519 keypair
	fmt.Print("  Generating ed25519 keypair... ")
	pubKey, privKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		fmt.Println("FAILED")
		return SSHKeyConfig{}, fmt.Errorf("generate key: %w", err)
	}

	// Marshal private key to OpenSSH format
	pemBlock, err := ssh.MarshalPrivateKey(privKey, "")
	if err != nil {
		fmt.Println("FAILED")
		return SSHKeyConfig{}, fmt.Errorf("marshal private key: %w", err)
	}

	privPEM := pem.EncodeToMemory(pemBlock)
	if err := os.WriteFile(privPath, privPEM, 0600); err != nil {
		fmt.Println("FAILED")
		return SSHKeyConfig{}, fmt.Errorf("write private key: %w", err)
	}

	// Marshal public key to authorized_keys format
	sshPubKey, err := ssh.NewPublicKey(pubKey)
	if err != nil {
		return SSHKeyConfig{}, fmt.Errorf("convert public key: %w", err)
	}
	pubStr := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPubKey)))

	if err := os.WriteFile(pubPath, []byte(pubStr+"\n"), 0644); err != nil {
		return SSHKeyConfig{}, fmt.Errorf("write public key: %w", err)
	}
	fmt.Println("OK")

	// Upload to Hetzner
	fmt.Print("  Uploading to Hetzner... ")
	key, err := client.UploadSSHKey("orama-sandbox", pubStr)
	if err != nil {
		fmt.Println("FAILED")
		return SSHKeyConfig{}, fmt.Errorf("upload SSH key: %w", err)
	}
	fmt.Printf("OK (ID: %d)\n", key.ID)

	return SSHKeyConfig{
		HetznerID:      key.ID,
		PrivateKeyPath: "~/.orama/sandbox_key",
		PublicKeyPath:  "~/.orama/sandbox_key.pub",
	}, nil
}

// verifyDNS checks if glue records for the sandbox domain are configured.
//
// There's a chicken-and-egg problem: NS records can't fully resolve until
// CoreDNS is running on the floating IPs (which requires a sandbox cluster).
// So instead of resolving NS → A records, we check for glue records at the
// TLD level, which proves the registrar configuration is correct.
func verifyDNS(domain string, floatingIPs []FloatIP, reader *bufio.Reader) {
	expectedIPs := make(map[string]bool)
	for _, fip := range floatingIPs {
		expectedIPs[fip.IP] = true
	}

	// Find the TLD nameserver to query for glue records
	findTLDServer := func() string {
		// For "dbrs.space", the TLD is "space." — ask the root for its NS
		parts := strings.Split(domain, ".")
		if len(parts) < 2 {
			return ""
		}
		tld := parts[len(parts)-1]
		out, err := exec.Command("dig", "+short", "NS", tld+".", "@8.8.8.8").Output()
		if err != nil {
			return ""
		}
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		if len(lines) > 0 && lines[0] != "" {
			return strings.TrimSpace(lines[0])
		}
		return ""
	}

	check := func() (glueFound bool, foundIPs []string) {
		tldNS := findTLDServer()
		if tldNS == "" {
			return false, nil
		}

		// Query the TLD nameserver for NS + glue of our domain
		// dig NS domain @tld-server will include glue in ADDITIONAL section
		out, err := exec.Command("dig", "NS", domain, "@"+tldNS, "+norecurse", "+additional").Output()
		if err != nil {
			return false, nil
		}

		output := string(out)
		remaining := make(map[string]bool)
		for k, v := range expectedIPs {
			remaining[k] = v
		}

		// Look for our floating IPs in the ADDITIONAL section (glue records)
		// or anywhere in the response
		for _, fip := range floatingIPs {
			if strings.Contains(output, fip.IP) {
				foundIPs = append(foundIPs, fip.IP)
				delete(remaining, fip.IP)
			}
		}

		return len(remaining) == 0, foundIPs
	}

	fmt.Printf("  Checking glue records for %s at TLD nameserver...\n", domain)
	matched, foundIPs := check()

	if matched {
		fmt.Println("  ✓ Glue records configured correctly:")
		for i, ip := range foundIPs {
			fmt.Printf("    ns%d.%s → %s\n", i+1, domain, ip)
		}
		fmt.Println()
		fmt.Println("  Note: Full DNS resolution will work once a sandbox is running")
		fmt.Println("  (CoreDNS on the floating IPs needs to be up to answer queries).")
		return
	}

	if len(foundIPs) > 0 {
		fmt.Println("  ⚠ Partial glue records found:")
		for _, ip := range foundIPs {
			fmt.Printf("    %s\n", ip)
		}
		fmt.Println("  Missing floating IPs in glue:")
		for _, fip := range floatingIPs {
			if expectedIPs[fip.IP] {
				fmt.Printf("    %s\n", fip.IP)
			}
		}
	} else {
		fmt.Println("  ✗ No glue records found yet.")
		fmt.Println("  Make sure you configured at your registrar:")
		fmt.Printf("    ns1.%s → %s\n", domain, floatingIPs[0].IP)
		fmt.Printf("    ns2.%s → %s\n", domain, floatingIPs[1].IP)
	}

	fmt.Println()
	fmt.Print("  Wait for glue propagation? (polls every 30s, Ctrl+C to stop) [y/N]: ")
	choice, _ := reader.ReadString('\n')
	choice = strings.TrimSpace(strings.ToLower(choice))
	if choice != "y" && choice != "yes" {
		fmt.Println("  Skipping. You can create the sandbox now — DNS will work once glue propagates.")
		return
	}

	fmt.Println("  Waiting for glue record propagation...")
	for i := 1; ; i++ {
		time.Sleep(30 * time.Second)
		matched, _ = check()
		if matched {
			fmt.Printf("\n  ✓ Glue records propagated after %d checks\n", i)
			fmt.Println("  You can now create a sandbox: orama sandbox create")
			return
		}
		fmt.Printf("  [%d] Not yet... checking again in 30s\n", i)
	}
}
