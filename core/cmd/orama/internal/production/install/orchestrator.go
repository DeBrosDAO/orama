package install

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/utils"
	joinhandlers "github.com/DeBrosOfficial/network/pkg/gateway/handlers/join"
	oramainstall "github.com/DeBrosOfficial/network/pkg/install"
)

// Orchestrator manages the install process
type Orchestrator struct {
	oramaHome string
	oramaDir  string
	setup     *oramainstall.ProductionSetup
	flags     *Flags
	validator *Validator
	peers     []string
}

// NewOrchestrator creates a new install orchestrator
func NewOrchestrator(flags *Flags) (*Orchestrator, error) {
	oramaHome := oramainstall.OramaBase
	oramaDir := oramainstall.OramaDir

	// Normalize peers
	peers, err := utils.NormalizePeers(flags.PeersStr)
	if err != nil {
		return nil, fmt.Errorf("invalid peers: %w", err)
	}

	setup := oramainstall.NewProductionSetup(oramaHome, os.Stdout, flags.Force, flags.SkipChecks)
	setup.SetNameserver(flags.Nameserver)
	setup.SetACMECA(flags.ACMECA)
	setup.SetPublicIP(flags.VpsIP)

	// Set operator metadata (from orama node setup)
	setup.SSHUser = flags.SSHUser
	setup.Environment = flags.Environment
	setup.OperatorWallet = flags.OperatorWallet

	validator := NewValidator(flags, oramaDir)

	return &Orchestrator{
		oramaHome: oramaHome,
		oramaDir:  oramaDir,
		setup:     setup,
		flags:     flags,
		validator: validator,
		peers:     peers,
	}, nil
}

// Execute runs the installation process
// pinnedTLSConfig verifies the far end against one certificate fingerprint,
// and refuses to build a client without one.
func pinnedTLSConfig(fingerprint, serverName string) (*tls.Config, error) {
	fingerprint = strings.TrimSpace(fingerprint)
	if fingerprint == "" {
		return nil, fmt.Errorf("refusing to join without a certificate to pin: the invite token carries " +
			"the cluster's TLS fingerprint, so pass the encoded invite to --token rather than a bare " +
			"token, or give --ca-fingerprint explicitly. Joining sends a credential for every secret " +
			"the cluster holds, and there is nothing to verify the far end with")
	}
	expected, err := hex.DecodeString(fingerprint)
	if err != nil {
		return nil, fmt.Errorf("invalid --ca-fingerprint: must be hex-encoded SHA-256: %w", err)
	}
	if len(expected) != sha256.Size {
		return nil, fmt.Errorf("invalid --ca-fingerprint: a SHA-256 fingerprint is %d bytes, got %d",
			sha256.Size, len(expected))
	}

	// InsecureSkipVerify turns off the chain check; VerifyPeerCertificate then
	// does the only check that matters here, which is that the certificate is
	// the exact one the invite named. A cluster node's certificate is issued
	// for its own domain and there is no CA to chain to at this point.
	return &tls.Config{
		// ServerName selects the certificate the node serves (by SNI); the pin
		// below is what is checked.
		ServerName:         serverName,
		InsecureSkipVerify: true,
		VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
			if len(rawCerts) == 0 {
				return fmt.Errorf("the server presented no TLS certificate")
			}
			hash := sha256.Sum256(rawCerts[0])
			if !bytes.Equal(hash[:], expected) {
				return fmt.Errorf("TLS certificate fingerprint mismatch: the invite named %s, the server "+
					"presented %x — something is between this node and the cluster", fingerprint, hash[:])
			}
			return nil
		},
	}, nil
}

