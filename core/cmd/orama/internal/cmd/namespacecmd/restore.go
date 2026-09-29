package namespacecmd

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/shared"
	backuphandlers "github.com/DeBrosOfficial/network/pkg/gateway/handlers/backup"
	"github.com/DeBrosOfficial/network/pkg/nsbackup"
	"github.com/spf13/cobra"
)

// maxErrorBodyBytes is how much of a gateway error body is shown.
const maxErrorBodyBytes = 4096

var backupCmd = &cobra.Command{
	Use:   "backup",
	Short: "Take a backup of the namespace, sealed to your X25519 public key",
	Long: `Ask the namespace gateway for a backup: its RQLite snapshot, the CIDs it
has pinned, and its secrets, decrypted by the cluster and sealed with the rest
to the public key you give. The cluster never holds the private key and cannot
open what it wrote. Keep the private key off the cluster.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		keyHex, _ := cmd.Flags().GetString("key")
		outPath, _ := cmd.Flags().GetString("out")
		gw, err := resolveGateway()
		if err != nil {
			return err
		}
		blob, err := fetchBackup(cmd.Context(), gw, keyHex)
		if err != nil {
			return err
		}
		if err := os.WriteFile(outPath, blob, 0600); err != nil {
			return fmt.Errorf("write %s: %w", outPath, err)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Backup written to %s (%d bytes)\n", outPath, len(blob))
		return nil
	},
}

var restoreKeyCmd = &cobra.Command{
	Use:   "restore-key",
	Short: "Print the namespace gateway's restore public key",
	Long: `Print the X25519 public key a restore's secrets are sealed to. It is
derived from the destination cluster's encryption root and the namespace, so
it is different for every namespace and changes when that root is rotated.
Pass it to 'orama namespace restore --dest-key'.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		gw, err := resolveGateway()
		if err != nil {
			return err
		}
		key, err := fetchRestoreKey(cmd.Context(), gw)
		if err != nil {
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), key)
		return nil
	},
}

