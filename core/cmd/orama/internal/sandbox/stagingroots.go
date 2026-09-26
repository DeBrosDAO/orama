package sandbox

import (
	"crypto/sha256"
	"crypto/x509"
	_ "embed"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/DeBrosOfficial/network/cmd/orama/internal"
)

// Sandbox nodes get their certificates from Let's Encrypt staging
// (sandboxACMECA), which no system trusts. The CLI trusts staging's roots for
// the sandbox domain only, through the environment's CA file (docs/SANDBOX.md).
//
// The roots ship in the binary rather than being fetched when a sandbox is
// created: nothing on the network can then change what is trusted, and create
// needs no request to letsencrypt.org. They are Let's Encrypt's published
// staging roots, https://letsencrypt.org/docs/staging-environment/.

//go:embed letsencrypt-staging-roots.pem
var stagingRootsPEM []byte

// stagingRootFingerprints are the SHA-256 fingerprints of the certificates
// stagingRootsPEM must hold, and nothing else: "(STAGING) Pretend Pear X1"
// (RSA, until 2035) and "(STAGING) Bogus Broccoli X2" (ECDSA, until 2040).
var stagingRootFingerprints = []string{
	"9b2a339fe6a3e85585c4cd75536cb8c1cf7cd603b9a64bec2521858ae48da85d",
	"e70570a989f8565aabdf7cae27abd1621872d6a3f811e3fef27e3dba02912198",
}

// stagingRootsFile names the roots written next to the sandbox state.
const stagingRootsFile = "letsencrypt-staging-roots.pem"

// checkStagingRoots refuses a bundle that is not exactly the pinned roots.
func checkStagingRoots(bundle []byte) error {
	var got []string
	for rest := bundle; ; {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return fmt.Errorf("parse a staging root: %w", err)
		}
		if !cert.IsCA {
			return fmt.Errorf("staging root %q is not a CA", cert.Subject.CommonName)
		}
		sum := sha256.Sum256(cert.Raw)
		got = append(got, hex.EncodeToString(sum[:]))
	}
	sort.Strings(got)
	if strings.Join(got, ",") != strings.Join(stagingRootFingerprints, ",") {
		return fmt.Errorf("the staging roots are %v, not the pinned %v", got, stagingRootFingerprints)
	}
	return nil
}

// writeStagingRoots writes the pinned staging roots to the sandboxes
// directory and returns the file's path.
func writeStagingRoots() (string, error) {
	if err := checkStagingRoots(stagingRootsPEM); err != nil {
		return "", err
	}
	dir, err := sandboxesDir()
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, stagingRootsFile)
	if err := os.WriteFile(path, stagingRootsPEM, 0o600); err != nil {
		return "", fmt.Errorf("write the Let's Encrypt staging roots to %s: %w", path, err)
	}
	return path, nil
}

// registerEnvironment records the sandbox as the sandbox environment, trusting
// the staging roots for its domain only, and makes it the active environment.
func registerEnvironment(cfg *Config, state *SandboxState) error {
	gatewayURL := "https://" + cfg.Domain
	desc := fmt.Sprintf("Sandbox cluster: %s (%s)", state.Name, cfg.Domain)
	if err := cli.AddEnvironment(sandboxEnvironment, gatewayURL, desc); err != nil {
		return fmt.Errorf("register the %s environment: %w", sandboxEnvironment, err)
	}
	caFile, err := writeStagingRoots()
	if err != nil {
		return err
	}
	if err := cli.SetEnvironmentCA(sandboxEnvironment, caFile); err != nil {
		return fmt.Errorf("trust the Let's Encrypt staging roots for %s: %w", cfg.Domain, err)
	}
	if err := cli.SwitchEnvironment(sandboxEnvironment); err != nil {
		return fmt.Errorf("switch to the %s environment: %w", sandboxEnvironment, err)
	}
	return nil
}