func (o *Orchestrator) Execute() error {
	fmt.Printf("🚀 Starting production installation...\n\n")

	if err := o.validator.ValidateFlags(); err != nil {
		return err
	}

	// Validate DNS if domain is provided
	o.validator.ValidateDNS()

	// Dry-run mode: show what would be done and exit
	if o.flags.DryRun {
		utils.ShowDryRunSummary(o.flags.VpsIP, o.flags.Domain, "main", o.peers, o.flags.JoinAddress, o.validator.IsFirstNode(), o.oramaDir)
		return nil
	}

	// Save secrets before installation (only for genesis; join flow gets secrets from response)
	if !o.isJoiningNode() {
		if err := o.validator.SaveSecrets(); err != nil {
			return err
		}
	}

	// Save preferences for future upgrades.
	prefs := &oramainstall.NodePreferences{
		Branch:     "main",
		Nameserver: o.flags.Nameserver,
	}
	if err := oramainstall.SavePreferences(o.oramaDir, prefs); err != nil {
		fmt.Fprintf(os.Stderr, "⚠️  Warning: Failed to save preferences: %v\n", err)
	}
	if o.flags.Nameserver {
		fmt.Printf("  ℹ️  This node will be a nameserver (CoreDNS + Caddy)\n")
	}

	// Phase 1: Check prerequisites
	fmt.Printf("\n📋 Phase 1: Checking prerequisites...\n")
	if err := o.setup.Phase1CheckPrerequisites(); err != nil {
		return fmt.Errorf("prerequisites check failed: %w", err)
	}

	// Phase 2: Provision environment
	fmt.Printf("\n🛠️  Phase 2: Provisioning environment...\n")
	if err := o.setup.Phase2ProvisionEnvironment(); err != nil {
		return fmt.Errorf("environment provisioning failed: %w", err)
	}

	// Phase 2d: Tor client (the node's anonymity proxy). It needs the
	// network and nothing from the archive, so it runs before a join can
	// spend the invite.
	fmt.Printf("\nPhase 2d: Installing the Tor client...\n")
	if err := o.setup.PhaseTorSetup(); err != nil {
		return fmt.Errorf("tor setup failed: %w", err)
	}

	// The trust anchor, before any binary is installed: Phase 2b installs
	// only an archive signed by a signer it lists. A joining node gets it
	// from the join, so the join is requested here — after the archive has
	// been checked as far as it can be without the cluster's signers.
	join, err := establishArchiveTrust(o.setup, o.isJoiningNode(), o.flags.expectedArchiveSigners, o.requestJoin)
	if err != nil {
		return err
	}

	// Phase 2b: Install binaries
	fmt.Printf("\nPhase 2b: Installing binaries...\n")
	if err := o.setup.Phase2bInstallBinaries(); err != nil {
		return fmt.Errorf("binary installation failed: %w", err)
	}

	// Branch: genesis node vs joining node
	if join != nil {
		return o.executeJoinFlow(join)
	}
	return o.executeGenesisFlow()
}

// joinedCluster is what the join step established: this node's identity, its
// WireGuard keypair and the cluster's answer.
type joinedCluster struct {
	peerID  string
	privKey string
	resp    *joinhandlers.JoinResponse
}

// archiveTrust is the part of ProductionSetup that checks the archive and
// writes the trust anchor.
type archiveTrust interface {
	SeedGenesisArchiveSigners() error
	PreflightJoinArchive(expected []string) error
	TrustJoinedArchiveSigners(signers []string, rotatedAt string, expected []string) error
}

// establishArchiveTrust writes the archive trust anchor before anything is
// installed from the archive. A genesis node trusts its --operator-wallet. A
// joining node requests the join first, because the cluster's signers come
// back in the join response over the pinned, invite-authenticated channel.
// The invite is spent then, so everything about the archive that can fail
// without the cluster's signers is checked before: its presence, its
// architecture and its integrity — against the signers the operator expects
// (--expect-archive-signers), when given, which the response must then match.
func establishArchiveTrust(trust archiveTrust, joining bool, expected []string, requestJoin func() (*joinedCluster, error)) (*joinedCluster, error) {
	if !joining {
		fmt.Printf("\n🔏 Creating the archive trust anchor...\n")
		if err := trust.SeedGenesisArchiveSigners(); err != nil {
			return nil, fmt.Errorf("archive trust anchor: %w", err)
		}
		return nil, nil
	}
	fmt.Printf("\n🔏 Checking the build archive before joining...\n")
	if err := trust.PreflightJoinArchive(expected); err != nil {
		return nil, err
	}
	join, err := requestJoin()
	if err != nil {
		return nil, err
	}
	fmt.Printf("\n🔏 Trusting the cluster's archive signers...\n")
	if err := trust.TrustJoinedArchiveSigners(join.resp.ArchiveSigners, join.resp.ArchiveSignersRotatedAt, expected); err != nil {
		return nil, fmt.Errorf("archive trust anchor: %w", err)
	}
	return join, nil
}

