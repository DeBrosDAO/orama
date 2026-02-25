package sandbox

import (
	"bufio"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"os"
	"os/exec"
	"strings"

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
	cfg.Defaults()

	// Step 3: Floating IPs
	fmt.Println()
	fmt.Println("Checking floating IPs...")
	floatingIPs, err := setupFloatingIPs(client, cfg.Location)
	if err != nil {
		return err
	}
	cfg.FloatingIPs = floatingIPs

	// Step 4: Firewall
	fmt.Println()
	fmt.Println("Checking firewall...")
	fwID, err := setupFirewall(client)
	if err != nil {
		return err
	}
	cfg.FirewallID = fwID

	// Step 5: SSH key
	fmt.Println()
	fmt.Println("Setting up SSH key...")
	sshKeyConfig, err := setupSSHKey(client)
	if err != nil {
		return err
	}
	cfg.SSHKey = sshKeyConfig

	// Step 6: Display DNS instructions
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

	// Step 7: Verify DNS (optional)
	fmt.Print("Verify DNS now? [y/N]: ")
	verifyChoice, _ := reader.ReadString('\n')
	verifyChoice = strings.TrimSpace(strings.ToLower(verifyChoice))
	if verifyChoice == "y" || verifyChoice == "yes" {
		verifyDNS(domain)
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
			// Key likely already exists on Hetzner — find it by listing
			fmt.Printf("  SSH key may already be on Hetzner (upload: %v)\n", err)
			fmt.Print("  Enter the Hetzner SSH key ID (or 0 to re-upload): ")
			reader := bufio.NewReader(os.Stdin)
			idStr, _ := reader.ReadString('\n')
			idStr = strings.TrimSpace(idStr)
			var hetznerID int64
			fmt.Sscanf(idStr, "%d", &hetznerID)

			if hetznerID == 0 {
				return SSHKeyConfig{}, fmt.Errorf("could not resolve SSH key on Hetzner, try deleting and re-running setup")
			}

			return SSHKeyConfig{
				HetznerID:      hetznerID,
				PrivateKeyPath: "~/.orama/sandbox_key",
				PublicKeyPath:  "~/.orama/sandbox_key.pub",
			}, nil
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

// verifyDNS checks if the sandbox domain resolves.
func verifyDNS(domain string) {
	fmt.Printf("  Checking NS records for %s...\n", domain)
	out, err := exec.Command("dig", "+short", "NS", domain, "@8.8.8.8").Output()
	if err != nil {
		fmt.Printf("  Warning: dig failed: %v\n", err)
		fmt.Println("  DNS verification skipped. You can verify later with:")
		fmt.Printf("    dig NS %s @8.8.8.8\n", domain)
		return
	}

	result := strings.TrimSpace(string(out))
	if result == "" {
		fmt.Println("  Warning: No NS records found yet.")
		fmt.Println("  DNS propagation can take up to 48 hours.")
		fmt.Println("  The sandbox will still work once DNS is configured.")
	} else {
		fmt.Printf("  NS records:\n")
		for _, line := range strings.Split(result, "\n") {
			fmt.Printf("    %s\n", line)
		}
	}
}
