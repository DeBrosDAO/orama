package installers

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

func fetchTorArchiveKey() ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), torKeyFetchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, torArchiveKeyURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build request for %s: %w", torArchiveKeyURL, err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download Tor archive key from %s: %w", torArchiveKeyURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download Tor archive key from %s: HTTP %d", torArchiveKeyURL, resp.StatusCode)
	}
	key, err := io.ReadAll(io.LimitReader(resp.Body, torKeyDownloadMax))
	if err != nil {
		return nil, fmt.Errorf("read Tor archive key from %s: %w", torArchiveKeyURL, err)
	}
	return key, nil
}

// installVerifiedKeyring imports the downloaded key into a throwaway gpg home,
// checks it there, and writes only the pinned key to TorKeyringPath. Exporting
// by fingerprint — rather than dearmoring the download — means no other packet
// in the file can reach the keyring apt trusts, even one gpg would not list.
func (ti *TorInstaller) installVerifiedKeyring(armored []byte) (err error) {
	home, err := os.MkdirTemp("", "tor-archive-gpg-*")
	if err != nil {
		return fmt.Errorf("create a temporary gpg home for the Tor archive key: %w", err)
	}
	defer os.RemoveAll(home)
	// GnuPG 2.4 starts keyboxd/gpg-agent for a new home; stop them before the
	// home is removed so none outlives the install.
	defer func() {
		if killErr := runChecked(ti.run, "gpgconf", "--homedir", home, "--kill", "all"); killErr != nil && err == nil {
			err = fmt.Errorf("stop the gpg daemons of the temporary home %s: %w", home, killErr)
		}
	}()
	keyFile := filepath.Join(home, "archive-key.asc")
	if err := os.WriteFile(keyFile, armored, 0600); err != nil {
		return fmt.Errorf("write %s: %w", keyFile, err)
	}

	if err := runChecked(ti.run, "gpg", "--homedir", home, "--batch", "--import", keyFile); err != nil {
		return fmt.Errorf("import the downloaded Tor archive key: %w", err)
	}
	out, err := ti.run("gpg", "--homedir", home, "--batch", "--with-colons", "--list-keys")
	if err != nil {
		return fmt.Errorf("list the imported Tor archive key: %w (%s)", err, strings.TrimSpace(out))
	}
	if err := VerifyTorArchiveKey(out); err != nil {
		return err
	}
	keyring := ti.path(TorKeyringPath)
	if err := runChecked(ti.run, "gpg", "--homedir", home, "--batch", "--yes", "--export", "-o", keyring, TorArchiveKeyFingerprint); err != nil {
		return fmt.Errorf("write %s: %w", keyring, err)
	}
	return nil
}

// VerifyTorArchiveKey accepts gpg --with-colons output only if it holds exactly
// one primary key and that key is TorArchiveKeyFingerprint. A second key would
// be written into the keyring alongside the real one and trusted by apt.
func VerifyTorArchiveKey(colons string) error {
	fprs := PrimaryKeyFingerprints(colons)
	if len(fprs) != 1 || fprs[0] != TorArchiveKeyFingerprint {
		return fmt.Errorf("downloaded Tor archive key has primary fingerprints %v, want exactly [%s] — refusing to trust it", fprs, TorArchiveKeyFingerprint)
	}
	return nil
}

// PrimaryKeyFingerprints returns the fingerprint of every primary key in
// gpg --with-colons output: the first fpr record after each pub record.
func PrimaryKeyFingerprints(colons string) []string {
	var fprs []string
	awaitingPrimary := false
	scanner := bufio.NewScanner(strings.NewReader(colons))
	for scanner.Scan() {
		fields := strings.Split(scanner.Text(), ":")
		switch {
		case fields[0] == "pub":
			awaitingPrimary = true
		case fields[0] == "fpr" && awaitingPrimary && len(fields) > 9:
			fprs = append(fprs, fields[9])
			awaitingPrimary = false
		}
	}
	return fprs
}