// requestJoin establishes this node's identity and WireGuard keypair and asks
// the cluster to admit it.
func (o *Orchestrator) requestJoin() (*joinedCluster, error) {
	// The peer id is how every store in the cluster keys this machine, so the
	// join request has to carry it. It used to be generated in Phase 3, well
	// after the join, which left the receiving node no choice but to invent a
	// synthetic "node-<wgip>" for the wireguard_peers row — an id that matched
	// no dns_nodes row and never would.
	fmt.Printf("\n🪪 Establishing node identity...\n")
	peerID, err := o.setup.EnsureNodeIdentity()
	if err != nil {
		return nil, err
	}
	fmt.Printf("  ✓ Node identity: %s\n", peerID)

	fmt.Printf("\n🔑 Generating WireGuard keypair...\n")
	privKey, pubKey, err := oramainstall.GenerateKeyPair()
	if err != nil {
		return nil, fmt.Errorf("failed to generate WG keypair: %w", err)
	}
	fmt.Printf("  ✓ WireGuard keypair generated\n")

	fmt.Printf("\n🤝 Requesting cluster join from %s...\n", o.flags.JoinAddress)
	resp, err := o.callJoinEndpoint(pubKey, peerID)
	if err != nil {
		return nil, fmt.Errorf("join request failed: %w", err)
	}
	fmt.Printf("  ✓ Join approved — assigned WG IP: %s\n", resp.WGIP)
	fmt.Printf("  ✓ Received %d WG peers\n", len(resp.WGPeers))
	return &joinedCluster{peerID: peerID, privKey: privKey, resp: resp}, nil
}

// isJoiningNode returns true if --join and --token are both set
func (o *Orchestrator) isJoiningNode() bool {
	return o.flags.JoinAddress != "" && o.flags.Token != ""
}