var restoreCmd = &cobra.Command{
	Use:   "restore",
	Short: "Restore a namespace backup onto the namespace gateway (DESTRUCTIVE)",
	Long: `Open a backup on this machine with your private key, seal its secrets to
the destination gateway's restore key (--dest-key, from 'orama namespace
restore-key'), and send it to the namespace gateway you are signed in to.

The gateway replaces the namespace's entire RQLite database with the backup,
writes the secrets under its own cluster's encryption root, and pins every CID
in the backup. The namespace must already exist on the destination, and
--namespace must name the namespace the backup was taken of. A wrong key, a
corrupt file or a different namespace stops before anything is sent.

The gateway also refuses, before writing anything, a restore that would put
the namespace over its storage quota on the destination, and it keeps the
destination's quota rather than the one in the backup. It runs one backup or
restore at a time and answers 429 while one is running.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		gw, err := resolveGateway()
		if err != nil {
			return err
		}
		return runRestore(cmd.Context(), cmd.OutOrStdout(), gw, restoreOptionsFrom(cmd))
	},
}

type restoreOptions struct {
	inPath, keyFile, namespace, destKey string
}

func restoreOptionsFrom(cmd *cobra.Command) restoreOptions {
	var o restoreOptions
	o.inPath, _ = cmd.Flags().GetString("in")
	o.keyFile, _ = cmd.Flags().GetString("key-file")
	o.namespace, _ = cmd.Flags().GetString("namespace")
	o.destKey, _ = cmd.Flags().GetString("dest-key")
	return o
}

// gatewayTarget is the gateway a command talks to and its bearer token.
type gatewayTarget struct {
	url    string
	token  string
	client *http.Client
}

func resolveGateway() (gatewayTarget, error) {
	url, err := shared.GetAPIURL()
	if err != nil {
		return gatewayTarget{}, err
	}
	token, err := shared.GetAuthToken()
	if err != nil {
		return gatewayTarget{}, err
	}
	return gatewayTarget{url: url, token: token, client: &http.Client{
		Transport: &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}},
	}}, nil
}

// runRestore does everything that can fail on this machine first, so a wrong
// key, a corrupt backup or a namespace mismatch sends nothing.
func runRestore(ctx context.Context, out io.Writer, gw gatewayTarget, o restoreOptions) error {
	priv, err := readKeyFile(o.keyFile)
	if err != nil {
		return err
	}
	dest, err := parseHexKey(o.destKey, "--dest-key")
	if err != nil {
		return err
	}
	blob, err := os.ReadFile(o.inPath)
	if err != nil {
		return fmt.Errorf("read %s: %w", o.inPath, err)
	}
	body, err := buildRestore(blob, priv, dest, o.namespace)
	if err != nil {
		return err
	}
	resp, err := submitRestore(ctx, gw, body)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "Restored namespace %s: %d-byte database, %d secrets, %d pins\n",
		resp.Namespace, resp.RQLiteBytes, resp.Secrets, resp.Pins)
	if resp.SecretsWithoutRow > 0 {
		fmt.Fprintf(out, "%d secrets matched no row: they were created after the snapshot was taken\n", resp.SecretsWithoutRow)
	}
	return nil
}

// buildRestore opens a backup and turns it into a restore request for the
// gateway whose restore key is dest.
func buildRestore(blob []byte, priv, dest *[32]byte, namespace string) ([]byte, error) {
	plain, err := nsbackup.Open(priv, blob)
	if err != nil {
		return nil, fmt.Errorf("open the backup: %w", err)
	}
	p, err := nsbackup.UnmarshalPayload(plain)
	if err != nil {
		return nil, fmt.Errorf("read the backup: %w", err)
	}
	if p.Namespace != namespace {
		return nil, fmt.Errorf("the backup is of namespace %q, not %q; refusing to restore it", p.Namespace, namespace)
	}
	req, err := p.Rewrap(dest)
	if err != nil {
		return nil, err
	}
	return req.Marshal()
}

func fetchBackup(ctx context.Context, gw gatewayTarget, keyHex string) ([]byte, error) {
	if _, err := parseHexKey(keyHex, "--key"); err != nil {
		return nil, err
	}
	body, err := json.Marshal(backuphandlers.BackupRequest{PublicKey: keyHex})
	if err != nil {
		return nil, fmt.Errorf("encode the backup request: %w", err)
	}
	return gw.call(ctx, http.MethodPost, "/v1/namespace/backup", "application/json", body)
}

func fetchRestoreKey(ctx context.Context, gw gatewayTarget) (string, error) {
	raw, err := gw.call(ctx, http.MethodGet, "/v1/namespace/restore-key", "", nil)
	if err != nil {
		return "", err
	}
	var resp backuphandlers.RestoreKeyResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return "", fmt.Errorf("decode the restore key: %w", err)
	}
	return resp.PublicKey, nil
}

func submitRestore(ctx context.Context, gw gatewayTarget, body []byte) (backuphandlers.RestoreResponse, error) {
	var resp backuphandlers.RestoreResponse
	raw, err := gw.call(ctx, http.MethodPost, "/v1/namespace/restore", "application/octet-stream", body)
	if err != nil {
		return resp, err
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return resp, fmt.Errorf("decode the restore result: %w", err)
	}
	return resp, nil
}

func (gw gatewayTarget) call(ctx context.Context, method, path, contentType string, body []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, gw.url+path, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build %s %s: %w", method, path, err)
	}
	req.Header.Set("Authorization", "Bearer "+gw.token)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := gw.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("reach the gateway at %s: %w", gw.url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodyBytes))
		return nil, fmt.Errorf("%s %s failed (HTTP %d): %s", method, path, resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read the %s response: %w", path, err)
	}
	return raw, nil
}

// readKeyFile reads a private key kept in a file as 64 hex characters, so it
// is not on the command line or in shell history.
func readKeyFile(path string) (*[32]byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read the key file %s: %w", path, err)
	}
	return parseHexKey(string(raw), "the key file")
}

func parseHexKey(s, what string) (*[32]byte, error) {
	raw, err := hex.DecodeString(strings.TrimSpace(s))
	if err != nil || len(raw) != 32 {
		return nil, fmt.Errorf("%s must be a 32-byte X25519 key, 64 hex characters", what)
	}
	var k [32]byte
	copy(k[:], raw)
	return &k, nil
}

func init() {
	backupCmd.Flags().String("key", "", "your X25519 backup public key, 64 hex characters")
	backupCmd.Flags().String("out", "", "file to write the sealed backup to")
	_ = backupCmd.MarkFlagRequired("key")
	_ = backupCmd.MarkFlagRequired("out")

	restoreCmd.Flags().String("in", "", "sealed backup file")
	restoreCmd.Flags().String("key-file", "", "file holding your X25519 backup private key, 64 hex characters")
	restoreCmd.Flags().String("namespace", "", "namespace the backup was taken of; must match the backup")
	restoreCmd.Flags().String("dest-key", "", "destination gateway's restore public key, from 'orama namespace restore-key'")
	for _, f := range []string{"in", "key-file", "namespace", "dest-key"} {
		_ = restoreCmd.MarkFlagRequired(f)
	}

	Cmd.AddCommand(backupCmd, restoreKeyCmd, restoreCmd)
}