// executeGenesisFlow runs the install for the first node in a new cluster
func (o *Orchestrator) executeGenesisFlow() error {
	// Phase 3: Generate secrets locally
	fmt.Printf("\n🔐 Phase 3: Generating secrets...\n")
	if err := o.setup.Phase3GenerateSecrets(); err != nil {
		return fmt.Errorf("secret generation failed: %w", err)
	}

	// Phase 6a: WireGuard — self-assign 10.0.0.1
	fmt.Printf("\n🔒 Phase 6a: Setting up WireGuard mesh VPN...\n")
	// Fatal, both of them: every service below binds the WireGuard address,
	// and a wrong firewall is either a node exposed to the internet or one cut
	// off from the overlay. The upgrade path treats the firewall the same way.
	if _, _, err := o.setup.Phase6SetupWireGuard(true); err != nil {
		return fmt.Errorf("WireGuard setup failed: %w", err)
	}
	fmt.Printf("  ✓ WireGuard configured (10.0.0.1)\n")

	// Phase 6b: UFW firewall
	fmt.Printf("\n🛡️  Phase 6b: Setting up UFW firewall...\n")
	if err := o.setup.Phase6bSetupFirewall(o.flags.SkipFirewall); err != nil {
		return fmt.Errorf("firewall setup failed: %w", err)
	}

	// Phase 4: Generate configs using WG IP (10.0.0.1) as advertise address
	// All inter-node communication uses WireGuard IPs, not public IPs
	fmt.Printf("\n⚙️  Phase 4: Generating configurations...\n")
	enableHTTPS := false
	genesisWGIP := "10.0.0.1"
	if err := o.setup.Phase4GenerateConfigs(o.peers, genesisWGIP, enableHTTPS, o.flags.Domain, o.flags.BaseDomain, ""); err != nil {
		return fmt.Errorf("configuration generation failed: %w", err)
	}

	if err := o.validator.ValidateGeneratedConfig(); err != nil {
		return err
	}

	// Phase 2c: Initialize services (use WG IP for IPFS Cluster peer discovery)
	fmt.Printf("\nPhase 2c: Initializing services...\n")
	if err := o.setup.Phase2cInitializeServices(o.peers, genesisWGIP, nil, nil); err != nil {
		return fmt.Errorf("service initialization failed: %w", err)
	}

	// Namespace templates BEFORE the services that use them. Phase 5 starts
	// orama-node, whose first act is to start orama-namespace-wireguard@index;
	// with no template installed systemd answers "Unit not found", the
	// supervisor exits, and systemd restarts it. Install used to depend on that
	// retry loop to converge, which worked well enough that nobody noticed the
	// ordering was backwards.
	fmt.Printf("\n🔧 Phase 4b: Installing namespace systemd templates...\n")
	if err := o.setup.InstallNamespaceTemplates(); err != nil {
		return fmt.Errorf("namespace template installation failed: %w", err)
	}

	// Phase 5: Create systemd services
	fmt.Printf("\n🔧 Phase 5: Creating systemd services...\n")
	if err := o.setup.Phase5CreateSystemdServices(enableHTTPS); err != nil {
		return fmt.Errorf("service creation failed: %w", err)
	}

	// Nameserver zone records (NS, SOA, glue, apex A) are not seeded here:
	// orama-node's DNS component writes them from the claimed nameserver
	// slots on its sweep (pkg/node/dns_nameservers.go).
	if err := o.setup.Phase8Verify(context.Background()); err != nil {
		return err
	}

	o.setup.LogSetupComplete(o.setup.NodePeerID)
	fmt.Printf("✅ Production installation complete!\n\n")
	o.printFirstNodeSecrets()
	return nil
}

// executeJoinFlow runs the rest of the install for a node the cluster has
// admitted (requestJoin, before Phase 2b).
func (o *Orchestrator) executeJoinFlow(join *joinedCluster) error {
	joinResp, privKey := join.resp, join.privKey

	// Step 3: Configure WireGuard with assigned IP and peers
	fmt.Printf("\n🔒 Configuring WireGuard tunnel...\n")
	var wgPeers []oramainstall.WireGuardPeer
	for _, p := range joinResp.WGPeers {
		wgPeers = append(wgPeers, oramainstall.WireGuardPeer{
			PublicKey: p.PublicKey,
			Endpoint:  p.Endpoint,
			AllowedIP: p.AllowedIP,
		})
	}
	// Install WG package first
	wp := oramainstall.NewWireGuardProvisioner(oramainstall.WireGuardConfig{})
	if err := wp.Install(); err != nil {
		return fmt.Errorf("failed to install wireguard: %w", err)
	}
	if err := o.setup.EnableWireGuardWithPeers(privKey, joinResp.WGIP, wgPeers); err != nil {
		return fmt.Errorf("failed to enable WireGuard: %w", err)
	}

	// Step 4: Verify WG tunnel
	fmt.Printf("\n🔍 Verifying WireGuard tunnel...\n")
	if err := o.verifyWGTunnel(joinResp.WGPeers, o.flags.JoinAddress); err != nil {
		return fmt.Errorf("WireGuard tunnel verification failed: %w", err)
	}
	fmt.Printf("  ✓ WireGuard tunnel established\n")

	// Step 5: UFW firewall
	fmt.Printf("\n🛡️  Setting up UFW firewall...\n")
	if err := o.setup.Phase6bSetupFirewall(o.flags.SkipFirewall); err != nil {
		return fmt.Errorf("firewall setup failed: %w", err)
	}

	// Step 6: Save secrets from join response
	fmt.Printf("\n🔐 Saving cluster secrets...\n")
	if err := o.saveSecretsFromJoinResponse(joinResp); err != nil {
		return fmt.Errorf("failed to save secrets: %w", err)
	}
	fmt.Printf("  ✓ Secrets saved\n")

	// Auto-generate domain for non-nameserver joining nodes
	if o.flags.Domain == "" && !o.flags.Nameserver && joinResp.BaseDomain != "" {
		o.flags.Domain = generateNodeDomain(joinResp.BaseDomain)
		fmt.Printf("\n🌐 Auto-generated domain: %s\n", o.flags.Domain)
	}

	// Step 7: Generate configs using WG IP as advertise address
	// All inter-node communication uses WireGuard IPs, not public IPs
	fmt.Printf("\n⚙️  Generating configurations...\n")
	enableHTTPS := false
	rqliteJoin := joinResp.RQLiteJoinAddress
	if err := o.setup.Phase4GenerateConfigs(joinResp.BootstrapPeers, joinResp.WGIP, enableHTTPS, o.flags.Domain, joinResp.BaseDomain, rqliteJoin, joinResp.OlricPeers); err != nil {
		return fmt.Errorf("configuration generation failed: %w", err)
	}

	if err := o.validator.ValidateGeneratedConfig(); err != nil {
		return err
	}

	// Step 8: Initialize services with IPFS peer info from join response
	fmt.Printf("\nInitializing services...\n")
	var ipfsPeerInfo *oramainstall.IPFSPeerInfo
	if joinResp.IPFSPeer.ID != "" {
		ipfsPeerInfo = &oramainstall.IPFSPeerInfo{
			PeerID: joinResp.IPFSPeer.ID,
			Addrs:  joinResp.IPFSPeer.Addrs,
		}
	}
	var ipfsClusterPeerInfo *oramainstall.IPFSClusterPeerInfo
	if joinResp.IPFSClusterPeer.ID != "" {
		ipfsClusterPeerInfo = &oramainstall.IPFSClusterPeerInfo{
			PeerID: joinResp.IPFSClusterPeer.ID,
			Addrs:  joinResp.IPFSClusterPeer.Addrs,
		}
	}

	if err := o.setup.Phase2cInitializeServices(joinResp.BootstrapPeers, joinResp.WGIP, ipfsPeerInfo, ipfsClusterPeerInfo); err != nil {
		return fmt.Errorf("service initialization failed: %w", err)
	}

	// Namespace templates BEFORE the services that use them. Phase 5 starts
	// orama-node, whose first act is to start orama-namespace-wireguard@index;
	// with no template installed systemd answers "Unit not found", the
	// supervisor exits, and systemd restarts it. Install used to depend on that
	// retry loop to converge, which worked well enough that nobody noticed the
	// ordering was backwards.
	fmt.Printf("\n🔧 Installing namespace systemd templates...\n")
	if err := o.setup.InstallNamespaceTemplates(); err != nil {
		return fmt.Errorf("namespace template installation failed: %w", err)
	}

	// Step 9: Create systemd services
	fmt.Printf("\n🔧 Creating systemd services...\n")
	if err := o.setup.Phase5CreateSystemdServices(enableHTTPS); err != nil {
		return fmt.Errorf("service creation failed: %w", err)
	}

	if err := o.setup.Phase8Verify(context.Background()); err != nil {
		return err
	}

	o.setup.LogSetupComplete(o.setup.NodePeerID)
	fmt.Printf("✅ Production installation complete! Joined cluster via %s\n\n", o.flags.JoinAddress)
	return nil
}

// callJoinEndpoint sends the join request to the existing node's HTTPS endpoint
func (o *Orchestrator) callJoinEndpoint(wgPubKey, peerID string) (*joinhandlers.JoinResponse, error) {
	reqBody := joinhandlers.JoinRequest{
		Token:       o.flags.Token,
		WGPublicKey: wgPubKey,
		PublicIP:    o.flags.VpsIP,
		PeerID:      peerID,
		// The minting node refuses a mismatch before it spends the invite.
		ExpectedArchiveSigners: o.flags.expectedArchiveSigners,
	}
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	url := strings.TrimRight(o.flags.JoinAddress, "/") + "/v1/internal/join"

	// Joining sends the invite token, which is a credential for every secret
	// the cluster holds. Without a fingerprint to pin, this fell back to
	// InsecureSkipVerify with nothing checked at all — the token went to
	// whoever answered the address, and a machine in the path could take it
	// and join the cluster itself.
	//
	// Every invite carries the fingerprint: `orama node invite` reads this
	// node's certificate and refuses to mint an invite without it, and
	// `orama node install` decodes it from the token. An invocation with no
	// fingerprint is a bare token from somewhere else, and there is nothing to
	// verify the far end with.
	tlsConfig, err := pinnedTLSConfig(o.flags.CAFingerprint, o.flags.JoinSNI)
	if err != nil {
		return nil, err
	}

	client := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: tlsConfig,
		},
	}

	req, err := newJoinRequest(url, o.flags.JoinSNI, bodyBytes)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to contact %s: %w", url, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("join rejected (HTTP %d): %s", resp.StatusCode, string(respBody))
	}

	// Caddy answers a Host it has no site for with an empty 200, so a 200 is
	// not proof the gateway saw the request. The join handler always answers
	// JSON; anything else never reached it.
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		return nil, fmt.Errorf("join endpoint %s answered HTTP 200 with Content-Type %q and %d bytes: the request did not reach the Orama gateway (check that the invite's server name is a site on that node)", url, ct, len(respBody))
	}

	var joinResp joinhandlers.JoinResponse
	if err := json.Unmarshal(respBody, &joinResp); err != nil {
		return nil, fmt.Errorf("failed to parse join response: %w", err)
	}

	return &joinResp, nil
}

// newJoinRequest builds the join POST. The invite names the node by IP so the
// joiner reaches the node that minted it, and names its site separately; the
// site goes in the Host header as well as the SNI, because the node's Caddy
// routes by Host and has no site for a bare IP.
func newJoinRequest(url, serverName string, body []byte) (*http.Request, error) {
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to build join request for %s: %w", url, err)
	}
	req.Header.Set("Content-Type", "application/json")
	if serverName != "" {
		req.Host = serverName
	}
	return req, nil
}

// saveSecretsFromJoinResponse writes cluster secrets received from the join endpoint to disk
func (o *Orchestrator) saveSecretsFromJoinResponse(resp *joinhandlers.JoinResponse) error {
	// secrets/ is the orama user's once Phase 5 has run: write it without
	// following symlinks.
	root := oramainstall.OramaRoot(o.oramaDir)
	secretsDir := filepath.Join(o.oramaDir, "secrets")
	if err := root.MkdirAll(secretsDir, 0700); err != nil {
		return fmt.Errorf("failed to create secrets dir: %w", err)
	}

	// Write cluster secret
	if resp.ClusterSecret != "" {
		if err := root.WriteFile(filepath.Join(secretsDir, "cluster-secret"), []byte(resp.ClusterSecret), 0600); err != nil {
			return fmt.Errorf("failed to write cluster-secret: %w", err)
		}
	}

	// Write swarm key
	if resp.SwarmKey != "" {
		if err := root.WriteFile(filepath.Join(secretsDir, "swarm.key"), []byte(resp.SwarmKey), 0600); err != nil {
			return fmt.Errorf("failed to write swarm.key: %w", err)
		}
	}

	// Write API key HMAC secret
	if resp.APIKeyHMACSecret != "" {
		if err := root.WriteFile(filepath.Join(secretsDir, "api-key-hmac-secret"), []byte(resp.APIKeyHMACSecret), 0600); err != nil {
			return fmt.Errorf("failed to write api-key-hmac-secret: %w", err)
		}
	}

	// Write RQLite password and generate auth JSON file
	if resp.RQLitePassword != "" {
		if err := root.WriteFile(filepath.Join(secretsDir, "rqlite-password"), []byte(resp.RQLitePassword), 0600); err != nil {
			return fmt.Errorf("failed to write rqlite-password: %w", err)
		}
		// Also generate the auth JSON file that rqlited uses with -auth flag
		authJSON := fmt.Sprintf(`[{"username": "orama", "password": "%s", "perms": ["all"]}]`, resp.RQLitePassword)
		if err := root.WriteFile(filepath.Join(secretsDir, "rqlite-auth.json"), []byte(authJSON), 0600); err != nil {
			return fmt.Errorf("failed to write rqlite-auth.json: %w", err)
		}
	}

	// Write Olric encryption key
	if resp.OlricEncryptionKey != "" {
		if err := root.WriteFile(filepath.Join(secretsDir, "olric-encryption-key"), []byte(resp.OlricEncryptionKey), 0600); err != nil {
			return fmt.Errorf("failed to write olric-encryption-key: %w", err)
		}
	}

	// Write serverless secrets encryption key (bugboard #837) — identical on
	// every node so namespace function secrets decrypt cluster-wide.
	if resp.SecretsEncryptionKey != "" {
		if err := root.WriteFile(filepath.Join(secretsDir, "secrets-encryption-key"), []byte(resp.SecretsEncryptionKey), 0600); err != nil {
			return fmt.Errorf("failed to write secrets-encryption-key: %w", err)
		}
	}

	// Write TURN shared secret (feat-124 #913) — identical on every node so
	// WebRTC TURN credentials validate cluster-wide and survive config regen.
	if resp.EncryptionRoot != "" {
		if err := root.WriteFile(filepath.Join(secretsDir, "encryption-root"), []byte(resp.EncryptionRoot), 0600); err != nil {
			return fmt.Errorf("failed to write encryption-root: %w", err)
		}
		id := resp.EncryptionRootID
		if id == "" {
			id = "1"
		}
		if err := root.WriteFile(filepath.Join(secretsDir, "encryption-root.id"), []byte(id), 0600); err != nil {
			return fmt.Errorf("failed to write encryption-root.id: %w", err)
		}
	} else if resp.ClusterSecret != "" {
		if err := root.WriteFile(filepath.Join(secretsDir, "encryption-root"), []byte(resp.ClusterSecret), 0600); err != nil {
			return fmt.Errorf("failed to write encryption-root: %w", err)
		}
		if err := root.WriteFile(filepath.Join(secretsDir, "encryption-root.id"), []byte("1"), 0600); err != nil {
			return fmt.Errorf("failed to write encryption-root.id: %w", err)
		}
	}

	if resp.TURNSecret != "" {
		if err := root.WriteFile(filepath.Join(secretsDir, "turn-secret"), []byte(resp.TURNSecret), 0600); err != nil {
			return fmt.Errorf("failed to write turn-secret: %w", err)
		}
	}

	// Write IPFS Cluster trusted peer IDs
	if len(resp.IPFSClusterPeerIDs) > 0 {
		content := strings.Join(resp.IPFSClusterPeerIDs, "\n") + "\n"
		if err := root.WriteFile(filepath.Join(secretsDir, "ipfs-cluster-trusted-peers"), []byte(content), 0600); err != nil {
			return fmt.Errorf("failed to write ipfs-cluster-trusted-peers: %w", err)
		}
	}

	return nil
}

// verifyWGTunnel pings a WG peer to verify the tunnel is working.
// It targets the node that handled the join request (joinAddress), since that
// node is the only one guaranteed to have the new peer's key immediately.
// Other peers learn the key via the WireGuard sync loop (up to 60s delay),
// so pinging them would race against replication.
func (o *Orchestrator) verifyWGTunnel(peers []joinhandlers.WGPeerInfo, joinAddress string) error {
	if len(peers) == 0 {
		return fmt.Errorf("no WG peers to verify")
	}

	// Find the join node's WG IP by matching its public IP against peer endpoints.
	targetIP := ""
	joinHost := extractHost(joinAddress)
	for _, p := range peers {
		endpointHost := extractHost(p.Endpoint)
		if endpointHost == joinHost {
			targetIP = strings.TrimSuffix(p.AllowedIP, "/32")
			break
		}
	}

	// Fallback to first peer if the join node wasn't found in the peer list.
	if targetIP == "" {
		targetIP = strings.TrimSuffix(peers[0].AllowedIP, "/32")
	}

	// Retry ping for up to 30 seconds
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		cmd := exec.Command("ping", "-c", "1", "-W", "2", targetIP)
		if err := cmd.Run(); err == nil {
			return nil
		}
		time.Sleep(2 * time.Second)
	}

	return fmt.Errorf("could not reach %s via WireGuard after 30s", targetIP)
}

// extractHost returns the host part from a URL or host:port string.
func extractHost(addr string) string {
	// Strip scheme (http://, https://)
	addr = strings.TrimPrefix(addr, "http://")
	addr = strings.TrimPrefix(addr, "https://")
	// Strip port
	if idx := strings.LastIndex(addr, ":"); idx != -1 {
		addr = addr[:idx]
	}
	// Strip trailing path
	if idx := strings.Index(addr, "/"); idx != -1 {
		addr = addr[:idx]
	}
	return addr
}

func (o *Orchestrator) printFirstNodeSecrets() {
	fmt.Printf("📋 To add more nodes to this cluster:\n\n")
	fmt.Printf("  1. Generate an invite token:\n")
	fmt.Printf("     orama node invite\n\n")
	fmt.Printf("  2. Run the printed command on the new VPS.\n\n")
	fmt.Printf("  Node Peer ID: %s\n\n", o.setup.NodePeerID)
}

// promptForBaseDomain interactively prompts the user to select a network environment
// Returns the selected base domain for deployment routing. An empty custom
// domain or an unknown option is an error: it used to install the node into
// devnet's zone without asking.
func promptForBaseDomain(in io.Reader) (string, error) {
	reader := bufio.NewReader(in)

	fmt.Println("\n🌐 Network Environment Selection")
	fmt.Println("=================================")
	fmt.Println("Select the network environment for this node:")
	fmt.Println()
	fmt.Println("  1. orama-devnet.network   (Development - for testing)")
	fmt.Println("  2. orama-testnet.network  (Testnet - pre-production)")
	fmt.Println("  3. orama-mainnet.network  (Mainnet - production)")
	fmt.Println("  4. Custom domain...")
	fmt.Println()
	fmt.Print("Select option [1-4] (default: 1): ")

	choice, _ := reader.ReadString('\n')
	choice = strings.TrimSpace(choice)

	switch choice {
	case "", "1":
		fmt.Println("✓ Selected: orama-devnet.network")
		return "orama-devnet.network", nil
	case "2":
		fmt.Println("✓ Selected: orama-testnet.network")
		return "orama-testnet.network", nil
	case "3":
		fmt.Println("✓ Selected: orama-mainnet.network")
		return "orama-mainnet.network", nil
	case "4":
		fmt.Print("Enter custom base domain (e.g., example.com): ")
		customDomain, _ := reader.ReadString('\n')
		customDomain = strings.TrimSpace(customDomain)
		if customDomain == "" {
			return "", clierr.Usage("no custom base domain entered; run again and enter one, or pass --base-domain")
		}
		// Remove any protocol prefix if user included it
		customDomain = strings.TrimPrefix(customDomain, "https://")
		customDomain = strings.TrimPrefix(customDomain, "http://")
		customDomain = strings.TrimSuffix(customDomain, "/")
		fmt.Printf("✓ Selected: %s\n", customDomain)
		return customDomain, nil
	default:
		return "", clierr.Usage("%q is not one of the options 1-4; run again, or pass --base-domain", choice)
	}
}

// generateNodeDomain creates a random subdomain like "node-a3f8k2.example.com"
func generateNodeDomain(baseDomain string) string {
	const chars = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		// Fallback to timestamp-based
		return fmt.Sprintf("node-%06x.%s", time.Now().UnixNano()%0xffffff, baseDomain)
	}
	for i := range b {
		b[i] = chars[int(b[i])%len(chars)]
	}
	return fmt.Sprintf("node-%s.%s", string(b), baseDomain)
}
